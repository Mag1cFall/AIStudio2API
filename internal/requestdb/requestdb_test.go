package requestdb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

// TestLedgerUsageAndBodies 验证关闭前排队的记录落盘、当前与上一周期分开汇总、正文只保留最近条数
func TestLedgerUsageAndBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	logs := func(level, message string, attrs ...any) { t.Errorf("%s %s %v", level, message, attrs) }
	store, err := Open(path, logs)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.Record(Row{ID: "a", Time: now.Add(-time.Hour), Model: "flash", State: "completed", Duration: 2 * time.Second, InputTokens: 10, TotalTokens: 30})
	store.Record(Row{ID: "b", Time: now.Add(-2 * time.Hour), Model: "flash", State: "failed", Status: 429, Duration: 4 * time.Second})
	store.Record(Row{ID: "c", Time: now.Add(-3 * time.Hour), Model: "pro", State: "completed", Duration: 6 * time.Second, TotalTokens: 5})
	store.Record(Row{ID: "old", Time: now.Add(-30 * time.Hour), Model: "pro", State: "completed", Duration: time.Second})
	for index := range bodyRetention + 2 {
		store.SaveBody(api.RequestBody{ID: fmt.Sprintf("body-%d", index), Time: now, Request: "in", Response: "out"})
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, logs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := store.Usage(context.Background(), api.UsageQuery{
		From: now.Add(-24 * time.Hour), To: now, Bucket: time.Hour, Location: time.UTC, Stack: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	totals := report.Totals
	if totals.Requests != 3 || totals.Failed != 1 || totals.RateLimited != 1 || totals.TotalTokens != 35 || report.Previous.Requests != 1 {
		t.Fatalf("totals=%+v previous=%+v", totals, report.Previous)
	}
	if len(report.Buckets) != 25 || math.Abs(totals.Duration.P95MS-6000) > 60 || totals.Duration.AvgMS != 4000 {
		t.Fatalf("buckets=%d duration=%+v", len(report.Buckets), totals.Duration)
	}
	bucketed := int64(0)
	for _, bucket := range report.Buckets {
		bucketed += bucket.Requests
	}
	models := report.Groups["model"]
	if bucketed != 3 || len(models) != 2 || models[0].Key != "flash" || models[0].Requests != 2 {
		t.Fatalf("bucketed=%d models=%+v", bucketed, models)
	}
	if _, err := store.RequestBody(context.Background(), "body-1"); !errors.Is(err, api.ErrRequestBodyNotFound) {
		t.Fatalf("超出保留条数的正文仍存在: %v", err)
	}
	body, err := store.RequestBody(context.Background(), fmt.Sprintf("body-%d", bodyRetention+1))
	if err != nil || body.Request != "in" || body.Response != "out" {
		t.Fatalf("body=%+v err=%v", body, err)
	}
}

// TestLedgerTimeZoneChange 验证服务器时区改变后重新打开账本时按新时区重建本地日汇总，小时汇总与原始记录不变
func TestLedgerTimeZoneChange(t *testing.T) {
	local := time.Local
	t.Cleanup(func() { time.Local = local })
	logs := func(level, message string, attrs ...any) {
		if level != "INFO" {
			t.Errorf("%s %s %v", level, message, attrs)
		}
	}
	path := filepath.Join(t.TempDir(), "requests.db")
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	store, err := Open(path, logs)
	if err != nil {
		t.Fatal(err)
	}
	midnight := time.Now().UTC().Truncate(24 * time.Hour).Add(-48 * time.Hour)
	// at 在 UTC+8 是 midnight 当日 23:30，在 UTC+9 是次日 00:30
	at := midnight.Add(15*time.Hour + 30*time.Minute)
	store.Record(Row{ID: "a", Time: at, Model: "flash", State: "completed", Duration: 2 * time.Second, InputTokens: 10, TotalTokens: 30})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("UTC+9", 9*60*60)
	time.Local = zone
	store, err = Open(path, logs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	usage := func(from, to time.Time, bucket time.Duration) api.UsageReport {
		t.Helper()
		report, err := store.Usage(context.Background(), api.UsageQuery{From: from, To: to, Bucket: bucket, Location: zone, Stack: "model"})
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	// day 是 at 在 UTC+9 所在本地日的起点
	day := midnight.Add(15 * time.Hour)
	if report := usage(day, day.Add(24*time.Hour), 24*time.Hour); report.Totals.Requests != 1 || report.Totals.TotalTokens != 30 || report.Previous.Requests != 0 {
		t.Fatalf("本地日 totals=%+v previous=%+v", report.Totals, report.Previous)
	}
	if report := usage(day, day.Add(time.Hour), time.Hour); report.Totals.Requests != 1 || report.Totals.Duration.AvgMS != 2000 {
		t.Fatalf("小时 totals=%+v", report.Totals)
	}
	if report := usage(at.Add(-time.Minute), at.Add(time.Minute), time.Minute); report.Totals.Requests != 1 || report.Totals.InputTokens != 10 {
		t.Fatalf("原始记录 totals=%+v", report.Totals)
	}
}
