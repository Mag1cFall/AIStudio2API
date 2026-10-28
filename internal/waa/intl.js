// intl.js 按 Firefox 152 的 Intl 行为为一个 Realm 实现 Intl 构造器、toLocaleString 与 localeCompare
(function installIntl(Intl, host, helpers) {
  'use strict';
  const { native, setLength, define } = helpers;
  const slots = new WeakMap();
  const defaultLocale = host.defaultLocale;
  const accountRegion = (/-([A-Z]{2}|\d{3})$/.exec(defaultLocale) || [])[1];
  const widths = ['narrow', 'short', 'long'];
  const fieldOrder = ['weekday', 'era', 'year', 'month', 'day', 'dayPeriod', 'hour', 'minute', 'second', 'fractionalSecondDigits', 'timeZoneName'];
  const keyValues = {
    ca: value => value === 'islamic' || host.supportedValues('calendar').includes(value),
    co: value => host.supportedValues('collation').includes(value),
    nu: value => host.supportedValues('numberingSystem').includes(value),
    hc: value => ['h11', 'h12', 'h23', 'h24'].includes(value),
    kf: value => ['upper', 'lower', 'false'].includes(value),
    kn: value => value === 'true' || value === 'false',
  };

  const incompatible = (method, value) => {
    const kind = value === null || value === undefined ? String(value) : typeof value === 'object' ? (Array.isArray(value) ? 'Array' : 'Object') : typeof value === 'function' ? 'Function' : typeof value;
    return new TypeError(method + ' method called on incompatible ' + kind);
  };
  const slotOf = (object, kind, method) => {
    const slot = object !== null && (typeof object === 'object' || typeof object === 'function') ? slots.get(object) : undefined;
    if (!slot || slot.kind !== kind) throw incompatible(method, object);
    return slot;
  };
  const toObject = value => {
    if (value === null || value === undefined) throw new TypeError("can't convert " + value + ' to object');
    return Object(value);
  };
  const getOption = (options, name, type, values, fallback) => {
    let value = options[name];
    if (value === undefined) return fallback;
    value = type === 'boolean' ? Boolean(value) : String(value);
    if (values && !values.includes(value)) throw new RangeError('invalid value ' + JSON.stringify(value) + ' for option ' + name);
    return value;
  };
  const getNumberOption = (options, name, minimum, maximum, fallback) => {
    const value = options[name];
    if (value === undefined) return fallback;
    const number = Number(value);
    if (Number.isNaN(number) || number < minimum || number > maximum) throw new RangeError('invalid digits value: ' + String(value));
    return Math.floor(number);
  };
  const typeIdentifier = (value, name) => {
    if (!/^[a-zA-Z0-9]{3,8}(-[a-zA-Z0-9]{3,8})*$/.test(value)) throw new RangeError('invalid value ' + JSON.stringify(value) + ' for option ' + name);
    return value.toLowerCase();
  };

  const canonicalTag = tag => {
    const result = host.icu(1, [tag]);
    if (result === -3) throw new RangeError('invalid language tag: ' + JSON.stringify(tag));
    if (result === -4) throw new RangeError('duplicate variant subtag');
    if (typeof result === 'number') throw new Error('internal error while computing Intl data');
    return result[0];
  };
  const tagParts = tag => {
    const parts = tag.split('-');
    let index = 1;
    const result = { language: parts[0], script: '', region: '', variants: '' };
    if (index < parts.length && parts[index].length === 4) result.script = parts[index++];
    if (index < parts.length && (parts[index].length === 2 || parts[index].length === 3 && /^\d/.test(parts[index]))) result.region = parts[index++];
    const variants = [];
    while (index < parts.length && parts[index].length > 1) variants.push(parts[index++]);
    result.variants = variants.join('-');
    return result;
  };
  const isLocale = value => value !== null && typeof value === 'object' && slots.has(value) && slots.get(value).kind === 'Locale';
  const canonicalList = locales => {
    if (locales === undefined) return [];
    const seen = [];
    const add = value => {
      const tag = String(value);
      const canonical = canonicalTag(tag);
      if (!seen.includes(canonical)) seen.push(canonical);
    };
    if (typeof locales === 'string' || isLocale(locales)) {
      add(isLocale(locales) ? slots.get(locales).tag : locales);
      return seen;
    }
    const list = toObject(locales);
    const length = Math.min(Math.max(Math.floor(Number(list.length)) || 0, 0), Number.MAX_SAFE_INTEGER);
    for (let index = 0; index < length; index++) {
      if (!(index in list)) continue;
      const value = list[index];
      if (typeof value !== 'string' && (value === null || typeof value !== 'object')) throw new TypeError('invalid element in locales argument');
      add(isLocale(value) ? slots.get(value).tag : value);
    }
    return seen;
  };
  const extensionOf = tag => {
    const match = /(?:^|-)u((?:-[a-z0-9]{2,8})+)/.exec(tag.split('-x-')[0]);
    const keywords = {};
    if (!match) return keywords;
    let key = null;
    for (const part of match[1].slice(1).split('-')) {
      if (part.length === 1) break;
      if (part.length === 2) { key = part; keywords[key] = ''; continue; }
      if (key !== null) keywords[key] = keywords[key] ? keywords[key] + '-' + part : part;
    }
    return keywords;
  };
  const resolveLocale = (requested, collator, keys, options) => {
    let found = '';
    let keywords = {};
    for (const tag of requested) {
      found = host.lookup(tag, collator);
      if (found) { keywords = extensionOf(tag); break; }
    }
    if (!found) found = defaultLocale;
    const result = { found };
    const added = [];
    for (const key of keys) {
      let value;
      let fromExtension = false;
      if (Object.prototype.hasOwnProperty.call(keywords, key)) {
        const keyword = keywords[key] === '' ? 'true' : keywords[key];
        if (keyValues[key](keyword)) { value = keyword; fromExtension = true; }
      }
      const option = options[key];
      if (option === null) { value = undefined; fromExtension = false; } else if (option !== undefined && keyValues[key](option) && option !== value) { value = option; fromExtension = false; }
      result[key] = value;
      if (fromExtension) added.push(value === 'true' ? key : key + '-' + value);
    }
    result.locale = added.length ? found + '-u-' + added.join('-') : found;
    return result;
  };
  const supportedLocalesOf = collator => function supportedLocalesOf(locales, options) {
    const requested = canonicalList(locales);
    if (options !== undefined) getOption(toObject(options), 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
    return requested.filter(tag => host.lookup(tag, collator) !== '');
  };

  const install = (target, members, lengths = {}) => {
    for (const key of Reflect.ownKeys(members)) {
      const descriptor = Object.getOwnPropertyDescriptor(members, key);
      const name = typeof key === 'symbol' ? '[' + key.description.replace(/^Symbol\./, 'Symbol.') + ']' : key;
      if (descriptor.get) {
        native(descriptor.get, 'get ' + name, 'get ' + name);
        setLength(descriptor.get, 0);
        define(target, key, { get: descriptor.get, set: undefined, enumerable: false, configurable: true });
        continue;
      }
      if (typeof descriptor.value === 'function') {
        native(descriptor.value, name, name);
        if (lengths[name] !== undefined) setLength(descriptor.value, lengths[name]);
      }
      define(target, key, { value: descriptor.value, writable: true, enumerable: false, configurable: true });
    }
  };
  const anonymous = (length, implementation) => {
    const fn = { ''(...args) { return implementation(...args); } }[''];
    native(fn, '', '');
    setLength(fn, length);
    return fn;
  };
  const makeClass = (Constructor, name, length, prototypeMembers, lengths, collator) => {
    native(Constructor, name, name);
    setLength(Constructor, length);
    const prototype = Constructor.prototype;
    install(prototype, prototypeMembers, lengths);
    define(prototype, 'constructor', { value: Constructor, writable: true, enumerable: false, configurable: true });
    define(prototype, Symbol.toStringTag, { value: 'Intl.' + name, writable: false, enumerable: false, configurable: true });
    define(Constructor, 'prototype', { writable: false, enumerable: false, configurable: false });
    if (collator !== null) {
      const supported = supportedLocalesOf(collator);
      native(supported, 'supportedLocalesOf', 'supportedLocalesOf');
      define(Constructor, 'supportedLocalesOf', { value: supported, writable: true, enumerable: false, configurable: true });
    }
    define(Intl, name, { value: Constructor, writable: true, enumerable: false, configurable: true });
    return Constructor;
  };
  const requireNew = (target, name) => {
    if (target === undefined) throw new TypeError('calling a builtin Intl.' + name + ' constructor without new is forbidden');
  };

  // Locale
  const localeString = (parts, keywords) => {
    let tag = [parts.language, parts.script, parts.region, parts.variants].filter(Boolean).join('-');
    const keys = Object.keys(keywords).filter(key => keywords[key] !== undefined).sort();
    if (keys.length) tag += '-u-' + keys.map(key => keywords[key] === '' || keywords[key] === 'true' ? key : key + '-' + keywords[key]).join('-');
    return tag;
  };
  const localeSlot = tag => {
    const canonical = canonicalTag(tag);
    const parts = tagParts(canonical);
    const slot = { kind: 'Locale', language: parts.language, script: parts.script, region: parts.region, variants: parts.variants, keywords: extensionOf(canonical) };
    slot.tag = localeString(slot, slot.keywords);
    return slot;
  };
  const Locale = function Locale(tag, options) {
    requireNew(new.target, 'Locale');
    if (typeof tag !== 'string' && (tag === null || typeof tag !== 'object')) throw new TypeError('invalid type for tag argument');
    const slot = localeSlot(isLocale(tag) ? slots.get(tag).tag : String(tag));
    if (options !== undefined) {
      const opts = toObject(options);
      const language = getOption(opts, 'language', 'string');
      if (language !== undefined) { if (!/^([a-zA-Z]{2,3}|[a-zA-Z]{5,8})$/.test(language)) throw new RangeError('invalid language subtag: ' + language); slot.language = language.toLowerCase(); }
      const script = getOption(opts, 'script', 'string');
      if (script !== undefined) { if (!/^[a-zA-Z]{4}$/.test(script)) throw new RangeError('invalid script subtag: ' + script); slot.script = script[0].toUpperCase() + script.slice(1).toLowerCase(); }
      const region = getOption(opts, 'region', 'string');
      if (region !== undefined) { if (!/^([a-zA-Z]{2}|\d{3})$/.test(region)) throw new RangeError('invalid region subtag: ' + region); slot.region = region.toUpperCase(); }
      const set = (key, value) => { if (value !== undefined) slot.keywords[key] = value; };
      const calendar = getOption(opts, 'calendar', 'string');
      set('ca', calendar === undefined ? undefined : typeIdentifier(calendar, 'calendar'));
      const collation = getOption(opts, 'collation', 'string');
      set('co', collation === undefined ? undefined : typeIdentifier(collation, 'collation'));
      set('hc', getOption(opts, 'hourCycle', 'string', ['h11', 'h12', 'h23', 'h24']));
      set('kf', getOption(opts, 'caseFirst', 'string', ['upper', 'lower', 'false']));
      const numeric = getOption(opts, 'numeric', 'boolean');
      set('kn', numeric === undefined ? undefined : String(numeric));
      const numberingSystem = getOption(opts, 'numberingSystem', 'string');
      set('nu', numberingSystem === undefined ? undefined : typeIdentifier(numberingSystem, 'numberingSystem'));
      const rebuilt = localeSlot(localeString(slot, slot.keywords));
      Object.assign(slot, rebuilt);
    }
    slots.set(this, slot);
  };
  const keyword = name => function () { return slotOf(this, 'Locale', 'get ' + name).keywords[{ calendar: 'ca', caseFirst: 'kf', collation: 'co', hourCycle: 'hc', numberingSystem: 'nu' }[name]]; };
  const adjustLocale = (slot, adjust) => new Locale(adjust(localeString(slot, {})) + localeString({}, slot.keywords));
  const localeMembers = {
    maximize() { return adjustLocale(slotOf(this, 'Locale', 'maximize'), tag => icu(16, [tag])[0]); },
    minimize() { return adjustLocale(slotOf(this, 'Locale', 'minimize'), tag => icu(17, [tag])[0]); },
    toString() { return slotOf(this, 'Locale', 'toString').tag; },
    get baseName() { const slot = slotOf(this, 'Locale', 'get baseName'); return [slot.language, slot.script, slot.region, slot.variants].filter(Boolean).join('-'); },
    get calendar() { return keyword('calendar').call(this); },
    get caseFirst() { return keyword('caseFirst').call(this); },
    get collation() { return keyword('collation').call(this); },
    get hourCycle() { return keyword('hourCycle').call(this); },
    get numeric() { const value = slotOf(this, 'Locale', 'get numeric').keywords.kn; return value === '' || value === 'true'; },
    get numberingSystem() { return keyword('numberingSystem').call(this); },
    get language() { return slotOf(this, 'Locale', 'get language').language; },
    get script() { return slotOf(this, 'Locale', 'get script').script || undefined; },
    get region() { return slotOf(this, 'Locale', 'get region').region || undefined; },
    get variants() { return slotOf(this, 'Locale', 'get variants').variants || undefined; },
  };
  makeClass(Locale, 'Locale', 1, localeMembers, {}, null);

  // Collator
  const Collator = function Collator(locales, options) {
    if (new.target === undefined) return new Collator(locales, options);
    const requested = canonicalList(locales);
    const opts = options === undefined ? {} : toObject(options);
    const usage = getOption(opts, 'usage', 'string', ['sort', 'search'], 'sort');
    getOption(opts, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
    const collationOption = getOption(opts, 'collation', 'string');
    const numericOption = getOption(opts, 'numeric', 'boolean');
    const caseFirstOption = getOption(opts, 'caseFirst', 'string', ['upper', 'lower', 'false']);
    const resolved = resolveLocale(requested, true, ['co', 'kf', 'kn'], {
      co: collationOption === undefined ? undefined : typeIdentifier(collationOption, 'collation'),
      kf: caseFirstOption,
      kn: numericOption === undefined ? undefined : String(numericOption),
    });
    const defaults = host.localeDefaults(resolved.found);
    const sensitivity = getOption(opts, 'sensitivity', 'string', ['base', 'accent', 'case', 'variant'], 'variant');
    const ignorePunctuation = getOption(opts, 'ignorePunctuation', 'boolean', undefined, Boolean(defaults[3]));
    slots.set(this, {
      kind: 'Collator', locale: resolved.locale, found: resolved.found, usage, sensitivity, ignorePunctuation,
      collation: resolved.co || 'default', numeric: resolved.kn === 'true', caseFirst: resolved.kf || 'false',
    });
  };
  const collatorCompare = slot => {
    if (!slot.compareImplementation) slot.compareImplementation = host.collator(slot.found, slot.sensitivity, slot.ignorePunctuation, slot.numeric, slot.collation === 'default' ? '' : slot.collation, slot.caseFirst);
    return slot.compareImplementation;
  };
  makeClass(Collator, 'Collator', 0, {
    resolvedOptions() {
      const slot = slotOf(this, 'Collator', 'resolvedOptions');
      return { locale: slot.locale, usage: slot.usage, sensitivity: slot.sensitivity, ignorePunctuation: slot.ignorePunctuation, collation: slot.collation, numeric: slot.numeric, caseFirst: slot.caseFirst };
    },
    get compare() {
      const slot = slotOf(this, 'Collator', 'get compare');
      if (!slot.bound) slot.bound = anonymous(2, (x, y) => collatorCompare(slot)(String(x), String(y)));
      return slot.bound;
    },
  }, {}, true);

  // ICU
  const icu = (op, args) => {
    const result = host.icu(op, args);
    if (typeof result !== 'number') return result;
    if (result === -4) throw new RangeError('duplicate variant subtag');
    throw new Error('internal error while computing Intl data');
  };
  const partsOf = (list, names) => {
    const parts = [];
    for (let index = 0; index < list.length; index += names.length) {
      const part = {};
      names.forEach((name, offset) => { part[name] = list[index + offset]; });
      parts.push(part);
    }
    return parts;
  };
  const icuLocale = (locale, keywords) => {
    const base = locale.split('-u-')[0];
    const merged = extensionOf(locale);
    for (const key of Object.keys(keywords)) if (keywords[key] !== undefined) merged[key] = keywords[key];
    const keys = Object.keys(merged).sort();
    return keys.length ? base + '-u-' + keys.map(key => merged[key] === '' ? key : key + '-' + merged[key]).join('-') : base;
  };
  const numberText = value => Object.is(value, -0) ? '-0' : String(value);
  const toPrimitiveNumber = value => {
    const exotic = value[Symbol.toPrimitive];
    if (exotic !== undefined && exotic !== null) {
      const result = exotic.call(value, 'number');
      if (result !== null && (typeof result === 'object' || typeof result === 'function')) throw new TypeError("can't convert object to primitive type");
      return result;
    }
    for (const name of ['valueOf', 'toString']) {
      const method = value[name];
      if (typeof method !== 'function') continue;
      const result = method.call(value);
      if (result === null || typeof result !== 'object' && typeof result !== 'function') return result;
    }
    throw new TypeError("can't convert object to number");
  };
  const mathValue = value => {
    const primitive = value !== null && (typeof value === 'object' || typeof value === 'function') ? toPrimitiveNumber(value) : value;
    if (typeof primitive === 'bigint') return ['s', primitive.toString()];
    if (typeof primitive === 'string') {
      const text = primitive.trim();
      if (/^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(text)) return ['s', text.replace(/^\+/, '')];
    }
    return ['n', numberText(Number(primitive))];
  };
  const isNaNValue = value => value[0] === 'n' && value[1] === 'NaN';
  const rangeValues = (start, end, kind, method) => {
    if (start === undefined || end === undefined) throw new TypeError('undefined ' + (start === undefined ? 'start' : 'end') + ' number in ' + kind + '.' + method + '()');
    const x = mathValue(start);
    const y = mathValue(end);
    if (isNaNValue(x) || isNaNValue(y)) throw new RangeError("range can't " + (isNaNValue(x) ? 'start' : 'end') + ' with NaN in ' + kind + '.' + method + '()');
    return x[0] === 'n' && y[0] === 'n' ? ['n', x[1], y[1]] : ['s', x[1], y[1]];
  };
  const optionsObject = (options, name) => {
    if (options === undefined) return {};
    if (options === null || typeof options !== 'object' && typeof options !== 'function') throw new TypeError('options argument of Intl.' + name + ' must be an object, got ' + (options === null ? 'null' : typeof options));
    return options;
  };
  const numberingOption = opts => {
    const value = getOption(opts, 'numberingSystem', 'string');
    return value === undefined ? undefined : typeIdentifier(value, 'numberingSystem');
  };

  // DateTimeFormat
  const DateTimeFormat = function DateTimeFormat(locales, options) {
    if (new.target === undefined) return new DateTimeFormat(locales, options);
    slots.set(this, createDateTimeFormat(locales, options, 'any', 'date'));
  };
  const createDateTimeFormat = (locales, options, required, defaults) => {
    const requested = canonicalList(locales);
    const opts = options === undefined ? {} : toObject(options);
    getOption(opts, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
    const calendarOption = getOption(opts, 'calendar', 'string');
    const numberingSystemOption = getOption(opts, 'numberingSystem', 'string');
    const hour12 = getOption(opts, 'hour12', 'boolean');
    let hourCycleOption = getOption(opts, 'hourCycle', 'string', ['h11', 'h12', 'h23', 'h24']);
    if (hour12 !== undefined) hourCycleOption = null;
    const resolved = resolveLocale(requested, false, ['ca', 'hc', 'nu'], {
      ca: calendarOption === undefined ? undefined : typeIdentifier(calendarOption, 'calendar'),
      hc: hourCycleOption,
      nu: numberingSystemOption === undefined ? undefined : typeIdentifier(numberingSystemOption, 'numberingSystem'),
    });
    const calendarKeyword = resolved.ca === 'islamic' ? 'islamic-tbla' : resolved.ca;
    const localeDefaults = host.localeDefaults(resolved.found);
    let timeZone = opts.timeZone;
    if (timeZone === undefined) timeZone = host.defaultTimeZone;
    else {
      const name = String(timeZone);
      timeZone = host.timeZone(name);
      if (!timeZone) throw new RangeError('invalid time zone: ' + name);
    }
    const fields = {};
    const values = {
      weekday: widths, era: widths, year: ['2-digit', 'numeric'], month: ['2-digit', 'numeric', 'narrow', 'short', 'long'], day: ['2-digit', 'numeric'],
      dayPeriod: widths, hour: ['2-digit', 'numeric'], minute: ['2-digit', 'numeric'], second: ['2-digit', 'numeric'],
      timeZoneName: ['short', 'long', 'shortOffset', 'longOffset', 'shortGeneric', 'longGeneric'],
    };
    for (const name of fieldOrder) {
      fields[name] = name === 'fractionalSecondDigits' ? getNumberOption(opts, name, 1, 3, undefined) : getOption(opts, name, 'string', values[name]);
    }
    getOption(opts, 'formatMatcher', 'string', ['basic', 'best fit'], 'best fit');
    const dateStyle = getOption(opts, 'dateStyle', 'string', ['full', 'long', 'medium', 'short']);
    const timeStyle = getOption(opts, 'timeStyle', 'string', ['full', 'long', 'medium', 'short']);
    if (dateStyle !== undefined || timeStyle !== undefined) {
      const explicit = fieldOrder.find(name => fields[name] !== undefined);
      if (explicit) throw new TypeError("can't set option " + explicit + ' when ' + (dateStyle !== undefined ? 'dateStyle' : 'timeStyle') + ' is used');
      if (required === 'date' && timeStyle !== undefined) throw new TypeError('invalid option: timeStyle');
      if (required === 'time' && dateStyle !== undefined) throw new TypeError('invalid option: dateStyle');
    } else {
      let needDefaults = true;
      if (required === 'date' || required === 'any') for (const name of ['weekday', 'year', 'month', 'day']) if (fields[name] !== undefined) needDefaults = false;
      if (required === 'time' || required === 'any') for (const name of ['dayPeriod', 'hour', 'minute', 'second', 'fractionalSecondDigits']) if (fields[name] !== undefined) needDefaults = false;
      if (needDefaults && (defaults === 'date' || defaults === 'all')) fields.year = fields.month = fields.day = 'numeric';
      if (needDefaults && (defaults === 'time' || defaults === 'all')) fields.hour = fields.minute = fields.second = 'numeric';
    }
    const locale = icuLocale(resolved.locale, { ca: calendarKeyword, nu: resolved.nu });
    const args = [locale, /^[+-]/.test(timeZone) ? 'GMT' + timeZone : timeZone, dateStyle || '', timeStyle || '', resolved.hc || '', hour12 === undefined ? '' : String(hour12)]
      .concat(fieldOrder.map(name => fields[name] === undefined ? '' : String(fields[name])));
    const components = icu(2, args);
    return {
      kind: 'DateTimeFormat', locale: resolved.locale, calendar: calendarKeyword || localeDefaults[0], numberingSystem: resolved.nu || localeDefaults[1], timeZone, args, components, dateStyle, timeStyle,
      intervalLocale: icuLocale(resolved.locale, { ca: calendarKeyword, nu: resolved.nu, hc: components[1] || undefined }),
    };
  };
  const componentNames = ['hourCycle', 'hour12', 'weekday', 'era', 'year', 'month', 'day', 'dayPeriod', 'hour', 'minute', 'second', 'timeZoneName', 'fractionalSecondDigits'];
  const timeClip = (value, method) => {
    const time = value === undefined ? Date.now() : Number(value);
    if (!Number.isFinite(time) || Math.abs(time) > 8.64e15) throw new RangeError('date value is not finite in DateTimeFormat.' + method + '()');
    return Math.trunc(time) + 0;
  };
  const dateParts = (slot, time) => partsOf(icu(3, slot.args.concat([String(time)])), ['type', 'value']);
  const joinParts = parts => parts.map(part => part.value).join('');
  const dateRangeParts = (slot, start, end) => partsOf(icu(4, slot.args.concat([slot.intervalLocale, String(start), String(end)])), ['type', 'value', 'source']);
  const rangeArguments = (start, end, method) => {
    if (start === undefined || end === undefined) throw new TypeError('DateTimeFormat.' + method + ' requires two arguments');
    return [timeClip(start, method), timeClip(end, method)];
  };
  makeClass(DateTimeFormat, 'DateTimeFormat', 0, {
    resolvedOptions() {
      const slot = slotOf(this, 'DateTimeFormat', 'resolvedOptions');
      const result = { locale: slot.locale, calendar: slot.calendar, numberingSystem: slot.numberingSystem, timeZone: slot.timeZone };
      const names = slot.dateStyle !== undefined || slot.timeStyle !== undefined ? (slot.timeStyle !== undefined ? ['hourCycle', 'hour12'] : []) : componentNames;
      for (const name of names) {
        const value = slot.components[componentNames.indexOf(name) + 1];
        if (value === '') continue;
        result[name] = name === 'hour12' ? value === 'true' : name === 'fractionalSecondDigits' ? Number(value) : value;
      }
      if (slot.dateStyle !== undefined) result.dateStyle = slot.dateStyle;
      if (slot.timeStyle !== undefined) result.timeStyle = slot.timeStyle;
      return result;
    },
    formatToParts(date) { const slot = slotOf(this, 'DateTimeFormat', 'formatToParts'); return dateParts(slot, timeClip(date, 'formatToParts')); },
    formatRange(startDate, endDate) { const slot = slotOf(this, 'DateTimeFormat', 'formatRange'); const [start, end] = rangeArguments(startDate, endDate, 'formatRange'); return joinParts(dateRangeParts(slot, start, end)); },
    formatRangeToParts(startDate, endDate) { const slot = slotOf(this, 'DateTimeFormat', 'formatRangeToParts'); const [start, end] = rangeArguments(startDate, endDate, 'formatRangeToParts'); return dateRangeParts(slot, start, end); },
    get format() {
      const slot = slotOf(this, 'DateTimeFormat', 'get format');
      if (!slot.bound) slot.bound = anonymous(1, date => joinParts(dateParts(slot, timeClip(date, 'format'))));
      return slot.bound;
    },
  }, {}, false);

  // 数位选项
  const roundingIncrements = [1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 2500, 5000];
  const defaultDigits = { minimumIntegerDigits: 1, minimumFractionDigits: 0, maximumFractionDigits: 3, minimumSignificantDigits: 0, maximumSignificantDigits: 0, roundingIncrement: 1, roundingMode: 'halfExpand', roundingPriority: 'auto', trailingZeroDisplay: 'auto' };
  const defaultNumberOption = (value, minimum, maximum, fallback) => {
    if (value === undefined) return fallback;
    const number = Number(value);
    if (Number.isNaN(number) || number < minimum || number > maximum) throw new RangeError('invalid digits value: ' + String(value));
    return Math.floor(number);
  };
  const digitOptions = (opts, mnfdDefault, mxfdDefault, notation) => {
    const minimumIntegerDigits = getNumberOption(opts, 'minimumIntegerDigits', 1, 21, 1);
    const mnfd = opts.minimumFractionDigits;
    const mxfd = opts.maximumFractionDigits;
    const mnsd = opts.minimumSignificantDigits;
    const mxsd = opts.maximumSignificantDigits;
    const roundingIncrement = getNumberOption(opts, 'roundingIncrement', 1, 5000, 1);
    if (!roundingIncrements.includes(roundingIncrement)) throw new RangeError('invalid value ' + roundingIncrement + ' for option roundingIncrement');
    const roundingMode = getOption(opts, 'roundingMode', 'string', ['ceil', 'floor', 'expand', 'trunc', 'halfCeil', 'halfFloor', 'halfExpand', 'halfTrunc', 'halfEven'], 'halfExpand');
    const roundingPriority = getOption(opts, 'roundingPriority', 'string', ['auto', 'morePrecision', 'lessPrecision'], 'auto');
    const trailingZeroDisplay = getOption(opts, 'trailingZeroDisplay', 'string', ['auto', 'stripIfInteger'], 'auto');
    if (roundingIncrement !== 1) mxfdDefault = mnfdDefault;
    const digits = { minimumIntegerDigits, minimumFractionDigits: -1, maximumFractionDigits: -1, minimumSignificantDigits: 0, maximumSignificantDigits: 0, roundingIncrement, roundingMode, roundingPriority, trailingZeroDisplay };
    const hasSd = mnsd !== undefined || mxsd !== undefined;
    const hasFd = mnfd !== undefined || mxfd !== undefined;
    let needSd = true;
    let needFd = true;
    if (roundingPriority === 'auto') {
      needSd = hasSd;
      if (needSd || !hasFd && notation === 'compact') needFd = false;
    }
    if (needSd) {
      digits.minimumSignificantDigits = hasSd ? defaultNumberOption(mnsd, 1, 21, 1) : 1;
      digits.maximumSignificantDigits = hasSd ? defaultNumberOption(mxsd, digits.minimumSignificantDigits, 21, 21) : 21;
    }
    if (needFd) {
      if (hasFd) {
        let minimum = defaultNumberOption(mnfd, 0, 100, undefined);
        let maximum = defaultNumberOption(mxfd, 0, 100, undefined);
        if (minimum === undefined) minimum = Math.min(mnfdDefault, maximum);
        else if (maximum === undefined) maximum = Math.max(mxfdDefault, minimum);
        else if (minimum > maximum) throw new RangeError('invalid digits value: ' + roundingIncrement);
        digits.minimumFractionDigits = minimum;
        digits.maximumFractionDigits = maximum;
      } else {
        digits.minimumFractionDigits = mnfdDefault;
        digits.maximumFractionDigits = mxfdDefault;
      }
    }
    if (!needSd && !needFd) Object.assign(digits, { minimumFractionDigits: 0, maximumFractionDigits: 0, minimumSignificantDigits: 1, maximumSignificantDigits: 2, roundingPriority: 'morePrecision' });
    if (roundingIncrement !== 1) {
      if (roundingPriority !== 'auto' || hasSd) throw new TypeError("can't set option roundingIncrement when " + (mnsd !== undefined ? 'minimumSignificantDigits' : mxsd !== undefined ? 'maximumSignificantDigits' : 'roundingPriority') + ' is used');
      if (digits.minimumFractionDigits !== digits.maximumFractionDigits) throw new RangeError('fraction digits must be the same when roundingIncrement is used');
    }
    return digits;
  };
  const digitArgs = digits => [
    String(digits.minimumIntegerDigits),
    digits.minimumFractionDigits < 0 ? '' : String(digits.minimumFractionDigits), digits.minimumFractionDigits < 0 ? '' : String(digits.maximumFractionDigits),
    digits.minimumSignificantDigits > 0 ? String(digits.minimumSignificantDigits) : '', digits.minimumSignificantDigits > 0 ? String(digits.maximumSignificantDigits) : '',
    String(digits.roundingIncrement), digits.roundingMode, digits.roundingPriority, digits.trailingZeroDisplay,
  ];
  const resolveDigits = (result, digits) => {
    result.minimumIntegerDigits = digits.minimumIntegerDigits;
    if (digits.minimumFractionDigits >= 0) Object.assign(result, { minimumFractionDigits: digits.minimumFractionDigits, maximumFractionDigits: digits.maximumFractionDigits });
    if (digits.minimumSignificantDigits > 0) Object.assign(result, { minimumSignificantDigits: digits.minimumSignificantDigits, maximumSignificantDigits: digits.maximumSignificantDigits });
  };
  const resolveNotation = (result, slot) => {
    result.notation = slot.notation;
    if (slot.notation === 'compact') result.compactDisplay = slot.compactDisplay;
  };
  const resolveRounding = (result, digits) => Object.assign(result, { roundingIncrement: digits.roundingIncrement, roundingMode: digits.roundingMode, roundingPriority: digits.roundingPriority, trailingZeroDisplay: digits.trailingZeroDisplay });

  // NumberFormat
  const currencyDigits = { BHD: 3, BIF: 0, CLF: 4, CLP: 0, DJF: 0, GNF: 0, IQD: 3, ISK: 0, JOD: 3, JPY: 0, KMF: 0, KRW: 0, KWD: 3, LYD: 3, OMR: 3, PYG: 0, RWF: 0, TND: 3, UGX: 0, UYI: 0, UYW: 4, VND: 0, VUV: 0, XAF: 0, XOF: 0, XPF: 0 };
  const simpleUnits = 'acre bit byte celsius centimeter day degree fahrenheit fluid-ounce foot gallon gigabit gigabyte gram hectare hour inch kilobit kilobyte kilogram kilometer liter megabit megabyte meter microsecond mile mile-scandinavian milliliter millimeter millisecond minute month nanosecond ounce percent petabyte pound second stone terabit terabyte week yard year'.split(' ');
  const isUnit = unit => {
    if (simpleUnits.includes(unit)) return true;
    const index = unit.indexOf('-per-');
    return index > 0 && simpleUnits.includes(unit.slice(0, index)) && simpleUnits.includes(unit.slice(index + 5));
  };
  const createNumberFormat = (locales, options) => {
    const requested = canonicalList(locales);
    const slot = { kind: 'NumberFormat', style: 'decimal', currencyDisplay: 'symbol', currencySign: 'standard', unitDisplay: 'short', notation: 'standard', compactDisplay: 'short', useGrouping: 'auto', signDisplay: 'auto', digits: defaultDigits };
    let numberingSystem;
    if (options !== undefined) {
      const opts = toObject(options);
      getOption(opts, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
      numberingSystem = numberingOption(opts);
      slot.style = getOption(opts, 'style', 'string', ['decimal', 'percent', 'currency', 'unit'], 'decimal');
      const currency = getOption(opts, 'currency', 'string');
      if (currency === undefined) {
        if (slot.style === 'currency') throw new TypeError('undefined currency in NumberFormat() with currency style');
      } else {
        if (!/^[a-zA-Z]{3}$/.test(currency)) throw new RangeError('invalid currency code in NumberFormat(): ' + currency);
        slot.currency = currency.toUpperCase();
      }
      slot.currencyDisplay = getOption(opts, 'currencyDisplay', 'string', ['code', 'symbol', 'narrowSymbol', 'name'], 'symbol');
      slot.currencySign = getOption(opts, 'currencySign', 'string', ['standard', 'accounting'], 'standard');
      const unit = getOption(opts, 'unit', 'string');
      if (unit === undefined) {
        if (slot.style === 'unit') throw new TypeError('undefined unit in NumberFormat() with unit style');
      } else {
        if (!isUnit(unit)) throw new RangeError('invalid unit identifier in NumberFormat(): ' + unit);
        slot.unit = unit;
      }
      slot.unitDisplay = getOption(opts, 'unitDisplay', 'string', ['short', 'narrow', 'long'], 'short');
      slot.notation = getOption(opts, 'notation', 'string', ['standard', 'scientific', 'engineering', 'compact'], 'standard');
      const cDigits = slot.style === 'currency' && slot.notation === 'standard' ? (currencyDigits[slot.currency] === undefined ? 2 : currencyDigits[slot.currency]) : undefined;
      slot.digits = digitOptions(opts, cDigits === undefined ? 0 : cDigits, cDigits === undefined ? (slot.style === 'percent' ? 0 : 3) : cDigits, slot.notation);
      slot.compactDisplay = getOption(opts, 'compactDisplay', 'string', ['short', 'long'], 'short');
      const defaultGrouping = slot.notation === 'compact' ? 'min2' : 'auto';
      const grouping = opts.useGrouping;
      if (grouping === undefined) slot.useGrouping = defaultGrouping;
      else if (grouping === true) slot.useGrouping = 'always';
      else if (!grouping) slot.useGrouping = false;
      else {
        const text = String(grouping);
        if (!['min2', 'auto', 'always', 'true', 'false'].includes(text)) throw new RangeError('invalid value ' + JSON.stringify(text) + ' for option useGrouping');
        slot.useGrouping = text === 'true' || text === 'false' ? defaultGrouping : text;
      }
      slot.signDisplay = getOption(opts, 'signDisplay', 'string', ['auto', 'never', 'always', 'exceptZero', 'negative'], 'auto');
    }
    const resolved = resolveLocale(requested, false, ['nu'], { nu: numberingSystem });
    slot.locale = resolved.locale;
    slot.numberingSystem = resolved.nu || host.localeDefaults(resolved.found)[2];
    slot.args = [icuLocale(resolved.locale, { nu: resolved.nu }), slot.style, slot.currency || '', slot.currencyDisplay, slot.currencySign, slot.unit || '', slot.unitDisplay]
      .concat(digitArgs(slot.digits), [slot.notation, slot.compactDisplay, slot.useGrouping === false ? 'false' : slot.useGrouping, slot.signDisplay]);
    return slot;
  };
  const NumberFormat = function NumberFormat(locales, options) {
    if (new.target === undefined) return new NumberFormat(locales, options);
    slots.set(this, createNumberFormat(locales, options));
  };
  const numberParts = (slot, value) => partsOf(icu(5, slot.args.concat(mathValue(value))), ['type', 'value']);
  const numberRangeParts = (slot, start, end, method) => partsOf(icu(6, slot.args.concat(rangeValues(start, end, 'NumberFormat', method))), ['type', 'value', 'source']);
  makeClass(NumberFormat, 'NumberFormat', 0, {
    resolvedOptions() {
      const slot = slotOf(this, 'NumberFormat', 'resolvedOptions');
      const result = { locale: slot.locale, numberingSystem: slot.numberingSystem, style: slot.style };
      if (slot.style === 'currency') Object.assign(result, { currency: slot.currency, currencyDisplay: slot.currencyDisplay, currencySign: slot.currencySign });
      if (slot.style === 'unit') Object.assign(result, { unit: slot.unit, unitDisplay: slot.unitDisplay });
      resolveDigits(result, slot.digits);
      result.useGrouping = slot.useGrouping;
      resolveNotation(result, slot);
      result.signDisplay = slot.signDisplay;
      return resolveRounding(result, slot.digits);
    },
    formatToParts(value) { return numberParts(slotOf(this, 'NumberFormat', 'formatToParts'), value); },
    formatRange(start, end) { return joinParts(numberRangeParts(slotOf(this, 'NumberFormat', 'formatRange'), start, end, 'formatRange')); },
    formatRangeToParts(start, end) { return numberRangeParts(slotOf(this, 'NumberFormat', 'formatRangeToParts'), start, end, 'formatRangeToParts'); },
    get format() {
      const slot = slotOf(this, 'NumberFormat', 'get format');
      if (!slot.bound) slot.bound = anonymous(1, value => joinParts(numberParts(slot, value)));
      return slot.bound;
    },
  }, {}, false);

  // PluralRules
  const PluralRules = function PluralRules(locales, options) {
    requireNew(new.target, 'PluralRules');
    const requested = canonicalList(locales);
    const slot = { kind: 'PluralRules', type: 'cardinal', notation: 'standard', compactDisplay: 'short', digits: defaultDigits };
    if (options !== undefined) {
      const opts = toObject(options);
      getOption(opts, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
      slot.type = getOption(opts, 'type', 'string', ['cardinal', 'ordinal'], 'cardinal');
      slot.notation = getOption(opts, 'notation', 'string', ['standard', 'scientific', 'engineering', 'compact'], 'standard');
      slot.compactDisplay = getOption(opts, 'compactDisplay', 'string', ['short', 'long'], 'short');
      slot.digits = digitOptions(opts, 0, 3, slot.notation);
    }
    slot.locale = resolveLocale(requested, false, [], {}).locale;
    slot.args = [slot.locale, slot.type, slot.notation, slot.compactDisplay].concat(digitArgs(slot.digits));
    slots.set(this, slot);
  };
  makeClass(PluralRules, 'PluralRules', 0, {
    select(value) { return icu(7, slotOf(this, 'PluralRules', 'select').args.concat(['n', numberText(Number(value))]))[0]; },
    selectRange(start, end) { return icu(8, slotOf(this, 'PluralRules', 'selectRange').args.concat(rangeValues(start, end, 'PluralRules', 'selectRange')))[0]; },
    resolvedOptions() {
      const slot = slotOf(this, 'PluralRules', 'resolvedOptions');
      const result = { locale: slot.locale, type: slot.type };
      resolveNotation(result, slot);
      resolveDigits(result, slot.digits);
      result.pluralCategories = icu(9, slot.args);
      return resolveRounding(result, slot.digits);
    },
  }, { select: 1, selectRange: 2 }, false);

  // RelativeTimeFormat
  const relativeUnits = ['second', 'minute', 'hour', 'day', 'week', 'month', 'quarter', 'year'];
  const RelativeTimeFormat = function RelativeTimeFormat(locales, options) {
    requireNew(new.target, 'RelativeTimeFormat');
    const requested = canonicalList(locales);
    const opts = options === undefined ? {} : toObject(options);
    getOption(opts, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
    const numberingSystem = numberingOption(opts);
    const style = getOption(opts, 'style', 'string', ['long', 'short', 'narrow'], 'long');
    const numeric = getOption(opts, 'numeric', 'string', ['always', 'auto'], 'always');
    const resolved = resolveLocale(requested, false, ['nu'], { nu: numberingSystem });
    slots.set(this, {
      kind: 'RelativeTimeFormat', locale: resolved.locale, style, numeric, numberingSystem: resolved.nu || host.localeDefaults(resolved.found)[2],
      args: [icuLocale(resolved.locale, { nu: resolved.nu }), style, numeric],
    });
  };
  const relativeParts = (slot, value, unit, method) => {
    const number = Number(value);
    const name = String(unit);
    if (!Number.isFinite(number)) throw new RangeError('date value is not finite in RelativeTimeFormat.' + method + '()');
    const singular = relativeUnits.includes(name) ? name : relativeUnits.find(item => item + 's' === name);
    if (!singular) throw new RangeError('invalid value ' + JSON.stringify(name) + ' for option unit');
    return partsOf(icu(10, slot.args.concat([numberText(number), singular])), ['type', 'value']).map(part => part.type === 'literal' ? part : Object.assign(part, { unit: singular }));
  };
  makeClass(RelativeTimeFormat, 'RelativeTimeFormat', 0, {
    format(value, unit) { return joinParts(relativeParts(slotOf(this, 'RelativeTimeFormat', 'format'), value, unit, 'format')); },
    formatToParts(value, unit) { return relativeParts(slotOf(this, 'RelativeTimeFormat', 'formatToParts'), value, unit, 'formatToParts'); },
    resolvedOptions() { const slot = slotOf(this, 'RelativeTimeFormat', 'resolvedOptions'); return { locale: slot.locale, style: slot.style, numeric: slot.numeric, numberingSystem: slot.numberingSystem }; },
  }, { format: 2, formatToParts: 2 }, false);

  // ListFormat
  const informalType = value => value === null ? 'null' : typeof value === 'object' ? Object.prototype.toString.call(value).slice(8, -1) : typeof value;
  const ListFormat = function ListFormat(locales, options) {
    requireNew(new.target, 'ListFormat');
    const requested = canonicalList(locales);
    const opts = optionsObject(options, 'ListFormat');
    getOption(opts, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
    const type = getOption(opts, 'type', 'string', ['conjunction', 'disjunction', 'unit'], 'conjunction');
    const style = getOption(opts, 'style', 'string', ['long', 'short', 'narrow'], 'long');
    const locale = resolveLocale(requested, false, [], {}).locale;
    slots.set(this, { kind: 'ListFormat', locale, type, style, args: [locale, type, style] });
  };
  const listParts = (slot, list, method) => {
    const strings = [];
    if (list !== undefined) {
      for (const value of list) {
        if (typeof value !== 'string') throw new TypeError(method + ': expected string, got ' + informalType(value));
        strings.push(value);
      }
    }
    return partsOf(icu(11, slot.args.concat(strings)), ['type', 'value']);
  };
  makeClass(ListFormat, 'ListFormat', 0, {
    format(list) { return joinParts(listParts(slotOf(this, 'ListFormat', 'format'), list, 'format')); },
    formatToParts(list) { return listParts(slotOf(this, 'ListFormat', 'formatToParts'), list, 'formatToParts'); },
    resolvedOptions() { const slot = slotOf(this, 'ListFormat', 'resolvedOptions'); return { locale: slot.locale, type: slot.type, style: slot.style }; },
  }, { format: 1, formatToParts: 1 }, false);

  // DisplayNames
  const DisplayNames = function DisplayNames(locales, options) {
    requireNew(new.target, 'DisplayNames');
    const requested = canonicalList(locales);
    if (options === null || typeof options !== 'object' && typeof options !== 'function') throw new TypeError('options argument of Intl.DisplayNames must be an object, got ' + (options === null ? 'null' : typeof options));
    getOption(options, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
    const style = getOption(options, 'style', 'string', ['long', 'short', 'narrow'], 'long');
    const type = getOption(options, 'type', 'string', ['language', 'region', 'script', 'currency', 'calendar', 'dateTimeField']);
    if (type === undefined) throw new TypeError('missing "type" option in DisplayNames()');
    const fallback = getOption(options, 'fallback', 'string', ['code', 'none'], 'code');
    const languageDisplay = getOption(options, 'languageDisplay', 'string', ['dialect', 'standard'], 'dialect');
    const locale = resolveLocale(requested, false, [], {}).locale;
    slots.set(this, { kind: 'DisplayNames', locale, style, type, fallback, languageDisplay });
  };
  makeClass(DisplayNames, 'DisplayNames', 2, {
    of(code) {
      const slot = slotOf(this, 'DisplayNames', 'of');
      const text = slot.type === 'region' && accountRegion ? accountRegion : String(code);
      const result = host.icu(12, [slot.locale, slot.style, slot.languageDisplay, slot.type, slot.fallback, text]);
      if (result === -2) throw new RangeError('invalid value ' + JSON.stringify(text) + ' for option ' + slot.type);
      if (result === -3) throw new RangeError('invalid language tag: ' + text);
      if (result === -4) throw new RangeError('duplicate variant subtag');
      if (typeof result === 'number') throw new Error('internal error while computing Intl data');
      return result[0] === '' ? undefined : result[0];
    },
    resolvedOptions() {
      const slot = slotOf(this, 'DisplayNames', 'resolvedOptions');
      const result = { locale: slot.locale, style: slot.style, type: slot.type, fallback: slot.fallback };
      if (slot.type === 'language') result.languageDisplay = slot.languageDisplay;
      return result;
    },
  }, { of: 1 }, false);

  // DurationFormat
  const durationUnits = ['years', 'months', 'weeks', 'days', 'hours', 'minutes', 'seconds', 'milliseconds', 'microseconds', 'nanoseconds'];
  const singularUnit = unit => unit.slice(0, -1);
  const isNumericStyle = style => style === 'numeric' || style === '2-digit';
  const durationUnitOptions = (unit, opts, baseStyle, digitalBase, prevStyle) => {
    const index = durationUnits.indexOf(unit);
    const styles = index < 4 ? ['long', 'short', 'narrow'] : index < 7 ? ['long', 'short', 'narrow', 'numeric', '2-digit'] : ['long', 'short', 'narrow', 'numeric'];
    let style = getOption(opts, unit, 'string', styles);
    let displayDefault = 'always';
    if (style === undefined) {
      if (baseStyle === 'digital') {
        style = digitalBase;
        if (index < 4 || index > 6) displayDefault = 'auto';
      } else if (isNumericStyle(prevStyle)) {
        style = 'numeric';
        if (unit !== 'minutes' && unit !== 'seconds') displayDefault = 'auto';
      } else {
        style = baseStyle;
        displayDefault = 'auto';
      }
    }
    const fractional = style === 'numeric' && index > 6;
    if (fractional) displayDefault = 'auto';
    const display = getOption(opts, unit + 'Display', 'string', ['auto', 'always'], displayDefault);
    if (display === 'always' && fractional) throw new RangeError(unit + ' can\'t use "numeric" style with "always" display');
    if (isNumericStyle(prevStyle) && !isNumericStyle(style)) throw new RangeError('invalid non-numeric value ' + unit + ' for option ' + style);
    if ((unit === 'minutes' || unit === 'seconds') && isNumericStyle(prevStyle)) style = '2-digit';
    return { style, display };
  };
  const DurationFormat = function DurationFormat(locales, options) {
    requireNew(new.target, 'DurationFormat');
    const requested = canonicalList(locales);
    const opts = options === undefined ? {} : toObject(options);
    getOption(opts, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
    const numberingSystem = numberingOption(opts);
    const style = getOption(opts, 'style', 'string', ['long', 'short', 'narrow', 'digital'], 'short');
    const units = {};
    let prevStyle = 'long';
    for (const unit of durationUnits) {
      units[unit] = durationUnitOptions(unit, opts, style, durationUnits.indexOf(unit) < 4 ? 'short' : 'numeric', durationUnits.indexOf(unit) < 5 ? 'long' : prevStyle);
      prevStyle = units[unit].style;
    }
    const fractionalDigits = getNumberOption(opts, 'fractionalDigits', 0, 9, undefined);
    const resolved = resolveLocale(requested, false, ['nu'], { nu: numberingSystem });
    slots.set(this, {
      kind: 'DurationFormat', locale: resolved.locale, numberingSystem: resolved.nu || host.localeDefaults(resolved.found)[2], style, units, fractionalDigits,
      numberLocale: icuLocale(resolved.locale, { nu: resolved.nu }),
    });
  };
  const durationRecord = value => {
    if (value === null || typeof value !== 'object' && typeof value !== 'function') throw new TypeError('Duration-like objects must have at least one duration unit');
    const record = {};
    let found = false;
    for (const unit of durationUnits.slice().sort()) {
      const field = value[unit];
      if (field === undefined) { record[unit] = 0; continue; }
      found = true;
      const number = Number(field);
      if (!Number.isFinite(number)) throw new RangeError('duration value "' + unit + '" is non-finite: ' + numberText(number));
      if (!Number.isInteger(number)) throw new RangeError(numberText(number) + " isn't a valid " + unit + " duration because it isn't an integer");
      record[unit] = number + 0;
    }
    if (!found) throw new TypeError('Duration-like objects must have at least one duration unit');
    const signs = durationUnits.map(unit => Math.sign(record[unit])).filter(Boolean);
    if (signs.some(sign => sign !== signs[0])) throw new RangeError('duration value "' + durationUnits.find(unit => Math.sign(record[unit]) === -signs[0]) + '" has a mismatched sign: ' + signs[0]);
    record.sign = signs.length ? signs[0] : 0;
    return record;
  };
  const durationDecimal = (record, unit) => {
    const index = durationUnits.indexOf(unit);
    const scale = [1000000000n, 1000000n, 1000n][index - 6];
    const smaller = durationUnits.slice(index + 1);
    if (smaller.every(name => record[name] === 0)) return ['n', numberText(record[unit])];
    let total = 0n;
    for (const name of durationUnits.slice(index)) total += BigInt(record[name]) * [1000000000n, 1000000n, 1000n, 1n][durationUnits.indexOf(name) - 6];
    const negative = total < 0n;
    if (negative) total = -total;
    const integer = total / scale;
    const fraction = (total % scale).toString().padStart(String(scale).length - 1, '0').replace(/0+$/, '');
    return ['s', (negative ? '-' : '') + integer + (fraction ? '.' + fraction : '')];
  };
  const isZeroValue = value => Number(value[1]) === 0;
  const absValue = value => value[0] === 'n' ? ['n', numberText(Math.abs(Number(value[1])))] : ['s', value[1].replace(/^-/, '')];
  const durationNumberArgs = (slot, style, unit, fractional) => {
    const digits = fractional
      ? { minimumIntegerDigits: 1, minimumFractionDigits: slot.fractionalDigits === undefined ? 0 : slot.fractionalDigits, maximumFractionDigits: slot.fractionalDigits === undefined ? 9 : slot.fractionalDigits, minimumSignificantDigits: 0, maximumSignificantDigits: 0, roundingIncrement: 1, roundingMode: 'trunc', roundingPriority: 'auto', trailingZeroDisplay: 'auto' }
      : { minimumIntegerDigits: style === '2-digit' ? 2 : 1, minimumFractionDigits: -1, maximumFractionDigits: -1, minimumSignificantDigits: 0, maximumSignificantDigits: 0, roundingIncrement: 1, roundingMode: 'halfExpand', roundingPriority: 'auto', trailingZeroDisplay: 'auto' };
    if (isNumericStyle(style)) {
      if (unit === 'seconds') Object.assign(digits, { minimumFractionDigits: slot.fractionalDigits === undefined ? 0 : slot.fractionalDigits, maximumFractionDigits: slot.fractionalDigits === undefined ? 9 : slot.fractionalDigits, roundingMode: 'trunc' });
      return [slot.numberLocale, 'decimal', '', '', '', '', 'short'].concat(digitArgs(digits), ['standard', 'short', 'false', 'auto']);
    }
    return [slot.numberLocale, 'unit', '', '', '', singularUnit(unit), style].concat(digitArgs(digits), ['standard', 'short', 'auto', 'auto']);
  };
  const durationValueParts = (slot, style, unit, value, fractional) => partsOf(icu(5, durationNumberArgs(slot, style, unit, fractional).concat(value)), ['type', 'value']).map(part => Object.assign(part, { unit: singularUnit(unit) }));
  const nextUnitFractional = (slot, unit) => {
    const index = durationUnits.indexOf(unit);
    return index >= 6 && index <= 8 && slot.units[durationUnits[index + 1]].style === 'numeric';
  };
  const numericUnitParts = (slot, record, firstUnit, signDisplayed) => {
    let hours = ['n', numberText(record.hours)];
    let minutes = ['n', numberText(record.minutes)];
    let seconds = durationDecimal(record, 'seconds');
    const hoursFormatted = firstUnit === 'hours' && (!isZeroValue(hours) || slot.units.hours.display === 'always');
    const secondsFormatted = !isZeroValue(seconds) || slot.units.seconds.display === 'always';
    const minutesFormatted = (firstUnit === 'hours' || firstUnit === 'minutes') && (hoursFormatted && secondsFormatted || !isZeroValue(minutes) || slot.units.minutes.display === 'always');
    const list = [];
    const signed = value => {
      if (!signDisplayed) return absValue(value);
      signDisplayed = false;
      return isZeroValue(value) && record.sign < 0 ? ['n', '-0'] : value;
    };
    if (hoursFormatted) list.push(durationValueParts(slot, slot.units.hours.style, 'hours', signed(hours), false));
    if (minutesFormatted) list.push(durationValueParts(slot, slot.units.minutes.style, 'minutes', signed(minutes), false));
    if (secondsFormatted) list.push(durationValueParts(slot, slot.units.seconds.style, 'seconds', signDisplayed ? seconds : absValue(seconds), false));
    if (list.length === 0) return undefined;
    if (list.length === 1) return list[0];
    const separator = icu(13, [slot.numberLocale.split('-u-')[0], slot.numberingSystem])[0];
    return list.reduce((all, parts, index) => index === 0 ? parts : all.concat([{ type: 'literal', value: separator }], parts), []);
  };
  const durationParts = (slot, duration) => {
    const record = durationRecord(duration);
    const formatted = [];
    let signDisplayed = true;
    for (const unit of durationUnits) {
      const { style, display } = slot.units[unit];
      if (isNumericStyle(style)) {
        const parts = numericUnitParts(slot, record, unit, signDisplayed);
        if (parts) formatted.push(parts);
        break;
      }
      let value = ['n', numberText(record[unit])];
      const fractional = nextUnitFractional(slot, unit);
      if (fractional) value = durationDecimal(record, unit);
      if (display === 'auto' && isZeroValue(value)) {
        if (fractional) break;
        continue;
      }
      if (signDisplayed) {
        signDisplayed = false;
        if (isZeroValue(value) && record.sign < 0) value = ['n', '-0'];
      } else value = absValue(value);
      formatted.push(durationValueParts(slot, style, unit, value, fractional));
      if (fractional) break;
    }
    const listStyle = slot.style === 'digital' ? 'short' : slot.style;
    const list = partsOf(icu(11, [slot.numberLocale.split('-u-')[0], 'unit', listStyle].concat(formatted.map(joinParts))), ['type', 'value']);
    let element = 0;
    return list.reduce((all, part) => all.concat(part.type === 'element' ? formatted[element++] : [{ type: 'literal', value: part.value }]), []);
  };
  makeClass(DurationFormat, 'DurationFormat', 0, {
    format(duration) { return joinParts(durationParts(slotOf(this, 'DurationFormat', 'format'), duration)); },
    formatToParts(duration) { return durationParts(slotOf(this, 'DurationFormat', 'formatToParts'), duration); },
    resolvedOptions() {
      const slot = slotOf(this, 'DurationFormat', 'resolvedOptions');
      const result = { locale: slot.locale, numberingSystem: slot.numberingSystem, style: slot.style };
      for (const unit of durationUnits) Object.assign(result, { [unit]: slot.units[unit].style, [unit + 'Display']: slot.units[unit].display });
      if (slot.fractionalDigits !== undefined) result.fractionalDigits = slot.fractionalDigits;
      return result;
    },
  }, { format: 1, formatToParts: 1 }, false);

  // Segmenter
  const segmentsPrototype = {};
  const segmentIteratorPrototype = Object.create(Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]())));
  const segmentBoundaries = slot => {
    if (!slot.starts) {
      const [starts, wordLike] = host.segment(slot.granularity, slot.found, slot.string);
      slot.starts = starts;
      slot.wordLike = wordLike;
    }
    return slot.starts;
  };
  const segmentData = (slot, index) => {
    const starts = segmentBoundaries(slot);
    const start = starts[index];
    const data = { segment: slot.string.slice(start, index + 1 < starts.length ? starts[index + 1] : slot.string.length), index: start, input: slot.string };
    if (slot.wordLike) data.isWordLike = slot.wordLike[index];
    return data;
  };
  install(segmentsPrototype, {
    containing(index) {
      const slot = slotOf(this, 'Segments', 'containing');
      const position = Math.trunc(Number(index)) || 0;
      if (position < 0 || position >= slot.string.length) return undefined;
      const starts = segmentBoundaries(slot);
      let low = 0;
      let high = starts.length - 1;
      while (low < high) {
        const middle = (low + high + 1) >> 1;
        if (starts[middle] <= position) low = middle; else high = middle - 1;
      }
      return segmentData(slot, low);
    },
    [Symbol.iterator]() {
      const iterator = Object.create(segmentIteratorPrototype);
      slots.set(iterator, { kind: 'SegmentIterator', segments: slotOf(this, 'Segments', '[Symbol.iterator]'), next: 0 });
      return iterator;
    },
  }, { containing: 1 });
  install(segmentIteratorPrototype, {
    next() {
      const slot = slotOf(this, 'SegmentIterator', 'next');
      if (slot.next >= segmentBoundaries(slot.segments).length) return { value: undefined, done: true };
      return { value: segmentData(slot.segments, slot.next++), done: false };
    },
  });
  define(segmentIteratorPrototype, Symbol.toStringTag, { value: 'Segmenter String Iterator', writable: false, enumerable: false, configurable: true });
  const Segmenter = function Segmenter(locales, options) {
    requireNew(new.target, 'Segmenter');
    const requested = canonicalList(locales);
    let granularity = 'grapheme';
    if (options !== undefined) {
      if (options === null || typeof options !== 'object' && typeof options !== 'function') throw new TypeError('options argument of Intl.Segmenter must be an object, got ' + (options === null ? 'null' : typeof options));
      getOption(options, 'localeMatcher', 'string', ['lookup', 'best fit'], 'best fit');
      granularity = getOption(options, 'granularity', 'string', ['grapheme', 'word', 'sentence'], 'grapheme');
    }
    const resolved = resolveLocale(requested, false, [], {});
    slots.set(this, { kind: 'Segmenter', locale: resolved.locale, found: resolved.found, granularity });
  };
  makeClass(Segmenter, 'Segmenter', 0, {
    resolvedOptions() { const slot = slotOf(this, 'Segmenter', 'resolvedOptions'); return { locale: slot.locale, granularity: slot.granularity }; },
    segment(string) {
      const slot = slotOf(this, 'Segmenter', 'segment');
      const segments = Object.create(segmentsPrototype);
      slots.set(segments, { kind: 'Segments', granularity: slot.granularity, found: slot.found, string: String(string) });
      return segments;
    },
  }, { segment: 1 }, false);

  install(Intl, {
    getCanonicalLocales(locales) { return canonicalList(locales); },
    supportedValuesOf(key) {
      const name = String(key);
      const values = host.supportedValues(name);
      if (!values) throw new RangeError('invalid key: ' + JSON.stringify(name));
      return Array.from(values);
    },
  }, { getCanonicalLocales: 1, supportedValuesOf: 1 });

  const cached = new Map();
  const cachedFormat = (key, locales, options, create) => {
    if (locales !== undefined || options !== undefined) return create();
    if (!cached.has(key)) cached.set(key, create());
    return cached.get(key);
  };
  const dateMethod = (name, required, defaults) => ({ [name](locales = undefined, options = undefined) {
    const time = Date.prototype.getTime.call(this);
    if (Number.isNaN(time)) return 'Invalid Date';
    const slot = cachedFormat(name, locales, options, () => createDateTimeFormat(locales, options, required, defaults));
    return joinParts(dateParts(slot, time));
  } })[name];
  install(Date.prototype, {
    toLocaleString: dateMethod('toLocaleString', 'any', 'all'),
    toLocaleDateString: dateMethod('toLocaleDateString', 'date', 'date'),
    toLocaleTimeString: dateMethod('toLocaleTimeString', 'time', 'time'),
  }, { toLocaleString: 0, toLocaleDateString: 0, toLocaleTimeString: 0 });
  const numberFormatFor = (locales, options) => cachedFormat('NumberFormat', locales, options, () => new NumberFormat(locales, options));
  install(Number.prototype, {
    toLocaleString(locales = undefined, options = undefined) { return numberFormatFor(locales, options).format(Number.prototype.valueOf.call(this)); },
  }, { toLocaleString: 0 });
  install(BigInt.prototype, {
    toLocaleString(locales = undefined, options = undefined) { return numberFormatFor(locales, options).format(BigInt.prototype.valueOf.call(this)); },
  }, { toLocaleString: 0 });
  install(String.prototype, {
    localeCompare(that, locales = undefined, options = undefined) {
      if (this === null || this === undefined) throw new TypeError('String.prototype.localeCompare called on null or undefined');
      const collator = cachedFormat('Collator', locales, options, () => new Collator(locales, options));
      return collator.compare(String(this), String(that));
    },
  }, { localeCompare: 1 });
})
