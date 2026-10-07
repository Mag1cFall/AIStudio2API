package requestdb

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// recordColumns 是请求记录查询读取的列，最后一列表示是否保存了正文
const recordColumns = `r.id, r.time, r.protocol, r.path, r.model, r.account, r.channel, r.status, r.state,
 r.duration_ms, r.first_event_ms, r.queue_ms, r.input_tokens, r.reasoning_tokens, r.reply_tokens, r.total_tokens,
 r.tool_calls, r.error, r.attempts, EXISTS(SELECT 1 FROM bodies b WHERE b.id = r.id)`

// recordFilter 返回请求记录的范围、维度筛选、状态码与关键字条件
func recordFilter(query api.UsageRecordQuery) (string, []any) {
	clause, args := filterSQL(query.Filters)
	clause = ` WHERE r.time >= ? AND r.time < ?` + strings.ReplaceAll(clause, " AND ", " AND r.")
	args = append([]any{query.From.UnixMilli(), query.To.UnixMilli()}, args...)
	if query.Status != 0 {
		clause += ` AND r.status = ?`
		args = append(args, query.Status)
	}
	if query.Search != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query.Search) + "%"
		clause += ` AND (r.id LIKE ? ESCAPE '\' OR r.error LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern)
	}
	return clause, args
}

// scanRecord 读取一条请求记录
func scanRecord(rows *sql.Rows) (api.UsageRecord, error) {
	var record api.UsageRecord
	var at int64
	var attempts string
	err := rows.Scan(&record.ID, &at, &record.Protocol, &record.Path, &record.Model, &record.Account, &record.Channel,
		&record.Status, &record.State, &record.DurationMS, &record.FirstEventMS, &record.QueueMS, &record.InputTokens,
		&record.ReasoningTokens, &record.ReplyTokens, &record.TotalTokens, &record.ToolCalls, &record.Error, &attempts, &record.HasBody)
	if err != nil {
		return record, err
	}
	record.Time = time.UnixMilli(at).UTC()
	if err := json.Unmarshal([]byte(attempts), &record.Attempts); err != nil {
		return record, err
	}
	return record, nil
}

// Records 按完成时间倒序返回一页请求记录
func (store *Store) Records(ctx context.Context, query api.UsageRecordQuery) (api.UsageRecordPage, error) {
	clause, args := recordFilter(query)
	if before := query.Before; before != nil {
		clause += ` AND (r.time < ? OR r.time = ? AND r.id < ?)`
		args = append(args, before.Time.UnixMilli(), before.Time.UnixMilli(), before.ID)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT `+recordColumns+` FROM requests r`+clause+
		` ORDER BY r.time DESC, r.id DESC LIMIT ?`, append(args, query.Limit+1)...)
	if err != nil {
		return api.UsageRecordPage{}, err
	}
	page := api.UsageRecordPage{Items: []api.UsageRecord{}}
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return api.UsageRecordPage{}, errors.Join(err, rows.Close())
		}
		page.Items = append(page.Items, record)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return api.UsageRecordPage{}, err
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = api.EncodeUsageCursor(api.UsageCursor{Time: last.Time, ID: last.ID})
	}
	return page, nil
}

// csvHeader 是导出文件的列名
var csvHeader = []string{
	"time", "id", "protocol", "path", "model", "account", "channel", "status", "state", "duration_ms", "first_event_ms",
	"queue_ms", "input_tokens", "reasoning_tokens", "reply_tokens", "total_tokens", "tool_calls", "error", "attempts",
}

// csvText 为可能被电子表格当作公式的文本加单引号前缀
func csvText(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

// ExportRecords 以带 BOM 的 UTF-8 CSV 按完成时间倒序写出范围内全部符合条件的请求记录
func (store *Store) ExportRecords(ctx context.Context, query api.UsageRecordQuery, output io.Writer) error {
	clause, args := recordFilter(query)
	rows, err := store.db.QueryContext(ctx, `SELECT `+recordColumns+` FROM requests r`+clause+` ORDER BY r.time DESC, r.id DESC`, args...)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(output, "\xef\xbb\xbf"); err != nil {
		return errors.Join(err, rows.Close())
	}
	writer := csv.NewWriter(output)
	if err := writer.Write(csvHeader); err != nil {
		return errors.Join(err, rows.Close())
	}
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return errors.Join(err, rows.Close())
		}
		attempts, err := json.Marshal(record.Attempts)
		if err != nil {
			return errors.Join(err, rows.Close())
		}
		integer := func(value int64) string { return strconv.FormatInt(value, 10) }
		if err := writer.Write([]string{
			record.Time.Format(time.RFC3339Nano), record.ID, record.Protocol, csvText(record.Path), csvText(record.Model),
			csvText(record.Account), record.Channel, strconv.Itoa(record.Status), record.State, integer(record.DurationMS),
			integer(record.FirstEventMS), integer(record.QueueMS), integer(record.InputTokens), integer(record.ReasoningTokens),
			integer(record.ReplyTokens), integer(record.TotalTokens), strconv.Itoa(record.ToolCalls), csvText(record.Error), string(attempts),
		}); err != nil {
			return errors.Join(err, rows.Close())
		}
	}
	writer.Flush()
	return errors.Join(rows.Err(), rows.Close(), writer.Error())
}
