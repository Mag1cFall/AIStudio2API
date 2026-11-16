// Package rhythm 生成并描述一个账户在官网页面上点击与粘贴时各组可信事件之间的间隔
package rhythm

import (
	"math/rand/v2"
	"slices"
	"time"
)

// Rhythm 是一个账户在官网页面上点击与粘贴时各组可信事件之间的毫秒间隔
type Rhythm struct {
	// Press 是指针移到按钮到按下
	Press float64 `json:"press"`
	// Hold 是按下到抬起
	Hold float64 `json:"hold"`
	// Proof 是 click 到页面应用为 GenerateContent 取 proof
	Proof float64 `json:"proof"`
	// Control 是按下 Control 到按下 V
	Control float64 `json:"control"`
	// Key 是按下 V 到抬起 V
	Key float64 `json:"key"`
	// Release 是抬起 V 到抬起 Control
	Release float64 `json:"release"`
}

// official 是 Firefox 158 官网新对话页现场中各间隔的毫秒取值，click 到取 proof 只取页面在点击后 100 ms 内取 proof 的 10 次
var official = struct {
	press, hold, proof, control, key, release []float64
}{
	press:   []float64{9, 5, 66, 8, 1, 6, 3, 11, 4, 12, 10, 3, 10, 18},
	hold:    []float64{69, 143, 86, 67, 63, 88, 83, 68, 65, 63, 68, 73, 64, 70},
	proof:   []float64{79, 77, 57, 56, 49, 40, 87, 58, 74, 52},
	control: []float64{31, 28},
	key:     []float64{108, 101},
	release: []float64{27, 29},
}

// New 为一个账户生成一套节奏：每个间隔取官网现场的一个值加上 ±1.5 ms 的均匀抖动，限制在现场的最小与最大值之间
func New() Rhythm {
	return Rhythm{
		Press:   sample(official.press),
		Hold:    sample(official.hold),
		Proof:   sample(official.proof),
		Control: sample(official.control),
		Key:     sample(official.key),
		Release: sample(official.release),
	}
}

// sample 取 values 中的一个值加上 ±1.5 ms 的均匀抖动，限制在 values 的最小与最大值之间
func sample(values []float64) float64 {
	value := values[rand.IntN(len(values))] + rand.Float64()*3 - 1.5
	return min(max(value, slices.Min(values)), slices.Max(values))
}

// Duration 返回毫秒间隔对应的时长
func Duration(milliseconds float64) time.Duration {
	return time.Duration(milliseconds * float64(time.Millisecond))
}
