import type { MarkdownIt as Markdown } from 'markdown-it'

// trailingPunctuation 是 GFM 自动链接末尾不计入网址的标点
const trailingPunctuation = /[?!.,:*_~]+$/

// gfmLinkify 让自动链接按 GFM 规则不含末尾标点，`**https://a.b/c**:` 中的 `**:` 留在链接之外
export function gfmLinkify(md: Markdown) {
  const linkify = md.linkify
  const normalize = linkify.normalize.bind(linkify)
  linkify.normalize = (match: Parameters<typeof normalize>[0]) => {
    const text = match.text.replace(trailingPunctuation, '')
    match.lastIndex -= match.text.length - text.length
    match.raw = match.text = match.url = text
    normalize(match)
  }
}
