package requestdb

import (
	"encoding/binary"
	"errors"
	"math"
)

// histogramBase 是相邻分桶上界的比例，分位数的相对误差约为 0.5%
const histogramBase = 1.01

// histogramLimit 是分桶数，约 1 天以上的耗时计入最后一个分桶
const histogramLimit = 1840

// histogramLogBase 是 histogramBase 的自然对数
var histogramLogBase = math.Log(histogramBase)

// bucketCount 是一个分桶及其计数
type bucketCount struct {
	bucket int32
	count  int64
}

// counts 是按分桶升序的稀疏耗时分布，0 号分桶保存不大于 0 的值
type counts []bucketCount

// histogramBucket 返回毫秒值所在的分桶
func histogramBucket(milliseconds int64) int32 {
	if milliseconds <= 0 {
		return 0
	}
	return min(int32(math.Floor(math.Log(float64(milliseconds))/histogramLogBase))+1, histogramLimit-1)
}

// histogramValue 返回分桶的代表值，取分桶上下界的几何中点
func histogramValue(bucket int32) float64 {
	if bucket == 0 {
		return 0
	}
	return math.Exp((float64(bucket) - 0.5) * histogramLogBase)
}

// single 返回只含一个毫秒值的分布
func single(milliseconds int64) counts {
	return counts{{bucket: histogramBucket(milliseconds), count: 1}}
}

// mergeCounts 合并两个有序稀疏分布
func mergeCounts(left, right counts) counts {
	merged := make(counts, 0, len(left)+len(right))
	for len(left) > 0 && len(right) > 0 {
		switch {
		case left[0].bucket < right[0].bucket:
			merged, left = append(merged, left[0]), left[1:]
		case left[0].bucket > right[0].bucket:
			merged, right = append(merged, right[0]), right[1:]
		default:
			merged = append(merged, bucketCount{bucket: left[0].bucket, count: left[0].count + right[0].count})
			left, right = left[1:], right[1:]
		}
	}
	return append(append(merged, left...), right...)
}

// encode 将分布编码为 (分桶增量, 数量) 变长整数序列
func (values counts) encode() []byte {
	data := make([]byte, 0, len(values)*4)
	previous := int32(0)
	for _, value := range values {
		data = binary.AppendUvarint(data, uint64(value.bucket-previous))
		data = binary.AppendUvarint(data, uint64(value.count))
		previous = value.bucket
	}
	return data
}

// decodeCounts 解析 encode 生成的分布
func decodeCounts(data []byte) (counts, error) {
	values := make(counts, 0, len(data)/2)
	bucket := int32(0)
	for len(data) > 0 {
		delta, size := binary.Uvarint(data)
		if size <= 0 {
			return nil, errors.New("耗时分布编码无效")
		}
		data = data[size:]
		count, size := binary.Uvarint(data)
		if size <= 0 {
			return nil, errors.New("耗时分布编码无效")
		}
		data = data[size:]
		bucket += int32(delta)
		values = append(values, bucketCount{bucket: bucket, count: int64(count)})
	}
	return values, nil
}

// dense 是查询汇总使用的稠密分布，覆盖从 low 起的连续分桶并按需扩展
type dense struct {
	low    int32
	counts []int64
}

// add 累加一个稀疏分布
func (distribution *dense) add(values counts) {
	if len(values) == 0 {
		return
	}
	first, last := values[0].bucket, values[len(values)-1].bucket
	if distribution.counts == nil {
		distribution.low, distribution.counts = first, make([]int64, last-first+1)
	} else if high := distribution.low + int32(len(distribution.counts)); first < distribution.low || last >= high {
		low := min(first, distribution.low)
		grown := make([]int64, max(last+1, high)-low)
		copy(grown[distribution.low-low:], distribution.counts)
		distribution.low, distribution.counts = low, grown
	}
	for _, value := range values {
		distribution.counts[value.bucket-distribution.low] += value.count
	}
}

// quantiles 按最近秩法返回各分位数的代表值，分布为空时返回 0
func (distribution *dense) quantiles(fractions ...float64) []float64 {
	result := make([]float64, len(fractions))
	total := int64(0)
	for _, count := range distribution.counts {
		total += count
	}
	if total == 0 {
		return result
	}
	for index, fraction := range fractions {
		rank := max(int64(math.Ceil(fraction*float64(total))), 1)
		seen := int64(0)
		for offset, count := range distribution.counts {
			seen += count
			if seen >= rank {
				result[index] = histogramValue(distribution.low + int32(offset))
				break
			}
		}
	}
	return result
}
