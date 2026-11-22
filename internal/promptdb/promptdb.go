// Package promptdb 在本地 SQLite 中保存 Playground 对话与附件媒体
package promptdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/sqlitedb"
)

const schema = `
CREATE TABLE IF NOT EXISTS prompts (
 id TEXT PRIMARY KEY, title TEXT NOT NULL, model TEXT NOT NULL, description TEXT NOT NULL,
 created INTEGER NOT NULL, updated INTEGER NOT NULL, data BLOB NOT NULL, turns INTEGER, tokens INTEGER);
CREATE INDEX IF NOT EXISTS prompts_list ON prompts(updated, id, title, model, description, created, turns, tokens);
CREATE TABLE IF NOT EXISTS media (
 id TEXT PRIMARY KEY, mime TEXT NOT NULL, created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS prompt_media (
 prompt_id TEXT NOT NULL, media_id TEXT NOT NULL, PRIMARY KEY (prompt_id, media_id));
CREATE INDEX IF NOT EXISTS prompt_media_id ON prompt_media(media_id);
CREATE TABLE IF NOT EXISTS system_instructions (
 id TEXT PRIMARY KEY, title TEXT NOT NULL, text TEXT NOT NULL);`

// summaryColumns 是对话列表项的列
const summaryColumns = `id, title, model, description, created, updated, turns, tokens`

// mediaIDPattern 匹配按 SHA-256 命名的媒体 ID
var mediaIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// orphanMediaGrace 是未关联对话的临时媒体在 Reclaim 回收前的保留时长
const orphanMediaGrace = 24 * time.Hour

// Store 读写对话库与附件媒体目录
type Store struct {
	db       *sql.DB
	mediaDir string
	mediaMu  sync.Mutex
}

// Open 打开或创建对话库
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sqlitedb.Open(path, schema, 0)
	if err != nil {
		return nil, fmt.Errorf("初始化对话库: %w", err)
	}
	return &Store{db: db, mediaDir: filepath.Join(dir, "prompt-media")}, nil
}

// Close 关闭对话库
func (s *Store) Close() error {
	return s.db.Close()
}

// Reclaim 回收过期无引用的附件媒体并把空闲页交还文件系统
func (s *Store) Reclaim(ctx context.Context) error {
	cutoff := time.Now().Add(-orphanMediaGrace).UnixMilli()
	rows, err := s.db.QueryContext(ctx, `SELECT m.id FROM media m
LEFT JOIN prompt_media pm ON pm.media_id = m.id
WHERE pm.media_id IS NULL AND m.created < ?`, cutoff)
	if err == nil {
		var orphans []string
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr == nil {
				orphans = append(orphans, id)
			}
		}
		rowsErr := rows.Err()
		_ = rows.Close()
		if rowsErr == nil {
			s.deleteUnreferencedMedia(ctx, orphans)
		}
	}
	return sqlitedb.Reclaim(ctx, s.db)
}

// SaveMedia 流式写入附件媒体文件并返回按 SHA-256 计算的媒体 ID
func (s *Store) SaveMedia(ctx context.Context, mimeType string, content io.Reader) (string, error) {
	if err := os.MkdirAll(s.mediaDir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(s.mediaDir, ".tmp-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), content); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	id := hex.EncodeToString(hasher.Sum(nil))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	finalPath := filepath.Join(s.mediaDir, id)
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	moved := false
	if _, err := os.Stat(finalPath); errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(tmpPath, finalPath); err != nil {
			return "", err
		}
		cleanup = false
		moved = true
	} else if err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO media (id, mime, created) VALUES (?, ?, ?)
ON CONFLICT(id) DO UPDATE SET mime = excluded.mime, created = excluded.created`, id, mimeType, time.Now().UnixMilli()); err != nil {
		if moved {
			_ = os.Remove(finalPath)
		}
		return "", err
	}
	return id, nil
}

// OpenMedia 打开已保存的附件媒体文件并返回其 MIME 类型
func (s *Store) OpenMedia(ctx context.Context, id string) (*os.File, string, error) {
	if !mediaIDPattern.MatchString(id) {
		return nil, "", api.ErrPromptNotFound
	}
	var mimeType string
	err := s.db.QueryRowContext(ctx, `SELECT mime FROM media WHERE id = ?`, id).Scan(&mimeType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", api.ErrPromptNotFound
	}
	if err != nil {
		return nil, "", err
	}
	file, err := os.Open(filepath.Join(s.mediaDir, id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", api.ErrPromptNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return file, mimeType, nil
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

// SavePrompt 创建或覆盖一份对话，保留首次创建时间并同步对话的媒体引用
func (s *Store) SavePrompt(ctx context.Context, prompt api.Prompt) (api.PromptSummary, error) {
	mediaIDs := promptMediaRefs(prompt.Data)
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	previous, err := s.promptMediaIDs(ctx, prompt.ID)
	if err != nil {
		return api.PromptSummary{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return api.PromptSummary{}, err
	}
	now := time.Now().UnixMilli()
	summary, err := scanSummary(tx.QueryRowContext(ctx, `INSERT INTO prompts (id, title, model, description, created, updated, data, turns, tokens) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET title = excluded.title, model = excluded.model, description = excluded.description, updated = excluded.updated, data = excluded.data,
 turns = excluded.turns, tokens = excluded.tokens
RETURNING `+summaryColumns,
		prompt.ID, prompt.Title, prompt.Model, prompt.Description, now, now, []byte(prompt.Data), prompt.Turns, prompt.Tokens))
	if err != nil {
		_ = tx.Rollback()
		return api.PromptSummary{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM prompt_media WHERE prompt_id = ?`, prompt.ID); err != nil {
		_ = tx.Rollback()
		return api.PromptSummary{}, err
	}
	next := make(map[string]struct{}, len(mediaIDs))
	for _, id := range mediaIDs {
		next[id] = struct{}{}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO prompt_media (prompt_id, media_id) VALUES (?, ?)`, prompt.ID, id); err != nil {
			_ = tx.Rollback()
			return api.PromptSummary{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return api.PromptSummary{}, err
	}
	var removed []string
	for _, id := range previous {
		if _, kept := next[id]; !kept {
			removed = append(removed, id)
		}
	}
	s.deleteUnreferencedMediaLocked(ctx, removed)
	return summary, nil
}

// RenamePrompt 改写一份对话的标题与描述，不存在时返回 api.ErrPromptNotFound
func (s *Store) RenamePrompt(ctx context.Context, id, title, description string) (api.PromptSummary, error) {
	return scanSummary(s.db.QueryRowContext(ctx, `UPDATE prompts SET title = ?, description = ?, updated = ? WHERE id = ? RETURNING `+summaryColumns,
		title, description, time.Now().UnixMilli(), id))
}

// CopyPrompt 以 id 与 title 复制 source 的模型、描述、内容与附件媒体引用，source 不存在时返回 api.ErrPromptNotFound
func (s *Store) CopyPrompt(ctx context.Context, source, id, title string) (api.PromptSummary, error) {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return api.PromptSummary{}, err
	}
	now := time.Now().UnixMilli()
	summary, err := scanSummary(tx.QueryRowContext(ctx, `INSERT INTO prompts (id, title, model, description, created, updated, data, turns, tokens)
SELECT ?, ?, model, description, ?, ?, data, turns, tokens FROM prompts WHERE id = ? RETURNING `+summaryColumns,
		id, title, now, now, source))
	if err != nil {
		_ = tx.Rollback()
		return api.PromptSummary{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO prompt_media (prompt_id, media_id)
SELECT ?, media_id FROM prompt_media WHERE prompt_id = ?`, id, source); err != nil {
		_ = tx.Rollback()
		return api.PromptSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return api.PromptSummary{}, err
	}
	return summary, nil
}

// DeletePrompt 删除一份对话并清理不再被引用的附件媒体，不存在时返回 api.ErrPromptNotFound
func (s *Store) DeletePrompt(ctx context.Context, id string) error {
	if err := s.deletePromptLocked(ctx, id); err != nil {
		return err
	}
	return sqlitedb.Reclaim(ctx, s.db)
}

// deletePromptLocked 在媒体锁与事务内删除对话及其媒体引用并清理孤立文件
func (s *Store) deletePromptLocked(ctx context.Context, id string) error {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	previous, err := s.promptMediaIDs(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM prompts WHERE id = ?`, id)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		_ = tx.Rollback()
		return err
	} else if count == 0 {
		_ = tx.Rollback()
		return api.ErrPromptNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM prompt_media WHERE prompt_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.deleteUnreferencedMediaLocked(ctx, previous)
	return nil
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

// promptMediaRefs 提取对话轮次与对比窗格中引用的媒体 ID
func promptMediaRefs(data []byte) []string {
	if !bytes.Contains(data, []byte(`"mediaId"`)) {
		return nil
	}
	type turnList struct {
		Turns []struct {
			Parts []struct {
				InlineData *struct {
					MediaID string `json:"mediaId"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"turns"`
	}
	var parsed struct {
		turnList
		Compare *turnList `json:"compare"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var ids []string
	collect := func(list *turnList) {
		if list == nil {
			return
		}
		for _, turn := range list.Turns {
			for _, part := range turn.Parts {
				if part.InlineData == nil {
					continue
				}
				id := part.InlineData.MediaID
				if !mediaIDPattern.MatchString(id) {
					continue
				}
				if _, ok := seen[id]; !ok {
					seen[id] = struct{}{}
					ids = append(ids, id)
				}
			}
		}
	}
	collect(&parsed.turnList)
	collect(parsed.Compare)
	return ids
}

// promptMediaIDs 返回对话当前关联的全部媒体 ID
func (s *Store) promptMediaIDs(ctx context.Context, promptID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT media_id FROM prompt_media WHERE prompt_id = ?`, promptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// deleteUnreferencedMedia 删除不再被任何对话引用的媒体记录与磁盘文件
func (s *Store) deleteUnreferencedMedia(ctx context.Context, candidates []string) {
	if len(candidates) == 0 {
		return
	}
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	s.deleteUnreferencedMediaLocked(ctx, candidates)
}

// deleteUnreferencedMediaLocked 在持有 mediaMu 时删除无引用的媒体记录与磁盘文件
func (s *Store) deleteUnreferencedMediaLocked(ctx context.Context, candidates []string) {
	for _, id := range candidates {
		res, err := s.db.ExecContext(ctx, `DELETE FROM media WHERE id = ? AND NOT EXISTS (SELECT 1 FROM prompt_media WHERE media_id = ?)`, id, id)
		if err != nil {
			continue
		}
		if rows, err := res.RowsAffected(); err == nil && rows > 0 {
			_ = os.Remove(filepath.Join(s.mediaDir, id))
		}
	}
}

// scanSummary 读取一行对话列表项，没有行时返回 api.ErrPromptNotFound
func scanSummary(row interface{ Scan(...any) error }) (api.PromptSummary, error) {
	var item api.PromptSummary
	var created, updated int64
	var turns, tokens sql.NullInt64
	err := row.Scan(&item.ID, &item.Title, &item.Model, &item.Description, &created, &updated, &turns, &tokens)
	if errors.Is(err, sql.ErrNoRows) {
		return api.PromptSummary{}, api.ErrPromptNotFound
	}
	if err != nil {
		return api.PromptSummary{}, err
	}
	item.Created, item.Updated = time.UnixMilli(created), time.UnixMilli(updated)
	if turns.Valid {
		item.Turns = new(int(turns.Int64))
	}
	if tokens.Valid {
		item.Tokens = new(int(tokens.Int64))
	}
	return item, nil
}
