// verb 匹配 Go 格式串中的一个格式动词，与 internal/i18n 相同
const verb = /%(?:\[\d+\])?[-+# 0]*\d*(?:\.\d+)?[a-zA-Z%]/g
// han 判断文案是否含汉字
const han = /\p{Script=Han}/u
// separators 是整句没有匹配时依次尝试的列表切分符，各段分别翻译
const separators = ['\n', ' | ', '; ', '；']
// wrappers 是错误链的切分符，在第一次出现处拆成外层说明与内层原因
const wrappers = [': ', '：']
// halfWidth 是全角切分符在不用全角标点的语言中的写法，与 internal/i18n 相同
const halfWidth: Record<string, string> = { '；': '; ', '：': ': ' }
// cacheLimit 是译文缓存的条数上限，超过时清空
const cacheLimit = 20000

// Entry 是一条格式串的匹配规则与译文
interface Entry {
  pattern: RegExp
  anchor: string
  weight: number
  plain: boolean
  text: string
}

// escape 转义正则中的特殊字符
const escape = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

// codePoints 返回文字的码位数，与 Go 按 rune 计数相同
const codePoints = (text: string) => text.replace(/[\uD800-\uDBFF][\uDC00-\uDFFF]/g, '_').length

// compile 把格式串编译为整句匹配的正则，记录最长的字面片段与字面字符数
function compile(template: string, text: string): Entry {
  let pattern = '^'
  let literal = ''
  let anchor = ''
  let weight = 0
  let plain = true
  const flush = () => {
    if (codePoints(literal) > codePoints(anchor)) anchor = literal
    weight += codePoints(literal)
    pattern += escape(literal)
    literal = ''
  }
  let last = 0
  for (const match of template.matchAll(verb)) {
    literal += template.slice(last, match.index)
    last = match.index + match[0].length
    if (match[0] === '%%') {
      literal += '%'
      continue
    }
    flush()
    plain = false
    const kind = match[0].at(-1)
    pattern +=
      kind === 'd'
        ? '(-?\\d+)'
        : kind === 'x'
          ? '([0-9a-f]+)'
          : kind === 'q'
            ? '("(?:[^"\\\\]|\\\\.)*")'
            : '([\\s\\S]*?)'
  }
  literal += template.slice(last)
  flush()
  return { pattern: new RegExp(`${pattern}$`), anchor, weight, plain, text }
}

// Catalog 按服务文案翻译表把服务生成的简体中文换成界面语言，匹配规则与 internal/i18n 相同
export class Catalog {
  private readonly entries: Entry[]
  private readonly cache = new Map<string, string>()
  private readonly wide: boolean

  constructor(table: Record<string, string>, locale: string) {
    this.wide = locale === 'zh-TW' || locale === 'ja'
    this.entries = Object.entries(table)
      .map(([template, text]) => compile(template, text))
      .sort((a, b) => b.weight - a.weight)
  }

  // translate 返回文案的译文，不含汉字或没有匹配的文案原样返回
  translate(text: string): string {
    if (!han.test(text)) return text
    const cached = this.cache.get(text)
    if (cached !== undefined) return cached
    const result = this.resolve(text)
    if (this.cache.size >= cacheLimit) this.cache.clear()
    this.cache.set(text, result)
    return result
  }

  // punctuation 返回切分符在译文中的写法，不用全角标点的语言把全角分号与冒号写成半角加空格
  private punctuation(separator: string): string {
    return this.wide ? separator : (halfWidth[separator] ?? separator)
  }

  // resolve 先按整句匹配格式串，没有匹配时去掉首尾空白，依次按列表切分符分段、按错误链拆开外层与内层、按不含动词的文案前缀拆开，最后拆开键与值
  private resolve(text: string): string {
    for (const item of this.entries) {
      if (!text.includes(item.anchor)) continue
      const groups = item.pattern.exec(text)
      if (groups === null) continue
      return item.text.replace(/\{(\d+)\}/g, (token, index: string) => {
        const value = groups[Number(index) + 1]
        return value === undefined ? token : this.translate(value)
      })
    }
    const trimmed = text.trim()
    if (trimmed !== text) {
      const start = text.indexOf(trimmed)
      return text.slice(0, start) + this.translate(trimmed) + text.slice(start + trimmed.length)
    }
    for (const separator of separators) {
      const parts = text.split(separator)
      if (parts.length > 1)
        return parts.map((part) => this.translate(part)).join(this.punctuation(separator))
    }
    for (const wrapper of wrappers) {
      const index = text.indexOf(wrapper)
      if (index >= 0)
        return (
          this.translate(text.slice(0, index)) +
          this.punctuation(wrapper) +
          this.translate(text.slice(index + wrapper.length))
        )
    }
    for (const item of this.entries) {
      if (item.plain && text.startsWith(`${item.anchor} `))
        return `${item.text} ${this.translate(text.slice(item.anchor.length + 1))}`
    }
    const equals = text.indexOf('=')
    if (equals >= 0)
      return `${this.translate(text.slice(0, equals))}=${this.translate(text.slice(equals + 1))}`
    return text
  }
}
