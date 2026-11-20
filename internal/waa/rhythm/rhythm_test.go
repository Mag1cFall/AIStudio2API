package rhythm

import (
	"math"
	"slices"
	"testing"
)

// TestNewRhythmsDistinct 核对新生成的 300 份节奏两两不同，每个间隔都落在官网现场的最小与最大值之间
func TestNewRhythmsDistinct(t *testing.T) {
	seen := map[Rhythm]bool{}
	for range 300 {
		value := New()
		if seen[value] {
			t.Fatalf("节奏重复 %+v", value)
		}
		seen[value] = true
		for name, pair := range map[string]struct {
			got  float64
			from []float64
		}{"press": {value.Press, official.press}, "hold": {value.Hold, official.hold}, "proof": {value.Proof, official.proof}, "control": {value.Control, official.control}, "key": {value.Key, official.key}, "release": {value.Release, official.release}} {
			if pair.got < slices.Min(pair.from) || pair.got > slices.Max(pair.from) {
				t.Fatalf("%s=%.2f 超出官网现场 %v", name, pair.got, pair.from)
			}
		}
	}
}

// TestNewRhythmsMatchOfficial 核对 300 份节奏中点击的三个间隔与官网现场同分布：两样本 Kolmogorov–Smirnov 距离小于 0.05 显著性水平的临界值
func TestNewRhythmsMatchOfficial(t *testing.T) {
	var press, hold, proof []float64
	for range 300 {
		value := New()
		press, hold, proof = append(press, value.Press), append(hold, value.Hold), append(proof, value.Proof)
	}
	for name, pair := range map[string][2][]float64{"press": {press, official.press}, "hold": {hold, official.hold}, "proof": {proof, official.proof}} {
		generated, observed := pair[0], pair[1]
		critical := 1.358 * math.Sqrt(float64(len(generated)+len(observed))/float64(len(generated)*len(observed)))
		if distance := ksDistance(generated, observed); distance >= critical {
			t.Fatalf("%s 的 KS 距离 %.3f 不小于临界值 %.3f", name, distance, critical)
		}
	}
}

// ksDistance 返回两个样本经验分布函数的最大差
func ksDistance(a, b []float64) float64 {
	a, b = slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b))
	var distance float64
	for _, x := range slices.Concat(a, b) {
		fa := float64(countAtMost(a, x)) / float64(len(a))
		fb := float64(countAtMost(b, x)) / float64(len(b))
		distance = max(distance, math.Abs(fa-fb))
	}
	return distance
}

// countAtMost 返回有序样本中不大于 x 的个数
func countAtMost(sorted []float64, x float64) int {
	index, found := slices.BinarySearch(sorted, x)
	for found && index < len(sorted) && sorted[index] <= x {
		index++
	}
	return index
}
