package mozjs

// #include "clock.h"
import "C"

// SetClock 在任务开始时把当前线程 VM 时钟设为 base 毫秒，此后按当前线程 CPU 时间的 pace 倍走，走过 limit 毫秒后停住，只在持有 JSContext 的线程调用
func SetClock(base, pace, limit float64) {
	C.mz_set_clock(C.double(base), C.double(pace), C.double(limit))
}

// CPU 返回当前线程已用的 CPU 毫秒数
func CPU() float64 { return float64(C.mz_cpu()) }

// Clock 返回当前线程 VM 时钟的毫秒数
func Clock() float64 { return float64(C.mz_clock()) }
