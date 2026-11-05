// Package sqlitedb 以原生 SQLite 打开服务的本地数据库
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

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

// Open 打开或创建数据库，为已有表补上缺少的列、执行 schema 并删除 schema 没有定义的对象，非增量收缩的已有库先整理为增量收缩，最后交还空闲页
func Open(path string, schema string) (*sql.DB, error) {
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
	if err := applySchema(db, schema); err != nil {
		return nil, errors.Join(fmt.Errorf("同步表结构: %w", err), db.Close())
	}
	if err := Reclaim(context.Background(), db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

// applySchema 为已有表补上 schema 定义而缺少的列，执行 schema，再删除 schema 没有定义的表、索引、视图与触发器
func applySchema(db *sql.DB, schema string) error {
	defined, err := sql.Open(driverName, ":memory:")
	if err != nil {
		return err
	}
	defer defined.Close()
	defined.SetMaxOpenConns(1)
	if _, err := defined.Exec(schema); err != nil {
		return err
	}
	want, err := schemaObjects(defined)
	if err != nil {
		return err
	}
	have, err := schemaObjects(db)
	if err != nil {
		return err
	}
	for name, kind := range want {
		if kind == "table" && have[name] == "table" {
			if err := addMissingColumns(defined, db, name); err != nil {
				return err
			}
		}
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	for name, kind := range have {
		if _, ok := want[name]; !ok {
			if _, err := db.Exec(`DROP ` + strings.ToUpper(kind) + ` IF EXISTS ` + quoteIdentifier(name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// schemaObjects 返回库中除 SQLite 内部对象外的表、索引、视图与触发器的名称与类型
func schemaObjects(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT name, type FROM sqlite_master WHERE type IN ('table', 'index', 'view', 'trigger') AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objects := make(map[string]string)
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, err
		}
		objects[name] = kind
	}
	return objects, rows.Err()
}

// addMissingColumns 按 defined 中同名表的列定义为 db 中的表追加缺少的列
func addMissingColumns(defined *sql.DB, db *sql.DB, table string) error {
	existing := make(map[string]bool)
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return errors.Join(err, rows.Close())
		}
		existing[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	rows, err = defined.Query(`SELECT name, type, "notnull", dflt_value FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	var additions []string
	for rows.Next() {
		var name, kind string
		var notNull bool
		var fallback sql.NullString
		if err := rows.Scan(&name, &kind, &notNull, &fallback); err != nil {
			return errors.Join(err, rows.Close())
		}
		if existing[name] {
			continue
		}
		column := quoteIdentifier(name) + " " + kind
		if notNull {
			column += " NOT NULL"
		}
		if fallback.Valid {
			column += " DEFAULT " + fallback.String
		}
		additions = append(additions, `ALTER TABLE `+quoteIdentifier(table)+` ADD COLUMN `+column)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, addition := range additions {
		if _, err := db.Exec(addition); err != nil {
			return err
		}
	}
	return nil
}

// quoteIdentifier 返回双引号括起的 SQL 标识符
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
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
