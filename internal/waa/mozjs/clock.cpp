// clock.cpp 实现 clock.h：performance.now、事件时间戳与 Date 共用的线程 VM 时钟
#include "clock.h"

#include "mozjs.h"

#include <algorithm>
#include <limits>

#include "js/Date.h"

// clockBase 是本线程当前任务开始时的 VM 时钟毫秒数，clockStart 是同一时刻的 mz_now，clockPace 是任务中 VM 时钟相对真实时间走过的倍数，clockLimit 是任务中 VM 时钟最多走过的毫秒数
static thread_local double clockBase = 0;
static thread_local double clockStart = 0;
static thread_local double clockPace = 1;
static thread_local double clockLimit = std::numeric_limits<double>::infinity();

extern "C" void mz_set_clock(double base, double pace, double limit) {
  clockBase = base;
  clockStart = mz_now();
  clockPace = pace;
  clockLimit = limit;
}

extern "C" double mz_clock(void) {
  return clockBase + std::min(clockPace * (mz_now() - clockStart), clockLimit);
}

extern "C" double mz_clock_task(void) { return clockBase; }

// ShiftedTime 把 Date 读到的系统时间（微秒）移到本线程 VM 时钟
static double ShiftedTime(double microseconds, JS::RTPCallerTypeToken, JSContext*) {
  return microseconds + (mz_clock() - mz_now()) * 1000;
}

void mz_clock_realm(JS::RealmOptions& options) {
  JS::SetReduceMicrosecondTimePrecisionCallback(ShiftedTime);
  options.behaviors().setReduceTimerPrecisionCallerType(JS::RTPCallerTypeToken{0});
}
