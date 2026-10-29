// mozjs.h 是 Go 经 cgo 调用的 SpiderMonkey C 接口
#ifndef MOZJS_SHIM_H
#define MOZJS_SHIM_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// mz_context 是一个线程上的 JSContext 与其根表、微任务队列和宿主状态
typedef struct mz_context mz_context;

// mz_call 是一次原生函数调用的参数与返回值
typedef struct mz_call mz_call;

// MZ_* 是 mz_value 的种类
enum {
  MZ_UNDEFINED = 0,
  MZ_NULL = 1,
  MZ_BOOL = 2,
  MZ_NUMBER = 3,
  MZ_STRING = 4,
  MZ_HANDLE = 5
};

// mz_value 是跨边界传递的值：字符串为 UTF-8，对象等其余值为根表句柄
typedef struct {
  int32_t kind;
  bool boolean;
  double number;
  char* str;
  size_t len;
  uint32_t handle;
} mz_value;

// mz_exception 是取出的待决异常：消息、栈与抛出位置，value 为异常值的句柄
typedef struct {
  char* message;
  char* stack;
  char* filename;
  uint32_t line;
  uint32_t column;
  uint32_t value;
} mz_exception;

// mz_init 初始化进程内的 SpiderMonkey，失败时返回原因
const char* mz_init(void);

// mz_context_new 在当前线程创建 JSContext，id 为 Go 侧的上下文编号
mz_context* mz_context_new(uintptr_t id, uint32_t heap_bytes, size_t stack_quota, const char* locale);

// mz_context_free 销毁 JSContext 及其根表
void mz_context_free(mz_context* mz);

// mz_set_camou_locale 设置当前线程 mozilla::intl::Locale 的 Camoufox 语言、地区与 navigator.language
void mz_set_camou_locale(const char* language, const char* region, const char* locale);

// mz_global_new 新建 Window 全局对象，same 非 0 时与该全局对象共用 compartment，timezone 为该 Realm 的时区
uint32_t mz_global_new(mz_context* mz, uint32_t same, const char* timezone);

// mz_release 释放根表句柄
void mz_release(mz_context* mz, uint32_t handle);

// mz_free 释放接口分配的字符串
void mz_free(void* pointer);

// mz_evaluate 在 global 的 Realm 中执行 UTF-8 脚本
bool mz_evaluate(mz_context* mz, uint32_t global, const char* source, size_t len, const char* filename, uint32_t line, mz_value* out);

// mz_call_function 以 thisv 调用 fn，thisv 为 0 时为 undefined
bool mz_call_function(mz_context* mz, uint32_t fn, uint32_t thisv, const mz_value* args, int argc, mz_value* out);

// mz_get_property 读取对象属性
bool mz_get_property(mz_context* mz, uint32_t object, const char* name, mz_value* out);

// mz_set_property 写入对象属性
bool mz_set_property(mz_context* mz, uint32_t object, const char* name, const mz_value* value);

// mz_new_object 在 global 的 Realm 中新建普通对象
uint32_t mz_new_object(mz_context* mz, uint32_t global);

// mz_parse_json 在 global 的 Realm 中解析 UTF-8 JSON
bool mz_parse_json(mz_context* mz, uint32_t global, const char* json, size_t len, mz_value* out);

// mz_new_function 在 global 的 Realm 中新建调用 Go 原生函数 native 的函数
uint32_t mz_new_function(mz_context* mz, uint32_t global, const char* name, uint32_t nargs, int32_t native);

// mz_install_host_natives 在 target 上定义 C++ 实现的浏览器宿主原生函数
bool mz_install_host_natives(mz_context* mz, uint32_t target);

// mz_install_window_natives 在全局对象上定义 queueMicrotask、atob 与 btoa
bool mz_install_window_natives(mz_context* mz, uint32_t global);

// mz_set_trusted_types 设置 TrustedScript 源码登记表（WeakMap 句柄）与是否按页面 CSP 拒绝以字符串编译代码
void mz_set_trusted_types(mz_context* mz, uint32_t registry, bool block_strings);

// mz_call_arg 读取第 index 个参数
bool mz_call_arg(mz_call* call, uint32_t index, mz_value* out);

// mz_call_arg_bytes 复制第 index 个参数（ArrayBuffer 或视图）的字节，不是二进制数据时返回 false
bool mz_call_arg_bytes(mz_call* call, uint32_t index, uint8_t** data, size_t* len);

// mz_call_return 设置返回值
bool mz_call_return(mz_call* call, const mz_value* value);

// mz_call_return_bytes 以字节新建 ArrayBuffer 作为返回值
bool mz_call_return_bytes(mz_call* call, const uint8_t* data, size_t len);

// mz_call_throw 在当前全局对象中以名为 type 的错误构造器抛出 message
void mz_call_throw(mz_call* call, const char* type, const char* message);

// mz_take_exception 取出并清除待决异常
bool mz_take_exception(mz_context* mz, mz_exception* out);

// mz_set_throw_location 设置宿主 throwLocation 返回的最近一次抛出位置
void mz_set_throw_location(mz_context* mz, const char* filename, uint32_t line, uint32_t column);

// mz_run_jobs 执行微任务检查点，回调抛出的异常经 report 句柄报告
void mz_run_jobs(mz_context* mz);

// mz_clear_kept_objects 结束当前任务的 WeakRef 存活期
void mz_clear_kept_objects(mz_context* mz);

// mz_set_microtask_reporter 设置微任务回调异常的报告函数
void mz_set_microtask_reporter(mz_context* mz, uint32_t report);

// mz_flush_rejections 按发生顺序以 (promise, handled, global) 调用 fn 并清空记录的 Promise 拒绝事件
bool mz_flush_rejections(mz_context* mz, uint32_t fn);

// mz_request_interrupt 请求中断当前执行，可在任意线程调用
void mz_request_interrupt(mz_context* mz);

// mz_now 返回单调时钟的毫秒数
double mz_now(void);

// mz_gc 执行一次完整 GC
void mz_gc(mz_context* mz);

// mz_thread_stack_size 返回当前线程栈的可用字节数
size_t mz_thread_stack_size(void);

#ifdef __cplusplus
}
#endif

#endif
