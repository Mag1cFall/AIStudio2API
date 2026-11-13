package aistudio

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa"
)

const (
	// streamzSource 是官网 BotGuard 包装层 client streamz 上报的 log source
	streamzSource = 1828
	// streamzClientVersion 是官网 BotGuard 包装层上报 client streamz 时声明的客户端版本
	streamzClientVersion = "53"
	// streamzInterval 是官网 client streamz 定时上报积攒记录的周期
	streamzInterval = 30 * time.Second
	// streamzMachineKey 是官网 BotGuard 包装层未配置时的 mk 标签
	streamzMachineKey = "_"
)

// streamzMetric 是官网 BotGuard 包装层登记的一个 client streamz 指标：counter 为计数，否则为逐次记录的分布
type streamzMetric struct {
	name    string
	fields  []string
	counter bool
}

// streamzMetrics 是官网 BotGuard 包装层按登记顺序排列的 client streamz 指标
var streamzMetrics = []streamzMetric{
	{name: "frs", fields: []string{"mk"}},
	{name: "cec", fields: []string{"ec", "mk"}, counter: true},
	{name: "ec", fields: []string{"en", "mk"}, counter: true},
	{name: "el", fields: []string{"en", "mk"}},
	{name: "wrl", fields: []string{"mn", "ac", "sc", "rk", "mk"}},
	{name: "od/p", fields: []string{"mk"}},
	{name: "od/n", fields: []string{"et", "mk"}},
}

// uiLocalePattern 匹配官网前端主程序地址中的界面语言
var uiLocalePattern = regexp.MustCompile(`\.MakerSuite\.([a-z]{2,3}(?:_[A-Za-z0-9]+)*)\.`)

// uiLocale 返回官网前端主程序地址声明的界面语言，形如 en-US
func uiLocale(bundle string) string {
	match := uiLocalePattern.FindStringSubmatch(bundle)
	if match == nil {
		return ""
	}
	return strings.ReplaceAll(match[1], "_", "-")
}

// streamzRecord 是一条待上报的 client streamz 记录，labels 依指标字段顺序给出，整数标签为 int
type streamzRecord struct {
	metric string
	labels []any
	value  float64
}

// streamzRecordOf 把宿主记录的 BotGuard 指标转换为待上报记录
func streamzRecordOf(metric waa.BotGuardMetric) streamzRecord {
	switch metric.Metric {
	case "ec", "el", "od/n":
		return streamzRecord{metric: metric.Metric, labels: []any{metric.Label, streamzMachineKey}, value: metric.Value}
	case "cec":
		code, _ := strconv.Atoi(metric.Label)
		return streamzRecord{metric: metric.Metric, labels: []any{code, streamzMachineKey}, value: metric.Value}
	default:
		return streamzRecord{metric: metric.Metric, labels: []any{streamzMachineKey}, value: metric.Value}
	}
}

// streamzLabels 按 streamz JSPB 形状编码标签：字符串为 [值]，整数为 [null,值]
func streamzLabels(labels []any) []any {
	encoded := make([]any, len(labels))
	for index, label := range labels {
		if number, ok := label.(int); ok {
			encoded[index] = []any{nil, number}
		} else {
			encoded[index] = []any{label}
		}
	}
	return encoded
}

// streamzMetricsJSON 按官网 client streamz 的形状编码一批记录：指标按登记顺序，同一指标内按标签首次出现的顺序分组，计数累加，分布逐次列出
func streamzMetricsJSON(records []streamzRecord) string {
	entries := []any{}
	for _, metric := range streamzMetrics {
		type cell struct {
			labels []any
			values []float64
		}
		var cells []*cell
		index := map[string]*cell{}
		for _, record := range records {
			if record.metric != metric.name {
				continue
			}
			key, _ := json.Marshal(record.labels)
			current := index[string(key)]
			if current == nil {
				current = &cell{labels: record.labels}
				index[string(key)] = current
				cells = append(cells, current)
			}
			current.values = append(current.values, record.value)
		}
		if len(cells) == 0 {
			continue
		}
		values := []any{}
		for _, current := range cells {
			labels := streamzLabels(current.labels)
			if metric.counter {
				total := 0.0
				for _, value := range current.values {
					total += value
				}
				values = append(values, []any{labels, []any{total}})
				continue
			}
			for _, value := range current.values {
				values = append(values, []any{labels, []any{nil, value}})
			}
		}
		entries = append(entries, []any{"/client_streamz/bg/" + metric.name, nil, metric.fields, values})
	}
	encoded, _ := json.Marshal([]any{entries})
	return string(encoded)
}

// streamzBody 按官网 play.google.com/log 正文形状编码一次 1828 上报，offset 为本地时区相对 UTC 的秒数
func streamzBody(records []streamzRecord, locale string, offset int, seq int, now time.Time) []byte {
	millis := now.UnixMilli()
	event := make([]any, 21)
	event[0] = millis
	event[7] = streamzMetricsJSON(records)
	event[14] = -offset
	event[20] = seq
	body, _ := json.Marshal([]any{clearcutClient(locale, streamzClientVersion, 3), streamzSource, []any{event}, millis})
	return body
}

// streamzReporter 是官网一个 BotGuard 包装层 logger 的 1828 上报通道：记录积攒到上报时一次发出，序号从 1 开始逐次加 1，并按 streamzInterval 定时上报积攒的记录
type streamzReporter struct {
	mu      sync.Mutex
	pending []streamzRecord
	seq     int
	// take 从 VM 取出宿主记录的 BotGuard 指标，WAA 服务的 logger 没有 VM 时为 nil
	take   func(context.Context) ([]waa.BotGuardMetric, error)
	send   func([]byte)
	locale string
	zone   *time.Location
	stop   chan struct{}
	once   sync.Once
}

// newStreamzReporter 创建上报通道并启动定时上报
func newStreamzReporter(locale string, zone *time.Location, send func([]byte), take func(context.Context) ([]waa.BotGuardMetric, error)) *streamzReporter {
	reporter := &streamzReporter{take: take, send: send, locale: locale, zone: zone, stop: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(streamzInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				reporter.flush(context.Background())
			case <-reporter.stop:
				return
			}
		}
	}()
	return reporter
}

// record 积攒待上报的记录
func (reporter *streamzReporter) record(records ...streamzRecord) {
	reporter.mu.Lock()
	reporter.pending = append(reporter.pending, records...)
	reporter.mu.Unlock()
}

// flush 取出 VM 中新记录的指标，与积攒的记录一起作为下一个序号上报，没有记录时不发送
func (reporter *streamzReporter) flush(ctx context.Context) {
	if reporter.take != nil {
		metrics, err := reporter.take(ctx)
		if err != nil {
			return
		}
		for _, metric := range metrics {
			reporter.record(streamzRecordOf(metric))
		}
	}
	reporter.mu.Lock()
	if len(reporter.pending) == 0 {
		reporter.mu.Unlock()
		return
	}
	records := reporter.pending
	reporter.pending = nil
	reporter.seq++
	seq := reporter.seq
	reporter.mu.Unlock()
	now := time.Now()
	_, offset := now.In(reporter.zone).Zone()
	reporter.send(streamzBody(records, reporter.locale, offset, seq, now))
}

// close 停止定时上报
func (reporter *streamzReporter) close() {
	reporter.once.Do(func() { close(reporter.stop) })
}
