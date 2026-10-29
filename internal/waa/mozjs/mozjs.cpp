// mozjs.cpp 实现 mozjs.h：上下文、根表、求值、Go 原生函数、微任务队列、Promise 拒绝记录与浏览器宿主原生函数
#include "mozjs.h"

#include <atomic>
#include <chrono>
#include <cstdlib>
#include <cstring>
#include <deque>
#include <string>
#include <utility>
#include <vector>

#include "jsapi.h"
#include "jsfriendapi.h"
#include "js/Array.h"
#include "js/ArrayBuffer.h"
#include "js/CallAndConstruct.h"
#include "js/CharacterEncoding.h"
#include "js/CompilationAndEvaluation.h"
#include "js/ContextOptions.h"
#include "js/Conversions.h"
#include "js/ErrorReport.h"
#include "js/Exception.h"
#include "js/GCAPI.h"
#include "js/GlobalObject.h"
#include "js/Initialization.h"
#include "js/Interrupt.h"
#include "js/JSON.h"
#include "js/LocaleSensitive.h"
#include "js/MemoryFunctions.h"
#include "js/Object.h"
#include "js/Promise.h"
#include "js/PropertyAndElement.h"
#include "js/Proxy.h"
#include "js/RealmOptions.h"
#include "js/SavedFrameAPI.h"
#include "js/SourceText.h"
#include "js/Stack.h"
#include "js/String.h"
#include "js/Warnings.h"
#include "js/WeakMap.h"
#include "js/experimental/TypedData.h"
#include "js/friend/MicroTask.h"

#ifdef _WIN32
#  include <windows.h>
extern "C" __declspec(dllimport) void WINAPI GetCurrentThreadStackLimits(PULONG_PTR low, PULONG_PTR high);
#else
#  include <pthread.h>
#endif

extern "C" bool mzGoNative(uintptr_t id, int32_t native, mz_call* call);
extern "C" void mozjs_set_camou_locale(const char* language, const char* region, const char* locale);
extern "C" bool mozjs_delete_global_property(JSContext* cx, JSObject* global, JS::HandleId id);

class HostJobQueue;

// Rejection 是一次 Promise 拒绝跟踪事件
struct Rejection {
  JS::Heap<JSObject*> promise;
  bool handled;
};

// CapturedException 是失败调用留下的异常信息
struct CapturedException {
  std::string message;
  std::string stack;
  std::string filename;
  uint32_t line = 0;
  uint32_t column = 0;
  uint32_t value = 0;
  bool present = false;
};

struct mz_context {
  JSContext* cx = nullptr;
  uintptr_t id = 0;
  std::deque<JS::Heap<JS::Value>> slots;
  std::vector<uint32_t> freeSlots;
  std::deque<Rejection> rejections;
  HostJobQueue* jobQueue = nullptr;
  uint32_t microtaskReporter = 0;
  uint32_t trustedRegistry = 0;
  bool blockStrings = false;
  bool draining = false;
  bool resolving = false;
  std::atomic<bool> interrupt{false};
  std::string throwFile;
  uint32_t throwLine = 0;
  uint32_t throwColumn = 0;
  CapturedException exception;
};

struct mz_call {
  JSContext* cx;
  JS::CallArgs* args;
  mz_context* mz;
};

static bool WindowResolve(JSContext* cx, JS::HandleObject object, JS::HandleId id, bool* resolved);
static bool WindowEnumerate(JSContext* cx, JS::HandleObject object, JS::MutableHandleIdVector properties, bool enumerableOnly);

// WindowClassOps 按形状表惰性解析与枚举全局名称
static const JSClassOps WindowClassOps = {
    .newEnumerate = WindowEnumerate,
    .resolve = WindowResolve,
    .trace = JS_GlobalObjectTraceHook,
};

// WindowClass 的两个宿主槽保存惰性解析与枚举回调
static const JSClass WindowClass = {"Window", JSCLASS_GLOBAL_FLAGS_WITH_SLOTS(2), &WindowClassOps};

// Keep 把值放入根表并返回句柄
static uint32_t Keep(mz_context* mz, const JS::Value& value) {
  if (!mz->freeSlots.empty()) {
    uint32_t index = mz->freeSlots.back();
    mz->freeSlots.pop_back();
    mz->slots[index] = value;
    return index + 1;
  }
  mz->slots.emplace_back(value);
  return uint32_t(mz->slots.size());
}

// Slot 返回句柄对应的值，0 为 undefined
static JS::Value Slot(mz_context* mz, uint32_t handle) {
  if (handle == 0 || handle > mz->slots.size()) {
    return JS::UndefinedValue();
  }
  return mz->slots[handle - 1].get();
}

// TraceRoots 追踪根表与拒绝记录中的值
static void TraceRoots(JSTracer* trc, void* data) {
  auto* mz = static_cast<mz_context*>(data);
  for (auto& slot : mz->slots) {
    JS::TraceEdge(trc, &slot, "mozjs slot");
  }
  for (auto& rejection : mz->rejections) {
    JS::TraceEdge(trc, &rejection.promise, "mozjs rejection");
  }
}

// CopyString 以 malloc 复制字节
static char* CopyString(const char* data, size_t len) {
  char* copy = static_cast<char*>(malloc(len + 1));
  if (len) {
    memcpy(copy, data, len);
  }
  copy[len] = 0;
  return copy;
}

// EncodeUTF8 把 JS 字符串编码为 UTF-8
static bool EncodeUTF8(JSContext* cx, JS::HandleString str, std::string* out) {
  JS::UniqueChars chars = JS_EncodeStringToUTF8(cx, str);
  if (!chars) {
    return false;
  }
  out->assign(chars.get());
  return true;
}

// ToJS 把跨边界值转为 JS 值
static bool ToJS(mz_context* mz, JSContext* cx, const mz_value& value, JS::MutableHandleValue out) {
  switch (value.kind) {
    case MZ_NULL:
      out.setNull();
      return true;
    case MZ_BOOL:
      out.setBoolean(value.boolean);
      return true;
    case MZ_NUMBER:
      out.setNumber(value.number);
      return true;
    case MZ_STRING: {
      JSString* str = JS_NewStringCopyUTF8N(cx, JS::UTF8Chars(value.str, value.len));
      if (!str) {
        return false;
      }
      out.setString(str);
      return true;
    }
    case MZ_HANDLE:
      out.set(Slot(mz, value.handle));
      return JS_WrapValue(cx, out);
    default:
      out.setUndefined();
      return true;
  }
}

// FromJS 把 JS 值转为跨边界值，字符串复制为 UTF-8，其余非原始值放入根表
static bool FromJS(mz_context* mz, JSContext* cx, JS::HandleValue value, mz_value* out) {
  memset(out, 0, sizeof(*out));
  if (value.isUndefined()) {
    out->kind = MZ_UNDEFINED;
  } else if (value.isNull()) {
    out->kind = MZ_NULL;
  } else if (value.isBoolean()) {
    out->kind = MZ_BOOL;
    out->boolean = value.toBoolean();
  } else if (value.isNumber()) {
    out->kind = MZ_NUMBER;
    out->number = value.toNumber();
  } else if (value.isString()) {
    JSLinearString* linear = JS_EnsureLinearString(cx, value.toString());
    if (!linear) {
      return false;
    }
    out->kind = MZ_STRING;
    out->len = JS::GetDeflatedUTF8StringLength(linear);
    out->str = static_cast<char*>(malloc(out->len + 1));
    JS::DeflateStringToUTF8Buffer(linear, mozilla::Span(out->str, out->len));
    out->str[out->len] = 0;
  } else {
    out->kind = MZ_HANDLE;
    out->handle = Keep(mz, value);
  }
  return true;
}

// WindowResolve 把首次访问的全局名称交给所属 Realm 的宿主解析
static bool WindowResolve(JSContext* cx, JS::HandleObject object, JS::HandleId id, bool* resolved) {
  auto* mz = static_cast<mz_context*>(JS_GetContextPrivate(cx));
  *resolved = false;
  if (mz->resolving) {
    return true;
  }
  JS::RootedValue callback(cx, JS::GetReservedSlot(object, JSCLASS_GLOBAL_SLOT_COUNT));
  if (callback.isUndefined()) {
    mz->resolving = true;
    bool ok = JS_ResolveStandardClass(cx, object, id, resolved);
    mz->resolving = false;
    return ok;
  }
  JS::RootedValue argument(cx);
  if (!JS_IdToValue(cx, id, &argument)) {
    return false;
  }
  mz->resolving = true;
  JS::RootedValue result(cx);
  bool ok = JS::Call(cx, JS::UndefinedHandleValue, callback, JS::HandleValueArray(argument), &result);
  mz->resolving = false;
  *resolved = ok && result.toBoolean();
  return ok;
}

// WindowEnumerate 先列尚未解析的名称，再由引擎枚举已定义属性
static bool WindowEnumerate(JSContext* cx, JS::HandleObject object, JS::MutableHandleIdVector properties, bool enumerableOnly) {
  JS::RootedValue callback(cx, JS::GetReservedSlot(object, JSCLASS_GLOBAL_SLOT_COUNT + 1));
  if (callback.isUndefined()) {
    return JS_NewEnumerateStandardClasses(cx, object, properties, enumerableOnly);
  }
  JS::RootedValue argument(cx, JS::BooleanValue(enumerableOnly));
  JS::RootedValue result(cx);
  if (!JS::Call(cx, JS::UndefinedHandleValue, callback, JS::HandleValueArray(argument), &result)) {
    return false;
  }
  JS::RootedObject names(cx, &result.toObject());
  uint32_t count;
  if (!JS::GetArrayLength(cx, names, &count)) {
    return false;
  }
  JS::RootedValue name(cx);
  JS::RootedId id(cx);
  for (uint32_t index = 0; index < count; index++) {
    if (!JS_GetElement(cx, names, index, &name) || !JS_ValueToId(cx, name, &id) || !properties.append(id)) {
      return false;
    }
  }
  return true;
}

// HostLazyGlobals 在宿主安装结束后挂起惰性属性并登记 Window 钩子
static bool HostLazyGlobals(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  JS::RootedObject global(cx, JS::CurrentGlobalOrNull(cx));
  JS::RootedObject names(cx, &args[2].toObject());
  uint32_t count;
  if (!JS::GetArrayLength(cx, names, &count)) {
    return false;
  }
  JS::RootedValue name(cx);
  JS::RootedId id(cx);
  for (uint32_t index = 0; index < count; index++) {
    if (!JS_GetElement(cx, names, index, &name) || !JS_ValueToId(cx, name, &id) ||
        !mozjs_delete_global_property(cx, global, id)) {
      return false;
    }
  }
  JS::SetReservedSlot(global, JSCLASS_GLOBAL_SLOT_COUNT, args[0]);
  JS::SetReservedSlot(global, JSCLASS_GLOBAL_SLOT_COUNT + 1, args[1]);
  args.rval().setUndefined();
  return true;
}

// FrameLocation 读取 SavedFrame 栈首帧的来源、行与列
static void FrameLocation(JSContext* cx, JS::HandleObject stack, std::string* file, uint32_t* line, uint32_t* column) {
  if (!stack) {
    return;
  }
  JS::RootedString source(cx);
  if (JS::GetSavedFrameSource(cx, nullptr, stack, &source, JS::SavedFrameSelfHosted::Exclude) == JS::SavedFrameResult::Ok && source) {
    EncodeUTF8(cx, source, file);
  }
  JS::GetSavedFrameLine(cx, nullptr, stack, line, JS::SavedFrameSelfHosted::Exclude);
  JS::TaggedColumnNumberOneOrigin tagged;
  if (JS::GetSavedFrameColumn(cx, nullptr, stack, &tagged, JS::SavedFrameSelfHosted::Exclude) == JS::SavedFrameResult::Ok) {
    *column = tagged.oneOriginValue();
  }
}

// Capture 取出当前待决异常存入上下文，没有可捕获的异常时记为终止执行
static void Capture(mz_context* mz) {
  JSContext* cx = mz->cx;
  CapturedException& captured = mz->exception;
  if (captured.value) {
    mz_release(mz, captured.value);
  }
  captured = CapturedException();
  captured.present = true;
  if (!JS_IsExceptionPending(cx)) {
    captured.message = mz->interrupt.load() ? "执行已中断" : "执行已终止";
    return;
  }
  JS::ExceptionStack exnStack(cx);
  if (!JS::StealPendingExceptionStack(cx, &exnStack)) {
    captured.message = "无法读取异常";
    JS_ClearPendingException(cx);
    return;
  }
  captured.value = Keep(mz, exnStack.exception());
  JS::ErrorReportBuilder report(cx);
  if (report.init(cx, exnStack, JS::ErrorReportBuilder::NoSideEffects)) {
    if (report.toStringResult()) {
      captured.message = report.toStringResult().c_str();
    }
    if (JSErrorReport* error = report.report()) {
      if (error->filename) {
        captured.filename = error->filename.c_str();
      }
      captured.line = error->lineno;
      captured.column = error->column.oneOriginValue();
    }
  }
  JS_ClearPendingException(cx);
  if (exnStack.stack()) {
    JS::RootedObject stack(cx, exnStack.stack());
    JS::RootedString text(cx);
    if (JS::BuildStackString(cx, nullptr, stack, &text) && text) {
      EncodeUTF8(cx, text, &captured.stack);
    }
    std::string file;
    uint32_t line = 0, column = 0;
    FrameLocation(cx, stack, &file, &line, &column);
    if (!file.empty() || line) {
      mz->throwFile = file;
      mz->throwLine = line;
      mz->throwColumn = column;
    }
  }
  JS_ClearPendingException(cx);
}

// ThrowNamed 以当前全局对象中名为 type 的构造器构造异常并抛出
static bool ThrowNamed(JSContext* cx, const char* type, const char* message, const char* name) {
  JS::RootedObject global(cx, JS::CurrentGlobalOrNull(cx));
  JS::RootedValue ctor(cx);
  if (!global || !JS_GetProperty(cx, global, type, &ctor) || !ctor.isObject()) {
    JS_ReportErrorUTF8(cx, "%s", message);
    return false;
  }
  JS::RootedValueArray<2> args(cx);
  JSString* text = JS_NewStringCopyUTF8Z(cx, JS::ConstUTF8CharsZ(message, strlen(message)));
  if (!text) {
    return false;
  }
  args[0].setString(text);
  unsigned argc = 1;
  if (name) {
    JSString* nameText = JS_NewStringCopyZ(cx, name);
    if (!nameText) {
      return false;
    }
    args[1].setString(nameText);
    argc = 2;
  }
  JS::RootedObject error(cx);
  if (!JS::Construct(cx, ctor, JS::HandleValueArray::subarray(args, 0, argc), &error)) {
    return false;
  }
  JS::RootedValue value(cx, JS::ObjectValue(*error));
  JS_SetPendingException(cx, value);
  return false;
}

// ReportMicrotask 报告微任务回调抛出的异常
static void ReportMicrotask(mz_context* mz, JS::HandleObject global) {
  JSContext* cx = mz->cx;
  if (!JS_IsExceptionPending(cx)) {
    return;
  }
  Capture(mz);
  uint32_t value = mz->exception.value;
  mz->exception.value = 0;
  if (!mz->microtaskReporter || !value) {
    mz_release(mz, value);
    return;
  }
  JS::RootedValue reporter(cx, Slot(mz, mz->microtaskReporter));
  JS::RootedValueArray<2> args(cx);
  args[0].setObject(*global);
  args[1].set(Slot(mz, value));
  mz_release(mz, value);
  JS::RootedValue rval(cx);
  if (!JS::Call(cx, JS::UndefinedHandleValue, reporter, args, &rval)) {
    JS_ClearPendingException(cx);
  }
}

// RunMicrotasks 依次执行微任务队列：Promise 任务在其 Realm 中执行，嵌入方任务为 queueMicrotask 的回调
static void RunMicrotasks(mz_context* mz) {
  JSContext* cx = mz->cx;
  if (mz->draining) {
    return;
  }
  mz->draining = true;
  JS::Rooted<JS::GenericMicroTask> task(cx);
  JS::Rooted<JS::JSMicroTask*> job(cx);
  JS::RootedObject global(cx);
  while (JS::HasAnyMicroTasks(cx)) {
    if (mz->interrupt.load()) {
      break;
    }
    task = JS::DequeueNextMicroTask(cx);
    if (!JS::HasAnyMicroTasks(cx)) {
      JS::JobQueueIsEmpty(cx);
    }
    if (JS::IsJSMicroTask(task)) {
      job = JS::ToMaybeWrappedJSMicroTask(task);
      global = JS::GetExecutionGlobalFromJSMicroTask(job);
      if (!global) {
        continue;
      }
      JSAutoRealm ar(cx, global);
      if (!JS::RunJSMicroTask(cx, job)) {
        ReportMicrotask(mz, global);
      }
    } else if (task.isObject()) {
      JS::RootedObject callback(cx, &task.toObject());
      global = JS::GetNonCCWObjectGlobal(callback);
      JSAutoRealm ar(cx, callback);
      JS::RootedValue fn(cx, task);
      JS::RootedValue rval(cx);
      if (!JS::Call(cx, JS::UndefinedHandleValue, fn, JS::HandleValueArray::empty(), &rval)) {
        ReportMicrotask(mz, global);
      }
    }
  }
  mz->draining = false;
}

// HostJobQueue 是使用引擎内微任务队列的宿主任务队列
class HostJobQueue : public JS::JobQueue {
 public:
  explicit HostJobQueue(mz_context* mz) : mz_(mz) {}

  bool getHostDefinedData(JSContext* cx, JS::MutableHandle<JSObject*> incumbentGlobal,
                          JS::MutableHandle<JSObject*> optionalHostDefinedData) const override {
    incumbentGlobal.set(nullptr);
    optionalHostDefinedData.set(nullptr);
    return true;
  }

  bool getHostDefinedGlobal(JSContext* cx, JS::MutableHandle<JSObject*> data) const override { return true; }

  void runJobs(JSContext* cx) override { RunMicrotasks(mz_); }

  bool isDrainingStopped() const override { return false; }

 private:
  // SavedQueue 保存调试器打断时的微任务队列
  class SavedQueue : public JS::JobQueue::SavedJobQueue {
   public:
    SavedQueue(JSContext* cx, js::UniquePtr<JS::SavedMicroTaskQueue> saved) : cx_(cx), saved_(std::move(saved)) {}
    ~SavedQueue() override { JS::RestoreMicroTaskQueue(cx_, std::move(saved_)); }

   private:
    JSContext* cx_;
    js::UniquePtr<JS::SavedMicroTaskQueue> saved_;
  };

  js::UniquePtr<SavedJobQueue> saveJobQueue(JSContext* cx) override {
    js::UniquePtr<JS::SavedMicroTaskQueue> saved = JS::SaveMicroTaskQueue(cx);
    if (!saved) {
      return nullptr;
    }
    return js::MakeUnique<SavedQueue>(cx, std::move(saved));
  }

  mz_context* mz_;
};

// TrackRejection 记录 Promise 拒绝跟踪事件
static void TrackRejection(JSContext* cx, bool mutedErrors, JS::HandleObject promise,
                           JS::PromiseRejectionHandlingState state, void* data) {
  auto* mz = static_cast<mz_context*>(data);
  mz->rejections.push_back(Rejection{JS::Heap<JSObject*>(promise.get()), state == JS::PromiseRejectionHandlingState::Handled});
}

// InterruptCallback 在中断请求时终止执行
static bool InterruptCallback(JSContext* cx) {
  auto* mz = static_cast<mz_context*>(JS_GetContextPrivate(cx));
  return !mz->interrupt.load();
}

// IgnoreWarning 丢弃引擎警告
static void IgnoreWarning(JSContext* cx, JSErrorReport* report) {}

// TrustedSource 从 TrustedScript 登记表读取对象的源码，不是 TrustedScript 时为 nullptr
static bool TrustedSource(JSContext* cx, mz_context* mz, JS::HandleValue value, JS::MutableHandleString out) {
  out.set(nullptr);
  if (!mz->trustedRegistry || !value.isObject()) {
    return true;
  }
  JS::RootedObject registry(cx, &Slot(mz, mz->trustedRegistry).toObject());
  JS::RootedValue entry(cx);
  if (!JS::GetWeakMapEntry(cx, registry, value, &entry)) {
    return false;
  }
  if (entry.isString()) {
    out.set(entry.toString());
  }
  return true;
}

// CodeForEvalGets 把 eval 的 TrustedScript 参数解包为源码
static bool CodeForEvalGets(JSContext* cx, JS::HandleObject code, JS::MutableHandle<JSString*> outCode) {
  auto* mz = static_cast<mz_context*>(JS_GetContextPrivate(cx));
  JS::RootedValue value(cx, JS::ObjectValue(*code));
  return TrustedSource(cx, mz, value, outCode);
}

// ContentSecurityPolicyAllows 在页面要求 Trusted Types 时只允许以 TrustedScript 编译代码
static bool ContentSecurityPolicyAllows(JSContext* cx, JS::RuntimeCode kind, JS::Handle<JSString*> codeString,
                                        JS::CompilationType compilationType,
                                        JS::Handle<JS::StackGCVector<JSString*>> parameterStrings,
                                        JS::Handle<JSString*> bodyString,
                                        JS::Handle<JS::StackGCVector<JS::Value>> parameterArgs,
                                        JS::Handle<JS::Value> bodyArg, bool* outCanCompileStrings) {
  auto* mz = static_cast<mz_context*>(JS_GetContextPrivate(cx));
  *outCanCompileStrings = true;
  if (!mz->blockStrings || kind != JS::RuntimeCode::JS || compilationType == JS::CompilationType::Undefined) {
    return true;
  }
  if (compilationType == JS::CompilationType::Function && parameterArgs.empty() && bodyArg.isUndefined()) {
    return true;
  }
  JS::RootedString source(cx);
  if (!TrustedSource(cx, mz, bodyArg, &source)) {
    return false;
  }
  bool allowed = source != nullptr;
  if (compilationType == JS::CompilationType::Function) {
    JS::RootedValue parameter(cx);
    for (size_t index = 0; allowed && index < parameterArgs.length(); index++) {
      parameter = parameterArgs[index];
      if (!TrustedSource(cx, mz, parameter, &source)) {
        return false;
      }
      allowed = source != nullptr;
    }
  }
  *outCanCompileStrings = allowed;
  return true;
}

// SecurityCallbacks 是 Trusted Types 与 CSP 的引擎回调
static const JSSecurityCallbacks SecurityCallbacks = {ContentSecurityPolicyAllows, CodeForEvalGets, nullptr};

extern "C" const char* mz_init(void) { return JS_InitWithFailureDiagnostic(); }

extern "C" mz_context* mz_context_new(uintptr_t id, uint32_t heap_bytes, size_t stack_quota, const char* locale) {
  JSContext* cx = JS_NewContext(heap_bytes ? heap_bytes : JS::DefaultHeapMaxBytes);
  if (!cx) {
    return nullptr;
  }
  auto* mz = new mz_context();
  mz->cx = cx;
  mz->id = id;
  JS_SetContextPrivate(cx, mz);
  if (stack_quota) {
    JS_SetNativeStackQuota(cx, JS::NativeStackSize(stack_quota));
  }
  if (!JS::InitSelfHostedCode(cx)) {
    JS_DestroyContext(cx);
    delete mz;
    return nullptr;
  }
  mz->jobQueue = new HostJobQueue(mz);
  JS::SetJobQueue(cx, mz->jobQueue);
  JS::SetPromiseRejectionTrackerCallback(cx, TrackRejection, mz);
  JS_AddInterruptCallback(cx, InterruptCallback);
  JS_AddExtraGCRootsTracer(cx, TraceRoots, mz);
  JS::SetWarningReporter(cx, IgnoreWarning);
  JS_SetSecurityCallbacks(cx, &SecurityCallbacks);
  JS::ContextOptionsRef(cx).setAsyncStackCaptureDebuggeeOnly(true);
  if (locale && *locale) {
    JS_SetDefaultLocale(JS_GetRuntime(cx), locale);
  }
  return mz;
}

extern "C" void mz_context_free(mz_context* mz) {
  if (!mz) {
    return;
  }
  for (auto& slot : mz->slots) {
    slot = JS::UndefinedValue();
  }
  for (auto& rejection : mz->rejections) {
    rejection.promise = nullptr;
  }
  JS_RemoveExtraGCRootsTracer(mz->cx, TraceRoots, mz);
  mz->slots.clear();
  mz->rejections.clear();
  JS_DestroyContext(mz->cx);
  delete mz->jobQueue;
  delete mz;
}

extern "C" void mz_set_camou_locale(const char* language, const char* region, const char* locale) {
  mozjs_set_camou_locale(language, region, locale);
}

extern "C" uint32_t mz_global_new(mz_context* mz, uint32_t same, const char* timezone) {
  JSContext* cx = mz->cx;
  JS::RealmOptions options;
  options.creationOptions().setSharedMemoryAndAtomicsEnabled(true).setDefineSharedArrayBufferConstructor(false);
  if (same) {
    options.creationOptions().setExistingCompartment(&Slot(mz, same).toObject());
  } else {
    options.creationOptions().setNewCompartmentAndZone();
  }
  if (timezone && *timezone) {
    options.behaviors().setTimeZoneOverride(timezone);
  }
  JS::RootedObject global(cx, JS_NewGlobalObject(cx, &WindowClass, nullptr, JS::DontFireOnNewGlobalHook, options));
  if (!global) {
    Capture(mz);
    return 0;
  }
  JSAutoRealm ar(cx, global);
  JS_FireOnNewGlobalObject(cx, global);
  return Keep(mz, JS::ObjectValue(*global));
}

extern "C" void mz_release(mz_context* mz, uint32_t handle) {
  if (handle == 0 || handle > mz->slots.size()) {
    return;
  }
  mz->slots[handle - 1] = JS::UndefinedValue();
  mz->freeSlots.push_back(handle - 1);
}

extern "C" void mz_free(void* pointer) { free(pointer); }

extern "C" void mz_clear_kept_objects(mz_context* mz) { JS::ClearKeptObjects(mz->cx); }

extern "C" bool mz_evaluate(mz_context* mz, uint32_t global, const char* source, size_t len, const char* filename,
                            uint32_t line, mz_value* out) {
  JSContext* cx = mz->cx;
  JS::RootedObject target(cx, &Slot(mz, global).toObject());
  JSAutoRealm ar(cx, target);
  JS::CompileOptions options(cx);
  options.setFileAndLine(filename, line);
  JS::SourceText<mozilla::Utf8Unit> text;
  JS::RootedValue rval(cx);
  if (!text.init(cx, source, len, JS::SourceOwnership::Borrowed) || !JS::Evaluate(cx, options, text, &rval) ||
      !FromJS(mz, cx, rval, out)) {
    Capture(mz);
    return false;
  }
  return true;
}

extern "C" bool mz_call_function(mz_context* mz, uint32_t fn, uint32_t thisv, const mz_value* args, int argc,
                                 mz_value* out) {
  JSContext* cx = mz->cx;
  JS::RootedValue callee(cx, Slot(mz, fn));
  if (!callee.isObject()) {
    JS::RootedObject global(cx, &mz->slots.front().get().toObject());
    JSAutoRealm ar(cx, global);
    JS_ReportErrorASCII(cx, "callee is not an object");
    Capture(mz);
    return false;
  }
  JS::RootedObject calleeObject(cx, &callee.toObject());
  JSAutoRealm ar(cx, calleeObject);
  JS::RootedValueVector argv(cx);
  JS::RootedValue item(cx);
  for (int index = 0; index < argc; index++) {
    if (!ToJS(mz, cx, args[index], &item) || !argv.append(item)) {
      Capture(mz);
      return false;
    }
  }
  JS::RootedValue self(cx, Slot(mz, thisv));
  JS::RootedValue rval(cx);
  if (!JS_WrapValue(cx, &self) || !JS::Call(cx, self, callee, argv, &rval) || !FromJS(mz, cx, rval, out)) {
    Capture(mz);
    return false;
  }
  return true;
}

extern "C" bool mz_get_property(mz_context* mz, uint32_t object, const char* name, mz_value* out) {
  JSContext* cx = mz->cx;
  JS::RootedObject target(cx, &Slot(mz, object).toObject());
  JSAutoRealm ar(cx, target);
  JS::RootedValue value(cx);
  if (!JS_GetProperty(cx, target, name, &value) || !FromJS(mz, cx, value, out)) {
    Capture(mz);
    return false;
  }
  return true;
}

extern "C" bool mz_set_property(mz_context* mz, uint32_t object, const char* name, const mz_value* value) {
  JSContext* cx = mz->cx;
  JS::RootedObject target(cx, &Slot(mz, object).toObject());
  JSAutoRealm ar(cx, target);
  JS::RootedValue item(cx);
  if (!ToJS(mz, cx, *value, &item) || !JS_SetProperty(cx, target, name, item)) {
    Capture(mz);
    return false;
  }
  return true;
}

extern "C" uint32_t mz_new_object(mz_context* mz, uint32_t global) {
  JSContext* cx = mz->cx;
  JS::RootedObject target(cx, &Slot(mz, global).toObject());
  JSAutoRealm ar(cx, target);
  JSObject* object = JS_NewPlainObject(cx);
  if (!object) {
    Capture(mz);
    return 0;
  }
  return Keep(mz, JS::ObjectValue(*object));
}

extern "C" bool mz_parse_json(mz_context* mz, uint32_t global, const char* json, size_t len, mz_value* out) {
  JSContext* cx = mz->cx;
  JS::RootedObject target(cx, &Slot(mz, global).toObject());
  JSAutoRealm ar(cx, target);
  JS::RootedString text(cx, JS_NewStringCopyUTF8N(cx, JS::UTF8Chars(json, len)));
  JS::RootedValue value(cx);
  if (!text || !JS_ParseJSON(cx, text, &value) || !FromJS(mz, cx, value, out)) {
    Capture(mz);
    return false;
  }
  return true;
}

// GoTrampoline 把原生调用转给 Go，函数保留槽 0 为 Go 原生函数编号
static bool GoTrampoline(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  int32_t native = js::GetFunctionNativeReserved(&args.callee(), 0).toInt32();
  auto* mz = static_cast<mz_context*>(JS_GetContextPrivate(cx));
  mz_call call{cx, &args, mz};
  args.rval().setUndefined();
  return mzGoNative(mz->id, native, &call);
}

extern "C" uint32_t mz_new_function(mz_context* mz, uint32_t global, const char* name, uint32_t nargs, int32_t native) {
  JSContext* cx = mz->cx;
  JS::RootedObject target(cx, &Slot(mz, global).toObject());
  JSAutoRealm ar(cx, target);
  JSFunction* fn = js::NewFunctionWithReserved(cx, GoTrampoline, nargs, 0, name);
  if (!fn) {
    Capture(mz);
    return 0;
  }
  js::SetFunctionNativeReserved(JS_GetFunctionObject(fn), 0, JS::Int32Value(native));
  return Keep(mz, JS::ObjectValue(*JS_GetFunctionObject(fn)));
}

extern "C" void mz_set_trusted_types(mz_context* mz, uint32_t registry, bool block_strings) {
  mz->trustedRegistry = registry;
  mz->blockStrings = block_strings;
}

extern "C" bool mz_call_arg(mz_call* call, uint32_t index, mz_value* out) {
  return FromJS(call->mz, call->cx, call->args->get(index), out);
}

extern "C" bool mz_call_arg_bytes(mz_call* call, uint32_t index, uint8_t** data, size_t* len) {
  JS::HandleValue value = call->args->get(index);
  *data = nullptr;
  *len = 0;
  if (!value.isObject()) {
    return false;
  }
  JSObject* object = js::CheckedUnwrapStatic(&value.toObject());
  if (!object) {
    return false;
  }
  if (JS::IsArrayBufferObject(object)) {
    size_t length = 0;
    bool shared = false;
    uint8_t* bytes = nullptr;
    JS::GetArrayBufferLengthAndData(object, &length, &shared, &bytes);
    *data = static_cast<uint8_t*>(malloc(length ? length : 1));
    if (length) {
      memcpy(*data, bytes, length);
    }
    *len = length;
    return true;
  }
  if (JS_IsArrayBufferViewObject(object)) {
    size_t length = JS_GetArrayBufferViewByteLength(object);
    *data = static_cast<uint8_t*>(malloc(length ? length : 1));
    bool shared = false;
    JS::AutoCheckCannotGC nogc;
    void* bytes = JS_GetArrayBufferViewData(object, &shared, nogc);
    if (length) {
      memcpy(*data, bytes, length);
    }
    *len = length;
    return true;
  }
  return false;
}

extern "C" bool mz_call_return(mz_call* call, const mz_value* value) {
  JS::RootedValue item(call->cx);
  if (!ToJS(call->mz, call->cx, *value, &item)) {
    return false;
  }
  call->args->rval().set(item);
  return true;
}

extern "C" bool mz_call_return_bytes(mz_call* call, const uint8_t* data, size_t len) {
  JSContext* cx = call->cx;
  JSObject* buffer;
  if (len == 0) {
    buffer = JS::NewArrayBuffer(cx, 0);
  } else {
    void* contents = JS_malloc(cx, len);
    if (!contents) {
      return false;
    }
    memcpy(contents, data, len);
    buffer = JS::NewArrayBufferWithContents(cx, len, mozilla::UniquePtr<void, JS::FreePolicy>(contents));
  }
  if (!buffer) {
    return false;
  }
  call->args->rval().setObject(*buffer);
  return true;
}

extern "C" void mz_call_throw(mz_call* call, const char* type, const char* message) {
  ThrowNamed(call->cx, type, message, nullptr);
}

extern "C" bool mz_take_exception(mz_context* mz, mz_exception* out) {
  memset(out, 0, sizeof(*out));
  CapturedException& captured = mz->exception;
  if (!captured.present) {
    return false;
  }
  out->message = CopyString(captured.message.data(), captured.message.size());
  out->stack = CopyString(captured.stack.data(), captured.stack.size());
  out->filename = CopyString(captured.filename.data(), captured.filename.size());
  out->line = captured.line;
  out->column = captured.column;
  out->value = captured.value;
  captured = CapturedException();
  return true;
}

extern "C" void mz_set_throw_location(mz_context* mz, const char* filename, uint32_t line, uint32_t column) {
  mz->throwFile = filename ? filename : "";
  mz->throwLine = line;
  mz->throwColumn = column;
}

extern "C" void mz_run_jobs(mz_context* mz) {
  RunMicrotasks(mz);
  JS::ClearKeptObjects(mz->cx);
}

extern "C" void mz_set_microtask_reporter(mz_context* mz, uint32_t report) { mz->microtaskReporter = report; }

extern "C" bool mz_flush_rejections(mz_context* mz, uint32_t fn) {
  if (mz->rejections.empty()) {
    return true;
  }
  JSContext* cx = mz->cx;
  JS::RootedObjectVector promises(cx);
  std::vector<bool> handled;
  for (auto& rejection : mz->rejections) {
    if (!promises.append(rejection.promise.get())) {
      return false;
    }
    handled.push_back(rejection.handled);
  }
  for (auto& rejection : mz->rejections) {
    rejection.promise = nullptr;
  }
  mz->rejections.clear();
  JS::RootedValue callee(cx, Slot(mz, fn));
  JS::RootedObject calleeObject(cx, &callee.toObject());
  JSAutoRealm ar(cx, calleeObject);
  JS::RootedValueArray<3> args(cx);
  JS::RootedValue rval(cx);
  for (size_t index = 0; index < promises.length(); index++) {
    args[0].setObject(*promises[index]);
    args[1].setBoolean(handled[index]);
    args[2].setObject(*JS::GetNonCCWObjectGlobal(promises[index]));
    if (!JS_WrapValue(cx, args[0]) || !JS_WrapValue(cx, args[2]) ||
        !JS::Call(cx, JS::UndefinedHandleValue, callee, args, &rval)) {
      Capture(mz);
      return false;
    }
  }
  return true;
}

extern "C" void mz_request_interrupt(mz_context* mz) {
  mz->interrupt.store(true);
  JS_RequestInterruptCallback(mz->cx);
}

extern "C" double mz_now(void) {
  auto elapsed = std::chrono::steady_clock::now().time_since_epoch();
  return double(std::chrono::duration_cast<std::chrono::microseconds>(elapsed).count()) / 1000.0;
}

extern "C" void mz_gc(mz_context* mz) { JS_GC(mz->cx); }

extern "C" size_t mz_thread_stack_size(void) {
#ifdef _WIN32
  ULONG_PTR low = 0, high = 0;
  GetCurrentThreadStackLimits(&low, &high);
  return size_t(high - low);
#elif defined(__APPLE__)
  return pthread_get_stacksize_np(pthread_self());
#else
  pthread_attr_t attr;
  size_t size = 0;
  void* base = nullptr;
  if (pthread_getattr_np(pthread_self(), &attr) == 0) {
    pthread_attr_getstack(&attr, &base, &size);
    pthread_attr_destroy(&attr);
  }
  return size;
#endif
}

// HostNow 返回单调时钟毫秒数，精度为微秒
static bool HostNow(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  args.rval().setDouble(mz_now());
  return true;
}

// KindName 按内部类返回对象种类名
static const char* KindName(JSContext* cx, JS::HandleObject object, std::string* className) {
  if (js::IsScriptedProxy(object)) {
    return "Proxy";
  }
  bool isArray = false;
  if (JS::IsArrayObject(cx, object, &isArray) && isArray) {
    return "Array";
  }
  if (JS::IsArrayBufferObject(object)) {
    return "ArrayBuffer";
  }
  if (JS_IsTypedArrayObject(object)) {
    return "TypedArray";
  }
  if (JS_IsArrayBufferViewObject(object)) {
    return "DataView";
  }
  if (JS::IsWeakMapObject(object)) {
    return "WeakMap";
  }
  js::ESClass cls = js::ESClass::Other;
  if (!JS::GetBuiltinClass(cx, object, &cls)) {
    JS_ClearPendingException(cx);
  }
  switch (cls) {
    case js::ESClass::Map: return "Map";
    case js::ESClass::Set: return "Set";
    case js::ESClass::Date: return "Date";
    case js::ESClass::RegExp: return "RegExp";
    case js::ESClass::Error: return "Error";
    case js::ESClass::Promise: return "Promise";
    case js::ESClass::Arguments: return "Arguments";
    case js::ESClass::String: return "String";
    case js::ESClass::Boolean: return "Boolean";
    case js::ESClass::Number: return "Number";
    case js::ESClass::BigInt: return "BigInt";
    case js::ESClass::Function: return "Function";
    default: break;
  }
  if (JS::IsCallable(object)) {
    return "Function";
  }
  const char* name = JS::GetClass(object)->name;
  if (strcmp(name, "WeakSet") == 0 || strcmp(name, "Symbol") == 0) {
    return name;
  }
  if (strstr(name, "Iterator") || strstr(name, "Generator")) {
    className->assign(name);
    return className->c_str();
  }
  return "Object";
}

// HostKindOf 返回值的内部种类，原始值为空串
static bool HostKindOf(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  const char* kind = "";
  std::string className;
  if (args.get(0).isObject()) {
    JS::RootedObject object(cx, &args[0].toObject());
    kind = KindName(cx, object, &className);
  }
  JSString* str = JS_NewStringCopyZ(cx, kind);
  if (!str) {
    return false;
  }
  args.rval().setString(str);
  return true;
}

// HostThrowLocation 返回最近一次记录的抛出位置 [文件, 行, 列]
static bool HostThrowLocation(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  auto* mz = static_cast<mz_context*>(JS_GetContextPrivate(cx));
  JS::RootedValueArray<3> items(cx);
  JSString* file = JS_NewStringCopyUTF8N(cx, JS::UTF8Chars(mz->throwFile.data(), mz->throwFile.size()));
  if (!file) {
    return false;
  }
  items[0].setString(file);
  items[1].setNumber(mz->throwLine);
  items[2].setNumber(mz->throwColumn);
  JSObject* array = JS::NewArrayObject(cx, items);
  if (!array) {
    return false;
  }
  args.rval().setObject(*array);
  return true;
}

// HostCallListener 调用事件监听器并在异常离开回调时保存来源位置
static bool HostCallListener(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  if (JS::Call(cx, args[1], args[0], JS::HandleValueArray::subarray(args, 2, argc - 2), args.rval())) {
    return true;
  }
  auto* mz = static_cast<mz_context*>(JS_GetContextPrivate(cx));
  JS::ExceptionStack stack(cx);
  if (!JS::GetPendingExceptionStack(cx, &stack)) {
    return false;
  }
  JS::RootedObject frame(cx, stack.stack());
  FrameLocation(cx, frame, &mz->throwFile, &mz->throwLine, &mz->throwColumn);
  if (mz->throwFile.empty()) {
    JS::AutoFilename filename;
    JS::ColumnNumberOneOrigin column;
    JS::DescribeScriptedCaller(&filename, cx, &mz->throwLine, &column);
    mz->throwColumn = column.oneOriginValue();
    if (filename.get()) {
      mz->throwFile = filename.get();
    }
  }
  return false;
}

// HostIncumbent 返回调用栈上最近一帧非宿主脚本所属 Realm 的全局对象
static bool HostIncumbent(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  JS::RootedObject global(cx, JS::GetScriptedCallerGlobal(cx));
  if (!global) {
    args.rval().setUndefined();
    return true;
  }
  args.rval().setObject(*global);
  return JS_WrapValue(cx, args.rval());
}

// QueueMicrotask 实现 Window.queueMicrotask
static bool QueueMicrotask(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  if (argc < 1) {
    return ThrowNamed(cx, "TypeError", "Window.queueMicrotask: At least 1 argument required, but only 0 passed", nullptr);
  }
  if (!args[0].isObject() || !JS::IsCallable(&args[0].toObject())) {
    return ThrowNamed(cx, "TypeError", "Window.queueMicrotask: Argument 1 is not callable.", nullptr);
  }
  JS::JobQueueMayNotBeEmpty(cx);
  if (!JS::EnqueueMicroTask(cx, args[0])) {
    return false;
  }
  args.rval().setUndefined();
  return true;
}

// Base64Alphabet 是标准 base64 字母表
static const char Base64Alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

// InvalidCharacter 抛出 InvalidCharacterError
static bool InvalidCharacter(JSContext* cx) {
  return ThrowNamed(cx, "DOMException", "String contains an invalid character", "InvalidCharacterError");
}

// ArgumentChars 把第一个参数按 DOMString 转为 UTF-16 码元
static bool ArgumentChars(JSContext* cx, JS::CallArgs& args, const char* method, std::u16string* out) {
  if (args.length() < 1) {
    std::string message = std::string("Window.") + method + ": At least 1 argument required, but only 0 passed";
    return ThrowNamed(cx, "TypeError", message.c_str(), nullptr);
  }
  JS::RootedString str(cx, JS::ToString(cx, args[0]));
  if (!str) {
    return false;
  }
  size_t length = JS_GetStringLength(str);
  out->resize(length);
  return length == 0 || JS_CopyStringChars(cx, mozilla::Range<char16_t>(out->data(), length), str);
}

// Btoa 实现 Window.btoa
static bool Btoa(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  std::u16string input;
  if (!ArgumentChars(cx, args, "btoa", &input)) {
    return false;
  }
  std::string output;
  output.reserve((input.size() + 2) / 3 * 4);
  for (size_t index = 0; index < input.size(); index += 3) {
    uint32_t chunk = 0;
    size_t count = std::min<size_t>(3, input.size() - index);
    for (size_t offset = 0; offset < 3; offset++) {
      char16_t unit = offset < count ? input[index + offset] : 0;
      if (unit > 0xff) {
        return InvalidCharacter(cx);
      }
      chunk = (chunk << 8) | unit;
    }
    output.push_back(Base64Alphabet[(chunk >> 18) & 63]);
    output.push_back(Base64Alphabet[(chunk >> 12) & 63]);
    output.push_back(count > 1 ? Base64Alphabet[(chunk >> 6) & 63] : '=');
    output.push_back(count > 2 ? Base64Alphabet[chunk & 63] : '=');
  }
  JSString* str = JS_NewStringCopyN(cx, output.data(), output.size());
  if (!str) {
    return false;
  }
  args.rval().setString(str);
  return true;
}

// Atob 按 WHATWG forgiving-base64 解码实现 Window.atob
static bool Atob(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  std::u16string input;
  if (!ArgumentChars(cx, args, "atob", &input)) {
    return false;
  }
  std::u16string data;
  for (char16_t unit : input) {
    if (unit != ' ' && unit != '\t' && unit != '\n' && unit != '\f' && unit != '\r') {
      data.push_back(unit);
    }
  }
  if (data.size() % 4 == 0 && !data.empty() && data.back() == '=') {
    data.pop_back();
    if (!data.empty() && data.back() == '=') {
      data.pop_back();
    }
  }
  if (data.size() % 4 == 1) {
    return InvalidCharacter(cx);
  }
  std::string output;
  uint32_t buffer = 0;
  int bits = 0;
  for (char16_t unit : data) {
    const char* found = unit < 128 && unit ? strchr(Base64Alphabet, char(unit)) : nullptr;
    if (!found) {
      return InvalidCharacter(cx);
    }
    buffer = (buffer << 6) | uint32_t(found - Base64Alphabet);
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      output.push_back(char((buffer >> bits) & 0xff));
    }
  }
  JSString* str = JS_NewStringCopyN(cx, output.data(), output.size());
  if (!str) {
    return false;
  }
  args.rval().setString(str);
  return true;
}

// HostPromiseResult 返回已敲定 Promise 的结果
static bool HostPromiseResult(JSContext* cx, unsigned argc, JS::Value* vp) {
  JS::CallArgs args = JS::CallArgsFromVp(argc, vp);
  JS::RootedObject promise(cx, args.get(0).isObject() ? &args[0].toObject() : nullptr);
  if (!promise || !JS::IsPromiseObject(promise)) {
    args.rval().setUndefined();
    return true;
  }
  args.rval().set(JS::GetPromiseResult(promise));
  return JS_WrapValue(cx, args.rval());
}

extern "C" bool mz_install_host_natives(mz_context* mz, uint32_t target) {
  JSContext* cx = mz->cx;
  JS::RootedObject object(cx, &Slot(mz, target).toObject());
  JSAutoRealm ar(cx, object);
  if (!JS_DefineFunction(cx, object, "now", HostNow, 0, 0) || !JS_DefineFunction(cx, object, "kindOf", HostKindOf, 1, 0) ||
      !JS_DefineFunction(cx, object, "throwLocation", HostThrowLocation, 0, 0) ||
      !JS_DefineFunction(cx, object, "incumbent", HostIncumbent, 0, 0) ||
      !JS_DefineFunction(cx, object, "lazyGlobals", HostLazyGlobals, 3, 0) ||
      !JS_DefineFunction(cx, object, "callListener", HostCallListener, 3, 0) ||
      !JS_DefineFunction(cx, object, "promiseResult", HostPromiseResult, 1, 0)) {
    Capture(mz);
    return false;
  }
  return true;
}

extern "C" bool mz_install_window_natives(mz_context* mz, uint32_t global) {
  JSContext* cx = mz->cx;
  JS::RootedObject object(cx, &Slot(mz, global).toObject());
  JSAutoRealm ar(cx, object);
  unsigned attrs = JSPROP_ENUMERATE;
  if (!JS_DefineFunction(cx, object, "queueMicrotask", QueueMicrotask, 1, attrs) ||
      !JS_DefineFunction(cx, object, "atob", Atob, 1, attrs) || !JS_DefineFunction(cx, object, "btoa", Btoa, 1, attrs)) {
    Capture(mz);
    return false;
  }
  return true;
}
