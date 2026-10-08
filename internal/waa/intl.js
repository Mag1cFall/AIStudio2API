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
    ca: value => host.supportedValues('calendar').includes(value),
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
  const copyOptions = (options, names, extra) => {
    const copy = {};
    for (const name of names) {
      const value = options[name];
      if (value !== undefined) copy[name] = value;
    }
    return Object.assign(copy, extra);
  };
  const typeIdentifier = (value, name) => {
    if (!/^[a-zA-Z0-9]{3,8}(-[a-zA-Z0-9]{3,8})*$/.test(value)) throw new RangeError('invalid value ' + JSON.stringify(value) + ' for option ' + name);
    return value.toLowerCase();
  };

  const isLocale = value => value !== null && typeof value === 'object' && slots.has(value) && slots.get(value).kind === 'Locale';
  const canonicalList = locales => {
    if (locales === undefined) return [];
    const seen = [];
    const add = value => {
      const tag = String(value);
      const canonical = host.canonicalize(tag);
      if (!canonical) throw new RangeError('invalid language tag: ' + JSON.stringify(tag));
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

  const shim = {
    getCanonicalLocales: locales => canonicalList(locales),
    Locale: class Locale {
      constructor(tag) {
        const text = String(tag);
        const parts = host.localeParts(host.likely(text));
        const own = host.localeParts(text);
        this.language = own.language;
        this.script = own.script || undefined;
        this.region = own.region || undefined;
        this.baseName = [own.language, own.script, own.region].filter(Boolean).join('-');
        this.maximal = [parts.language, parts.script, parts.region].filter(Boolean).join('-');
      }
      maximize() { return new shim.Locale(this.maximal); }
      toString() { return this.baseName; }
    },
  };
  const formatjsPackages = { NumberFormat: 'intl-numberformat', PluralRules: 'intl-pluralrules', RelativeTimeFormat: 'intl-relativetimeformat', ListFormat: 'intl-listformat', DisplayNames: 'intl-displaynames' };
  const formatjsNeeds = { NumberFormat: ['PluralRules'], RelativeTimeFormat: ['PluralRules', 'NumberFormat'], DurationFormat: ['PluralRules', 'NumberFormat', 'ListFormat'] };
  const formatjsLoaded = new Map();
  const formatjs = (name, found) => {
    const dataLocale = host.dataLocale(found);
    let dataName = '';
    for (const dependency of (formatjsNeeds[name] || []).concat([name])) {
      if (!shim[dependency]) shim[dependency] = host.formatjsChunk(dependency)(shim, {});
      const pkg = formatjsPackages[dependency];
      if (!pkg) continue;
      const key = pkg + '/' + dataLocale;
      if (!formatjsLoaded.has(key)) {
        const [loadedName, loader] = host.formatjsData(dataLocale, pkg);
        loader(shim);
        formatjsLoaded.set(key, loadedName);
      }
      dataName = formatjsLoaded.get(key);
    }
    if (name === 'DurationFormat') dataName = formatjsLoaded.get('intl-numberformat/' + dataLocale);
    if (!dataName) dataName = found;
    return { Constructor: shim[name], dataName };
  };
  const resolvedWith = (impl, locale) => {
    const options = impl.resolvedOptions();
    options.locale = locale;
    return options;
  };

  // Locale
  const localeString = (parts, keywords) => {
    let tag = [parts.language, parts.script, parts.region, parts.variants].filter(Boolean).join('-');
    const keys = Object.keys(keywords).filter(key => keywords[key] !== undefined).sort();
    if (keys.length) tag += '-u-' + keys.map(key => keywords[key] === '' || keywords[key] === 'true' ? key : key + '-' + keywords[key]).join('-');
    return tag;
  };
  const localeSlot = tag => {
    const canonical = host.canonicalize(tag);
    if (!canonical) throw new RangeError('invalid language tag: ' + JSON.stringify(tag));
    const parts = host.localeParts(canonical);
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
    maximize() { return adjustLocale(slotOf(this, 'Locale', 'maximize'), host.maximize); },
    minimize() { return adjustLocale(slotOf(this, 'Locale', 'minimize'), host.minimize); },
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
    const numberingOption = getOption(opts, 'numberingSystem', 'string');
    const hour12 = getOption(opts, 'hour12', 'boolean');
    let hourCycleOption = getOption(opts, 'hourCycle', 'string', ['h11', 'h12', 'h23', 'h24']);
    if (hour12 !== undefined) hourCycleOption = null;
    const resolved = resolveLocale(requested, false, ['ca', 'hc', 'nu'], {
      ca: calendarOption === undefined ? undefined : typeIdentifier(calendarOption, 'calendar'),
      hc: hourCycleOption,
      nu: numberingOption === undefined ? undefined : typeIdentifier(numberingOption, 'numberingSystem'),
    });
    const localeDefaults = host.localeDefaults(resolved.found);
    const calendar = resolved.ca || localeDefaults[0];
    const numberingSystem = resolved.nu || localeDefaults[1];
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
    let hourCycle = resolved.hc || '';
    if (hour12 !== undefined) {
      const cycles = host.dateHourCycles(resolved.found);
      hourCycle = hour12 ? cycles[1] : cycles[2];
    }
    const key = fieldOrder.map(name => fields[name] === undefined ? '' : String(fields[name]));
    const [pattern, resolvedFields] = host.datePattern(resolved.found, [key[0], key[1], key[2], key[3], key[4], key[6], key[7], key[8], key[9], key[5], key[10], hourCycle, dateStyle || '', timeStyle || '']);
    const resolvedValues = {};
    for (const entry of String(resolvedFields).split(';')) {
      if (!entry) continue;
      const [name, value] = entry.split('=');
      resolvedValues[name] = name === 'fractionalSecondDigits' ? Number(value) : value;
    }
    return { kind: 'DateTimeFormat', locale: resolved.locale, found: resolved.found, calendar, numberingSystem, timeZone, pattern, resolvedValues, dateStyle, timeStyle };
  };
  const timeClip = (value, method) => {
    const time = value === undefined ? Date.now() : Number(value);
    if (!Number.isFinite(time) || Math.abs(time) > 8.64e15) throw new RangeError('date value is not finite in DateTimeFormat.' + method + '()');
    return Math.trunc(time) + 0;
  };
  const dateParts = (slot, time) => Array.from(host.formatDate(slot.found, slot.pattern, time, slot.timeZone, slot.calendar, slot.numberingSystem), part => ({ type: part[0], value: part[1] }));
  const joinParts = parts => parts.map(part => part.value).join('');
  const dateRangeParts = (slot, start, end) => {
    const first = dateParts(slot, start);
    const second = dateParts(slot, end);
    if (joinParts(first) === joinParts(second)) return first.map(part => Object.assign(part, { source: 'shared' }));
    return first.map(part => Object.assign(part, { source: 'startRange' }))
      .concat([{ type: 'literal', value: host.dateRangeSeparator(slot.found), source: 'shared' }])
      .concat(second.map(part => Object.assign(part, { source: 'endRange' })));
  };
  const rangeArguments = (start, end, method) => {
    if (start === undefined || end === undefined) throw new TypeError('DateTimeFormat.' + method + ' requires two arguments');
    return [timeClip(start, method), timeClip(end, method)];
  };
  makeClass(DateTimeFormat, 'DateTimeFormat', 0, {
    resolvedOptions() {
      const slot = slotOf(this, 'DateTimeFormat', 'resolvedOptions');
      const result = { locale: slot.locale, calendar: slot.calendar, numberingSystem: slot.numberingSystem, timeZone: slot.timeZone };
      const values = slot.resolvedValues;
      if (values.hourCycle !== undefined) {
        result.hourCycle = values.hourCycle;
        result.hour12 = values.hourCycle === 'h11' || values.hourCycle === 'h12';
      }
      for (const name of fieldOrder) if (values[name] !== undefined) result[name] = values[name];
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

  // NumberFormat
  const numberOptionNames = ['numberingSystem', 'style', 'currency', 'currencyDisplay', 'currencySign', 'unit', 'unitDisplay', 'notation', 'compactDisplay', 'minimumIntegerDigits', 'minimumFractionDigits', 'maximumFractionDigits', 'minimumSignificantDigits', 'maximumSignificantDigits', 'roundingPriority', 'roundingIncrement', 'roundingMode', 'trailingZeroDisplay', 'useGrouping', 'signDisplay'];
  const checkDigits = opts => {
    getNumberOption(opts, 'minimumIntegerDigits', 1, 21);
    getNumberOption(opts, 'minimumFractionDigits', 0, 100);
    getNumberOption(opts, 'maximumFractionDigits', 0, 100);
    getNumberOption(opts, 'minimumSignificantDigits', 1, 21);
    getNumberOption(opts, 'maximumSignificantDigits', 1, 21);
  };
  const NumberFormat = function NumberFormat(locales, options) {
    if (new.target === undefined) return new NumberFormat(locales, options);
    const requested = canonicalList(locales);
    const opts = options === undefined ? {} : toObject(options);
    const numberingOption = getOption(opts, 'numberingSystem', 'string');
    const resolved = resolveLocale(requested, false, ['nu'], { nu: numberingOption === undefined ? undefined : typeIdentifier(numberingOption, 'numberingSystem') });
    if (String(opts.style) === 'currency' && opts.currency === undefined) throw new TypeError('undefined currency in NumberFormat() with currency style');
    if (String(opts.style) === 'unit' && opts.unit === undefined) throw new TypeError('undefined unit in NumberFormat() with unit style');
    checkDigits(opts);
    const engine = formatjs('NumberFormat', resolved.found);
    const impl = new engine.Constructor(engine.dataName, copyOptions(opts, numberOptionNames, { localeMatcher: 'lookup', numberingSystem: resolved.nu || host.localeDefaults(resolved.found)[2] }));
    slots.set(this, { kind: 'NumberFormat', locale: resolved.locale, impl });
  };
  makeClass(NumberFormat, 'NumberFormat', 0, {
    resolvedOptions() { const slot = slotOf(this, 'NumberFormat', 'resolvedOptions'); return resolvedWith(slot.impl, slot.locale); },
    formatToParts(value) { return slotOf(this, 'NumberFormat', 'formatToParts').impl.formatToParts(value); },
    formatRange(start, end) { return slotOf(this, 'NumberFormat', 'formatRange').impl.formatRange(start, end); },
    formatRangeToParts(start, end) { return slotOf(this, 'NumberFormat', 'formatRangeToParts').impl.formatRangeToParts(start, end); },
    get format() {
      const slot = slotOf(this, 'NumberFormat', 'get format');
      if (!slot.bound) slot.bound = anonymous(1, value => slot.impl.format(value));
      return slot.bound;
    },
  }, {}, false);

  const implOf = (object, kind, method) => slotOf(object, kind, method).impl;
  const formatjsClass = (name, length, keys, optionNames, members, lengths, prepare) => {
    const Constructor = { [name]: function (locales, options) {
      requireNew(new.target, name);
      const requested = canonicalList(locales);
      if (prepare) prepare(options);
      const opts = options === undefined ? {} : toObject(options);
      const numberingOption = keys.includes('nu') ? getOption(opts, 'numberingSystem', 'string') : undefined;
      const resolved = resolveLocale(requested, false, keys, { nu: numberingOption === undefined ? undefined : typeIdentifier(numberingOption, 'numberingSystem') });
      const engine = formatjs(name, resolved.found);
      const extra = { localeMatcher: 'lookup' };
      if (keys.includes('nu')) extra.numberingSystem = resolved.nu || host.localeDefaults(resolved.found)[2];
      const impl = new engine.Constructor(engine.dataName, copyOptions(opts, optionNames, extra));
      slots.set(this, { kind: name, locale: resolved.locale, impl });
    } }[name];
    const all = { resolvedOptions() { const slot = slotOf(this, name, 'resolvedOptions'); return resolvedWith(slot.impl, slot.locale); } };
    return makeClass(Constructor, name, length, Object.assign(all, members), lengths, false);
  };
  formatjsClass('PluralRules', 0, [], ['type', 'notation', 'minimumIntegerDigits', 'minimumFractionDigits', 'maximumFractionDigits', 'minimumSignificantDigits', 'maximumSignificantDigits', 'roundingPriority', 'roundingIncrement', 'roundingMode', 'trailingZeroDisplay'], {
    select(number) { return implOf(this, 'PluralRules', 'select').select(number); },
    selectRange(start, end) { return implOf(this, 'PluralRules', 'selectRange').selectRange(start, end); },
  }, { select: 1, selectRange: 2 });
  formatjsClass('RelativeTimeFormat', 0, ['nu'], ['style', 'numeric'], {
    format(value, unit) { return implOf(this, 'RelativeTimeFormat', 'format').format(value, unit); },
    formatToParts(value, unit) { return implOf(this, 'RelativeTimeFormat', 'formatToParts').formatToParts(value, unit); },
  }, { format: 2, formatToParts: 2 });
  formatjsClass('ListFormat', 0, [], ['type', 'style'], {
    format(list) { return implOf(this, 'ListFormat', 'format').format(list); },
    formatToParts(list) { return implOf(this, 'ListFormat', 'formatToParts').formatToParts(list); },
  }, { format: 1, formatToParts: 1 });
  formatjsClass('DisplayNames', 2, [], ['style', 'type', 'fallback', 'languageDisplay'], {
    of(code) {
      const impl = implOf(this, 'DisplayNames', 'of');
      return impl.of(impl.resolvedOptions().type === 'region' && accountRegion ? accountRegion : code);
    },
  }, { of: 1 }, options => {
    if (options === null || typeof options !== 'object' && typeof options !== 'function') throw new TypeError('options argument of Intl.DisplayNames must be an object, got ' + (options === null ? 'null' : typeof options));
  });
  formatjsClass('DurationFormat', 0, ['nu'], ['style', 'years', 'yearsDisplay', 'months', 'monthsDisplay', 'weeks', 'weeksDisplay', 'days', 'daysDisplay', 'hours', 'hoursDisplay', 'minutes', 'minutesDisplay', 'seconds', 'secondsDisplay', 'milliseconds', 'millisecondsDisplay', 'microseconds', 'microsecondsDisplay', 'nanoseconds', 'nanosecondsDisplay', 'fractionalDigits'], {
    format(duration) { return implOf(this, 'DurationFormat', 'format').format(duration); },
    formatToParts(duration) { return implOf(this, 'DurationFormat', 'formatToParts').formatToParts(duration); },
  }, { format: 1, formatToParts: 1 });

  // Segmenter
  const segmentsPrototype = {};
  const segmentIteratorPrototype = Object.create(Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]())));
  const segmentData = segment => segment === undefined ? undefined : Object.assign({ segment: segment.segment, index: segment.index, input: segment.input }, segment.isWordLike === undefined ? {} : { isWordLike: segment.isWordLike });
  install(segmentsPrototype, {
    containing(index) { return segmentData(implOf(this, 'Segments', 'containing').containing(index)); },
    [Symbol.iterator]() {
      const iterator = Object.create(segmentIteratorPrototype);
      slots.set(iterator, { kind: 'SegmentIterator', impl: implOf(this, 'Segments', '[Symbol.iterator]')[Symbol.iterator]() });
      return iterator;
    },
  }, { containing: 1 });
  install(segmentIteratorPrototype, {
    next() { const step = implOf(this, 'SegmentIterator', 'next').next(); return step.done ? { value: undefined, done: true } : { value: segmentData(step.value), done: false }; },
  });
  define(segmentIteratorPrototype, Symbol.toStringTag, { value: 'Segmenter String Iterator', writable: false, enumerable: false, configurable: true });
  formatjsClass('Segmenter', 0, [], ['granularity'], {
    segment(string) {
      const segments = Object.create(segmentsPrototype);
      slots.set(segments, { kind: 'Segments', impl: implOf(this, 'Segmenter', 'segment').segment(String(string)) });
      return segments;
    },
  }, { segment: 1 });

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
