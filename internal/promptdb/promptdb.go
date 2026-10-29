// Package promptdb 在本地 SQLite 中保存 Playground 对话
package promptdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/sqlitedb"
)

const schema = `
CREATE TABLE IF NOT EXISTS prompts (
 id TEXT PRIMARY KEY, title TEXT NOT NULL, model TEXT NOT NULL, description TEXT NOT NULL,
 created INTEGER NOT NULL, updated INTEGER NOT NULL, data BLOB NOT NULL);
CREATE INDEX IF NOT EXISTS prompts_updated ON prompts(updated);
CREATE TABLE IF NOT EXISTS system_instructions (
 id TEXT PRIMARY KEY, title TEXT NOT NULL, text TEXT NOT NULL);`

// summaryColumns 是对话列表项的列
const summaryColumns = `id, title, model, description, created, updated`

// Store 读写对话库
type Store struct {
	db *sql.DB
}

// Open 打开或创建对话库
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sqlitedb.Open(path)
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

// Prompts 按更新时间倒序列出对话，pageSize 为 0 时列出 pageToken 之后的全部，返回下一页的令牌，没有下一页时为空
func (s *Store) Prompts(ctx context.Context, pageSize int, pageToken string) ([]api.PromptSummary, string, error) {
	after, id := int64(math.MaxInt64), ""
	if pageToken != "" {
		value, key, found := strings.Cut(pageToken, ".")
		number, err := strconv.ParseInt(value, 10, 64)
		if !found || err != nil {
			return nil, "", fmt.Errorf("%w: %q", api.ErrInvalidPageToken, pageToken)
		}
		after, id = number, key
	}
	limit := -1
	if pageSize > 0 {
		limit = pageSize + 1
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+summaryColumns+` FROM prompts
WHERE updated < ? OR (updated = ? AND id < ?) ORDER BY updated DESC, id DESC LIMIT ?`, after, after, id, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	prompts := []api.PromptSummary{}
	for rows.Next() {
		item, err := scanSummary(rows)
		if err != nil {
			return nil, "", err
		}
		prompts = append(prompts, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if pageSize <= 0 || len(prompts) <= pageSize {
		return prompts, "", nil
	}
	last := prompts[pageSize-1]
	return prompts[:pageSize], strconv.FormatInt(last.Updated.UnixMilli(), 10) + "." + last.ID, nil
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
	return scanSummary(s.db.QueryRowContext(ctx, `INSERT INTO prompts (id, title, model, description, created, updated, data) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET title = excluded.title, model = excluded.model, description = excluded.description, updated = excluded.updated, data = excluded.data
RETURNING `+summaryColumns,
		prompt.ID, prompt.Title, prompt.Model, prompt.Description, now, now, []byte(prompt.Data)))
}

// RenamePrompt 改写一份对话的标题与描述，不存在时返回 api.ErrPromptNotFound
func (s *Store) RenamePrompt(ctx context.Context, id, title, description string) (api.PromptSummary, error) {
	return scanSummary(s.db.QueryRowContext(ctx, `UPDATE prompts SET title = ?, description = ?, updated = ? WHERE id = ? RETURNING `+summaryColumns,
		title, description, time.Now().UnixMilli(), id))
}

// CopyPrompt 以 id 与 title 复制 source 的模型、描述与内容，source 不存在时返回 api.ErrPromptNotFound
func (s *Store) CopyPrompt(ctx context.Context, source, id, title string) (api.PromptSummary, error) {
	now := time.Now().UnixMilli()
	return scanSummary(s.db.QueryRowContext(ctx, `INSERT INTO prompts (id, title, model, description, created, updated, data)
SELECT ?, ?, model, description, ?, ?, data FROM prompts WHERE id = ? RETURNING `+summaryColumns,
		id, title, now, now, source))
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
	return sqlitedb.Reclaim(ctx, s.db)
}

// SystemInstructions 按创建顺序读取保存的系统指令预设
func (s *Store) SystemInstructions(ctx context.Context) ([]api.SystemInstructionPreset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, text FROM system_instructions ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	presets := []api.SystemInstructionPreset{}
	for rows.Next() {
		var preset api.SystemInstructionPreset
		if err := rows.Scan(&preset.ID, &preset.Title, &preset.Text); err != nil {
			return nil, err
		}
		presets = append(presets, preset)
	}
	return presets, rows.Err()
}

// SaveSystemInstruction 新增或改写一条系统指令预设
func (s *Store) SaveSystemInstruction(ctx context.Context, preset api.SystemInstructionPreset) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO system_instructions (id, title, text) VALUES (?, ?, ?)
ON CONFLICT(id) DO UPDATE SET title = excluded.title, text = excluded.text`, preset.ID, preset.Title, preset.Text)
	return err
}

// DeleteSystemInstruction 删除一条系统指令预设
func (s *Store) DeleteSystemInstruction(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM system_instructions WHERE id = ?`, id)
	return err
}

// scanSummary 读取一行对话列表项，没有行时返回 api.ErrPromptNotFound
func scanSummary(row interface{ Scan(...any) error }) (api.PromptSummary, error) {
	var item api.PromptSummary
	var created, updated int64
	err := row.Scan(&item.ID, &item.Title, &item.Model, &item.Description, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return api.PromptSummary{}, api.ErrPromptNotFound
	}
	if err != nil {
		return api.PromptSummary{}, err
	}
	item.Created, item.Updated = time.UnixMilli(created), time.UnixMilli(updated)
	return item, nil
}
