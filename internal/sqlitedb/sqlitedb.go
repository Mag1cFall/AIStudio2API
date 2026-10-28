// Package sqlitedb 以原生 SQLite 打开服务的本地数据库
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mattn/go-sqlite3"
)

// driverName 是每个连接都设置好收缩、日志与等待参数的 SQLite 驱动名
const driverName = "sqlite3_aistudio2api"

// connectPragmas 是每个新连接执行的设置，增量收缩须在启用 WAL 之前设置
const connectPragmas = `PRAGMA auto_vacuum = INCREMENTAL;
PRAGMA busy_timeout = 10000;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA journal_size_limit = 524288;`

// incrementalVacuum 是 PRAGMA auto_vacuum 表示增量收缩的值
const incrementalVacuum = 2

func init() {
	sql.Register(driverName, &sqlite3.SQLiteDriver{ConnectHook: func(conn *sqlite3.SQLiteConn) error {
		_, err := conn.Exec(connectPragmas, nil)
		return err
	}})
}

// Open 打开或创建数据库，非增量收缩的已有库先整理为增量收缩，再交还空闲页
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open(driverName, "file:"+filepath.ToSlash(path))
	if err != nil {
		return nil, err
	}
	var mode int
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return nil, errors.Join(fmt.Errorf("读取收缩模式: %w", err), db.Close())
	}
	if mode != incrementalVacuum {
		if _, err := db.Exec(`VACUUM`); err != nil {
			return nil, errors.Join(fmt.Errorf("整理数据库: %w", err), db.Close())
		}
	}
	if err := Reclaim(context.Background(), db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

// Reclaim 把空闲页交还文件系统并截短 WAL 文件，incremental_vacuum 每交还一页返回一行，读完全部行才交还全部空闲页
func Reclaim(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA incremental_vacuum`)
	if err != nil {
		return fmt.Errorf("交还空闲页: %w", err)
	}
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("交还空闲页: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("截短 WAL: %w", err)
	}
	return nil
}
