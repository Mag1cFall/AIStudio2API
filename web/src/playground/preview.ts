// installStorage 在不透明来源的预览文档中把访问会抛错的 localStorage、sessionStorage 与 document.cookie 换成内存实现，并注入之后出现的 srcdoc 框架
function installStorage(): void {
  const blocked = (read: () => unknown): boolean => {
    try {
      read()
      return false
    } catch {
      return true
    }
  }
  for (const name of ['localStorage', 'sessionStorage'] as const) {
    if (!blocked(() => window[name])) continue
    const items = new Map<string, string>()
    const methods = Object.create(Storage.prototype, {
      length: { get: () => items.size },
      key: { value: (index: number) => [...items.keys()][index] ?? null },
      getItem: { value: (key: string) => items.get(String(key)) ?? null },
      setItem: {
        value: (key: string, value: string) => void items.set(String(key), String(value)),
      },
      removeItem: { value: (key: string) => void items.delete(String(key)) },
      clear: { value: () => items.clear() },
    }) as Storage
    const named = (key: string | symbol): key is string =>
      typeof key === 'string' && !(key in methods)
    const storage = new Proxy(Object.create(methods) as Storage, {
      get: (target, key) => (named(key) ? items.get(key) : Reflect.get(target, key)),
      set: (target, key, value) =>
        typeof key === 'string'
          ? (items.set(key, String(value)), true)
          : Reflect.set(target, key, value),
      has: (target, key) => (named(key) ? items.has(key) : Reflect.has(target, key)),
      deleteProperty: (target, key) =>
        named(key) ? (items.delete(key), true) : Reflect.deleteProperty(target, key),
      ownKeys: () => [...items.keys()],
      getOwnPropertyDescriptor: (_target, key) =>
        named(key) && items.has(key)
          ? { value: items.get(key), writable: true, enumerable: true, configurable: true }
          : undefined,
    })
    Object.defineProperty(window, name, {
      configurable: true,
      enumerable: true,
      get: () => storage,
    })
  }
  if (blocked(() => document.cookie)) {
    const jar = new Map<string, string>()
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      enumerable: true,
      get: () => [...jar].map(([name, value]) => (name ? `${name}=${value}` : value)).join('; '),
      set: (text: string) => {
        const [pair = '', ...attributes] = String(text).split(';')
        const split = pair.indexOf('=')
        const name = split < 0 ? '' : pair.slice(0, split).trim()
        const expired = attributes.some((attribute) => {
          const [key = '', value = ''] = attribute.split('=').map((part) => part.trim())
          if (key.toLowerCase() === 'max-age') return Number(value) <= 0
          return key.toLowerCase() === 'expires' && Date.parse(value) <= Date.now()
        })
        if (expired) jar.delete(name)
        else jar.set(name, pair.slice(split + 1).trim())
      },
    })
  }
  const script = `<script>(${installStorage.toString()})()</` + 'script>'
  const patch = (node: Node): void => {
    if (!(node instanceof HTMLIFrameElement)) return
    const source = node.getAttribute('srcdoc')
    if (source === null || source.includes(script)) return
    const doctype = /^\s*<!doctype[^>]*>/i.exec(source)?.[0] ?? ''
    node.setAttribute('srcdoc', doctype + script + source.slice(doctype.length))
  }
  new MutationObserver((records) => {
    for (const record of records) {
      patch(record.target)
      for (const node of record.addedNodes) {
        patch(node)
        if (node instanceof Element) node.querySelectorAll('iframe').forEach(patch)
      }
    }
  }).observe(document, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ['srcdoc'],
  })
  document.currentScript?.remove()
}

// previewDocument 返回预览框的 srcdoc：指向 about:srcdoc 的 base 使页内锚点在预览内跳转，它与存储替身脚本插在 doctype 之后，没有 doctype 时插在最前面
export function previewDocument(code: string): string {
  const doctype = /^\s*<!doctype[^>]*>/i.exec(code)?.[0] ?? ''
  return `${doctype}<base href="about:srcdoc"><script>(${installStorage.toString()})()</script>${code.slice(doctype.length)}`
}
