// dom.js 按 Firefox 形状表为一个 Realm 生成 Window、WebIDL 接口对象与实例
(function installDOM(shape, profile, host) {
  'use strict';
  const g = globalThis;
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
  const isA = (iface, target) => {
    for (let current = iface; current; current = parentOf[current]) if (current === target) return true;
    return false;
  };
  const brandCheck = (self, iface) => {
    if ((self === undefined || self === null) && iface === 'Window') self = g;
    const brand = self !== null && (typeof self === 'object' || typeof self === 'function') ? brands.get(self) : undefined;
    return brand && isA(brand.iface, iface) ? brand : null;
  };
  const brandError = (iface, member) => {
    const error = new TypeError("'" + member + "' called on an object that does not implement interface " + iface + '.');
    return error;
  };
  const brandOf = (self, iface, member) => {
    const brand = brandCheck(self, iface);
    if (!brand) throw brandError(iface, member);
    return brand;
  };
  const realmOfPath = path => {
    if (path.startsWith('top.') && topRealm) return [topRealm, path.slice(4)];
    return [api, path.replace(/^(frame|top)\./, '')];
  };
  const resolveRef = path => {
    const [target, key] = realmOfPath(path);
    return target.singleton(key);
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
  const readValue = (self, brand, name, declared) => {
    if (Object.prototype.hasOwnProperty.call(brand.over, name)) return brand.over[name];
    if (brand.cache.has(name)) return brand.cache.get(name);
    const special = (declared && getters[declared + '.' + name]) || getters[brand.iface + '.' + name] || getters['*.' + name];
    if (special) return special.call(self, brand);
    const encoded = Object.prototype.hasOwnProperty.call(brand.values, name) ? brand.values[name] : defaultFor(brand.iface, name);
    if (encoded === undefined) {
      return undefined;
    }
    const value = decode(encoded);
    if (encoded.t === 'o' || encoded.t === 'a' || encoded.t === 'f') brand.cache.set(name, value);
    return value;
  };
  const read = (self, brand, name, declared) => {
    const value = exposed(readValue(self, brand, name, declared));
    return value;
  };
  const write = (self, brand, name, value, declared) => {
    const special = (declared && setters[declared + '.' + name]) || setters[brand.iface + '.' + name] || setters['*.' + name];
    if (special) return special.call(self, brand, value);
    const encoded = Object.prototype.hasOwnProperty.call(brand.values, name) ? brand.values[name] : defaultFor(brand.iface, name);
    brand.over[name] = coerceLike(encoded, value);
    brand.cache.delete(name);
  };
  const invoke = (self, brand, iface, name, args) => {
    const implementation = methods[iface + '.' + name] || methods['*.' + name];
    const result = implementation ? implementation.call(self, brand, args) : undefined;
    return result;
  };
  const makeGetter = (iface, entry) => {
    const cacheKey = 'g:' + iface + ':' + entry.n;
    if (getterCache.has(cacheKey)) return getterCache.get(cacheKey);
    const name = entry.n;
    const promise = promiseMembers.has(iface + '.' + name + ':get');
    const getter = promise
      ? Object.getOwnPropertyDescriptor({ get [name]() { const brand = brandCheck(this, iface); if (!brand) return Promise.reject(brandError(iface, entry.g)); const value = read(this, brand, name, iface); return value === undefined ? pending() : value; } }, name).get
      : Object.getOwnPropertyDescriptor({ get [name]() { return read(this === undefined && iface === 'Window' ? g : this, brandOf(this, iface, entry.g), name, iface); } }, name).get;
    native(getter, entry.g, name);
    setLength(getter, entry.gl || 0);
    getterCache.set(cacheKey, getter);
    return getter;
  };
  const makeSetter = (iface, entry) => {
    const cacheKey = 's:' + iface + ':' + entry.n;
    if (getterCache.has(cacheKey)) return getterCache.get(cacheKey);
    const name = entry.n;
    const setter = Object.getOwnPropertyDescriptor({ set [name](value) { const self = this === undefined && iface === 'Window' ? g : this; write(self, brandOf(this, iface, entry.s), name, value, iface); } }, name).set;
    native(setter, entry.s, name);
    setLength(setter, entry.sl === undefined ? 1 : entry.sl);
    getterCache.set(cacheKey, setter);
    return setter;
  };
  const makeMethod = (iface, entry) => {
    const name = entry.n;
    if (name === '@@iterator' && (entry.fn === 'values' || entry.fn === 'entries')) return entry.fn === 'values' ? Array.prototype.values : Array.prototype.entries;
    const key = keyOf(name);
    const member = typeof key === 'symbol' ? entry.fn : name;
    const promise = promiseMembers.has(iface + '.' + name);
    const method = promise
      ? ({ [member](...args) { const brand = brandCheck(this, iface); if (!brand) return Promise.reject(brandError(iface, member)); const value = invoke(this, brand, iface, member, args); return value === undefined ? pending() : value; } })[member]
      : ({ [member](...args) { const self = this === undefined && iface === 'Window' ? g : this; return invoke(self, brandOf(this, iface, member), iface, member, args); } })[member];
    nativeFunction(entry.fn || member, method);
    setLength(method, entry.l || 0);
    return method;
  };
  const installEntry = (target, iface, entry, ctor) => {
    const key = keyOf(entry.n);
    if (key === undefined) return;
    const flags = entry.f || '';
    if (entry.err) return;
    if (entry.k === 'i') {
      if (entry.n === 'constructor') define(target, 'constructor', { configurable: has(flags, 'c'), enumerable: has(flags, 'e'), writable: has(flags, 'w'), value: ctor });
      return;
    }
    if (entry.k === 'v') {
      define(target, key, { configurable: has(flags, 'c'), enumerable: has(flags, 'e'), writable: has(flags, 'w'), value: entry.v.t === 'ref' || entry.v.t === 'o' ? undefined : decode(entry.v) });
      return;
    }
    if (entry.k === 'm') {
      define(target, key, { configurable: has(flags, 'c'), enumerable: has(flags, 'e'), writable: has(flags, 'w'), value: makeMethod(iface, entry) });
      return;
    }
    if (entry.k === 'a') {
      define(target, key, { configurable: has(flags, 'c'), enumerable: has(flags, 'e'), get: entry.g ? makeGetter(iface, entry) : undefined, set: entry.s ? makeSetter(iface, entry) : undefined });
    }
  };
  let quietCreate = 0;
  const exposed = value => {
    if (host.resolveInterface && value !== null && typeof value === 'object') {
      const brand = brands.get(value);
      if (brand) host.resolveInterface(brand.iface);
    }
    return value;
  };
  const create = (iface, values) => {
    const info = infos[iface];
    const proto = protos[iface] || Object.prototype;
    const object = Object.create(proto);
    const brand = { iface: info ? info.cn : iface, values: values || shape.defaults[iface] || {}, over: Object.create(null), cache: new Map(), children: null, parent: null, attrs: null, listeners: null, realm: api };
    if (host.resolveInterface && !quietCreate) host.resolveInterface(brand.iface);
    brands.set(object, brand);
    let own = shape.own[iface];
    for (let current = parentOf[iface]; !own && current; current = parentOf[current]) own = shape.own[current];
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
    const proto = Object.create(info.pp && protos[info.pp] ? protos[info.pp] : Object.prototype);
    const ctor = makeConstructor(info, proto);
    if (info.p && ctors[info.p]) Object.setPrototypeOf(ctor, ctors[info.p]);
    for (const entry of info.st) installEntry(ctor, info.cn, entry, ctor);
    for (const entry of info.pr) installEntry(proto, info.cn, entry, ctor);
    ctors[info.n] = ctor;
    protos[info.n] = proto;
  }
  for (const info of shape.interfaces) {
    if (info.cn !== info.n) {
      ctors[info.n] = ctors[info.cn];
      protos[info.n] = protos[info.cn];
    }
  }
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
      if (value === undefined && name !== 'undefined') {
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
      const overrides = (profile.values || {})[path];
      if (overrides) { const brand = brands.get(object); for (const [name, value] of Object.entries(overrides)) brand.over[name] = value; }
      if (path === 'location' && profile.location) Object.assign(brands.get(object).over, profile.location);
      if (path === 'document' && profile.location) Object.assign(brands.get(object).over, { URL: profile.location.href, documentURI: profile.location.href });
      singletons[path] = object;
      if (path === 'document') {
        quietCreate++;
        const root = elementFor(object, 'html');
        const head = elementFor(object, 'head');
        const body = elementFor(object, 'body');
        quietCreate--;
        singletons['document.documentElement'] = root;
        singletons['document.head'] = head;
        singletons['document.body'] = body;
        insert(object, root, null);
        insert(root, head, null);
        insert(root, body, null);
      }
      return object;
    },
    ctor: name => ctors[name],
    proto: name => protos[name],
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
    const parentChildren = childrenOf(brandFor(brand.parent));
    const index = parentChildren.indexOf(node);
    if (index >= 0) parentChildren.splice(index, 1);
    brand.parent = null;
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
      host.setTimeout(() => { const event = create('Event', undefined); brandFor(event).over.isTrusted = true; dispatch(node, event, 'load'); }, 0);
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
  const collection = (iface, items) => {
    const object = create(iface);
    items.forEach((item, index) => define(object, index, { configurable: true, enumerable: true, value: item }));
    brandFor(object).items = items;
    return object;
  };
  const listenersOf = brand => brand.listeners || (brand.listeners = new Map());
  const listenerOptions = options => {
    if (options === null || typeof options !== 'object') return { capture: Boolean(options), once: false };
    return { capture: Boolean(options.capture), once: Boolean(options.once) };
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
  const invokeListeners = (node, event, eventBrand, name, phase) => {
    const nodeBrand = brandFor(node);
    if (!nodeBrand) return;
    eventBrand.over.currentTarget = node;
    const list = nodeBrand.listeners ? (nodeBrand.listeners.get(name) || []).slice() : [];
    for (const entry of list) {
      if (eventBrand.stopImmediate) break;
      if (phase === 1 && !entry.capture) continue;
      if (phase === 3 && entry.capture) continue;
      if (entry.once) nodeBrand.listeners.set(name, nodeBrand.listeners.get(name).filter(item => item !== entry));
      const listener = entry.listener;
      if (typeof listener === 'function') listener.call(node, event);
      else if (listener && typeof listener.handleEvent === 'function') listener.handleEvent(event);
    }
    if (phase !== 1) {
      const handler = nodeBrand.over['on' + name];
      if (typeof handler === 'function' && !eventBrand.stopImmediate) handler.call(node, event);
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
    for (let index = path.length - 1; index > 0 && !eventBrand.stop; index--) {
      eventBrand.over.eventPhase = 1;
      invokeListeners(path[index], event, eventBrand, name, 1);
    }
    if (!eventBrand.stop) {
      eventBrand.over.eventPhase = 2;
      invokeListeners(target, event, eventBrand, name, 2);
    }
    if (bubbles) {
      for (let index = 1; index < path.length && !eventBrand.stop; index++) {
        eventBrand.over.eventPhase = 3;
        invokeListeners(path[index], event, eventBrand, name, 3);
      }
    }
    eventBrand.over.currentTarget = null;
    eventBrand.over.eventPhase = 0;
    return !eventBrand.over.defaultPrevented;
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
    '*.childNodes': brand => collection('NodeList', childrenOf(brand).slice()),
    '*.children': brand => collection('HTMLCollection', childrenOf(brand).filter(isElement)),
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
    'Element.localName': brand => brand.tag,
    'Node.nodeName': function (brand) { if (isA(brand.iface, 'Element') && brand.tag) return tagName(brand); const encoded = brand.values.nodeName || defaultFor(brand.iface, 'nodeName'); return encoded ? decode(encoded) : undefined; },
    'Element.id': brand => attribute(brand, 'id') || '',
    'Element.className': brand => attribute(brand, 'class') || '',
    'HTMLIFrameElement.contentWindow': function (brand) { if (!brand.contentWindow && connected(this) && host.createIframeWindow) brand.contentWindow = host.createIframeWindow(); return brand.contentWindow || null; },
    'HTMLElement.offsetHeight': function () { return layoutHeight(this); },
    'HTMLIFrameElement.contentDocument': function (brand) { const window = getters['HTMLIFrameElement.contentWindow'].call(this, brand); return window ? window.document : null; },
    'Event.target': brand => brand.over.target === undefined ? null : brand.over.target,
    'Storage.length': brand => Object.keys(storageOf(brand)).length,
    'Performance.timeOrigin': () => host.timeOrigin,
    'Document.cookie': () => '',
  });
  Object.assign(setters, {
    'HTMLImageElement.src': function (brand, value) { trustedImage(this, brand, String(value)); },
    'Element.id': (brand, value) => { (brand.attrs || (brand.attrs = new Map())).set('id', String(value)); },
    'Element.className': (brand, value) => { (brand.attrs || (brand.attrs = new Map())).set('class', String(value)); },
    'HTMLTextAreaElement.value': (brand, value) => { brand.over.value = String(value).replace(/\r\n?/g, '\n'); },
    'Document.cookie': () => {},
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
    const text = brand.over.textContent;
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
    else host.setTimeout(() => finish(false), 0);
  };
  const trusted = (brand, method, iface, args) => {
    const rule = brand.rules[method];
    if (typeof rule !== 'function') throw new TypeError("Policy " + readValue(null, brand, 'name') + "'s TrustedTypePolicyOptions did not specify a '" + method + "' member.");
    const value = rule(...args);
    const text = value === undefined || value === null ? '' : String(value);
    const object = create(iface);
    brandFor(object).text = text;
    host.trust(object, text);
    return object;
  };
  const storageOf = brand => brand.store || (brand.store = Object.assign({}, brand === brandFor(api.singleton('localStorage')) ? (profile.localStorage || {}) : {}));
  Object.assign(methods, {
    'EventTarget.addEventListener': (brand, [type, listener, options]) => {
      if (listener == null) return;
      const parsed = listenerOptions(options);
      const list = listenersOf(brand).get(String(type)) || [];
      if (!list.some(entry => entry.listener === listener && entry.capture === parsed.capture)) list.push({ listener, capture: parsed.capture, once: parsed.once });
      listenersOf(brand).set(String(type), list);
    },
    'EventTarget.removeEventListener': (brand, [type, listener, options]) => {
      const capture = listenerOptions(options).capture;
      const list = listenersOf(brand).get(String(type)) || [];
      listenersOf(brand).set(String(type), list.filter(entry => entry.listener !== listener || entry.capture !== capture));
    },
    'EventTarget.dispatchEvent': function (brand, [event]) { brandOf(event, 'Event', 'dispatchEvent'); return dispatch(this, event); },
    'Node.appendChild': function (brand, [node]) { return insert(this, node, null); },
    'Node.insertBefore': function (brand, [node, before]) { return insert(this, node, before || null); },
    'Node.removeChild': function (brand, [node]) { detach(node); return node; },
    'Node.replaceChild': function (brand, [node, old]) { insert(this, node, old); detach(old); return old; },
    'Node.contains': function (brand, [node]) { if (node === this) return true; return walk(this, child => child === node); },
    'Node.hasChildNodes': brand => childrenOf(brand).length > 0,
    'Node.cloneNode': function (brand) { const clone = create(brand.iface, brand.values); Object.assign(brandFor(clone), { tag: brand.tag, document: brand.document, attrs: brand.attrs ? new Map(brand.attrs) : null }); return clone; },
    'Element.remove': function () { detach(this); },
    'Element.append': function (brand, nodes) { for (const node of nodes) insert(this, typeof node === 'string' ? api.singleton('document').createTextNode(node) : node, null); },
    'Element.prepend': function (brand, nodes) { for (const node of nodes.reverse()) insert(this, typeof node === 'string' ? api.singleton('document').createTextNode(node) : node, childrenOf(brand)[0] || null); },
    'Element.getAttribute': (brand, [name]) => attribute(brand, String(name).toLowerCase()),
    'Element.setAttribute': (brand, [name, value]) => { (brand.attrs || (brand.attrs = new Map())).set(String(name).toLowerCase(), String(value)); },
    'Element.removeAttribute': (brand, [name]) => { if (brand.attrs) brand.attrs.delete(String(name).toLowerCase()); },
    'Element.hasAttribute': (brand, [name]) => Boolean(brand.attrs && brand.attrs.has(String(name).toLowerCase())),
    'Element.getAttributeNames': brand => brand.attrs ? [...brand.attrs.keys()] : [],
    'Element.matches': function (brand, [selector]) { return matchesSelector(this, String(selector)); },
    'Element.getElementsByTagName': function (brand, [name]) { const found = []; walk(this, node => { if (isElement(node) && (name === '*' || brandFor(node).tag === String(name).toLowerCase())) found.push(node); }); return collection('HTMLCollection', found); },
    'Element.getElementsByClassName': function (brand, [name]) { const found = []; walk(this, node => { if (matchesSelector(node, '.' + String(name).trim().split(/\s+/).join('.'))) found.push(node); }); return collection('HTMLCollection', found); },
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
      host.setTimeout(() => { if (brand.targets.includes(target)) brand.callback.call(observer, intersectionEntries([target]), observer); }, 16);
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
    'Event.preventDefault': brand => { if (readValue(null, brand, 'cancelable')) brand.over.defaultPrevented = true; },
    'Event.stopPropagation': brand => { brand.stop = true; },
    'Event.stopImmediatePropagation': brand => { brand.stop = true; brand.stopImmediate = true; },
    'Event.composedPath': () => [],
    'Storage.getItem': (brand, [key]) => { const store = storageOf(brand); return Object.prototype.hasOwnProperty.call(store, String(key)) ? store[String(key)] : null; },
    'Storage.setItem': (brand, [key, value]) => { storageOf(brand)[String(key)] = String(value); },
    'Storage.removeItem': (brand, [key]) => { delete storageOf(brand)[String(key)]; },
    'Storage.key': (brand, [index]) => Object.keys(storageOf(brand))[index] ?? null,
    'Storage.clear': brand => { brand.store = {}; },
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
      return host.setTimeout(() => callback(Math.round(next * 100) / 100), Math.max(0, Math.ceil(next - now)));
    },
    'Window.cancelAnimationFrame': (brand, [id]) => host.clearTimeout(id),
    'Window.postMessage': () => {},
    'Window.structuredClone': (brand, [value]) => value,
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
    'Window.blur': () => {},
  });
  Object.assign(constructors, {
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
  });
  const iteratorPrototype = Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]()));
  const intrinsicPaths = {
    '%TypedArray%': () => Object.getPrototypeOf(g.Int8Array),
    '%TypedArray%.prototype': () => Object.getPrototypeOf(g.Int8Array.prototype),
    '%IteratorPrototype%': () => iteratorPrototype,
    '%ArrayIteratorPrototype%': () => Object.getPrototypeOf([][Symbol.iterator]()),
    '%MapIteratorPrototype%': () => Object.getPrototypeOf(new Map()[Symbol.iterator]()),
    '%SetIteratorPrototype%': () => Object.getPrototypeOf(new Set()[Symbol.iterator]()),
    '%StringIteratorPrototype%': () => Object.getPrototypeOf(''[Symbol.iterator]()),
    '%RegExpStringIteratorPrototype%': () => Object.getPrototypeOf(/a/g[Symbol.matchAll]('')),
    '%GeneratorFunction%': () => Object.getPrototypeOf(function* () {}).constructor,
    '%GeneratorFunction.prototype%': () => Object.getPrototypeOf(function* () {}),
    '%GeneratorPrototype%': () => Object.getPrototypeOf(function* () {}).prototype,
    '%AsyncFunction%': () => Object.getPrototypeOf(async function () {}).constructor,
    '%AsyncFunction.prototype%': () => Object.getPrototypeOf(async function () {}),
  };
  const resolveBuiltinPath = path => {
    if (intrinsicPaths[path]) { try { return intrinsicPaths[path](); } catch (e) { return undefined; } }
    let object = g;
    for (const part of path.split('.')) {
      if (object === undefined || object === null) return undefined;
      const descriptor = Object.getOwnPropertyDescriptor(object, part);
      object = descriptor && 'value' in descriptor ? descriptor.value : undefined;
    }
    return object;
  };
  const requireObjectCoercible = (value, name) => { if (value === undefined || value === null) throw new TypeError('String.prototype.' + name + ' called on null or undefined'); return value; };
  const htmlMethod = (name, tag, attribute) => function (value) {
    const text = String(requireObjectCoercible(this, name));
    return '<' + tag + (attribute ? ' ' + attribute + '="' + String(value).replace(/"/g, '&quot;') + '"' : '') + '>' + text + '</' + tag + '>';
  };
  const loneSurrogate = (text, index) => {
    const code = text.charCodeAt(index);
    if (code >= 0xD800 && code <= 0xDBFF) { const next = text.charCodeAt(index + 1); return next >= 0xDC00 && next <= 0xDFFF ? 0 : 1; }
    return code >= 0xDC00 && code <= 0xDFFF ? 1 : -1;
  };
  const float16Round = value => {
    const number = Number(value);
    if (!Number.isFinite(number) || number === 0) return number;
    const sign = number < 0 ? -1 : 1;
    const magnitude = Math.abs(number);
    const exponent = Math.max(Math.floor(Math.log2(magnitude)), -14);
    const quantum = Math.pow(2, exponent - 10);
    const scaled = magnitude / quantum;
    let rounded = Math.floor(scaled);
    const remainder = scaled - rounded;
    if (remainder > 0.5 || (remainder === 0.5 && rounded % 2 === 1)) rounded++;
    const result = rounded * quantum;
    return sign * (result > 65504 ? Infinity : result);
  };
  const setLike = other => ({ size: Number(other.size), has: value => other.has(value), keys: () => other.keys() });
  const builtinImplementations = {
    'String.prototype.anchor': htmlMethod('anchor', 'a', 'name'),
    'String.prototype.big': htmlMethod('big', 'big'),
    'String.prototype.blink': htmlMethod('blink', 'blink'),
    'String.prototype.bold': htmlMethod('bold', 'b'),
    'String.prototype.fixed': htmlMethod('fixed', 'tt'),
    'String.prototype.fontcolor': htmlMethod('fontcolor', 'font', 'color'),
    'String.prototype.fontsize': htmlMethod('fontsize', 'font', 'size'),
    'String.prototype.italics': htmlMethod('italics', 'i'),
    'String.prototype.link': htmlMethod('link', 'a', 'href'),
    'String.prototype.small': htmlMethod('small', 'small'),
    'String.prototype.strike': htmlMethod('strike', 'strike'),
    'String.prototype.sub': htmlMethod('sub', 'sub'),
    'String.prototype.sup': htmlMethod('sup', 'sup'),
    'String.prototype.isWellFormed': function () {
      const text = String(requireObjectCoercible(this, 'isWellFormed'));
      for (let index = 0; index < text.length; index++) { const kind = loneSurrogate(text, index); if (kind === 1) return false; if (kind === 0) index++; }
      return true;
    },
    'String.prototype.toWellFormed': function () {
      const text = String(requireObjectCoercible(this, 'toWellFormed'));
      let output = '';
      for (let index = 0; index < text.length; index++) {
        const kind = loneSurrogate(text, index);
        if (kind === 1) { output += '�'; continue; }
        if (kind === 0) { output += text[index] + text[index + 1]; index++; continue; }
        output += text[index];
      }
      return output;
    },
    'Date.prototype.getYear': function () { const year = Date.prototype.getFullYear.call(this); return Number.isNaN(year) ? NaN : year - 1900; },
    'Date.prototype.setYear': function (value) { let year = Number(value); if (Number.isNaN(year)) return Date.prototype.setTime.call(this, NaN); year = Math.trunc(year); if (year >= 0 && year <= 99) year += 1900; if (Number.isNaN(Date.prototype.getTime.call(this))) Date.prototype.setTime.call(this, 0); return Date.prototype.setFullYear.call(this, year); },
    'Date.prototype.toGMTString': Date.prototype.toUTCString,
    'Object.prototype.__defineGetter__': function (name, getter) { if (typeof getter !== 'function') throw new TypeError('invalid getter usage'); define(Object(this), name, { get: getter, enumerable: true, configurable: true }); },
    'Object.prototype.__defineSetter__': function (name, setter) { if (typeof setter !== 'function') throw new TypeError('invalid setter usage'); define(Object(this), name, { set: setter, enumerable: true, configurable: true }); },
    'Object.prototype.__lookupGetter__': function (name) { for (let object = Object(this); object; object = Object.getPrototypeOf(object)) { const descriptor = Object.getOwnPropertyDescriptor(object, name); if (descriptor) return descriptor.get; } return undefined; },
    'Object.prototype.__lookupSetter__': function (name) { for (let object = Object(this); object; object = Object.getPrototypeOf(object)) { const descriptor = Object.getOwnPropertyDescriptor(object, name); if (descriptor) return descriptor.set; } return undefined; },
    'Object.groupBy': (items, callback) => { const groups = Object.create(null); let index = 0; for (const item of items) { const key = callback(item, index++); const property = typeof key === 'symbol' ? key : String(key); (groups[property] || (groups[property] = [])).push(item); } return groups; },
    'Map.groupBy': (items, callback) => { const groups = new Map(); let index = 0; for (const item of items) { const key = callback(item, index++); if (!groups.has(key)) groups.set(key, []); groups.get(key).push(item); } return groups; },
    'Array.fromAsync': items => Promise.resolve(Array.from(items)),
    'Promise.withResolvers': function () { let resolve; let reject; const promise = new this((a, b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; },
    'Promise.try': function (callback, ...args) { return new this(resolve => resolve(callback(...args))); },
    'Error.captureStackTrace': object => { if (object === null || typeof object !== 'object') throw new TypeError('Error.captureStackTrace: Argument 1 is not an object.'); define(object, 'stack', { value: new Error().stack.split('\n').slice(1).join('\n'), writable: true, configurable: true }); },
    'Math.f16round': float16Round,
    'Math.sumPrecise': items => { let total = -0; for (const item of items) { if (typeof item !== 'number') throw new TypeError('Math.sumPrecise: value is not a number'); total += item; } return total; },
    'Set.prototype.union': function (other) { const result = new Set(this); for (const value of setLike(other).keys()) result.add(value); return result; },
    'Set.prototype.intersection': function (other) { const like = setLike(other); const result = new Set(); for (const value of this) if (like.has(value)) result.add(value); return result; },
    'Set.prototype.difference': function (other) { const like = setLike(other); const result = new Set(); for (const value of this) if (!like.has(value)) result.add(value); return result; },
    'Set.prototype.symmetricDifference': function (other) { const result = new Set(this); for (const value of setLike(other).keys()) { if (this.has(value)) result.delete(value); else result.add(value); } return result; },
    'Set.prototype.isSubsetOf': function (other) { const like = setLike(other); for (const value of this) if (!like.has(value)) return false; return true; },
    'Set.prototype.isSupersetOf': function (other) { for (const value of setLike(other).keys()) if (!this.has(value)) return false; return true; },
    'Set.prototype.isDisjointFrom': function (other) { const like = setLike(other); for (const value of this) if (like.has(value)) return false; return true; },
    'Map.prototype.getOrInsert': function (key, value) { if (!this.has(key)) this.set(key, value); return this.get(key); },
    'Map.prototype.getOrInsertComputed': function (key, callback) { if (!this.has(key)) this.set(key, callback(key)); return this.get(key); },
    'WeakMap.prototype.getOrInsert': function (key, value) { if (!this.has(key)) this.set(key, value); return this.get(key); },
    'WeakMap.prototype.getOrInsertComputed': function (key, callback) { if (!this.has(key)) this.set(key, callback(key)); return this.get(key); },
    'RegExp.escape': value => { if (typeof value !== 'string') throw new TypeError('RegExp.escape: Argument 1 is not a string.'); return value.replace(/[\\^$.*+?()[\]{}|\/]/g, '\\$&'); },
    'Uint8Array.fromBase64': text => { const binary = g.atob(String(text).replace(/-/g, '+').replace(/_/g, '/')); const bytes = new Uint8Array(binary.length); for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index); return bytes; },
    'Uint8Array.prototype.toBase64': function () { let binary = ''; for (let index = 0; index < this.length; index++) binary += String.fromCharCode(this[index]); return g.btoa(binary); },
    'Atomics.isLockFree': size => [1, 2, 4, 8].includes(Number(size)),
    'Atomics.load': (array, index) => array[index],
    'Atomics.store': (array, index, value) => (array[index] = value, array[index]),
    'Atomics.add': (array, index, value) => { const old = array[index]; array[index] = old + value; return old; },
    'Atomics.sub': (array, index, value) => { const old = array[index]; array[index] = old - value; return old; },
    'Atomics.and': (array, index, value) => { const old = array[index]; array[index] = old & value; return old; },
    'Atomics.or': (array, index, value) => { const old = array[index]; array[index] = old | value; return old; },
    'Atomics.xor': (array, index, value) => { const old = array[index]; array[index] = old ^ value; return old; },
    'Atomics.exchange': (array, index, value) => { const old = array[index]; array[index] = value; return old; },
    'Atomics.compareExchange': (array, index, expected, value) => { const old = array[index]; if (old === expected) array[index] = value; return old; },
    'Atomics.notify': () => 0,
    'Atomics.pause': () => undefined,
    '%IteratorPrototype%.map': function* (mapper) { let index = 0; for (const value of { [Symbol.iterator]: () => this }) yield mapper(value, index++); },
    '%IteratorPrototype%.filter': function* (predicate) { let index = 0; for (const value of { [Symbol.iterator]: () => this }) if (predicate(value, index++)) yield value; },
    '%IteratorPrototype%.take': function* (limit) { let remaining = Number(limit); if (remaining <= 0) return; for (const value of { [Symbol.iterator]: () => this }) { yield value; if (--remaining <= 0) return; } },
    '%IteratorPrototype%.drop': function* (limit) { let remaining = Number(limit); for (const value of { [Symbol.iterator]: () => this }) { if (remaining > 0) { remaining--; continue; } yield value; } },
    '%IteratorPrototype%.flatMap': function* (mapper) { let index = 0; for (const value of { [Symbol.iterator]: () => this }) yield* mapper(value, index++); },
    '%IteratorPrototype%.reduce': function (reducer, ...initial) { let index = 0; let accumulator = initial[0]; let first = initial.length === 0; for (const value of { [Symbol.iterator]: () => this }) { if (first) { accumulator = value; first = false; index++; continue; } accumulator = reducer(accumulator, value, index++); } if (first) throw new TypeError('reduce of empty iterator with no initial value'); return accumulator; },
    '%IteratorPrototype%.toArray': function () { return Array.from({ [Symbol.iterator]: () => this }); },
    '%IteratorPrototype%.forEach': function (callback) { let index = 0; for (const value of { [Symbol.iterator]: () => this }) callback(value, index++); },
    '%IteratorPrototype%.some': function (predicate) { let index = 0; for (const value of { [Symbol.iterator]: () => this }) if (predicate(value, index++)) return true; return false; },
    '%IteratorPrototype%.every': function (predicate) { let index = 0; for (const value of { [Symbol.iterator]: () => this }) if (!predicate(value, index++)) return false; return true; },
    '%IteratorPrototype%.find': function (predicate) { let index = 0; for (const value of { [Symbol.iterator]: () => this }) if (predicate(value, index++)) return value; return undefined; },
    '%IteratorPrototype%.@@dispose': function () { const close = this.return; if (close !== undefined) close.call(this); },
  };
  const builtinAccessors = {
    '%IteratorPrototype%.constructor': [function () { return g.Iterator; }, function (value) { define(this, 'constructor', { value, writable: true, enumerable: false, configurable: true }); }],
    '%IteratorPrototype%.@@toStringTag': [function () { return 'Iterator'; }, function (value) { define(this, Symbol.toStringTag, { value, writable: true, enumerable: false, configurable: true }); }],
    'RegExp.prototype.hasIndices': [function () { if (this === RegExp.prototype) return undefined; if (!(this instanceof RegExp)) throw new TypeError('RegExp.prototype.hasIndices getter called on non-RegExp object'); return false; }, null],
    'RegExp.prototype.unicodeSets': [function () { if (this === RegExp.prototype) return undefined; if (!(this instanceof RegExp)) throw new TypeError('RegExp.prototype.unicodeSets getter called on non-RegExp object'); return false; }, null],
    'ArrayBuffer.prototype.maxByteLength': [function () { return this.byteLength; }, null],
    'ArrayBuffer.prototype.resizable': [function () { return false; }, null],
    'ArrayBuffer.prototype.detached': [function () { return false; }, null],
  };
  const stubFunction = (entry, constructor) => {
    const implementation = constructor ? function () {} : { [entry.fn || 'value']() { return undefined; } }[entry.fn || 'value'];
    if (constructor) define(implementation, 'prototype', { value: Object.create(Object.prototype), writable: false, enumerable: false, configurable: false });
    return implementation;
  };
  const alignBuiltinObject = (path, own) => {
    const target = resolveBuiltinPath(path);
    if (target === undefined || target === null) return;
    const listed = new Set();
    for (const entry of own) {
      const key = entry.n.startsWith('@@') ? Symbol[entry.n.slice(2)] : entry.n;
      if (key === undefined) continue;
      listed.add(key);
      if (Object.prototype.hasOwnProperty.call(target, key)) continue;
      const fullName = path + '.' + entry.n;
      const flags = entry.f || '';
      if (entry.k === 'm') {
        let value = builtinImplementations[fullName];
        if (value === undefined) value = stubFunction(entry, entry.ctor);
        if (!builtinImplementations[fullName] || builtinImplementations[fullName] !== Date.prototype.toUTCString) {
          native(value, entry.fn, entry.fn);
          setLength(value, entry.l || 0);
        }
        define(target, key, { value, writable: has(flags, 'w'), enumerable: has(flags, 'e'), configurable: has(flags, 'c') });
      } else if (entry.k === 'a') {
        const pair = builtinAccessors[fullName] || [entry.g ? function () { return undefined; } : null, entry.s ? function () {} : null];
        const getter = entry.g ? native(pair[0] || function () { return undefined; }, entry.g, entry.g) : undefined;
        const setter = entry.s ? native(pair[1] || function () {}, entry.s, entry.s) : undefined;
        if (getter) setLength(getter, 0);
        if (setter) setLength(setter, 1);
        define(target, key, { get: getter, set: setter, enumerable: has(flags, 'e'), configurable: has(flags, 'c') });
      } else if (entry.k === 'o') {
        const value = {};
        const tag = /^\[object (.*)\]$/.exec(entry.tag || '');
        if (tag && tag[1] !== 'Object') define(value, Symbol.toStringTag, { value: tag[1], configurable: true });
        define(target, key, { value, writable: has(flags, 'w'), enumerable: has(flags, 'e'), configurable: has(flags, 'c') });
      } else if (entry.k === 'v') {
        define(target, key, { value: decode(entry.v), writable: has(flags, 'w'), enumerable: has(flags, 'e'), configurable: has(flags, 'c') });
      }
    }
    for (const key of Reflect.ownKeys(target)) {
      if (listed.has(key)) continue;
      const descriptor = Object.getOwnPropertyDescriptor(target, key);
      if (descriptor && descriptor.configurable) delete target[key];
    }
    if (host.orderKeys) host.orderKeys(target, own.map(entry => entry.n).filter(name => !name.startsWith('@@')));
  };
  if (typeof g.Iterator === 'function' && g.Iterator.prototype !== iteratorPrototype) {
    const Iterator = function Iterator() { if (new.target === undefined || new.target === Iterator) throw new TypeError('Iterator constructor can\'t be used directly'); };
    native(Iterator, 'Iterator', 'Iterator');
    define(Iterator, 'prototype', { value: iteratorPrototype, writable: false, enumerable: false, configurable: false });
    define(g, 'Iterator', { value: Iterator, writable: true, enumerable: false, configurable: true });
    ctors.Iterator = Iterator;
    protos.Iterator = iteratorPrototype;
  }
  for (const item of shape.builtins || []) alignBuiltinObject(item.path, item.own);
  host.onInstalled && host.onInstalled(api);
  return api;
})
