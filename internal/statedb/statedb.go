// Package statedb 在本地 SQLite 中保存公开 API 的续接节点
package statedb

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/sqlitedb"
)

const schema = `
CREATE TABLE IF NOT EXISTS states (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, data BLOB NOT NULL);`

// Store 读写续接状态库
type Store struct {
	db     *sql.DB
	log    func(level, message string)
	loaded []api.SavedState
}

// Open 打开或创建续接状态库，按保存顺序读出全部节点，写入失败时经 log 记录
func Open(path string, log func(level, message string)) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sqlitedb.Open(path, schema)
	if err != nil {
		return nil, fmt.Errorf("初始化续接状态库: %w", err)
	}
	loaded, err := readStates(db)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("读取续接状态库: %w", err), db.Close())
	}
	return &Store{db: db, log: log, loaded: loaded}, nil
}

// readStates 按保存顺序读出全部节点
func readStates(db *sql.DB) ([]api.SavedState, error) {
	rows, err := db.Query(`SELECT id, data FROM states ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var states []api.SavedState
	for rows.Next() {
		var state api.SavedState
		if err := rows.Scan(&state.ID, &state.Data); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

// Close 关闭续接状态库
func (s *Store) Close() error {
	return s.db.Close()
}

// Load 返回打开时读出的节点并释放这份副本
func (s *Store) Load() []api.SavedState {
	loaded := s.loaded
	s.loaded = nil
	return loaded
}

// Save 在一个事务中写入节点并删除已移除的节点，已有节点保留原保存顺序
func (s *Store) Save(saved []api.SavedState, removed []string) {
	if err := s.save(saved, removed); err != nil {
		s.log("WARN", "保存续接状态失败 | 错误="+err.Error())
	}
}

func (s *Store) save(saved []api.SavedState, removed []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, state := range saved {
		if _, err := tx.Exec(`INSERT INTO states (id, data) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, state.ID, state.Data); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	for _, id := range removed {
		if _, err := tx.Exec(`DELETE FROM states WHERE id = ?`, id); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	return tx.Commit()
}
