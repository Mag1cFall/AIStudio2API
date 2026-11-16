package mozjs

// #include "clock.h"
import "C"

// SetClock 在任务开始时把当前线程 VM 时钟设为 base 毫秒，此后按真实时间的 pace 倍走，最多走过 limit 毫秒，只在持有 JSContext 的线程调用
func SetClock(base, pace, limit float64) {
	C.mz_set_clock(C.double(base), C.double(pace), C.double(limit))
}

// Clock 返回当前线程 VM 时钟的毫秒数
func Clock() float64 { return float64(C.mz_clock()) }
