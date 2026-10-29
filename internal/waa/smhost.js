// smhost.js 为一个 Realm 组装宿主安装脚本的 input：计时器、任务、模板对象、品牌检查、Promise 拒绝通知与 Go 原生函数
(function buildHost(natives, installDOM, install, config, shared) {
  'use strict';
  const g = globalThis;
  const { Object, Array, Number, String, Boolean, Map, Set, WeakMap, Symbol, Math, JSON, Reflect, ArrayBuffer } = g;
  const defineProperty = Object.defineProperty;
  const getOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
  const getOwnPropertyNames = Object.getOwnPropertyNames;
  const apply = Reflect.apply;
  const parseJSON = JSON.parse;
  const mathFloor = Math.floor;
  const mathTrunc = Math.trunc;
  const arrayBufferTransfer = ArrayBuffer.prototype.transfer;
  const arrayBufferDetached = getOwnPropertyDescriptor(ArrayBuffer.prototype, 'detached').get;
  const indirectEval = g.eval;
  const now = natives.now;
  if (!shared) {
    shared = {
      registry: { native: new WeakMap(), trusted: new WeakMap() },
      shape: parseJSON(config.shapeJSON),
      brands: new WeakMap(),
      storage: {},
      topAPI: null,
      notifiers: new Map(),
      reporters: new Map(),
      rejections: { pending: [], handled: [], notified: new Map() },
    };
  }
  const registry = shared.registry;
  const brands = shared.brands;
  const realmName = config.realm;
  const started = config.started;
  let lazy = null;
  const parents = Object.create(null);
  const aliases = { Image: 'HTMLImageElement', Audio: 'HTMLAudioElement', Option: 'HTMLOptionElement' };
  const factories = { HTMLImageElement: 'Image', HTMLAudioElement: 'Audio', HTMLOptionElement: 'Option' };
  const numberNames = ['isNaN', 'isFinite', 'parseInt', 'parseFloat', 'NaN', 'Infinity', 'Number'];
  const stringNames = ['escape', 'unescape', 'decodeURI', 'encodeURI', 'decodeURIComponent', 'encodeURIComponent', 'String'];
  // resolve 在首次使用时恢复名称及同组标准属性、接口祖先与工厂函数
  const resolve = name => {
    if (!lazy || !lazy.has(name)) return false;
    const define = key => {
      const descriptor = lazy.get(key);
      if (descriptor === undefined) return;
      lazy.delete(key);
      defineProperty(g, key, descriptor);
    };
    if (aliases[name]) name = aliases[name];
    if (numberNames.includes(name)) {
      for (const key of numberNames) define(key);
    } else if (stringNames.includes(name)) {
      for (const key of stringNames) define(key);
    } else {
      if (/^(Internal|Aggregate|Eval|Range|Reference|Suppressed|Syntax|Type|URI)Error$/.test(name) || name === 'WebAssembly') resolve('Error');
      if (parents[name]) resolve(parents[name]);
      define(name);
      if (factories[name]) define(factories[name]);
    }
    return true;
  };

  const tasks = new Map();
  let nextTask = 0;
  // schedule 在 delay 毫秒后由事件循环执行 fn
  const schedule = (delay, fn) => {
    const id = ++nextTask;
    tasks.set(id, fn);
    natives.schedule(delay, id);
  };
  // dispatch 执行事件循环到期的任务，异常交给 Go 报告
  const dispatch = (id, argument) => {
    const fn = tasks.get(id);
    if (fn === undefined) return;
    tasks.delete(id);
    fn(argument);
  };

  const active = new Set();
  let nextTimer = 0;
  let nesting = 0;
  // toInteger 按 ToIntegerOrInfinity 取整，NaN 为 0
  const toInteger = value => {
    const number = +value;
    return number !== number ? 0 : mathTrunc(number);
  };
  // startTimer 按 Firefox 的嵌套钳制规则启动 setTimeout 或 setInterval
  const startTimer = (handler, delayValue, args, repeat) => {
    const callable = typeof handler === 'function';
    let source = '';
    if (!callable) {
      const trusted = handler !== null && typeof handler === 'object' ? registry.trusted.get(handler) : undefined;
      if (trusted === undefined && config.trustedTypes) {
        throw new TypeError('Window.' + (repeat ? 'setInterval' : 'setTimeout') + ': Sink type mismatch violation blocked by CSP');
      }
      source = trusted === undefined ? String(handler) : trusted;
    }
    const id = ++nextTimer;
    active.add(id);
    let delay = toInteger(delayValue);
    if (delay < 0) delay = 0;
    if ((repeat || nesting >= 4) && delay < 4) delay = 4;
    const level = nesting + 1;
    const fire = () => {
      if (!active.has(id)) return;
      if (repeat) schedule(delay, fire);
      else active.delete(id);
      const previous = nesting;
      nesting = level;
      try {
        if (callable) apply(handler, g, args);
        else indirectEval(source);
      } finally {
        nesting = previous;
      }
    };
    schedule(delay, fire);
    return id;
  };

  const ancestors = Object.create(null);
  // isA 判断 iface 是否为 target 或继承自 target
  const isA = (iface, target) => {
    const set = ancestors[iface];
    return set ? set.has(target) : iface === target;
  };
  const templates = [];
  let resolver = null;
  // materialize 按模板键顺序定义已创建原型的成员
  const materialize = () => {
    for (const [object, id, keys] of templates.splice(0)) {
      for (let index = 0; index < keys.length; index++) {
        const key = keys[index];
        const [accessor, value, get, set, writable, enumerable, configurable] = resolver(id, index);
        const name = typeof key === 'string' && key.startsWith('@@') ? Symbol[key.slice(2)] : key;
        defineProperty(object, name, accessor ? { get, set, enumerable, configurable } : { value, writable, enumerable, configurable });
      }
    }
  };
  // orderKeys 把自有字符串键从首个与 names 顺序不同的位置起按 names 顺序重新定义
  const orderKeys = (object, names) => {
    const wanted = new Set(names);
    const current = getOwnPropertyNames(object).filter(name => wanted.has(name));
    const desired = names.filter(name => current.includes(name));
    let first = 0;
    while (first < desired.length && current[first] === desired[first]) first++;
    for (let index = first; index < desired.length; index++) {
      const name = desired[index];
      const descriptor = getOwnPropertyDescriptor(object, name);
      if (!descriptor || !descriptor.configurable) continue;
      delete object[name];
      defineProperty(object, name, descriptor);
    }
  };
  // record 解析 Go 返回的 URL 记录
  const record = text => text === null ? null : parseJSON(text);

  const input = {
    markNative(fn, source) {
      if (fn !== null && (typeof fn === 'object' || typeof fn === 'function')) registry.native.set(fn, String(source));
      return fn;
    },
    nativeSource(object) {
      return object !== null && (typeof object === 'object' || typeof object === 'function') ? registry.native.get(object) : undefined;
    },
    trust(object, text) {
      if (object !== null && (typeof object === 'object' || typeof object === 'function')) registry.trusted.set(object, String(text));
    },
    timers: {
      setTimeout: (callback, delay, ...args) => startTimer(callback, delay, args, false),
      setInterval: (callback, delay, ...args) => startTimer(callback, delay, args, true),
      clear: id => { active.delete(toInteger(id)); },
    },
    installDOM,
    shape: shared.shape,
    brands,
    interfaceParents(table) {
      Object.assign(parents, table);
      for (const name of Object.keys(table)) {
        const set = new Set([name]);
        for (let parent = table[name]; parent; parent = table[String(parent)]) set.add(String(parent));
        ancestors[name] = set;
      }
      materialize();
    },
    isA: (iface, target) => isA(String(iface), String(target)),
    brandCheck(self, iface) {
      if (self === undefined || self === null) self = g;
      if (typeof self !== 'object' && typeof self !== 'function') return null;
      const brand = brands.get(self);
      if (brand && brand.iface !== undefined && isA(String(brand.iface), String(iface))) return brand;
      return null;
    },
    storage: shared.storage,
    realm: realmName,
    profile: parseJSON(config.profileJSON),
    timeOrigin: config.timeOrigin,
    performanceNow: () => mathFloor(now() - started),
    preciseNow: () => mathFloor((now() - started) * 1000) / 1000,
    queueTask(delay, callback) {
      const wait = +delay;
      schedule(wait > 0 ? wait : 0, () => { callback(); });
    },
    incumbent: natives.incumbent,
    kindOf: natives.kindOf,
    transferBuffer: (buffer, length) => apply(arrayBufferTransfer, buffer, [length]),
    bufferDetached: buffer => apply(arrayBufferDetached, buffer, []),
    throwLocation: natives.throwLocation,
    callListener: natives.callListener,
    digest: (name, data) => natives.digest(String(name), data),
    encodingName: label => natives.encodingName(String(label)),
    templatedObject(id, keys, proto) {
      const object = Object.create(proto);
      templates.push([object, id, keys]);
      return object;
    },
    setTemplateResolver(resolve) { resolver = resolve; },
    parseURL: (text, base) => record(natives.parseURL(String(text), base === undefined ? undefined : String(base))),
    updateURL: (href, field, value) => record(natives.updateURL(String(href), String(field), String(value))),
    randomUUID: () => natives.randomUUID(),
    randomBytes: size => natives.randomBytes(toInteger(size)),
    decodeText: (name, data, fatal) => natives.decodeText(String(name), data, Boolean(fatal)),
    exposed(value) {
      const brand = value !== null && (typeof value === 'object' || typeof value === 'function') ? brands.get(value) : undefined;
      if (brand) resolve(brand.iface);
      return value;
    },
    resolveInterface: resolve,
    orderKeys,
  };
  if (shared.topAPI) input.topRealm = shared.topAPI;
  if (natives.createIframeWindow) input.createIframeWindow = () => natives.createIframeWindow();
  if (natives.loadImage) {
    input.loadImage = (address, callback) => {
      const id = ++nextTask;
      tasks.set(id, loaded => { try { callback(loaded); } catch (error) {} });
      natives.loadImage(String(address), id);
    };
  }
  const api = install(input);
  if (realmName === 'frame') {
    const split = shared.shape.frame.freshKeys.indexOf('Function');
    orderKeys(g, shared.shape.frame.freshKeys.slice(split));
    const names = shared.shape.frame.freshKeys.slice(0, split);
    lazy = new Map();
    for (const name of names) {
      if (name !== 'undefined') lazy.set(name, getOwnPropertyDescriptor(g, name));
    }
    const enumerate = enumerableOnly => {
      if (enumerableOnly) return names.filter(name => lazy.has(name) && lazy.get(name).enumerable);
      const result = names.filter(name => name === 'undefined' || lazy.has(name));
      resolve('globalThis');
      return result;
    };
    natives.lazyGlobals(resolve, enumerate, names.filter(name => name !== 'undefined'));
  }
  if (realmName === 'top') shared.topAPI = api;
  shared.notifiers.set(g, api.reportRejection);
  shared.reporters.set(g, api.reportException);
  const queue = shared.rejections;
  return {
    api,
    dispatch,
    report: error => api.reportException(error),
    shared,
    // track 按 Promise 拒绝跟踪事件维护未处理与已处理队列
    track(promise, handled, global) {
      if (!handled) {
        queue.pending.push({ promise, global });
        return;
      }
      const index = queue.pending.findIndex(item => item.promise === promise);
      if (index >= 0) {
        queue.pending.splice(index, 1);
        return;
      }
      const item = queue.notified.get(promise);
      if (item) {
        queue.notified.delete(promise);
        queue.handled.push(item);
      }
    },
    // notify 为仍未处理的拒绝派发 unhandledrejection，为通知后才处理的拒绝派发 rejectionhandled
    notify() {
      while (queue.pending.length || queue.handled.length) {
        const pending = queue.pending;
        const handled = queue.handled;
        queue.pending = [];
        queue.handled = [];
        for (const item of pending) {
          queue.notified.set(item.promise, item);
          try { shared.notifiers.get(item.global)('unhandledrejection', item.promise, natives.promiseResult(item.promise)); } catch (error) {}
        }
        for (const item of handled) {
          try { shared.notifiers.get(item.global)('rejectionhandled', item.promise, natives.promiseResult(item.promise)); } catch (error) {}
        }
      }
    },
    // reportMicrotask 把微任务回调的异常交给所属 Realm 报告
    reportMicrotask(global, error) {
      const report = shared.reporters.get(global);
      if (report) report(error);
    },
    // orderGlobal 按采集时顶层页面的全局键顺序重排顶层全局对象，解释器定义的 botguard 落入其槽位
    orderGlobal() {
      orderKeys(g, shared.shape.top.global.map(entry => entry.n));
    },
  };
})
