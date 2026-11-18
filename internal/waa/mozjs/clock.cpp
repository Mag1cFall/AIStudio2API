// clock.cpp 实现 clock.h：performance.now、事件时间戳与 Date 共用的线程 VM 时钟
#include "clock.h"

#include "mozjs.h"

#include <algorithm>
#include <chrono>

#include "js/Date.h"

#ifdef _WIN32
#  include <windows.h>
#  include <intrin.h>
#else
#  include <time.h>
#endif

// clockBase 是本线程当前任务开始时的 VM 时钟毫秒数，clockStart 是同一时刻的 mz_cpu，clockPace 是任务中 VM 时钟相对本线程 CPU 时间走过的倍数，clockLimit 是任务中 VM 时钟最多走过的毫秒数
static thread_local double clockBase = 0;
static thread_local double clockStart = 0;
static thread_local double clockPace = 1;
static thread_local double clockLimit = 0;

extern "C" void mz_set_clock(double base, double pace, double limit) {
  clockBase = base;
  clockStart = mz_cpu();
  clockPace = pace;
  clockLimit = limit;
}

extern "C" double mz_clock(void) { return clockBase + std::min(clockLimit, clockPace * (mz_cpu() - clockStart)); }

extern "C" double mz_clock_task(void) { return clockBase; }

#ifdef _WIN32
// CyclesPerMillisecond 返回线程周期计数（时间戳计数器）每毫秒的增量，进程内第一次调用时以 2 ms 单调时钟测定
static double CyclesPerMillisecond() {
  static const double value = [] {
    auto started = std::chrono::steady_clock::now();
    unsigned long long counter = __rdtsc();
    while (std::chrono::steady_clock::now() - started < std::chrono::milliseconds(2)) {
    }
    unsigned long long elapsed = __rdtsc() - counter;
    return double(elapsed) / std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - started).count();
  }();
  return value;
}

extern "C" double mz_cpu(void) {
  ULONG64 cycles = 0;
  QueryThreadCycleTime(GetCurrentThread(), &cycles);
  return double(cycles) / CyclesPerMillisecond();
}
#else
extern "C" double mz_cpu(void) {
  timespec spec;
  clock_gettime(CLOCK_THREAD_CPUTIME_ID, &spec);
  return double(spec.tv_sec) * 1000.0 + double(spec.tv_nsec) / 1e6;
}
#endif

// ShiftedTime 把 Date 读到的系统时间（微秒）移到本线程 VM 时钟
static double ShiftedTime(double microseconds, JS::RTPCallerTypeToken, JSContext*) {
  return microseconds + (mz_clock() - mz_now()) * 1000;
}

void mz_clock_realm(JS::RealmOptions& options) {
  JS::SetReduceMicrosecondTimePrecisionCallback(ShiftedTime);
  options.behaviors().setReduceTimerPrecisionCallerType(JS::RTPCallerTypeToken{0});
}
