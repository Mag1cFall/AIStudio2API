// dom.js 按 Firefox 形状表为一个 Realm 生成 Window、WebIDL 接口对象与实例
(function installDOM(shape, profile, host) {
  'use strict';
  const g = globalThis;
  const { Object, Function, Array, Boolean, Date, Number, String, RegExp, Error, TypeError, RangeError, Map, Set, WeakMap, Promise, Symbol, Reflect, JSON, Math, Uint8Array, Uint8ClampedArray, Int8Array, ArrayBuffer, DataView } = g;
  const markNative = host.markNative;
  const native = (fn, name, sourceName) => {
    Object.defineProperty(fn, 'name', { configurable: true, value: name });
    markNative(fn, 'function ' + sourceName + '() {\n    [native code]\n}');
    return fn;
  };
  const nativeFunction = (name, fn) => native(fn, name, name);
  const hostGlobals = host.globals || {};
  const getters = {};
  const setters = {};
  const methods = {};
  const constructors = {};
  const brands = host.brands;
  const realmName = host.realm;
  const topRealm = host.topRealm || null;
  const ES = new Set(['Object', 'Function', 'Array', 'Boolean', 'Date', 'Number', 'String', 'RegExp', 'Error', 'InternalError', 'AggregateError', 'EvalError', 'RangeError', 'ReferenceError', 'SuppressedError', 'SyntaxError', 'TypeError', 'URIError', 'ArrayBuffer', 'SharedArrayBuffer', 'Int8Array', 'Uint8Array', 'Int16Array', 'Uint16Array', 'Int32Array', 'Uint32Array', 'Float32Array', 'Float64Array', 'Uint8ClampedArray', 'BigInt64Array', 'BigUint64Array', 'Float16Array', 'BigInt', 'Map', 'Set', 'WeakMap', 'WeakSet', 'DataView', 'Symbol', 'Promise', 'Proxy', 'FinalizationRegistry', 'WeakRef', 'Iterator', 'DisposableStack', 'AsyncDisposableStack']);
  const infos = Object.create(null);
  const protos = Object.create(null);
  const ctors = Object.create(null);
  const parentOf = Object.create(null);
  const singletons = Object.create(null);
  const getterCache = new Map();
  const promiseMembers = new Set(shape.promises || []);
  // overloaded 是参数不足时按重载报错的方法
  const overloaded = new Set(['Window.postMessage']);
  const pending = () => new Promise(() => {});
  const define = Object.defineProperty;
  const keyOf = name => name.startsWith('@@') ? Symbol[name.slice(2)] : name;
  const has = (flags, flag) => flags.indexOf(flag) >= 0;
  const setLength = (fn, length) => define(fn, 'length', { configurable: true, value: length });
  const errorClass = text => {
    const match = /^(\w+): ([\s\S]*)$/.exec(text || '');
    const type = match && typeof g[match[1]] === 'function' ? g[match[1]] : TypeError;
    return [type, match ? match[2] : text];
  };
  const throwCaptured = text => {
    const [type, message] = errorClass(text);
    if (type === g.DOMException || (match => match && /Error$/.test(match[1]) && typeof g[match[1]] !== 'function')(/^(\w+): /.exec(text))) {
      const name = /^(\w+): /.exec(text)[1];
      throw new g.DOMException(message, name);
    }
    throw new type(message);
  };
  const isA = host.isA;
  const brandCheck = host.brandCheck;
  const brandError = (iface, member) => {
    const error = new TypeError("'" + member + "' called on an object that does not implement interface " + iface + '.');
    return error;
  };
  const brandOf = (self, iface, member) => {
    const brand = brandCheck(self, iface);
    if (!brand) throw brandError(iface, member);
    return brand;
  };
  const resolveRef = path => {
    if (path.startsWith('top.')) return (topRealm || api).singleton(path.slice(4));
    return api.singleton(path.startsWith('frame.') ? path.slice(6) : path);
  };
  const decode = encoded => {
    switch (encoded.t) {
      case 'u': return undefined;
      case 'null': return null;
      case 's': case 'b': return encoded.v;
      case 'n': return typeof encoded.v === 'string' ? Number(encoded.v) : encoded.v;
      case 'bigint': return BigInt(encoded.v);
      case 'sym': return Symbol(encoded.v);
      case 'a': return encoded.v.map(decode);
      case 'ref': return resolveRef(encoded.v);
      case 'f': return nativeFunction(encoded.v, function () {});
      case 'o': return infos[encoded.k] || infos[encoded.c] ? create(infos[encoded.k] ? encoded.k : encoded.c) : plainObject(encoded.c);
      case 'throw': return throwCaptured(encoded.v);
      default: return undefined;
    }
  };
  const plainObject = tag => {
    if (tag === 'Array') return [];
    const object = {};
    if (tag && tag !== 'Object') define(object, Symbol.toStringTag, { configurable: true, value: tag });
    return object;
  };
  const defaultFor = (iface, name) => {
    for (let current = iface; current; current = parentOf[current]) {
      const values = shape.defaults[current];
      if (values && Object.prototype.hasOwnProperty.call(values, name)) return values[name];
    }
    return undefined;
  };
  const coerceLike = (encoded, value) => {
    if (!encoded) return value;
    if (encoded.t === 's') return value === null ? '' : String(value);
    if (encoded.t === 'n') return Number(value);
    if (encoded.t === 'b') return Boolean(value);
    return value;
  };
  // memberTables 是安装完成后按接口与成员名索引的特殊 getter 与方法表
  let memberTables = null;
  // memberTable 把以“接口.成员”为键的表转为按接口与成员名索引的两层表
  const memberTable = table => {
    const result = Object.create(null);
    for (const key of Object.keys(table)) {
      const dot = key.indexOf('.');
      const iface = key.slice(0, dot);
      (result[iface] || (result[iface] = Object.create(null)))[key.slice(dot + 1)] = table[key];
    }
    return result;
  };
  // lookupMember 按声明接口、实例接口与通配接口的顺序取成员实现
  const lookupMember = (table, indexed, declared, iface, name) => {
    if (indexed === null) return (declared && table[declared + '.' + name]) || table[iface + '.' + name] || table['*.' + name];
    const own = declared && indexed[declared];
    const exact = indexed[iface];
    const any = indexed['*'];
    return (own && own[name]) || (exact && exact[name]) || (any && any[name]);
  };
  const readValue = (self, brand, name, declared) => {
    if (name in brand.over) return brand.over[name];
    const cached = brand.cache.get(name);
    if (cached !== undefined) return cached;
    const special = lookupMember(getters, memberTables && memberTables.getters, declared, brand.iface, name);
    if (special) return special.call(self, brand);
    const encoded = Object.prototype.hasOwnProperty.call(brand.values, name) ? brand.values[name] : defaultFor(brand.iface, name);
    if (encoded === undefined) {
      return undefined;
    }
    const value = decode(encoded);
    if (encoded.t === 'o' || encoded.t === 'a' || encoded.t === 'f' || stableValue(encoded, name, value)) brand.cache.set(name, value);
    return value;
  };
  // stableValue 判断没有特殊 getter 的成员解码出的原始值或单例在之后的读取中不变
  const stableValue = (encoded, name, value) => {
    if (value === undefined || memberTables === null || name in memberTables.special) return false;
    switch (encoded.t) {
      case 's': case 'b': case 'n': case 'null': case 'bigint': return true;
      case 'ref':
        if (encoded.v.startsWith('top.')) return (topRealm || api).stableSingleton(encoded.v.slice(4));
        return api.stableSingleton(encoded.v.startsWith('frame.') ? encoded.v.slice(6) : encoded.v);
      default: return false;
    }
  };
  const read = (self, brand, name, declared) => {
    const value = exposed(readValue(self, brand, name, declared));
    return value;
  };
  const write = (self, brand, name, value, declared) => {
    const special = (declared && setters[declared + '.' + name]) || setters[brand.iface + '.' + name] || setters['*.' + name];
    if (special) return special.call(self, brand, value);
    if (/^on[a-z]+$/.test(name) && !brand.owner) return setEventHandler(brand, name, value);
    const encoded = Object.prototype.hasOwnProperty.call(brand.values, name) ? brand.values[name] : defaultFor(brand.iface, name);
    brand.over[name] = coerceLike(encoded, value);
    brand.cache.delete(name);
    if (brand.owner) (brand.owner.attrs || (brand.owner.attrs = new Map())).set('style', inlineStyle(brand));
  };
  const invoke = (self, brand, iface, name, args) => {
    const implementation = lookupMember(methods, memberTables && memberTables.methods, null, iface, name);
    const result = implementation ? implementation.call(self, brand, args) : undefined;
    return result;
  };
  const makeGetter = (iface, entry) => {
    const cacheKey = 'g:' + iface + ':' + entry.n;
    if (getterCache.has(cacheKey)) return getterCache.get(cacheKey);
    const name = entry.n;
    const promise = promiseMembers.has(iface + '.' + name + ':get');
    const getter = promise
      ? Object.getOwnPropertyDescriptor({ get [name]() { const self = this == null ? g : this; const brand = brandCheck(self, iface); if (!brand) return Promise.reject(brandError(iface, entry.g)); const value = read(self, brand, name, iface); return host.kindOf(value) === 'Promise' ? value : pending(); } }, name).get
      : Object.getOwnPropertyDescriptor({ get [name]() { const self = this == null ? g : this; return read(self, brandOf(self, iface, entry.g), name, iface); } }, name).get;
    native(getter, entry.g, name);
    setLength(getter, entry.gl || 0);
    getterCache.set(cacheKey, getter);
    return getter;
  };
  const makeSetter = (iface, entry) => {
    const cacheKey = 's:' + iface + ':' + entry.n;
    if (getterCache.has(cacheKey)) return getterCache.get(cacheKey);
    const name = entry.n;
    const setter = Object.getOwnPropertyDescriptor({ set [name](value) { const self = this == null ? g : this; write(self, brandOf(self, iface, entry.s), name, value, iface); } }, name).set;
    native(setter, entry.s, name);
    setLength(setter, entry.sl === undefined ? 1 : entry.sl);
    getterCache.set(cacheKey, setter);
    return setter;
  };
  // arityError 在实参少于 WebIDL 必需参数时返回 Firefox 的 TypeError，页面替换过的方法不检查
  const arityError = (iface, member, entry, args) => {
    const required = entry.nat === false ? 0 : entry.l || 0;
    if (args.length >= required) return null;
    if (overloaded.has(iface + '.' + member)) return new TypeError(iface + '.' + member + ': ' + args.length + ' is not a valid argument count for any overload.');
    return new TypeError(iface + '.' + member + ': At least ' + required + (required === 1 ? ' argument' : ' arguments') + ' required, but only ' + args.length + ' passed');
  };
  // makeStaticGetter 生成接口构造器上的静态 getter，不做 this 品牌检查
  const makeStaticGetter = (iface, entry) => {
    const name = entry.n;
    const getter = Object.getOwnPropertyDescriptor({ get [name]() { const implementation = getters[iface + '.' + name + ':static']; return implementation ? implementation() : undefined; } }, name).get;
    native(getter, entry.g, name);
    setLength(getter, entry.gl || 0);
    return getter;
  };
  // makeStatic 生成接口构造器上的静态方法，不做 this 品牌检查
  const makeStatic = (iface, entry) => {
    const member = entry.n;
    const method = ({ [member](...args) {
      const error = arityError(iface, member, entry, args);
      if (error) throw error;
      const implementation = methods[iface + '.' + member + ':static'];
      return implementation ? implementation.call(this, null, args) : undefined;
    } })[member];
    nativeFunction(entry.fn || member, method);
    setLength(method, entry.l || 0);
    return method;
  };
  const makeMethod = (iface, entry) => {
    const name = entry.n;
    if (name === '@@iterator' && entry.fn === 'values') return Array.prototype.values;
    if (name === '@@iterator' && entry.fn === 'entries') return makeMethod(iface, { n: 'entries', f: entry.f, k: 'm', l: 0, fn: 'entries', nat: true });
    const cacheKey = 'm:' + iface + ':' + name;
    if (name === 'entries' && getterCache.has(cacheKey)) return getterCache.get(cacheKey);
    const key = keyOf(name);
    const member = typeof key === 'symbol' ? entry.fn : name;
    const promise = promiseMembers.has(iface + '.' + name);
    const arity = args => arityError(iface, member, entry, args);
    const method = promise
      ? ({ [member](...args) { const self = this == null ? g : this; const brand = brandCheck(self, iface); if (!brand) return Promise.reject(brandError(iface, member)); const error = arity(args); if (error) return Promise.reject(error); const value = invoke(self, brand, iface, member, args); return value === undefined ? pending() : value; } })[member]
      : ({ [member](...args) { const self = this == null ? g : this; const brand = brandOf(self, iface, member); const error = arity(args); if (error) throw error; return invoke(self, brand, iface, member, args); } })[member];
    nativeFunction(entry.fn || member, method);
    setLength(method, entry.l || 0);
    if (name === 'entries') getterCache.set(cacheKey, method);
    return method;
  };
  // installable 判断形状表条目是否在目标对象上定义属性
  const installable = entry => keyOf(entry.n) !== undefined && !entry.err && (entry.k === 'v' || entry.k === 'm' || entry.k === 'a' || (entry.k === 'i' && entry.n === 'constructor'));
  // entryDescriptor 返回可安装条目在 target 上的属性描述符
  const entryDescriptor = (target, iface, entry, ctor) => {
    const flags = entry.f || '';
    const configurable = has(flags, 'c'), enumerable = has(flags, 'e'), writable = has(flags, 'w');
    if (entry.k === 'i') return { configurable, enumerable, writable, value: ctor };
    if (entry.k === 'v') return { configurable, enumerable, writable, value: entry.v.t === 'ref' || entry.v.t === 'o' ? undefined : decode(entry.v) };
    if (entry.k === 'm') return { configurable, enumerable, writable, value: target === ctor ? makeStatic(iface, entry) : makeMethod(iface, entry) };
    return { configurable, enumerable, get: entry.g ? (target === ctor ? makeStaticGetter(iface, entry) : makeGetter(iface, entry)) : undefined, set: entry.s ? makeSetter(iface, entry) : undefined };
  };
  const installEntry = (target, iface, entry, ctor) => {
    if (installable(entry)) define(target, keyOf(entry.n), entryDescriptor(target, iface, entry, ctor));
  };
  // templateEntries 保存各接口原型按需生成的条目，与模板键顺序一致
  const templateEntries = Object.create(null);
  // templateProperty 生成接口原型模板中第 index 个成员，返回是否访问器、值、getter、setter 与三个属性标志
  const templateProperty = (iface, index) => {
    const descriptor = entryDescriptor(protos[iface], iface, templateEntries[iface][index], ctors[iface]);
    return ['get' in descriptor, descriptor.value, descriptor.get, descriptor.set, Boolean(descriptor.writable), descriptor.enumerable, descriptor.configurable];
  };
  host.setTemplateResolver(templateProperty);
  let quietCreate = 0;
  const exposed = host.exposed;
  const create = (iface, values) => {
    const info = infos[iface];
    const proto = protos[iface] || Object.prototype;
    const object = Object.create(proto);
    const brand = { iface: info ? info.cn : iface, values: values || shape.defaults[iface] || {}, over: Object.create(null), cache: new Map(), children: null, parent: null, attrs: null, listeners: null, realm: api };
    if (host.resolveInterface && !quietCreate) host.resolveInterface(brand.iface);
    if (brand.iface === 'Storage') return storageProxy(object, brand);
    brands.set(object, brand);
    let own = shape.own[iface];
    for (let current = parentOf[iface]; !own && current && !ES.has(current); current = parentOf[current]) own = shape.own[current];
    if (own) for (const entry of own) installEntry(object, brand.iface, entry, null);
    return object;
  };
  const construct = (info, args) => {
    const implementation = constructors[info.n];
    if (implementation) return implementation(args);
    const behavior = info.construct || 'TypeError: Illegal constructor.';
    if (isA(info.n, 'Event') && /At least 1 argument required/.test(behavior)) {
      if (args.length < 1) return throwCaptured(behavior);
      return eventInit(create(info.n), args[0], args[1]);
    }
    if (behavior.startsWith('ok')) return create(info.n);
    const required = /At least (\d+) arguments? required/.exec(behavior);
    if (required && args.length >= Number(required[1])) return create(info.n);
    return throwCaptured(behavior);
  };
  const makeConstructor = (info, proto) => {
    const behavior = info.call || 'TypeError: Illegal constructor.';
    const ctor = function () {
      if (new.target === undefined) {
        if (behavior === 'ok') return construct(info, Array.prototype.slice.call(arguments));
        return throwCaptured(behavior);
      }
      return construct(info, Array.prototype.slice.call(arguments));
    };
    define(ctor, 'prototype', { value: proto, writable: Boolean(info.pw), enumerable: false, configurable: false });
    nativeFunction(info.cn, ctor);
    setLength(ctor, info.l || 0);
    return ctor;
  };
  for (const info of shape.interfaces) {
    infos[info.n] = info;
    if (info.cn !== info.n) continue;
    parentOf[info.n] = info.pp && info.pp !== 'Object' && infos[info.pp] ? info.pp : (info.p || null);
    if (ES.has(info.n) && typeof g[info.n] === 'function') {
      ctors[info.n] = g[info.n];
      protos[info.n] = g[info.n].prototype;
      continue;
    }
    templateEntries[info.cn] = info.pr.filter(installable);
    const proto = host.templatedObject(info.cn, templateEntries[info.cn].map(entry => entry.n), info.pp && protos[info.pp] ? protos[info.pp] : Object.prototype);
    const ctor = makeConstructor(info, proto);
    if (info.p && ctors[info.p]) Object.setPrototypeOf(ctor, ctors[info.p]);
    for (const entry of info.st) installEntry(ctor, info.cn, entry, ctor);
    ctors[info.n] = ctor;
    protos[info.n] = proto;
  }
  for (const info of shape.interfaces) {
    if (info.cn !== info.n) {
      ctors[info.n] = ctors[info.cn];
      protos[info.n] = protos[info.cn];
    }
  }
  host.interfaceParents(parentOf);
  const realmShape = shape[realmName];
  const windowValues = Object.assign({}, realmShape.windowValues);
  const profileWindow = (profile.window || {});
  const windowBrand = { iface: 'Window', values: windowValues, over: Object.create(null), cache: new Map(), children: null, parent: null, attrs: null, listeners: null, realm: null };
  for (const [name, value] of Object.entries(profileWindow)) windowBrand.over[name] = value;
  brands.set(g, windowBrand);
  const windowProperties = Object.create(protos.EventTarget);
  const windowPropertiesLevel = realmShape.chain.find(level => level.name === 'WindowProperties');
  if (windowPropertiesLevel) for (const entry of windowPropertiesLevel.own) installEntry(windowProperties, 'WindowProperties', entry, null);
  Object.setPrototypeOf(protos.Window, windowProperties);
  Object.setPrototypeOf(g, protos.Window);
  const existing = Object.create(null);
  for (const key of Reflect.ownKeys(g)) existing[typeof key === 'symbol' ? '@@' + key.description.replace(/^Symbol\./, '') : key] = Object.getOwnPropertyDescriptor(g, key);
  const keep = Object.create(null);
  const pageGlobals = profile.globals || {};
  for (const entry of realmShape.global) {
    const name = entry.n;
    if (/^\d+$/.test(name) || name === 'undefined' || name === 'NaN' || name === 'Infinity') continue;
    const key = keyOf(name);
    if (key === undefined) continue;
    const flags = entry.f || '';
    const previous = existing[name];
    keep[name] = true;
    if (previous && !previous.configurable) continue;
    if (previous) delete g[key];
    if (entry.k === 'i') {
      const value = ctors[name] || (previous && previous.value);
      if (value !== undefined) define(g, key, { configurable: has(flags, 'c'), enumerable: has(flags, 'e'), writable: has(flags, 'w'), value });
      continue;
    }
    if (entry.k === 'm') {
      let value = previous && 'value' in previous ? previous.value : undefined;
      if (value === undefined) value = hostGlobals[name] ? nativeFunction(entry.fn || name, hostGlobals[name]) : makeMethod('Window', entry);
      define(g, key, { configurable: has(flags, 'c'), enumerable: has(flags, 'e'), writable: has(flags, 'w'), value });
      continue;
    }
    if (entry.k === 'a') {
      define(g, key, { configurable: has(flags, 'c'), enumerable: has(flags, 'e'), get: entry.g ? makeGetter('Window', entry) : undefined, set: entry.s ? makeSetter('Window', entry) : undefined });
      continue;
    }
    if (entry.k === 'v') {
      let value = previous && 'value' in previous ? previous.value : undefined;
      if (Object.prototype.hasOwnProperty.call(pageGlobals, name)) value = Array.isArray(pageGlobals[name]) ? Array.from(pageGlobals[name]) : pageGlobals[name];
      else if (value === undefined && name !== 'undefined') {
        const namespace = shape.namespaces[name];
        value = namespace ? namespaceObject(name, namespace) : entry.v.t === 'o' ? plainObject(entry.v.c) : entry.v.t === 'ref' ? undefined : decode(entry.v);
      }
      define(g, key, { configurable: has(flags, 'c'), enumerable: has(flags, 'e'), writable: has(flags, 'w'), value });
    }
  }
  for (const name of Object.keys(existing)) {
    if (keep[name] || name.startsWith('__waa') || !existing[name].configurable) continue;
    delete g[keyOf(name)];
  }
  function namespaceObject(name, namespace) {
    const object = {};
    for (const entry of namespace.own) {
      if (entry.n === '@@toStringTag') { define(object, Symbol.toStringTag, { configurable: true, value: entry.v.v }); continue; }
      if (entry.k === 'm') {
        const method = ({ [entry.n](...args) { const implementation = methods[name + '.' + entry.n]; if (implementation) return implementation.call(this, null, args); return undefined; } })[entry.n];
        nativeFunction(entry.fn || entry.n, method);
        setLength(method, entry.l || 0);
        define(object, entry.n, { configurable: has(entry.f, 'c'), enumerable: has(entry.f, 'e'), writable: has(entry.f, 'w'), value: method });
      } else if (entry.k === 'v') {
        define(object, entry.n, { configurable: has(entry.f, 'c'), enumerable: has(entry.f, 'e'), writable: has(entry.f, 'w'), value: entry.v.t === 'o' ? plainObject(entry.v.c) : decode(entry.v) });
      }
    }
    return object;
  }
  const api = {
    realm: realmName,
    create,
    brandOf: object => brands.get(object),
    singleton(path) {
      if (path === 'window') return g;
      if (singletons[path] !== undefined) return singletons[path];
      const spec = (realmName === 'top' && shape.topSingletons[path]) || shape.singletons[path];
      if (!spec) return undefined;
      const values = Object.assign({}, spec.values);
      const object = create(spec.iface, values);
      if (spec.iface === 'Storage') brands.get(object).area = path;
      const overrides = (profile.values || {})[path];
      if (overrides) { const brand = brands.get(object); for (const [name, value] of Object.entries(overrides)) brand.over[name] = value; }
      if (path === 'location' && profile.location) Object.assign(brands.get(object).over, profile.location);
      if (path === 'document' && profile.location) Object.assign(brands.get(object).over, { URL: profile.location.href, documentURI: profile.location.href });
      singletons[path] = object;
      if (path === 'document') {
        quietCreate++;
        const doctype = create('DocumentType', { nodeType: { t: 'n', v: 10 }, nodeName: { t: 's', v: 'html' }, nodeValue: { t: 'null' }, textContent: { t: 'null' }, name: { t: 's', v: 'html' }, publicId: { t: 's', v: '' }, systemId: { t: 's', v: '' } });
        const root = elementFor(object, 'html');
        const head = elementFor(object, 'head');
        const body = elementFor(object, 'body');
        quietCreate--;
        brands.get(doctype).document = object;
        brands.get(object).over.doctype = doctype;
        singletons['document.documentElement'] = root;
        singletons['document.head'] = head;
        singletons['document.body'] = body;
        insert(object, doctype, null);
        insert(object, root, null);
        insert(root, head, null);
        insert(root, body, null);
      }
      return object;
    },
    // stableSingleton 判断已取得的单例在之后的 singleton 调用中保持同一对象；创建 document 时会替换先行创建的 document 子节点单例
    stableSingleton: path => !path.startsWith('document.') || singletons.document !== undefined,
    ctor: name => ctors[name],
    proto: name => protos[name],
    pageOrigin: () => pageOrigin(),
    reportException: error => reportException(error),
    reportRejection: (type, promise, reason) => reportRejection(type, promise, reason),
    idleDeadline: (didTimeout, remaining) => {
      const deadline = create('IdleDeadline');
      brandFor(deadline).over.didTimeout = didTimeout;
      brandFor(deadline).remaining = remaining;
      return deadline;
    },
  };
  windowBrand.realm = api;
  const childrenOf = brand => brand.children || (brand.children = []);
  const brandFor = node => brands.get(node);
  const isElement = node => { const brand = brandFor(node); return Boolean(brand) && isA(brand.iface, 'Element'); };
  const connected = node => {
    for (let current = node; current; ) {
      const brand = brandFor(current);
      if (!brand) return false;
      if (isA(brand.iface, 'Document')) return true;
      current = brand.parent;
    }
    return false;
  };
  const detach = node => {
    const brand = brandFor(node);
    if (!brand || !brand.parent) return;
    const wasConnected = connected(node);
    const parentChildren = childrenOf(brandFor(brand.parent));
    const index = parentChildren.indexOf(node);
    if (index >= 0) parentChildren.splice(index, 1);
    brand.parent = null;
    if (!wasConnected) return;
    const discard = current => {
      const frame = brandFor(current);
      if (frame.iface !== 'HTMLIFrameElement' || !frame.contentWindow) return;
      Object.assign(brandFor(frame.contentWindow).over, { closed: true, parent: null, top: null, frameElement: null });
      frame.contentWindow = null;
    };
    discard(node);
    walk(node, discard);
    syncFrames();
  };
  const syncFrames = () => {
    for (const name of Object.getOwnPropertyNames(g)) if (/^\d+$/.test(name)) delete g[name];
    const document = singletons.document;
    if (!document) return;
    let index = 0;
    walk(document, node => {
      const brand = brandFor(node);
      if (brand.iface === 'HTMLIFrameElement' && brand.contentWindow) define(g, String(index++), { configurable: true, enumerable: true, writable: false, value: brand.contentWindow });
    });
  };
  const insert = (parent, node, before) => {
    const parentBrand = brandFor(parent);
    const nodeBrand = brandFor(node);
    if (!nodeBrand) throw new TypeError("Node.appendChild: Argument 1 does not implement interface Node.");
    if (isA(nodeBrand.iface, 'DocumentFragment')) {
      for (const child of childrenOf(nodeBrand).slice()) insert(parent, child, before);
      return node;
    }
    detach(node);
    const list = childrenOf(parentBrand);
    const index = before ? list.indexOf(before) : -1;
    if (index >= 0) list.splice(index, 0, node); else list.push(node);
    nodeBrand.parent = parent;
    if (connected(node)) attached(node);
    return node;
  };
  const attached = node => {
    const brand = brandFor(node);
    if (brand.iface === 'HTMLIFrameElement' && !brand.contentWindow && host.createIframeWindow) {
      brand.contentWindow = host.createIframeWindow();
      syncFrames();
      host.queueTask(0, () => { const event = create('Event', undefined); brandFor(event).over.isTrusted = true; dispatch(node, event, 'load'); });
    }
    for (const child of childrenOf(brand)) attached(child);
  };
  const tagName = brand => (brand.tag || '').toUpperCase();
  const walk = (root, visit) => {
    for (const child of childrenOf(brandFor(root))) {
      if (visit(child) === true) return true;
      if (walk(child, visit) === true) return true;
    }
    return false;
  };
  const matchesSelector = (node, selector) => {
    const brand = brandFor(node);
    if (!brand || !isA(brand.iface, 'Element')) return false;
    return selector.split(',').some(part => {
      const text = part.trim();
      const match = /^([a-zA-Z][\w-]*|\*)?(?:#([\w-]+))?((?:\.[\w-]+)*)$/.exec(text);
      if (!match) return false;
      if (match[1] && match[1] !== '*' && match[1].toLowerCase() !== (brand.tag || '')) return false;
      if (match[2] && attribute(brand, 'id') !== match[2]) return false;
      if (match[3]) {
        const classes = String(attribute(brand, 'class') || '').split(/\s+/);
        for (const name of match[3].split('.').filter(Boolean)) if (!classes.includes(name)) return false;
      }
      return true;
    });
  };
  const attribute = (brand, name) => brand.attrs ? brand.attrs.get(name) ?? null : null;
  const inlineStyle = brand => Object.entries(brand.over).filter(([, value]) => value !== '').map(([name, value]) => name.replace(/[A-Z]/g, letter => '-' + letter.toLowerCase()) + ': ' + value + ';').join(' ');
  const resolveURL = value => {
    if (!value || /^[a-z][a-z0-9+.-]*:/i.test(value)) return value;
    const origin = profile.location ? profile.location.origin : '';
    if (value.startsWith('//')) return origin.slice(0, origin.indexOf('//')) + value;
    return origin + (value.startsWith('/') ? '' : '/') + value;
  };
  const collection = (iface, items) => {
    const object = create(iface);
    items.forEach((item, index) => define(object, index, { configurable: true, enumerable: true, value: item }));
    brandFor(object).items = items;
    return object;
  };
  const listenersOf = brand => brand.listeners || (brand.listeners = new Map());
  const listenerOptions = options => {
    if (options === null || typeof options !== 'object') return { capture: Boolean(options), once: false, passive: null, signal: null };
    const capture = Boolean(options.capture);
    const once = Boolean(options.once);
    const passive = options.passive === undefined ? null : Boolean(options.passive);
    const signal = options.signal;
    if (signal !== undefined && !brandCheck(signal, 'AbortSignal')) throw new TypeError("EventTarget.addEventListener: 'signal' member of AddEventListenerOptions does not implement interface AbortSignal.");
    return { capture, once, passive, signal: signal === undefined ? null : signal };
  };
  const captureOption = options => options === null || typeof options !== 'object' ? Boolean(options) : Boolean(options.capture);
  // passiveTypes 是在 window、document、根元素与 body 上默认 passive 的事件类型
  const passiveTypes = new Set(['touchstart', 'touchmove', 'wheel', 'mousewheel', 'DOMMouseScroll', 'MozMousePixelScroll']);
  const defaultPassive = (target, type) => passiveTypes.has(type) && (target === g || target === api.singleton('document') || target === api.singleton('document.documentElement') || target === api.singleton('document.body'));
  const removeListener = (brand, type, entry) => {
    entry.removed = true;
    const list = listenersOf(brand).get(type);
    if (list) listenersOf(brand).set(type, list.filter(item => item !== entry));
  };
  const setEventHandler = (brand, name, value) => {
    const type = name.slice(2);
    const handler = value !== null && (typeof value === 'object' || typeof value === 'function') ? value : null;
    brand.over[name] = handler;
    const list = listenersOf(brand).get(type) || [];
    const entry = list.find(item => item.handler);
    if (handler === null) {
      if (entry) removeListener(brand, type, entry);
      return;
    }
    if (!entry) listenersOf(brand).set(type, [...list, { handler: true, listener: null, capture: false, once: false, passive: false, removed: false }]);
  };
  const cancelEvent = eventBrand => {
    if (readValue(null, eventBrand, 'cancelable') && !eventBrand.passive) eventBrand.over.defaultPrevented = true;
  };
  const callHandler = (node, event, eventBrand, name) => {
    const handler = brandFor(node).over['on' + name];
    if (typeof handler !== 'function') return;
    if (name === 'error' && node === g && isA(eventBrand.iface, 'ErrorEvent')) {
      const field = key => readValue(event, eventBrand, key);
      if (host.callListener(handler, node, field('message'), field('filename'), field('lineno'), field('colno'), field('error')) === true) cancelEvent(eventBrand);
      return;
    }
    if (host.callListener(handler, node, event) === false) cancelEvent(eventBrand);
  };
  let reporting = false;
  // reportException 按 Firefox 报告未捕获异常：在本 Realm 的 window 上派发 ErrorEvent 并调用 onerror
  const reportException = error => {
    if (reporting) return;
    reporting = true;
    try {
      const isError = error !== null && typeof error === 'object' && Object.prototype.toString.call(error) === '[object Error]';
      let message;
      try {
        message = isError ? Error.prototype.toString.call(error) : 'uncaught exception: ' + String(error);
      } catch (e) {
        message = 'uncaught exception: ' + Object.prototype.toString.call(error);
      }
      const [thrownFile, thrownLine, thrownColumn] = host.throwLocation();
      const event = create('ErrorEvent');
      Object.assign(brandFor(event).over, {
        type: 'error', message, error,
        filename: isError && typeof error.fileName === 'string' ? error.fileName : thrownFile,
        lineno: isError && typeof error.lineNumber === 'number' ? error.lineNumber : thrownLine,
        colno: isError && typeof error.columnNumber === 'number' ? error.columnNumber : thrownColumn,
        isTrusted: true, bubbles: true, cancelable: true, timeStamp: host.performanceNow(),
      });
      dispatch(g, event);
    } finally {
      reporting = false;
    }
  };
  // reportRejection 在本 Realm 的 window 上派发 unhandledrejection 或 rejectionhandled
  const reportRejection = (type, promise, reason) => {
    const event = create('PromiseRejectionEvent');
    Object.assign(brandFor(event).over, { type, promise, reason, isTrusted: true, bubbles: false, cancelable: type === 'unhandledrejection', timeStamp: host.performanceNow() });
    dispatch(g, event);
  };
  const eventPath = target => {
    const path = [target];
    const brand = brandFor(target);
    if (!brand || brand.iface === 'Window') return path;
    for (let current = brand.parent; current; current = brandFor(current).parent) path.push(current);
    const last = path[path.length - 1];
    const lastBrand = brandFor(last);
    if (lastBrand && isA(lastBrand.iface, 'Document') && last === api.singleton('document')) path.push(g);
    return path;
  };
  const invokeListeners = (node, event, eventBrand, name, capture) => {
    const nodeBrand = brandFor(node);
    if (!nodeBrand) return;
    eventBrand.over.currentTarget = node;
    const list = nodeBrand.listeners ? (nodeBrand.listeners.get(name) || []).slice() : [];
    for (const entry of list) {
      if (eventBrand.stopImmediate) break;
      if (entry.removed || entry.capture !== capture) continue;
      if (entry.once) removeListener(nodeBrand, name, entry);
      eventBrand.passive = entry.passive;
      try {
        const listener = entry.listener;
        if (entry.handler) callHandler(node, event, eventBrand, name);
        else if (typeof listener === 'function') host.callListener(listener, node, event);
        else if (listener !== null && typeof listener === 'object') {
          const handleEvent = listener.handleEvent;
          if (typeof handleEvent !== 'function') throw new TypeError("Property 'handleEvent' is not callable.");
          host.callListener(handleEvent, listener, event);
        }
      } catch (error) {
        reportException(error);
      }
      eventBrand.passive = false;
    }
  };
  const dispatch = (target, event, type) => {
    const eventBrand = brandFor(event);
    if (type !== undefined) eventBrand.over.type = type;
    const name = String(readValue(event, eventBrand, 'type'));
    const bubbles = Boolean(readValue(event, eventBrand, 'bubbles'));
    eventBrand.over.target = target;
    eventBrand.stop = false;
    eventBrand.stopImmediate = false;
    const path = eventPath(target);
    eventBrand.path = path;
    for (let index = path.length - 1; index > 0 && !eventBrand.stop; index--) {
      eventBrand.over.eventPhase = 1;
      invokeListeners(path[index], event, eventBrand, name, true);
    }
    eventBrand.over.eventPhase = 2;
    if (!eventBrand.stop) invokeListeners(target, event, eventBrand, name, true);
    if (!eventBrand.stop) invokeListeners(target, event, eventBrand, name, false);
    if (bubbles) {
      for (let index = 1; index < path.length && !eventBrand.stop; index++) {
        eventBrand.over.eventPhase = 3;
        invokeListeners(path[index], event, eventBrand, name, false);
      }
    }
    eventBrand.path = null;
    eventBrand.over.currentTarget = null;
    eventBrand.over.eventPhase = 0;
    return !eventBrand.over.defaultPrevented;
  };
  const abortSignal = (signal, reason) => {
    const brand = brandFor(signal);
    if (brand.over.aborted) return;
    brand.over.aborted = true;
    brand.over.reason = reason === undefined ? new g.DOMException('The operation was aborted. ', 'AbortError') : reason;
    for (const algorithm of (brand.abortAlgorithms || []).splice(0)) algorithm();
    const event = create('Event');
    Object.assign(brandFor(event).over, { type: 'abort', isTrusted: true, timeStamp: host.performanceNow() });
    dispatch(signal, event);
  };
  const onAbort = (signal, algorithm) => {
    const brand = brandFor(signal);
    (brand.abortAlgorithms || (brand.abortAlgorithms = [])).push(algorithm);
  };
  const newSignal = () => {
    const signal = create('AbortSignal');
    Object.assign(brandFor(signal).over, { aborted: false, reason: undefined });
    return signal;
  };
  const dataCloneError = text => new g.DOMException(text, 'DataCloneError');
  // serializableInterfaces 是可经结构化克隆复制的平台对象接口
  const serializableInterfaces = new Set(['Blob', 'File', 'FileList', 'ImageData', 'ImageBitmap', 'DOMException', 'DOMRect', 'DOMRectReadOnly', 'DOMPoint', 'DOMPointReadOnly', 'DOMQuad', 'DOMMatrix', 'DOMMatrixReadOnly', 'CryptoKey']);
  const nativeErrorNames = new Set(['EvalError', 'RangeError', 'ReferenceError', 'SyntaxError', 'TypeError', 'URIError']);
  const ownDataValue = (object, key) => {
    const descriptor = Object.getOwnPropertyDescriptor(object, key);
    return descriptor && 'value' in descriptor ? descriptor.value : undefined;
  };
  const cloneProperties = (value, copy, memory) => {
    for (const key of Object.keys(value)) define(copy, key, { configurable: true, enumerable: true, writable: true, value: cloneValue(value[key], memory) });
    return copy;
  };
  // cloneValue 按 Firefox 结构化克隆复制值，memory 保存已复制的对象以保留共享与循环引用
  const cloneValue = (value, memory) => {
    if (typeof value === 'symbol') throw dataCloneError('unsupported type for structured data');
    if (value === null || (typeof value !== 'object' && typeof value !== 'function')) return value;
    if (memory.has(value)) return memory.get(value);
    const brand = brands.get(value);
    if (brand) {
      if (!serializableInterfaces.has(brand.iface)) throw dataCloneError(brand.iface + ' object could not be cloned.');
      const copy = create(brand.iface, brand.values);
      Object.assign(brandFor(copy).over, brand.over);
      memory.set(value, copy);
      return copy;
    }
    const kind = host.kindOf(value);
    let copy;
    switch (kind) {
      case 'Boolean': copy = Object(Boolean.prototype.valueOf.call(value)); break;
      case 'Number': copy = Object(Number.prototype.valueOf.call(value)); break;
      case 'String': copy = Object(String.prototype.valueOf.call(value)); break;
      case 'BigInt': copy = Object(BigInt.prototype.valueOf.call(value)); break;
      case 'Date': copy = new Date(Date.prototype.getTime.call(value)); break;
      case 'RegExp': copy = new RegExp(value.source, value.flags); break;
      case 'ArrayBuffer': copy = ArrayBuffer.prototype.slice.call(value, 0); break;
      case 'DataView': {
        const buffer = cloneValue(value.buffer, memory);
        copy = new DataView(buffer, value.byteOffset, value.byteLength);
        break;
      }
      case 'TypedArray': {
        const buffer = cloneValue(value.buffer, memory);
        copy = new g[value[Symbol.toStringTag]](buffer, value.byteOffset, value.length);
        break;
      }
      case 'Map': {
        const entries = [...Map.prototype.entries.call(value)];
        copy = new Map();
        memory.set(value, copy);
        for (const [key, item] of entries) copy.set(cloneValue(key, memory), cloneValue(item, memory));
        return copy;
      }
      case 'Set': {
        const items = [...Set.prototype.values.call(value)];
        copy = new Set();
        memory.set(value, copy);
        for (const item of items) copy.add(cloneValue(item, memory));
        return copy;
      }
      case 'Error': {
        const name = value.name;
        const message = ownDataValue(value, 'message');
        copy = new g[nativeErrorNames.has(name) ? name : 'Error'](message === undefined ? undefined : String(message));
        for (const key of ['fileName', 'lineNumber', 'columnNumber']) {
          const field = ownDataValue(value, key);
          if (field !== undefined) define(copy, key, { configurable: true, enumerable: false, writable: true, value: field });
        }
        break;
      }
      case 'Array':
        copy = new Array(value.length);
        memory.set(value, copy);
        return cloneProperties(value, copy, memory);
      case 'Object': case 'Math': case 'JSON': case 'Arguments':
        copy = {};
        memory.set(value, copy);
        return cloneProperties(value, copy, memory);
      case 'Function': case 'AsyncFunction': case 'GeneratorFunction': case 'AsyncGeneratorFunction':
        throw dataCloneError('Function object could not be cloned.');
      default:
        throw dataCloneError(kind + ' object could not be cloned.');
    }
    memory.set(value, copy);
    return copy;
  };
  const transferMemory = transfer => {
    const memory = new Map();
    for (const item of transfer) {
      if (host.kindOf(item) !== 'ArrayBuffer') throw dataCloneError('Transferable object could not be cloned.');
      memory.set(item, item.transfer());
    }
    return memory;
  };
  const pageOrigin = () => profile.location ? profile.location.origin : (topRealm ? topRealm.pageOrigin() : '');
  const urlOrigin = text => {
    const match = /^([a-z][a-z0-9+.-]*):\/\/([^/?#\\]+)/i.exec(text);
    if (!match) return null;
    const scheme = match[1].toLowerCase();
    let hostPart = match[2].replace(/^[^@]*@/, '').toLowerCase();
    if ((scheme === 'https' && hostPart.endsWith(':443')) || (scheme === 'http' && hostPart.endsWith(':80'))) hostPart = hostPart.replace(/:\d+$/, '');
    return scheme + '://' + hostPart;
  };
  const liveCollection = (owner, key, iface, compute) => {
    const ownerBrand = brandFor(owner);
    const cache = ownerBrand.live || (ownerBrand.live = new Map());
    if (cache.has(key)) return cache.get(key);
    const object = create(iface);
    const brand = brandFor(object);
    define(brand, 'items', { get: compute });
    const indexOf = property => typeof property === 'string' && /^(0|[1-9]\d*)$/.test(property) ? Number(property) : -1;
    const proxy = new HostProxy(object, {
      get: (target, property, receiver) => { const index = indexOf(property); return index >= 0 ? compute()[index] : reflectGet(target, property, receiver); },
      has: (target, property) => { const index = indexOf(property); return index >= 0 ? index < compute().length : reflectHas(target, property); },
      ownKeys: target => [...compute().map((item, index) => String(index)), ...reflectOwnKeys(target)],
      getOwnPropertyDescriptor: (target, property) => {
        const index = indexOf(property);
        if (index < 0) return reflectDescriptor(target, property);
        const items = compute();
        return index < items.length ? { value: items[index], writable: false, enumerable: true, configurable: true } : undefined;
      },
    });
    brands.set(proxy, brand);
    cache.set(key, proxy);
    return proxy;
  };
  const cloneTree = (brand, deep) => {
    const clone = create(brand.iface, brand.values);
    const cloneBrand = brandFor(clone);
    Object.assign(cloneBrand, { tag: brand.tag, document: brand.document, attrs: brand.attrs ? new Map(brand.attrs) : null });
    for (const name of ['data', 'textContent', 'nodeValue', 'value', 'checked']) if (Object.prototype.hasOwnProperty.call(brand.over, name)) cloneBrand.over[name] = brand.over[name];
    if (deep) for (const child of childrenOf(brand)) insert(clone, cloneTree(brandFor(child), true), null);
    return clone;
  };
  const isCharacterData = brand => isA(brand.iface, 'CharacterData');
  const dataOf = brand => brand.over.data === undefined ? '' : String(brand.over.data);
  const textOf = brand => {
    if (isCharacterData(brand)) return dataOf(brand);
    let text = '';
    for (const child of childrenOf(brand)) {
      const childBrand = brandFor(child);
      if (childBrand.iface === 'Text' || childBrand.iface === 'CDATASection') text += dataOf(childBrand);
      else if (!isCharacterData(childBrand)) text += textOf(childBrand);
    }
    return text;
  };
  const setCharacterData = (brand, value) => Object.assign(brand.over, { data: value, nodeValue: value, textContent: value });
  // voidElements 与 rawTextElements 是 HTML 序列化时不写结束标签与不转义文本的元素
  const voidElements = new Set(['area', 'base', 'basefont', 'bgsound', 'br', 'col', 'embed', 'frame', 'hr', 'img', 'input', 'keygen', 'link', 'meta', 'param', 'source', 'track', 'wbr']);
  const rawTextElements = new Set(['style', 'script', 'xmp', 'iframe', 'noembed', 'noframes', 'plaintext', 'noscript']);
  const escapeText = text => text.replace(/&/g, '&amp;').replace(/\u00a0/g, '&nbsp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const escapeAttribute = text => escapeText(text).replace(/"/g, '&quot;');
  const serializeChildren = brand => childrenOf(brand).map(child => serializeNode(child, brand)).join('');
  const serializeNode = (node, parentBrand) => {
    const brand = brandFor(node);
    if (isA(brand.iface, 'Element')) {
      const attributes = brand.attrs ? [...brand.attrs].map(([name, value]) => ' ' + name + '="' + escapeAttribute(String(value)) + '"').join('') : '';
      if (voidElements.has(brand.tag)) return '<' + brand.tag + attributes + '>';
      return '<' + brand.tag + attributes + '>' + serializeChildren(brand) + '</' + brand.tag + '>';
    }
    if (brand.iface === 'Text' || brand.iface === 'CDATASection') return parentBrand && rawTextElements.has(parentBrand.tag) ? dataOf(brand) : escapeText(dataOf(brand));
    if (brand.iface === 'Comment') return '<!--' + dataOf(brand) + '-->';
    if (brand.iface === 'DocumentType') return '<!DOCTYPE html>';
    return '';
  };
  const domExceptionCodes = { IndexSizeError: 1, HierarchyRequestError: 3, WrongDocumentError: 4, InvalidCharacterError: 5, NoModificationAllowedError: 7, NotFoundError: 8, NotSupportedError: 9, InUseAttributeError: 10, InvalidStateError: 11, SyntaxError: 12, InvalidModificationError: 13, NamespaceError: 14, InvalidAccessError: 15, TypeMismatchError: 17, SecurityError: 18, NetworkError: 19, AbortError: 20, URLMismatchError: 21, QuotaExceededError: 22, TimeoutError: 23, InvalidNodeTypeError: 24, DataCloneError: 25 };
  const bytesOf = value => {
    const kind = host.kindOf(value);
    if (kind === 'ArrayBuffer') return new Uint8Array(value.slice(0));
    if (kind === 'TypedArray' || kind === 'DataView') return new Uint8Array(value.buffer.slice(value.byteOffset, value.byteOffset + value.byteLength));
    return null;
  };
  const utf8Encode = text => {
    const bytes = [];
    for (let index = 0; index < text.length; index++) {
      let code = text.charCodeAt(index);
      if (code >= 0xd800 && code <= 0xdbff && index + 1 < text.length && (text.charCodeAt(index + 1) & 0xfc00) === 0xdc00) code = 0x10000 + ((code - 0xd800) << 10) + (text.charCodeAt(++index) - 0xdc00);
      else if (code >= 0xd800 && code <= 0xdfff) code = 0xfffd;
      if (code < 0x80) bytes.push(code);
      else if (code < 0x800) bytes.push(0xc0 | (code >> 6), 0x80 | (code & 63));
      else if (code < 0x10000) bytes.push(0xe0 | (code >> 12), 0x80 | ((code >> 6) & 63), 0x80 | (code & 63));
      else bytes.push(0xf0 | (code >> 18), 0x80 | ((code >> 12) & 63), 0x80 | ((code >> 6) & 63), 0x80 | (code & 63));
    }
    return new Uint8Array(bytes);
  };
  // utf8Decode 按 WHATWG UTF-8 解码器把字节解码为字符串，stream 为真时把末尾不完整的序列留在 state.pending
  const utf8Decode = (bytes, state, stream, fatal) => {
    const input = [...state.pending, ...bytes];
    state.pending = [];
    let output = '';
    let needed = 0, seen = 0, code = 0, lower = 0x80, upper = 0xbf, start = 0;
    const fail = () => { if (fatal) throw new TypeError('TextDecoder.decode: Decoding failed.'); output += '\ufffd'; };
    for (let index = 0; index < input.length; index++) {
      const byte = input[index];
      if (needed === 0) {
        start = index;
        if (byte <= 0x7f) { output += String.fromCharCode(byte); continue; }
        if (byte >= 0xc2 && byte <= 0xdf) { needed = 1; code = byte & 0x1f; }
        else if (byte >= 0xe0 && byte <= 0xef) { if (byte === 0xe0) lower = 0xa0; if (byte === 0xed) upper = 0x9f; needed = 2; code = byte & 0xf; }
        else if (byte >= 0xf0 && byte <= 0xf4) { if (byte === 0xf0) lower = 0x90; if (byte === 0xf4) upper = 0x8f; needed = 3; code = byte & 0x7; }
        else fail();
        continue;
      }
      if (byte < lower || byte > upper) {
        needed = seen = code = 0; lower = 0x80; upper = 0xbf;
        fail();
        index--;
        continue;
      }
      lower = 0x80; upper = 0xbf;
      code = (code << 6) | (byte & 0x3f);
      if (++seen !== needed) continue;
      output += String.fromCodePoint(code);
      needed = seen = code = 0;
    }
    if (needed !== 0) {
      if (stream) state.pending = input.slice(start);
      else fail();
    }
    return output;
  };
  const bodyText = bytes => utf8Decode(bytes, { pending: [] }, false, false);
  const blobFromParts = (iface, parts, type) => {
    const chunks = [];
    for (const part of parts) {
      const raw = bytesOf(part);
      if (raw) chunks.push(raw);
      else if (part !== null && typeof part === 'object' && brandFor(part) && brandFor(part).bytes) chunks.push(brandFor(part).bytes);
      else chunks.push(utf8Encode(String(part)));
    }
    const bytes = new Uint8Array(chunks.reduce((sum, chunk) => sum + chunk.length, 0));
    let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
    const blob = create(iface);
    brandFor(blob).bytes = bytes;
    Object.assign(brandFor(blob).over, { size: bytes.length, type: blobType(type) });
    return blob;
  };
  const blobType = type => {
    const text = type === undefined ? '' : String(type);
    return /^[\u0020-\u007e]*$/.test(text) ? text.toLowerCase() : '';
  };
  const blobParts = (iface, parts) => {
    if (parts === undefined) return [];
    if (parts === null || typeof parts !== 'object' || typeof parts[Symbol.iterator] !== 'function') throw new TypeError(iface + " constructor: Argument 1 can't be converted to a sequence.");
    return [...parts];
  };
  const consumeBody = (brand, iface, member) => {
    if (brand.over.bodyUsed) return Promise.reject(new TypeError(iface + '.' + member + ': Body has already been consumed.'));
    brand.over.bodyUsed = true;
    return Promise.resolve(brand.bytes || new Uint8Array(0));
  };
  const headerList = headers => brandFor(headers).list || (brandFor(headers).list = []);
  const newHeaders = init => {
    const headers = create('Headers');
    const list = headerList(headers);
    if (init !== undefined && init !== null) {
      const entries = typeof init[Symbol.iterator] === 'function' ? [...init] : Object.keys(init).map(name => [name, init[name]]);
      for (const [name, value] of entries) list.push([String(name).toLowerCase(), String(value).trim()]);
    }
    return headers;
  };
  // pairIterables 是 WebIDL 声明 iterable<K, V> 的接口，其余以 entries 为 @@iterator 的接口是 maplike
  const pairIterables = new Set(['URLSearchParams', 'Headers', 'FormData']);
  const iteratorStates = new WeakMap();
  const iteratorProtos = Object.create(null);
  // pairsOf 返回接口实例当前要迭代的键值对，Headers 按名称排序并合并同名值
  const pairsOf = (iface, brand) => {
    const list = brand.list || [];
    if (iface !== 'Headers') return list;
    const names = [...new Set(list.map(([name]) => name))].sort();
    return names.flatMap(name => {
      const values = list.filter(([item]) => item === name).map(([, value]) => value);
      return name === 'set-cookie' ? values.map(value => [name, value]) : [[name, values.join(', ')]];
    });
  };
  // iteratorProto 返回接口默认迭代器的原型，原型链接到 %IteratorPrototype%
  const iteratorProto = iface => {
    if (iteratorProtos[iface]) return iteratorProtos[iface];
    const proto = Object.create(iteratorPrototype);
    const next = nativeFunction('next', function next() {
      const state = iteratorStates.get(this);
      const pairs = pairsOf(state.iface, state.brand);
      if (state.index >= pairs.length) return { value: undefined, done: true };
      const [key, value] = pairs[state.index++];
      return { value: state.kind === 'key' ? key : state.kind === 'value' ? value : [key, value], done: false };
    });
    setLength(next, 0);
    define(proto, 'next', { configurable: true, enumerable: true, writable: true, value: next });
    define(proto, Symbol.toStringTag, { configurable: true, value: iface + ' Iterator' });
    return (iteratorProtos[iface] = proto);
  };
  // pairIterator 返回 entries、keys 或 values 的迭代器，maplike 接口用 Map 迭代器
  const pairIterator = (iface, brand, kind) => {
    if (!pairIterables.has(iface)) {
      const map = new Map(pairsOf(iface, brand));
      return kind === 'key' ? map.keys() : kind === 'value' ? map.values() : map.entries();
    }
    const iterator = Object.create(iteratorProto(iface));
    iteratorStates.set(iterator, { iface, brand, kind, index: 0 });
    return iterator;
  };
  // percentDecodeText 按 application/x-www-form-urlencoded 解析器解码一个名称或值
  const percentDecodeText = text => {
    const bytes = utf8Encode(text.replace(/\+/g, ' '));
    const output = [];
    const hex = byte => (byte >= 0x30 && byte <= 0x39) || (byte >= 0x41 && byte <= 0x46) || (byte >= 0x61 && byte <= 0x66);
    for (let index = 0; index < bytes.length; index++) {
      if (bytes[index] === 0x25 && index + 2 < bytes.length && hex(bytes[index + 1]) && hex(bytes[index + 2])) {
        output.push(parseInt(String.fromCharCode(bytes[index + 1], bytes[index + 2]), 16));
        index += 2;
      } else output.push(bytes[index]);
    }
    return utf8Decode(output, { pending: [] }, false, false);
  };
  const parseQuery = text => text.split('&').filter(Boolean).map(part => {
    const index = part.indexOf('=');
    return index < 0 ? [percentDecodeText(part), ''] : [percentDecodeText(part.slice(0, index)), percentDecodeText(part.slice(index + 1))];
  });
  // serializeForm 按 application/x-www-form-urlencoded 序列化器编码一个名称或值
  const serializeForm = text => {
    let output = '';
    for (const byte of utf8Encode(text)) {
      if (byte === 0x20) output += '+';
      else if ((byte >= 0x30 && byte <= 0x39) || (byte >= 0x41 && byte <= 0x5a) || (byte >= 0x61 && byte <= 0x7a) || byte === 0x2a || byte === 0x2d || byte === 0x2e || byte === 0x5f) output += String.fromCharCode(byte);
      else output += '%' + (byte < 16 ? '0' : '') + byte.toString(16).toUpperCase();
    }
    return output;
  };
  const serializeQuery = list => list.map(([name, value]) => serializeForm(name) + '=' + serializeForm(value)).join('&');
  // updateQuery 把 URLSearchParams 的列表写回所属 URL 的查询部分
  const updateQuery = brand => {
    if (!brand.url) return;
    const owner = brandFor(brand.url);
    owner.url = host.updateURL(owner.url.href, 'search', serializeQuery(brand.list));
  };
  const newParams = (list, url) => {
    const params = create('URLSearchParams');
    Object.assign(brandFor(params), { list, url });
    return params;
  };
  const urlFields = ['href', 'origin', 'protocol', 'username', 'password', 'host', 'hostname', 'port', 'pathname', 'search', 'hash'];
  // parseURLRecord 以可选基址解析地址，返回解析结果与出错时报告的输入
  const parseURLRecord = (input, base) => {
    const text = String(input);
    if (base === undefined) return [host.parseURL(text), text];
    const baseRecord = host.parseURL(String(base));
    if (baseRecord === null) return [null, String(base)];
    return [host.parseURL(text, baseRecord.href), text];
  };
  const newURL = record => {
    const url = create('URL');
    const brand = brandFor(url);
    brand.url = record;
    brand.params = newParams(parseQuery(record.search.slice(1)), url);
    return url;
  };
  // setURLField 按 URL 接口的 setter 修改地址并同步 searchParams
  const setURLField = (brand, field, value) => {
    const record = host.updateURL(brand.url.href, field, String(value));
    if (record === null) throw new TypeError('URL.href setter: ' + String(value) + ' is not a valid URL.');
    brand.url = record;
    if (field === 'href' || field === 'search') brandFor(brand.params).list = parseQuery(record.search.slice(1));
  };
  const typedArrayTag = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(Int8Array.prototype), Symbol.toStringTag).get;
  const integerArrays = new Set(['Int8Array', 'Uint8Array', 'Uint8ClampedArray', 'Int16Array', 'Uint16Array', 'Int32Array', 'Uint32Array', 'BigInt64Array', 'BigUint64Array']);
  const cookieItems = () => {
    const document = api.singleton('document');
    const text = String(readValue(document, brandFor(document), 'cookie') || '');
    return text.split(';').map(part => part.trim()).filter(Boolean).map(part => { const index = part.indexOf('='); return index < 0 ? { name: '', value: part } : { name: part.slice(0, index), value: part.slice(index + 1) }; });
  };
  const digestNames = { 'SHA-1': 'SHA-1', 'SHA-256': 'SHA-256', 'SHA-384': 'SHA-384', 'SHA-512': 'SHA-512' };
  let animationHandle = 0;
  const animationCallbacks = new Set();
  const descendants = (root, accept) => {
    const found = [];
    walk(root, node => { if (accept(node)) found.push(node); });
    return found;
  };
  const layoutRect = element => {
    if (realmName === 'top' && (element === api.singleton('document.body') || element === api.singleton('document.documentElement'))) {
      return [0, 0, g.innerWidth, g.innerHeight];
    }
    return [0, 0, 0, 0];
  };
  const rect = (iface, [x, y, width, height]) => {
    const object = create(iface);
    Object.assign(brandFor(object).over, { x, y, width, height, top: y, left: x, right: x + width, bottom: y + height });
    return object;
  };
  const refreshTime = () => {
    const frame = 1000 / 60;
    const now = host.performanceNow();
    return Math.round((Math.floor((now - 0.66) / frame) * frame + 0.66) * 100) / 100;
  };
  const intersectionEntries = targets => targets.map(target => {
    const bounds = layoutRect(target);
    const viewport = realmName === 'top' ? [0, 0, g.innerWidth, g.innerHeight] : [0, 0, 0, 0];
    const intersecting = bounds[2] > 0 && bounds[3] > 0 && viewport[2] > 0;
    const entry = create('IntersectionObserverEntry');
    Object.assign(brandFor(entry).over, {
      time: refreshTime(),
      rootBounds: rect('DOMRectReadOnly', viewport),
      boundingClientRect: rect('DOMRectReadOnly', bounds),
      intersectionRect: rect('DOMRectReadOnly', intersecting ? bounds : [0, 0, 0, 0]),
      isIntersecting: intersecting,
      intersectionRatio: intersecting ? 1 : 0,
      target,
    });
    return entry;
  });
  const eventInit = (event, type, init) => {
    const brand = brandFor(event);
    brand.over.type = String(type);
    brand.over.timeStamp = g.performance ? g.performance.now() : 0;
    if (init && typeof init === 'object') {
      for (const name of attributesOf(brand.iface)) {
        if (name === 'isTrusted' || name === 'type' || init[name] === undefined) continue;
        brand.over[name] = coerceLike(brand.values[name] || defaultFor(brand.iface, name), init[name]);
      }
    }
    return event;
  };
  const attributeCache = new Map();
  const attributesOf = iface => {
    if (attributeCache.has(iface)) return attributeCache.get(iface);
    const names = [];
    for (let current = iface; current; current = parentOf[current]) {
      const info = infos[current];
      if (info) for (const entry of info.pr) if (entry.k === 'a') names.push(entry.n);
    }
    attributeCache.set(iface, names);
    return names;
  };
  const eventInterfaces = { event: 'Event', events: 'Event', htmlevents: 'Event', svgevents: 'Event', uievent: 'UIEvent', uievents: 'UIEvent', mouseevent: 'MouseEvent', mouseevents: 'MouseEvent', keyboardevent: 'KeyboardEvent', focusevent: 'FocusEvent', customevent: 'CustomEvent', messageevent: 'MessageEvent', compositionevent: 'CompositionEvent', dragevent: 'DragEvent', beforeunloadevent: 'BeforeUnloadEvent', hashchangeevent: 'HashChangeEvent', popstateevent: 'PopStateEvent', storageevent: 'StorageEvent', textevent: 'CompositionEvent', svgzoomevents: 'Event', devicemotionevent: 'DeviceMotionEvent', deviceorientationevent: 'DeviceOrientationEvent', scrollareaevent: 'Event', simplegestureevent: 'SimpleGestureEvent', xulcommandevent: 'XULCommandEvent', commandevent: 'Event', notifypaintevent: 'Event', timeevent: 'TimeEvent', mutationevent: 'MutationEvent', mutationevents: 'MutationEvent' };
  const elementFor = (document, name) => {
    const tag = String(name).toLowerCase();
    const iface = shape.tagMap[tag] || (tag.includes('-') ? 'HTMLElement' : 'HTMLUnknownElement');
    const element = create(iface, shape.defaults[iface]);
    const brand = brandFor(element);
    brand.tag = tag;
    brand.document = document;
    return element;
  };
  Object.assign(getters, {
    '*.parentNode': brand => brand.parent || null,
    '*.parentElement': brand => brand.parent && isElement(brand.parent) ? brand.parent : null,
    '*.childNodes': function (brand) { return liveCollection(this, 'childNodes', 'NodeList', () => childrenOf(brand).slice()); },
    'NodeList.length': brand => brand.items ? brand.items.length : 0,
    'HTMLCollection.length': brand => brand.items ? brand.items.length : 0,
    'CharacterData.length': brand => dataOf(brand).length,
    '*.children': function (brand) { return liveCollection(this, 'children', 'HTMLCollection', () => childrenOf(brand).filter(isElement)); },
    '*.firstChild': brand => childrenOf(brand)[0] || null,
    '*.lastChild': brand => childrenOf(brand)[childrenOf(brand).length - 1] || null,
    '*.firstElementChild': brand => childrenOf(brand).find(isElement) || null,
    '*.lastElementChild': brand => childrenOf(brand).filter(isElement).pop() || null,
    '*.childElementCount': brand => childrenOf(brand).filter(isElement).length,
    '*.isConnected': function () { return connected(this); },
    '*.nextSibling': function (brand) { if (!brand.parent) return null; const list = childrenOf(brandFor(brand.parent)); return list[list.indexOf(this) + 1] || null; },
    '*.previousSibling': function (brand) { if (!brand.parent) return null; const list = childrenOf(brandFor(brand.parent)); return list[list.indexOf(this) - 1] || null; },
    'Document.documentElement': brand => childrenOf(brand).find(isElement) || null,
    'Document.head': brand => { const root = childrenOf(brand).find(isElement); return root ? childrenOf(brandFor(root)).find(node => brandFor(node).tag === 'head') || null : null; },
    'Document.body': brand => { const root = childrenOf(brand).find(isElement); return root ? childrenOf(brandFor(root)).find(node => brandFor(node).tag === 'body') || null : null; },
    '*.ownerDocument': brand => isA(brand.iface, 'Document') ? null : (brand.document || api.singleton('document')),
    'Element.tagName': brand => tagName(brand),
    '*.textContent': brand => isA(brand.iface, 'Document') || isA(brand.iface, 'DocumentType') ? null : textOf(brand),
    'Element.innerHTML': brand => serializeChildren(brand),
    'Element.outerHTML': function () { return serializeNode(this, null); },
    'FontFaceSet.ready': function (brand) { return brand.ready || (brand.ready = Promise.resolve(this)); },
    'Element.localName': brand => brand.tag,
    'Node.baseURI': brand => { if (profile.location) return profile.location.origin + '/'; const encoded = brand.values.baseURI || defaultFor(brand.iface, 'baseURI'); return encoded ? decode(encoded) : undefined; },
    'Node.nodeName': function (brand) { if (isA(brand.iface, 'Element') && brand.tag) return tagName(brand); const encoded = brand.values.nodeName || defaultFor(brand.iface, 'nodeName'); return encoded ? decode(encoded) : undefined; },
    'Element.id': brand => attribute(brand, 'id') || '',
    'Element.className': brand => attribute(brand, 'class') || '',
    'HTMLIFrameElement.contentWindow': function (brand) { if (!brand.contentWindow && connected(this) && host.createIframeWindow) brand.contentWindow = host.createIframeWindow(); return brand.contentWindow || null; },
    'HTMLElement.offsetHeight': function () { return layoutHeight(this); },
    'HTMLIFrameElement.src': brand => resolveURL(attribute(brand, 'src') || ''),
    'Window.length': () => Object.getOwnPropertyNames(g).filter(name => /^\d+$/.test(name)).length,
    'HTMLElement.style': brand => {
      let style = brand.cache.get('style');
      if (!style) {
        style = create('CSSStyleProperties');
        brandFor(style).owner = brand;
        brand.cache.set('style', style);
      }
      return style;
    },
    'HTMLIFrameElement.contentDocument': function (brand) { const window = getters['HTMLIFrameElement.contentWindow'].call(this, brand); return window ? window.document : null; },
    'Event.target': brand => brand.over.target === undefined ? null : brand.over.target,
    'Storage.length': brand => storageKeys(storageArea(brand)).length,
    'Performance.timeOrigin': () => host.timeOrigin,
    'Document.cookie': () => '',
  });
  Object.assign(setters, {
    'HTMLImageElement.src': function (brand, value) { trustedImage(this, brand, String(value)); },
    'HTMLIFrameElement.src': (brand, value) => { (brand.attrs || (brand.attrs = new Map())).set('src', String(value)); },
    'Element.id': (brand, value) => { (brand.attrs || (brand.attrs = new Map())).set('id', String(value)); },
    'CharacterData.data': (brand, value) => setCharacterData(brand, String(value)),
    'Node.nodeValue': (brand, value) => { if (isCharacterData(brand)) setCharacterData(brand, value === null ? '' : String(value)); },
    '*.textContent': function (brand, value) {
      const text = value === null ? '' : String(value);
      if (isCharacterData(brand)) return setCharacterData(brand, text);
      if (isA(brand.iface, 'Document') || isA(brand.iface, 'DocumentType')) return;
      for (const child of childrenOf(brand).slice()) detach(child);
      if (text !== '') insert(this, api.singleton('document').createTextNode(text), null);
    },
    'Element.className': (brand, value) => { (brand.attrs || (brand.attrs = new Map())).set('class', String(value)); },
    'HTMLTextAreaElement.value': (brand, value) => { brand.over.value = String(value).replace(/\r\n?/g, '\n'); },
    'Document.cookie': (brand, value) => {
      const [pair, ...attributes] = String(value).split(';');
      const index = pair.indexOf('=');
      const name = index < 0 ? '' : pair.slice(0, index).trim();
      const content = (index < 0 ? pair : pair.slice(index + 1)).trim();
      let expired = false;
      for (const attribute of attributes) {
        const separator = attribute.indexOf('=');
        const key = (separator < 0 ? attribute : attribute.slice(0, separator)).trim().toLowerCase();
        const setting = separator < 0 ? '' : attribute.slice(separator + 1).trim();
        if (key === 'max-age' && /^-?\d+$/.test(setting) && Number(setting) <= 0) expired = true;
        if (key === 'expires' && Date.parse(setting) <= Date.now()) expired = true;
      }
      const items = cookieItems();
      const existing = items.find(item => item.name === name);
      if (expired) items.splice(items.indexOf(existing), existing ? 1 : 0);
      else if (existing) existing.value = content;
      else items.push({ name, value: content });
      brand.over.cookie = items.map(item => item.name === '' ? item.value : item.name + '=' + item.value).join('; ');
    },
  });
  const navigationEntries = () => {
    const entry = api.singleton('performance.navigationEntry');
    return entry ? [entry] : [];
  };
  const mediaMatches = text => {
    const match = /^\(\s*(min|max)-(width|height)\s*:\s*([\d.]+)px\s*\)$/.exec(text);
    if (!match) return false;
    const size = match[2] === 'width' ? g.innerWidth : g.innerHeight;
    return match[1] === 'min' ? size >= Number(match[3]) : size <= Number(match[3]);
  };
  const styleOf = element => {
    const brand = brandFor(element);
    const style = brand && brand.cache.get('style');
    return style ? brandFor(style).over : {};
  };
  const pixels = value => { const match = /^([\d.]+)px$/.exec(String(value || '')); return match ? Number(match[1]) : null; };
  const layoutHeight = element => {
    const brand = brandFor(element);
    if (!brand || !connected(element)) return 0;
    const style = styleOf(element);
    if (style.display === 'none') return 0;
    const explicit = pixels(style.height);
    if (explicit !== null) return explicit;
    const elements = childrenOf(brand).filter(isElement);
    if (elements.length) return elements.reduce((sum, child) => sum + layoutHeight(child), 0);
    const text = textOf(brand);
    if (!text) return 0;
    return pixels(style.lineHeight) ?? Math.round((pixels(style.fontSize) || 16) * 1.2);
  };
  const trustedImage = (element, brand, source) => {
    brand.over.src = source;
    brand.over.complete = false;
    const base = api.singleton('location') ? readValue(api.singleton('location'), brandFor(api.singleton('location')), 'href') : '';
    const origin = /^https?:\/\/[^/]+/.exec(base);
    let absolute = source;
    if (/^\/\//.test(source)) absolute = 'https:' + source;
    else if (/^\//.test(source) && origin) absolute = origin[0] + source;
    else if (!/^[a-z][a-z0-9+.-]*:/i.test(source)) absolute = base.replace(/[^/]*([?#].*)?$/, '') + source;
    brand.over.currentSrc = absolute;
    const finish = loaded => {
      brand.over.complete = true;
      const event = create('Event');
      Object.assign(brandFor(event).over, { type: loaded ? 'load' : 'error', timeStamp: host.performanceNow(), isTrusted: true });
      dispatch(element, event);
    };
    if (host.loadImage) host.loadImage(absolute, finish);
    else host.queueTask(0, () => finish(false));
  };
  const trusted = (brand, method, iface, args) => {
    const rule = brand.rules[method];
    if (typeof rule !== 'function') throw new TypeError("Policy " + readValue(null, brand, 'name') + "'s TrustedTypePolicyOptions did not specify a '" + method + "' member.");
    const value = rule(...args);
    const text = value === undefined || value === null ? '' : String(value);
    const object = create(iface);
    brandFor(object).text = text;
    if (iface === 'TrustedScript') host.trust(object, text);
    return object;
  };
  // keyHash 是 nsStringHashKey 经 PLDHashTable 混淆后的键哈希，低位留作冲突标记
  const keyHash = key => {
    let hash = 0;
    for (let index = 0; index < key.length; index++) hash = Math.imul(0x9E3779B9, ((hash << 5) | (hash >>> 27)) ^ key.charCodeAt(index));
    hash = Math.imul(hash, 0x9E3779B9) >>> 0;
    if (hash < 2) hash = (hash - 2) >>> 0;
    return (hash & ~1) >>> 0;
  };
  // keyTable 按 Firefox PLDHashTable 的槽位保存 Storage 键，键顺序即槽位顺序
  const keyTable = log2 => ({ log2, hashes: new Array(2 ** log2).fill(0), keys: new Array(2 ** log2).fill(null), count: 0, removed: 0 });
  const tableSlot = (table, key, hash, adding) => {
    const mask = table.hashes.length - 1;
    const step = (hash & mask) | 1;
    let index = hash >>> (32 - table.log2);
    let removed = -1;
    for (;;) {
      const stored = table.hashes[index];
      if (stored === 0) return adding && removed >= 0 ? removed : index;
      if (stored > 1 && (stored & ~1) >>> 0 === hash && table.keys[index] === key) return index;
      if (adding && removed < 0) {
        if (stored === 1) removed = index;
        else table.hashes[index] = (stored | 1) >>> 0;
      }
      index = (index - step) & mask;
    }
  };
  const tableResize = (table, log2) => {
    const { hashes, keys } = table;
    Object.assign(table, keyTable(log2), { count: table.count });
    const mask = table.hashes.length - 1;
    for (let index = 0; index < hashes.length; index++) {
      if (hashes[index] < 2) continue;
      const hash = (hashes[index] & ~1) >>> 0;
      const step = (hash & mask) | 1;
      let slot = hash >>> (32 - log2);
      while (table.hashes[slot] > 1) {
        table.hashes[slot] = (table.hashes[slot] | 1) >>> 0;
        slot = (slot - step) & mask;
      }
      table.hashes[slot] = hash;
      table.keys[slot] = keys[index];
    }
  };
  const tableAdd = (table, key) => {
    const capacity = table.hashes.length;
    if (table.count + table.removed >= capacity - (capacity >> 2)) tableResize(table, table.log2 + (table.removed >= capacity >> 2 ? 0 : 1));
    let hash = keyHash(key);
    const index = tableSlot(table, key, hash, true);
    if (table.hashes[index] > 1) return;
    if (table.hashes[index] === 1) {
      table.removed--;
      hash = (hash | 1) >>> 0;
    }
    table.hashes[index] = hash;
    table.keys[index] = key;
    table.count++;
  };
  const tableRemove = (table, key) => {
    const index = tableSlot(table, key, keyHash(key), false);
    if (table.hashes[index] < 2) return;
    if (table.hashes[index] & 1) {
      table.hashes[index] = 1;
      table.removed++;
    } else table.hashes[index] = 0;
    table.keys[index] = null;
    table.count--;
    const capacity = table.hashes.length;
    if (table.removed >= capacity >> 2 || (capacity > 8 && table.count <= capacity >> 2)) tableResize(table, Math.max(3, Math.ceil(Math.log2(Math.ceil(table.count * 4 / 3)))));
  };
  // storageArea 返回同一 agent 内同源 Realm 共享的存储区，localStorage 按键名顺序写入账户条目
  const storageArea = brand => {
    const name = brand.area;
    if (!host.storage[name]) {
      const items = new Map();
      const restored = name === 'localStorage' ? profile.localStorage || {} : {};
      for (const key of Object.keys(restored).sort()) items.set(key, String(restored[key]));
      host.storage[name] = { items, table: null, used: 0, snapshot: name === 'localStorage' };
    }
    return host.storage[name];
  };
  // storageTable 返回当前键表；localStorage 的快照在空闲 5 秒后结束，下次访问按存储区写入顺序重建
  const storageTable = area => {
    const now = Date.now();
    if (!area.table || (area.snapshot && now - area.used >= 5000)) {
      area.table = keyTable(3);
      for (const key of area.items.keys()) tableAdd(area.table, key);
    }
    area.used = now;
    return area.table;
  };
  const storageKeys = area => storageTable(area).keys.filter(key => key !== null);
  const storageSet = (area, key, value) => {
    const table = storageTable(area);
    if (!area.items.has(key)) tableAdd(table, key);
    area.items.set(key, value);
  };
  const storageRemove = (area, key) => {
    const table = storageTable(area);
    if (!area.items.has(key)) return;
    tableRemove(table, key);
    area.items.delete(key);
  };
  const domString = value => {
    if (typeof value === 'symbol') throw new TypeError("can't convert symbol to string");
    return String(value);
  };
  const requireArguments = (member, args, count) => {
    if (args.length < count) throw new TypeError(member + ': At least ' + count + (count === 1 ? ' argument' : ' arguments') + ' required, but only ' + args.length + ' passed');
  };
  const HostProxy = Proxy;
  const { has: reflectHas, get: reflectGet, getOwnPropertyDescriptor: reflectDescriptor, defineProperty: reflectDefine, deleteProperty: reflectDelete, ownKeys: reflectOwnKeys, getPrototypeOf: reflectPrototype } = Reflect;
  const hasOwn = (object, key) => reflectDescriptor(object, key) !== undefined;
  // storageProxy 为 Storage 实例提供 WebIDL 命名属性：原型链与自有属性上没有的键名可见，赋值与 defineProperty 写入存储，delete 删除条目
  function storageProxy(target, brand) {
    const named = key => {
      if (typeof key !== 'string') return false;
      const area = storageArea(brand);
      storageTable(area);
      if (!area.items.has(key) || hasOwn(target, key)) return false;
      const proto = reflectPrototype(target);
      return proto === null || !reflectHas(proto, key);
    };
    const proxy = new HostProxy(target, {
      get(target, key, receiver) {
        return named(key) ? storageArea(brand).items.get(key) : reflectGet(target, key, receiver);
      },
      has(target, key) {
        return named(key) || reflectHas(target, key);
      },
      getOwnPropertyDescriptor(target, key) {
        return named(key) ? { value: storageArea(brand).items.get(key), writable: true, enumerable: true, configurable: true } : reflectDescriptor(target, key);
      },
      defineProperty(target, key, descriptor) {
        if (typeof key !== 'string' || hasOwn(target, key)) return reflectDefine(target, key, descriptor);
        if (!('value' in descriptor) && !('writable' in descriptor)) throw new TypeError("can't define a getter/setter for element '" + JSON.stringify(key) + "' of Storage object");
        storageSet(storageArea(brand), key, domString(descriptor.value));
        return true;
      },
      deleteProperty(target, key) {
        if (!named(key)) return reflectDelete(target, key);
        storageRemove(storageArea(brand), key);
        return true;
      },
      ownKeys(target) {
        return storageKeys(storageArea(brand)).filter(named).concat(reflectOwnKeys(target));
      },
      preventExtensions() {
        throw new TypeError("can't prevent extensions on this object");
      },
    });
    brands.set(proxy, brand);
    return proxy;
  }
  Object.assign(methods, {
    'EventTarget.addEventListener': function (brand, [type, listener, options]) {
      const name = String(type);
      const parsed = listenerOptions(options);
      if (listener == null) return;
      if (parsed.signal && brandFor(parsed.signal).over.aborted) return;
      const list = listenersOf(brand).get(name) || [];
      if (list.some(entry => !entry.handler && entry.listener === listener && entry.capture === parsed.capture)) return;
      const entry = { listener, capture: parsed.capture, once: parsed.once, passive: parsed.passive === null ? defaultPassive(this, name) : parsed.passive, removed: false };
      listenersOf(brand).set(name, [...list, entry]);
      if (parsed.signal) onAbort(parsed.signal, () => removeListener(brand, name, entry));
    },
    'EventTarget.removeEventListener': (brand, [type, listener, options]) => {
      const name = String(type);
      const capture = captureOption(options);
      const entry = (listenersOf(brand).get(name) || []).find(item => !item.handler && item.listener === listener && item.capture === capture);
      if (entry) removeListener(brand, name, entry);
    },
    'EventTarget.dispatchEvent': function (brand, [event]) { brandOf(event, 'Event', 'dispatchEvent'); return dispatch(this, event); },
    'Node.appendChild': function (brand, [node]) { return insert(this, node, null); },
    'Node.insertBefore': function (brand, [node, before]) { return insert(this, node, before || null); },
    'Node.removeChild': function (brand, [node]) { detach(node); return node; },
    'Node.replaceChild': function (brand, [node, old]) { insert(this, node, old); detach(old); return old; },
    'Node.contains': function (brand, [node]) { if (node === this) return true; return walk(this, child => child === node); },
    'Node.hasChildNodes': brand => childrenOf(brand).length > 0,
    'Node.cloneNode': function (brand, [deep]) { return cloneTree(brand, Boolean(deep)); },
    'Element.remove': function () { detach(this); },
    'Element.append': function (brand, nodes) { for (const node of nodes) insert(this, typeof node === 'string' ? api.singleton('document').createTextNode(node) : node, null); },
    'Element.prepend': function (brand, nodes) { for (const node of nodes.reverse()) insert(this, typeof node === 'string' ? api.singleton('document').createTextNode(node) : node, childrenOf(brand)[0] || null); },
    'Element.getAttribute': (brand, [name]) => attribute(brand, String(name).toLowerCase()),
    'Element.setAttribute': (brand, [name, value]) => { (brand.attrs || (brand.attrs = new Map())).set(String(name).toLowerCase(), String(value)); },
    'Element.removeAttribute': (brand, [name]) => { if (brand.attrs) brand.attrs.delete(String(name).toLowerCase()); },
    'Element.hasAttribute': (brand, [name]) => Boolean(brand.attrs && brand.attrs.has(String(name).toLowerCase())),
    'Element.getAttributeNames': brand => brand.attrs ? [...brand.attrs.keys()] : [],
    'Element.matches': function (brand, [selector]) { return matchesSelector(this, String(selector)); },
    'Element.getElementsByTagName': function (brand, [name]) {
      const tag = String(name);
      const lower = tag.toLowerCase();
      return liveCollection(this, 'tag:' + tag, 'HTMLCollection', () => descendants(this, node => isElement(node) && (tag === '*' || brandFor(node).tag === lower)));
    },
    'Element.getElementsByClassName': function (brand, [name]) {
      const text = String(name);
      const classes = text.split(/[ \t\n\f\r]+/).filter(Boolean);
      return liveCollection(this, 'class:' + text, 'HTMLCollection', () => classes.length ? descendants(this, node => isElement(node) && classes.every(item => String(attribute(brandFor(node), 'class') || '').split(/[ \t\n\f\r]+/).includes(item))) : []);
    },
    'Element.querySelector': function (brand, [selector]) { let found = null; walk(this, node => { if (matchesSelector(node, String(selector))) { found = node; return true; } }); return found; },
    'Element.querySelectorAll': function (brand, [selector]) { const found = []; walk(this, node => { if (matchesSelector(node, String(selector))) found.push(node); }); return collection('NodeList', found); },
    'Element.getBoundingClientRect': function () { return rect('DOMRect', layoutRect(this)); },
    'HTMLElement.click': function () {
      const event = create('PointerEvent');
      Object.assign(brandFor(event).over, { type: 'click', bubbles: true, cancelable: true, composed: true, detail: 0, view: null, pointerId: -1, pointerType: '', width: 1, height: 1, pressure: 0, isPrimary: true, altitudeAngle: Math.PI / 2, azimuthAngle: 0, timeStamp: host.performanceNow() });
      dispatch(this, event);
    },
    'IntersectionObserver.observe': function (brand, [target]) {
      brandOf(target, 'Element', 'observe');
      if (!brand.targets.includes(target)) brand.targets.push(target);
      const observer = this;
      host.queueTask(16, () => { if (brand.targets.includes(target)) brand.callback.call(observer, intersectionEntries([target]), observer); });
    },
    'IntersectionObserver.unobserve': (brand, [target]) => { brand.targets = brand.targets.filter(item => item !== target); },
    'IntersectionObserver.disconnect': brand => { brand.targets = []; },
    'IntersectionObserver.takeRecords': () => [],
    'GPU.requestAdapter': () => Promise.resolve(null),
    'Element.getClientRects': () => collection('DOMRectList', []),
    'Document.createElement': function (brand, [name]) { return elementFor(this, name); },
    'Document.createElementNS': function (brand, [, name]) { return elementFor(this, name); },
    'Document.createTextNode': function (brand, [data]) { const node = create('Text'); brandFor(node).over.data = String(data); brandFor(node).over.textContent = String(data); brandFor(node).over.nodeValue = String(data); brandFor(node).document = this; return node; },
    'Document.createComment': function (brand, [data]) { const node = create('Comment'); brandFor(node).over.data = String(data); brandFor(node).document = this; return node; },
    'Document.createDocumentFragment': function () { const node = create('DocumentFragment'); brandFor(node).document = this; return node; },
    'Document.createEvent': (brand, [type]) => {
      const iface = eventInterfaces[String(type).toLowerCase()];
      if (!iface || !protos[iface]) throw new g.DOMException('Operation is not supported', 'NotSupportedError');
      const event = create(iface);
      brandFor(event).over.timeStamp = g.performance ? g.performance.now() : 0;
      return event;
    },
    'Document.createRange': () => create('Range'),
    'Document.getElementById': function (brand, [id]) { let found = null; walk(this, node => { if (isElement(node) && attribute(brandFor(node), 'id') === String(id)) { found = node; return true; } }); return found; },
    'Document.getElementsByTagName': function (brand, args) { return methods['Element.getElementsByTagName'].call(this, brand, args); },
    'Document.getElementsByClassName': function (brand, args) { return methods['Element.getElementsByClassName'].call(this, brand, args); },
    'Document.querySelector': function (brand, args) { return methods['Element.querySelector'].call(this, brand, args); },
    'Document.querySelectorAll': function (brand, args) { return methods['Element.querySelectorAll'].call(this, brand, args); },
    'Document.hasFocus': () => Boolean(profile.hasFocus),
    'Event.initEvent': (brand, [type, bubbles, cancelable]) => { brand.over.type = String(type); brand.over.bubbles = Boolean(bubbles); brand.over.cancelable = Boolean(cancelable); },
    'UIEvent.initUIEvent': (brand, [type, bubbles, cancelable, view, detail]) => { Object.assign(brand.over, { type: String(type), bubbles: Boolean(bubbles), cancelable: Boolean(cancelable), view: view === undefined ? null : view, detail: Number(detail) || 0 }); },
    'MouseEvent.initMouseEvent': (brand, args) => {
      const names = ['type', 'bubbles', 'cancelable', 'view', 'detail', 'screenX', 'screenY', 'clientX', 'clientY', 'ctrlKey', 'altKey', 'shiftKey', 'metaKey', 'button', 'relatedTarget'];
      names.forEach((name, index) => { if (index < args.length) brand.over[name] = coerceLike(brand.values[name], args[index]); });
    },
    'Event.preventDefault': brand => cancelEvent(brand),
    'Event.stopPropagation': brand => { brand.stop = true; },
    'Event.stopImmediatePropagation': brand => { brand.stop = true; brand.stopImmediate = true; },
    'Event.composedPath': brand => brand.path ? brand.path.slice() : [],
    'IdleDeadline.timeRemaining': brand => brand.remaining(),
    'NodeList.item': (brand, [index]) => brand.items ? brand.items[index >>> 0] ?? null : null,
    'HTMLCollection.item': (brand, [index]) => brand.items ? brand.items[index >>> 0] ?? null : null,
    'HTMLCollection.namedItem': (brand, [name]) => { const key = String(name); return key && brand.items ? brand.items.find(node => attribute(brandFor(node), 'id') === key || attribute(brandFor(node), 'name') === key) ?? null : null; },
    'AbortController.abort': (brand, [reason]) => abortSignal(brand.over.signal, reason),
    'AbortSignal.throwIfAborted': brand => { if (brand.over.aborted) throw brand.over.reason; },
    'AbortSignal.abort:static': (brand, [reason]) => { const signal = newSignal(); Object.assign(brandFor(signal).over, { aborted: true, reason: reason === undefined ? new g.DOMException('The operation was aborted. ', 'AbortError') : reason }); return signal; },
    'AbortSignal.timeout:static': (brand, [milliseconds]) => { const signal = newSignal(); host.queueTask(Number(milliseconds), () => abortSignal(signal, new g.DOMException('The operation timed out.', 'TimeoutError'))); return signal; },
    'AbortSignal.any:static': (brand, [signals]) => {
      const sources = [...signals];
      const signal = newSignal();
      const aborted = sources.find(source => brandFor(source).over.aborted);
      if (aborted) {
        Object.assign(brandFor(signal).over, { aborted: true, reason: brandFor(aborted).over.reason });
        return signal;
      }
      for (const source of sources) onAbort(source, () => abortSignal(signal, brandFor(source).over.reason));
      return signal;
    },
    'Storage.getItem': (brand, args) => { requireArguments('Storage.getItem', args, 1); const area = storageArea(brand); const key = domString(args[0]); storageTable(area); return area.items.has(key) ? area.items.get(key) : null; },
    'Storage.setItem': (brand, args) => { requireArguments('Storage.setItem', args, 2); storageSet(storageArea(brand), domString(args[0]), domString(args[1])); },
    'Storage.removeItem': (brand, args) => { requireArguments('Storage.removeItem', args, 1); storageRemove(storageArea(brand), domString(args[0])); },
    'Storage.key': (brand, args) => { requireArguments('Storage.key', args, 1); const keys = storageKeys(storageArea(brand)); const index = args[0] >>> 0; return index < keys.length ? keys[index] : null; },
    'Storage.clear': brand => { const area = storageArea(brand); storageTable(area); area.items.clear(); area.table = keyTable(3); },
    'Location.toString': function (brand) { return readValue(this, brand, 'href'); },
    'Location.valueOf': function () { return this; },
    'Location.assign': () => {},
    'Location.replace': () => {},
    'Location.reload': () => {},
    'Permissions.query': (brand, [descriptor]) => { const status = create('PermissionStatus'); brandFor(status).over.name = String(descriptor && descriptor.name); brandFor(status).over.state = 'prompt'; return Promise.resolve(status); },
    'Navigator.javaEnabled': () => false,
    'Navigator.taintEnabled': () => false,
    'Navigator.getGamepads': () => [],
    'Navigator.sendBeacon': () => true,
    'Performance.now': () => host.performanceNow(),
    'Performance.toJSON': () => ({ timeOrigin: host.timeOrigin }),
    'Performance.getEntries': () => navigationEntries(),
    'Performance.getEntriesByType': (brand, [type]) => String(type) === 'navigation' ? navigationEntries() : [],
    'Performance.getEntriesByName': (brand, [name]) => navigationEntries().filter(entry => readValue(entry, brandFor(entry), 'name') === String(name)),
    'HTMLCanvasElement.getContext': function (brand, [type]) { if (String(type) !== '2d') return null; if (!brand.context2d) { brand.context2d = create('CanvasRenderingContext2D'); brandFor(brand.context2d).over.canvas = this; } return brand.context2d; },
    'Window.getComputedStyle': () => create('CSSStyleProperties', (shape.singletonsComputed || {}).values || shape.defaults.CSSStyleProperties),
    'Window.matchMedia': (brand, [query]) => {
      const list = create('MediaQueryList');
      const text = String(query);
      const known = (shape.mediaQueries || {})[text];
      Object.assign(brandFor(list).over, { media: Array.isArray(known) ? known[1] : text, matches: Array.isArray(known) ? Boolean(realmName === 'top' ? known[0] : known[2]) : mediaMatches(text) });
      return list;
    },
    'Window.getSelection': () => api.singleton('selection') || create('Selection'),
    'Window.requestAnimationFrame': (brand, [callback]) => {
      const frame = 1000 / 60;
      const now = host.performanceNow();
      const next = Math.floor((now - 0.66) / frame) * frame + 0.66 + frame;
      const handle = ++animationHandle;
      animationCallbacks.add(handle);
      host.queueTask(Math.max(0, Math.ceil(next - now)), () => { if (animationCallbacks.delete(handle)) callback(Math.round(next * 100) / 100); });
      return handle;
    },
    'Window.cancelAnimationFrame': (brand, [id]) => { animationCallbacks.delete(id); },
    'Window.postMessage': function (brand, args) {
      const [message, second, third] = args;
      let targetOrigin = '/';
      let transfer = [];
      if (args.length >= 3 || (second !== undefined && second !== null && typeof second !== 'object' && typeof second !== 'function')) {
        targetOrigin = String(second);
        if (third !== undefined) transfer = [...third];
      } else if (second !== undefined && second !== null) {
        if (second.targetOrigin !== undefined) targetOrigin = String(second.targetOrigin);
        if (second.transfer !== undefined) transfer = [...second.transfer];
      }
      const origin = pageOrigin();
      let accepted = true;
      if (targetOrigin !== '*' && targetOrigin !== '/') {
        const parsed = urlOrigin(targetOrigin);
        if (parsed === null) throw new g.DOMException('An invalid or illegal string was specified', 'SyntaxError');
        accepted = parsed === origin;
      }
      const data = cloneValue(message, transferMemory(transfer));
      if (!accepted) return;
      const source = host.incumbent();
      host.queueTask(0, () => {
        const event = create('MessageEvent');
        Object.assign(brandFor(event).over, { type: 'message', data, origin, lastEventId: '', source, ports: Object.freeze([]), isTrusted: true, bubbles: false, cancelable: false, timeStamp: host.performanceNow() });
        dispatch(g, event);
      });
    },
    'Window.structuredClone': (brand, [value, options]) => cloneValue(value, transferMemory(options && options.transfer !== undefined ? [...options.transfer] : [])),
    'TrustedTypePolicyFactory.createPolicy': (brand, [name, rules]) => { const policy = create('TrustedTypePolicy'); brandFor(policy).rules = rules || {}; brandFor(policy).over.name = String(name); return policy; },
    'TrustedTypePolicyFactory.isHTML': (brand, [value]) => Boolean(value && brandFor(value) && brandFor(value).iface === 'TrustedHTML'),
    'TrustedTypePolicyFactory.isScript': (brand, [value]) => Boolean(value && brandFor(value) && brandFor(value).iface === 'TrustedScript'),
    'TrustedTypePolicyFactory.isScriptURL': (brand, [value]) => Boolean(value && brandFor(value) && brandFor(value).iface === 'TrustedScriptURL'),
    'TrustedTypePolicy.createHTML': (brand, args) => trusted(brand, 'createHTML', 'TrustedHTML', args),
    'TrustedTypePolicy.createScript': (brand, args) => trusted(brand, 'createScript', 'TrustedScript', args),
    'TrustedTypePolicy.createScriptURL': (brand, args) => trusted(brand, 'createScriptURL', 'TrustedScriptURL', args),
    'TrustedHTML.toString': brand => brand.text === undefined ? '' : brand.text,
    'TrustedHTML.toJSON': brand => brand.text,
    'TrustedScript.toString': brand => brand.text,
    'TrustedScript.toJSON': brand => brand.text,
    'TrustedScriptURL.toString': brand => brand.text,
    'TrustedScriptURL.toJSON': brand => brand.text,
    'Window.focus': () => {},
    'Element.requestFullscreen': () => Promise.reject(new TypeError('Fullscreen request denied')),
    'Element.mozRequestFullScreen': () => Promise.reject(new TypeError('Fullscreen request denied')),
    'Element.requestPointerLock': () => Promise.reject(new g.DOMException('The document is not focused.', 'WrongDocumentError')),
    'Document.exitFullscreen': () => Promise.reject(new TypeError('Not in fullscreen mode')),
    'Document.mozCancelFullScreen': () => Promise.reject(new TypeError('Not in fullscreen mode')),
    'Document.hasStorageAccess': () => Promise.resolve(true),
    'Document.requestStorageAccess': () => Promise.resolve(undefined),
    'WakeLock.request': () => {
      const sentinel = create('WakeLockSentinel');
      Object.assign(brandFor(sentinel).over, { released: false, type: 'screen', onrelease: null });
      return Promise.resolve(sentinel);
    },
    'WakeLockSentinel.release': function (brand) {
      if (!brand.over.released) {
        brand.over.released = true;
        const event = create('Event');
        Object.assign(brandFor(event).over, { type: 'release', isTrusted: true, timeStamp: host.performanceNow() });
        dispatch(this, event);
      }
      return Promise.resolve(undefined);
    },
    'CookieStore.get': (brand, args) => {
      if (args.length === 0) return Promise.reject(new TypeError('CookieStore.get: CookieStoreGetOptions must not be empty'));
      const name = args[0] !== null && typeof args[0] === 'object' ? String(args[0].name) : String(args[0]);
      return Promise.resolve(cookieItems().find(item => item.name === name) || null);
    },
    'CookieStore.getAll': (brand, args) => {
      const name = args.length === 0 || args[0] === undefined ? undefined : args[0] !== null && typeof args[0] === 'object' ? args[0].name : String(args[0]);
      return Promise.resolve(cookieItems().filter(item => name === undefined || item.name === String(name)));
    },
    'GPU.requestAdapter': () => Promise.resolve(null),
    'MediaDevices.enumerateDevices': () => Promise.resolve(['audioinput', 'videoinput'].map(kind => {
      const device = create('MediaDeviceInfo');
      Object.assign(brandFor(device).over, { deviceId: '', kind, label: '', groupId: '' });
      return device;
    })),
    'MediaDevices.getUserMedia': (brand, [constraints]) => {
      if (!constraints || (!constraints.audio && !constraints.video)) return Promise.reject(new TypeError('audio and/or video is required'));
      return pending();
    },
    'MediaDevices.getDisplayMedia': () => Promise.reject(new g.DOMException('getDisplayMedia requires transient activation from a user gesture.', 'InvalidStateError')),
    'MediaDevices.selectAudioOutput': () => Promise.reject(new g.DOMException('selectAudioOutput requires transient user activation.', 'InvalidStateError')),
    'StorageManager.persisted': () => Promise.resolve(false),
    'StorageManager.estimate': () => Promise.resolve({ quota: 10737418240, usage: 0 }),
    'StorageManager.getDirectory': () => {
      const handle = create('FileSystemDirectoryHandle');
      Object.assign(brandFor(handle).over, { kind: 'directory', name: '' });
      return Promise.resolve(handle);
    },
    'Scheduler.yield': () => new Promise(resolve => host.queueTask(0, () => resolve(undefined))),
    'Scheduler.postTask': (brand, [callback, options]) => new Promise((resolve, reject) => host.queueTask(options && options.delay > 0 ? Number(options.delay) : 0, () => {
      try { resolve(callback()); } catch (error) { reject(error); }
    })),
    'IDBFactory.databases': () => Promise.resolve([]),
    'ServiceWorkerContainer.getRegistration': () => Promise.resolve(undefined),
    'ServiceWorkerContainer.getRegistrations': () => Promise.resolve([]),
    'LockManager.query': () => Promise.resolve({ held: [], pending: [] }),
    'CredentialsContainer.get': () => Promise.reject(new g.DOMException('CredentialsContainer request is not supported.', 'NotSupportedError')),
    'CredentialsContainer.create': () => Promise.reject(new g.DOMException('CredentialsContainer request is not supported.', 'NotSupportedError')),
    'CredentialsContainer.preventSilentAccess': () => Promise.resolve(undefined),
    'CacheStorage.keys': () => Promise.resolve([]),
    'CacheStorage.has': () => Promise.resolve(false),
    'CacheStorage.match': () => Promise.resolve(undefined),
    'CacheStorage.delete': () => Promise.resolve(false),
    'Response.error:static': () => {
      const response = constructors.Response([null, { status: 200 }]);
      Object.assign(brandFor(response).over, { type: 'error', status: 0, ok: false, statusText: '' });
      return response;
    },
    'Response.json:static': (brand, [data, init]) => {
      const text = JSON.stringify(data);
      if (text === undefined) throw new TypeError('Response.json: Value could not be serialized as JSON.');
      const supplied = headerList(newHeaders(init && init.headers)).some(([name]) => name === 'content-type');
      const response = constructors.Response([text, init]);
      if (!supplied) {
        const headers = brandFor(response).over.headers;
        brandFor(headers).list = headerList(headers).filter(([name]) => name !== 'content-type');
        headerList(headers).push(['content-type', 'application/json']);
      }
      return response;
    },
    'Notification.requestPermission:static': (brand, [callback]) => {
      if (typeof callback === 'function') host.queueTask(0, () => callback('default'));
      return Promise.resolve('default');
    },
    'Clipboard.read': () => Promise.reject(new g.DOMException('Clipboard read request was blocked due to lack of user activation.', 'NotAllowedError')),
    'Clipboard.readText': () => Promise.reject(new g.DOMException('Clipboard read request was blocked due to lack of user activation.', 'NotAllowedError')),
    'Blob.text': brand => Promise.resolve(bodyText(brand.bytes || new Uint8Array(0))),
    'Blob.arrayBuffer': brand => Promise.resolve((brand.bytes || new Uint8Array(0)).slice().buffer),
    'Blob.bytes': brand => Promise.resolve((brand.bytes || new Uint8Array(0)).slice()),
    'Blob.slice': (brand, [start, end, contentType]) => {
      const bytes = brand.bytes || new Uint8Array(0);
      const size = bytes.length;
      const clamp = (value, fallback) => { if (value === undefined) return fallback; const number = Math.trunc(Number(value)) || 0; return number < 0 ? Math.max(size + number, 0) : Math.min(number, size); };
      const from = clamp(start, 0);
      const to = clamp(end, size);
      const blob = blobFromParts('Blob', [bytes.slice(from, Math.max(from, to))], contentType);
      return blob;
    },
    'Response.text': (brand, args) => consumeBody(brand, 'Response', 'text').then(bodyText),
    'Response.json': (brand, args) => consumeBody(brand, 'Response', 'json').then(bytes => JSON.parse(bodyText(bytes))),
    'Response.arrayBuffer': (brand, args) => consumeBody(brand, 'Response', 'arrayBuffer').then(bytes => bytes.slice().buffer),
    'Response.bytes': (brand, args) => consumeBody(brand, 'Response', 'bytes').then(bytes => bytes.slice()),
    'Response.blob': (brand, args) => consumeBody(brand, 'Response', 'blob').then(bytes => { const type = brand.over.headers ? methods['Headers.get'](brandFor(brand.over.headers), ['content-type']) : null; return blobFromParts('Blob', [bytes], type === null ? '' : type); }),
    'URL.toString': brand => brand.url.href,
    'URL.toJSON': brand => brand.url.href,
    'URL.parse:static': (brand, [input, base]) => { const [record] = parseURLRecord(input, base); return record === null ? null : newURL(record); },
    'URL.canParse:static': (brand, [input, base]) => parseURLRecord(input, base)[0] !== null,
    'URL.createObjectURL:static': () => 'blob:' + pageOrigin() + '/' + host.randomUUID(),
    'URL.revokeObjectURL:static': () => undefined,
    'URLSearchParams.append': (brand, [name, value]) => { brand.list.push([String(name), String(value)]); updateQuery(brand); },
    'URLSearchParams.delete': (brand, [name, value]) => {
      const key = String(name);
      brand.list = brand.list.filter(([item, itemValue]) => item !== key || (value !== undefined && itemValue !== String(value)));
      updateQuery(brand);
    },
    'URLSearchParams.get': (brand, [name]) => { const pair = brand.list.find(([item]) => item === String(name)); return pair ? pair[1] : null; },
    'URLSearchParams.getAll': (brand, [name]) => brand.list.filter(([item]) => item === String(name)).map(([, value]) => value),
    'URLSearchParams.has': (brand, [name, value]) => brand.list.some(([item, itemValue]) => item === String(name) && (value === undefined || itemValue === String(value))),
    'URLSearchParams.set': (brand, [name, value]) => {
      const key = String(name);
      const index = brand.list.findIndex(([item]) => item === key);
      if (index < 0) brand.list.push([key, String(value)]);
      else brand.list = brand.list.filter(([item], position) => item !== key || position === index).map((pair, position) => position === index ? [key, String(value)] : pair);
      updateQuery(brand);
    },
    'URLSearchParams.sort': brand => {
      brand.list = brand.list.map((pair, index) => [pair, index]).sort(([a, i], [b, j]) => a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : i - j).map(([pair]) => pair);
      updateQuery(brand);
    },
    'URLSearchParams.toString': brand => serializeQuery(brand.list),
    'Crypto.getRandomValues': (brand, [array]) => {
      if (array === null || typeof array !== 'object') throw new TypeError('Crypto.getRandomValues: Argument 1 is not an object.');
      if (!integerArrays.has(typedArrayTag.call(array))) throw new g.DOMException('The type of an object is incompatible with the expected type of the parameter associated to the object', 'TypeMismatchError');
      if (array.byteLength > 65536) throw new g.DOMException('Crypto.getRandomValues: getRandomValues can only generate maximum 65536 bytes', 'QuotaExceededError');
      new Uint8Array(array.buffer, array.byteOffset, array.byteLength).set(new Uint8Array(host.randomBytes(array.byteLength)));
      return array;
    },
    'Crypto.randomUUID': () => host.randomUUID(),
    'Headers.get': (brand, [name]) => { const key = String(name).toLowerCase(); const values = (brand.list || []).filter(([item]) => item === key).map(([, value]) => value); return values.length ? values.join(', ') : null; },
    'Headers.has': (brand, [name]) => (brand.list || []).some(([item]) => item === String(name).toLowerCase()),
    'Headers.set': (brand, [name, value]) => { const key = String(name).toLowerCase(); brand.list = (brand.list || []).filter(([item]) => item !== key); brand.list.push([key, String(value).trim()]); },
    'Headers.append': (brand, [name, value]) => { (brand.list || (brand.list = [])).push([String(name).toLowerCase(), String(value).trim()]); },
    'Headers.delete': (brand, [name]) => { const key = String(name).toLowerCase(); brand.list = (brand.list || []).filter(([item]) => item !== key); },
    'SubtleCrypto.digest': (brand, [algorithm, data]) => {
      const bytes = bytesOf(data);
      if (bytes === null) return Promise.reject(new TypeError('SubtleCrypto.digest: Argument 2 could not be converted to any of: ArrayBufferView, ArrayBuffer.'));
      const name = String(algorithm !== null && typeof algorithm === 'object' ? algorithm.name : algorithm).toUpperCase();
      if (!digestNames[name]) return Promise.reject(new g.DOMException('Operation is not supported', 'NotSupportedError'));
      return Promise.resolve(host.digest(digestNames[name], bytes.buffer));
    },
    'TextEncoder.encode': (brand, [text]) => utf8Encode(text === undefined ? '' : String(text)),
    'TextEncoder.encodeInto': (brand, [text, target]) => {
      const source = String(text);
      let read = 0, written = 0;
      for (const character of source) {
        const encoded = utf8Encode(character);
        if (written + encoded.length > target.length) break;
        target.set(encoded, written);
        written += encoded.length;
        read += character.length;
      }
      return { read, written };
    },
    'TextDecoder.decode': (brand, [input, options]) => {
      const bytes = input === undefined ? new Uint8Array(0) : bytesOf(input);
      const stream = Boolean(options && options.stream);
      let text;
      if (brand.over.encoding === 'utf-8') text = utf8Decode(bytes, brand.state, stream, brand.over.fatal);
      else text = host.decodeText(brand.over.encoding, bytes.buffer, brand.over.fatal);
      if (!brand.over.ignoreBOM && !brand.bomSeen && text.length > 0) {
        brand.bomSeen = true;
        if (text.charCodeAt(0) === 0xfeff) text = text.slice(1);
      }
      if (!stream) brand.bomSeen = false;
      return text;
    },
    'Window.blur': () => {},
  });
  for (const info of shape.interfaces) {
    if (ES.has(info.n) || !info.pr.some(entry => entry.n === '@@iterator' && entry.fn === 'entries')) continue;
    const iface = info.n;
    methods[iface + '.entries'] = brand => pairIterator(iface, brand, 'key+value');
    methods[iface + '.keys'] = brand => pairIterator(iface, brand, 'key');
    methods[iface + '.values'] = brand => pairIterator(iface, brand, 'value');
    methods[iface + '.forEach'] = function (brand, [callback, thisArg]) {
      if (typeof callback !== 'function') throw new TypeError(iface + '.forEach: Argument 1 is not callable.');
      for (let index = 0; index < pairsOf(iface, brand).length; index++) {
        const [key, value] = pairsOf(iface, brand)[index];
        callback.call(thisArg, value, key, this);
      }
    };
  }
  for (const field of urlFields) {
    getters['URL.' + field] = brand => brand.url[field];
    if (field !== 'origin') setters['URL.' + field] = (brand, value) => setURLField(brand, field, value);
  }
  getters['URL.searchParams'] = brand => brand.params;
  getters['URLSearchParams.size'] = brand => brand.list.length;
  getters['PerformanceObserver.supportedEntryTypes:static'] = () => ['event', 'first-input', 'largest-contentful-paint', 'mark', 'measure', 'navigation', 'paint', 'resource'];
  getters['PushManager.supportedContentEncodings:static'] = () => Object.freeze(['aes128gcm']);
  getters['Notification.permission:static'] = () => 'default';
  getters['Notification.maxActions:static'] = () => 2;
  Object.assign(constructors, {
    URL: args => {
      if (args.length < 1) throw new TypeError('URL constructor: At least 1 argument required, but only 0 passed');
      const [record, text] = parseURLRecord(args[0], args[1]);
      if (record === null) throw new TypeError('URL constructor: ' + text + ' is not a valid URL.');
      return newURL(record);
    },
    URLSearchParams: ([init]) => {
      if (init === undefined) return newParams([], null);
      if (init === null || (typeof init !== 'object' && typeof init !== 'function')) {
        const text = String(init);
        return newParams(parseQuery(text.startsWith('?') ? text.slice(1) : text), null);
      }
      if (typeof init[Symbol.iterator] !== 'function') return newParams(Object.keys(init).map(name => [name, String(init[name])]), null);
      return newParams([...init].map(pair => {
        const items = [...pair];
        if (items.length !== 2) throw new TypeError('URLSearchParams constructor: Expected 2 items in pair but got ' + items.length);
        return [String(items[0]), String(items[1])];
      }), null);
    },
    Event: ([type, init]) => { if (type === undefined) throw new TypeError('Event constructor: At least 1 argument required, but only 0 passed'); return eventInit(create('Event'), type, init); },
    CustomEvent: ([type, init]) => { if (type === undefined) throw new TypeError('CustomEvent constructor: At least 1 argument required, but only 0 passed'); return eventInit(create('CustomEvent'), type, init); },
    UIEvent: ([type, init]) => { if (type === undefined) throw new TypeError('UIEvent constructor: At least 1 argument required, but only 0 passed'); return eventInit(create('UIEvent'), type, init); },
    MouseEvent: ([type, init]) => { if (type === undefined) throw new TypeError('MouseEvent constructor: At least 1 argument required, but only 0 passed'); return eventInit(create('MouseEvent'), type, init); },
    KeyboardEvent: ([type, init]) => { if (type === undefined) throw new TypeError('KeyboardEvent constructor: At least 1 argument required, but only 0 passed'); return eventInit(create('KeyboardEvent'), type, init); },
    FocusEvent: ([type, init]) => { if (type === undefined) throw new TypeError('FocusEvent constructor: At least 1 argument required, but only 0 passed'); return eventInit(create('FocusEvent'), type, init); },
    HTMLImageElement: () => { const element = elementFor(api.singleton('document'), 'img'); return element; },
    Image: () => elementFor(api.singleton('document'), 'img'),
    IntersectionObserver: ([callback, options]) => {
      if (callback === undefined) throw new TypeError('IntersectionObserver constructor: At least 1 argument required, but only 0 passed');
      if (typeof callback !== 'function') throw new TypeError('IntersectionObserver constructor: Argument 1 is not callable.');
      const observer = create('IntersectionObserver');
      const brand = brandFor(observer);
      Object.assign(brand, { callback, targets: [] });
      Object.assign(brand.over, { root: options && options.root !== undefined ? options.root : null, rootMargin: '0px 0px 0px 0px', scrollMargin: '0px 0px 0px 0px', thresholds: Object.freeze([0]) });
      return observer;
    },
    Audio: () => elementFor(api.singleton('document'), 'audio'),
    Option: () => elementFor(api.singleton('document'), 'option'),
    DOMException: ([message, name]) => {
      const error = create('DOMException');
      const kind = name === undefined ? 'Error' : String(name);
      Object.assign(brandFor(error).over, { message: message === undefined ? '' : String(message), name: kind, code: domExceptionCodes[kind] || 0 });
      return error;
    },
    Blob: ([parts, options]) => blobFromParts('Blob', blobParts('Blob', parts), options && options.type),
    File: ([parts, name, options]) => {
      const file = blobFromParts('File', blobParts('File', parts), options && options.type);
      Object.assign(brandFor(file).over, { name: String(name), lastModified: options && options.lastModified !== undefined ? Number(options.lastModified) : Date.now(), webkitRelativePath: '' });
      return file;
    },
    Response: ([body, init]) => {
      const response = create('Response');
      const brand = brandFor(response);
      const headers = newHeaders(init && init.headers);
      if (body !== undefined && body !== null) {
        const raw = bytesOf(body);
        const blob = brandFor(body);
        brand.bytes = raw || (blob && blob.bytes) || utf8Encode(String(body));
        if (!raw && !(blob && blob.bytes) && !headerList(headers).some(([name]) => name === 'content-type')) headerList(headers).push(['content-type', 'text/plain;charset=UTF-8']);
      }
      const status = init && init.status !== undefined ? Number(init.status) : 200;
      Object.assign(brand.over, { headers, status, ok: status >= 200 && status < 300, statusText: init && init.statusText !== undefined ? String(init.statusText) : '', type: 'default', url: '', redirected: false, bodyUsed: false });
      return response;
    },
    Headers: ([init]) => newHeaders(init),
    TextEncoder: () => create('TextEncoder'),
    TextDecoder: ([label, options]) => {
      const encoding = host.encodingName(label === undefined ? 'utf-8' : String(label));
      if (encoding === '') throw new RangeError("TextDecoder constructor: The given encoding '" + String(label).trim() + "' is not supported.");
      const decoder = create('TextDecoder');
      Object.assign(brandFor(decoder).over, { encoding, fatal: Boolean(options && options.fatal), ignoreBOM: Boolean(options && options.ignoreBOM) });
      brandFor(decoder).state = { pending: [] };
      return decoder;
    },
    AbortController: () => {
      const controller = create('AbortController');
      brandFor(controller).over.signal = newSignal();
      return controller;
    },
  });
  const iteratorPrototype = Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]()));
  memberTables = { getters: memberTable(getters), methods: memberTable(methods), special: Object.create(null) };
  for (const key of Object.keys(getters)) memberTables.special[key.slice(key.indexOf('.') + 1)] = true;
  host.onInstalled && host.onInstalled(api);
  return api;
})
