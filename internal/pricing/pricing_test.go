package pricing

import (
	"math"
	"testing"
	"time"
)

// TestPricingSchedule 验证单价历史表顺序、美国太平洋时间零点生效切换，以及 Boundaries 只返回开区间内的切点
func TestPricingSchedule(t *testing.T) {
	for model, entries := range prices {
		if len(entries) == 0 {
			t.Fatalf("%s 缺少单价分段", model)
		}
		if !entries[0].from.IsZero() {
			t.Errorf("%s 首段生效时间应为零值，得到 %v", model, entries[0].from)
		}
		for index := 1; index < len(entries); index++ {
			if !entries[index].from.After(entries[index-1].from) {
				t.Errorf("%s 第 %d 段生效时间 %v 不晚于前一段 %v", model, index, entries[index].from, entries[index-1].from)
			}
		}
	}
	pacific, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	dec1 := time.Date(2026, time.December, 1, 0, 0, 0, 0, pacific)
	jan1 := time.Date(2027, time.January, 1, 0, 0, 0, 0, pacific)
	for _, check := range []struct {
		name                  string
		model                 string
		at                    time.Time
		input, cached, output int64
		want                  float64
	}{
		{"Carbon 11-30 PT 末尾", "gemini-4-carbon", dec1.Add(-time.Millisecond), 1_000_000, 500_000, 100_000, 2.05},
		{"Carbon 12-01 PT 零点", "gemini-4-carbon", dec1, 1_000_000, 500_000, 100_000, 4.10},
		{"Argon 11-30 PT 末尾", "gemini-4-argon", dec1.Add(-time.Millisecond), 1_000_000, 0, 100_000, 3.00},
		{"Argon 12-01 PT 零点", "gemini-4-argon", dec1, 1_000_000, 0, 100_000, 6.00},
		{"3.8 Flash 12-31 PT 末尾", "gemini-3.8-flash", jan1.Add(-time.Millisecond), 1_000_000, 500_000, 100_000, 0.7875},
		{"3.8 Flash 01-01 PT 零点", "gemini-3.8-flash", jan1, 1_000_000, 500_000, 100_000, 1.575},
	} {
		if got := TierCost(check.model, check.at, check.input > LongContext, check.input, check.cached, check.output); math.Abs(got-check.want) > 1e-9 {
			t.Errorf("%s: TierCost = %v, 应为 %v", check.name, got, check.want)
		}
	}
	if got := Boundaries(dec1, jan1); len(got) != 0 {
		t.Errorf("Boundaries(dec1, jan1) 应为空，得到 %v", got)
	}
	if got := Boundaries(dec1.Add(-time.Second), jan1.Add(time.Second)); len(got) != 2 || !got[0].Equal(dec1) || !got[1].Equal(jan1) {
		t.Errorf("Boundaries 跨两切点 = %v, 应为 [%v %v]", got, dec1, jan1)
	}
}
