// Package statedb 在本地 SQLite 中保存公开 API 的续接节点
package statedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/sqlitedb"
)

const schema = `
CREATE TABLE IF NOT EXISTS nodes (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, parent TEXT NOT NULL, data BLOB NOT NULL);`

// Store 读写续接状态库
type Store struct {
	db    *sql.DB
	log   func(level, message string, attrs ...any)
	index []api.StateNode
}

// Open 打开或创建续接状态库，按保存顺序读出全部节点的 ID 与续接起点，读写失败时经 log 记录
func Open(path string, log func(level, message string, attrs ...any)) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sqlitedb.Open(path, schema, 0)
	if err != nil {
		return nil, fmt.Errorf("初始化续接状态库: %w", err)
	}
	index, err := readIndex(db)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("读取续接状态库: %w", err), db.Close())
	}
	return &Store{db: db, log: log, index: index}, nil
}

// readIndex 按保存顺序读出全部节点的 ID 与续接起点
func readIndex(db *sql.DB) ([]api.StateNode, error) {
	rows, err := db.Query(`SELECT id, parent FROM nodes ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var index []api.StateNode
	for rows.Next() {
		var node api.StateNode
		if err := rows.Scan(&node.ID, &node.ParentID); err != nil {
			return nil, err
		}
		index = append(index, node)
	}
	return index, rows.Err()
}

// Close 关闭续接状态库
func (s *Store) Close() error {
	return s.db.Close()
}

// Reclaim 把空闲页交还文件系统
func (s *Store) Reclaim(ctx context.Context) error {
	return sqlitedb.Reclaim(ctx, s.db)
}

// Index 返回打开时读出的节点 ID 与续接起点并释放这份副本
func (s *Store) Index() []api.StateNode {
	index := s.index
	s.index = nil
	return index
}

// Read 读出节点正文，节点不存在或读取失败时返回 false
func (s *Store) Read(id string) ([]byte, bool) {
	var data []byte
	if err := s.db.QueryRow(`SELECT data FROM nodes WHERE id = ?`, id).Scan(&data); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.log("WARN", "读取续接状态失败", "error", err)
		}
		return nil, false
	}
	return data, true
}

// Save 在一个事务中写入节点并删除已移除的节点，已有节点保留原保存顺序
func (s *Store) Save(saved []api.SavedState, removed []string) {
	if err := s.save(saved, removed); err != nil {
		s.log("WARN", "保存续接状态失败", "error", err)
	}
}

func (s *Store) save(saved []api.SavedState, removed []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, state := range saved {
		if _, err := tx.Exec(`INSERT INTO nodes (id, parent, data) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET parent = excluded.parent, data = excluded.data`, state.ID, state.ParentID, state.Data); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	for _, id := range removed {
		if _, err := tx.Exec(`DELETE FROM nodes WHERE id = ?`, id); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	return tx.Commit()
}
