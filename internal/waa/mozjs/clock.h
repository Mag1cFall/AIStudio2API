// clock.h 是 VM 时钟的 C 接口：每个事件循环线程上的 VM 读到的时间从任务开始时的 VM 时刻起按倍数随本线程 CPU 时间走，走过上限后停住
#ifndef MOZJS_CLOCK_H
#define MOZJS_CLOCK_H

#ifdef __cplusplus
extern "C" {
#endif

// mz_set_clock 在任务开始时把本线程 VM 时钟设为 base 毫秒，此后按本线程 CPU 时间的 pace 倍走，走过 limit 毫秒后停住
void mz_set_clock(double base, double pace, double limit);

// mz_cpu 返回本线程已用的 CPU 毫秒数
double mz_cpu(void);

// mz_clock 返回本线程 VM 时钟的毫秒数
double mz_clock(void);

// mz_clock_task 返回本线程当前任务开始时的 VM 时钟毫秒数
double mz_clock_task(void);

#ifdef __cplusplus
}

#include "js/RealmOptions.h"

// mz_clock_realm 让 options 建出的 Realm 中 Date 读到本线程 VM 时钟
void mz_clock_realm(JS::RealmOptions& options);
#endif

#endif
