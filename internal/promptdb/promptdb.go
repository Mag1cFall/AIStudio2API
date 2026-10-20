// Package promptdb 在本地 SQLite 中保存 Playground 对话
package promptdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS prompts (
 id TEXT PRIMARY KEY, title TEXT NOT NULL, model TEXT NOT NULL, description TEXT NOT NULL,
 created INTEGER NOT NULL, updated INTEGER NOT NULL, data BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS prompts_updated ON prompts(updated);`

// Store 读写对话库
type Store struct {
	db *sql.DB
}

// Open 打开或创建对话库
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	options := url.Values{"_pragma": {"busy_timeout(10000)", "journal_mode(WAL)", "synchronous(NORMAL)"}}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+options.Encode())
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, errors.Join(fmt.Errorf("初始化对话库: %w", err), db.Close())
	}
	return &Store{db: db}, nil
}

// Close 关闭对话库
func (s *Store) Close() error {
	return s.db.Close()
}

// Prompts 按更新时间倒序列出全部对话
func (s *Store) Prompts(ctx context.Context) ([]api.PromptSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, model, description, created, updated FROM prompts ORDER BY updated DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	prompts := []api.PromptSummary{}
	for rows.Next() {
		var item api.PromptSummary
		var created, updated int64
		if err := rows.Scan(&item.ID, &item.Title, &item.Model, &item.Description, &created, &updated); err != nil {
			return nil, err
		}
		item.Created, item.Updated = time.UnixMilli(created), time.UnixMilli(updated)
		prompts = append(prompts, item)
	}
	return prompts, rows.Err()
}

// Prompt 读取一份对话，不存在时返回 api.ErrPromptNotFound
func (s *Store) Prompt(ctx context.Context, id string) (api.Prompt, error) {
	var prompt api.Prompt
	var created, updated int64
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT id, title, model, description, created, updated, data FROM prompts WHERE id = ?`, id).
		Scan(&prompt.ID, &prompt.Title, &prompt.Model, &prompt.Description, &created, &updated, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return api.Prompt{}, api.ErrPromptNotFound
	}
	if err != nil {
		return api.Prompt{}, err
	}
	prompt.Created, prompt.Updated, prompt.Data = time.UnixMilli(created), time.UnixMilli(updated), json.RawMessage(data)
	return prompt, nil
}

// SavePrompt 创建或覆盖一份对话，保留首次创建时间
func (s *Store) SavePrompt(ctx context.Context, prompt api.Prompt) (api.PromptSummary, error) {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO prompts (id, title, model, description, created, updated, data) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET title = excluded.title, model = excluded.model, description = excluded.description, updated = excluded.updated, data = excluded.data`,
		prompt.ID, prompt.Title, prompt.Model, prompt.Description, now, now, []byte(prompt.Data))
	if err != nil {
		return api.PromptSummary{}, err
	}
	saved, err := s.Prompt(ctx, prompt.ID)
	return saved.PromptSummary, err
}

// DeletePrompt 删除一份对话，不存在时返回 api.ErrPromptNotFound
func (s *Store) DeletePrompt(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM prompts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count == 0 {
		return api.ErrPromptNotFound
	}
	return nil
}
