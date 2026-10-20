import MarkdownIt, { type Env, type Token } from 'markdown-it'
import katexPlugin from '@vscode/markdown-it-katex'
import 'katex/dist/katex.min.css'
import type { Support } from './types'

// markdown 是 Playground 正文解析器：原始 HTML 按文本转义，公式由 KaTeX 渲染
const markdown = new MarkdownIt({ linkify: true, breaks: false, html: false })
const katex = (katexPlugin as unknown as { default?: typeof katexPlugin }).default ?? katexPlugin
markdown.use(katex, { throwOnError: false, enableBareBlocks: true })
const renderLink =
  markdown.renderer.rules.link_open ??
  ((tokens, index, options, _env, self) => self.renderToken(tokens, index, options))
markdown.renderer.rules.link_open = (tokens, index, options, env, self) => {
  tokens[index]?.attrSet('target', '_blank')
  tokens[index]?.attrSet('rel', 'noopener noreferrer')
  return renderLink(tokens, index, options, env, self)
}

// MarkdownNode 是正文的渲染节点：块元素、块内行内 HTML、代码块或整段 HTML 的块
export type MarkdownNode =
  | { kind: 'element'; tag: string; attrs: Record<string, string>; children: MarkdownNode[] }
  | { kind: 'inline'; html: string }
  | { kind: 'code'; code: string; language: string }
  | { kind: 'html'; html: string }

// Block 是正文中一个顶层块，offset 为它在原文中的起始字符位置
export interface Block {
  key: number
  offset: number
  nodes: MarkdownNode[]
}

// buildNodes 把一段块级 token 转为节点树；紧凑列表中隐藏的段落不生成元素
function buildNodes(tokens: Token[], env: Env): MarkdownNode[] {
  const root: MarkdownNode[] = []
  const stack: MarkdownNode[][] = [root]
  for (const token of tokens) {
    const children = stack.at(-1)!
    if (token.nesting === 1) {
      if (token.hidden) {
        stack.push(children)
        continue
      }
      const element: Extract<MarkdownNode, { kind: 'element' }> = {
        kind: 'element',
        tag: token.tag,
        attrs: Object.fromEntries(
          (token.attrs ?? []).map(([name, value]) => [name, String(value)]),
        ),
        children: [],
      }
      children.push(element)
      stack.push(element.children)
    } else if (token.nesting === -1) {
      stack.pop()
    } else if (token.type === 'inline') {
      children.push({
        kind: 'inline',
        html: markdown.renderer.renderInline(token.children ?? [], markdown.options, env),
      })
    } else if (token.type === 'fence' || token.type === 'code_block') {
      children.push({
        kind: 'code',
        code: token.content.replace(/\n$/, ''),
        language: token.info.trim().split(/\s+/)[0]?.toLowerCase() ?? '',
      })
    } else if (token.type === 'hr') {
      children.push({ kind: 'element', tag: 'hr', attrs: {}, children: [] })
    } else {
      children.push({
        kind: 'html',
        html: markdown.renderer.render([token], markdown.options, env),
      })
    }
  }
  return root
}

// lineOffsets 返回每一行起始的字符位置
function lineOffsets(text: string): number[] {
  const offsets = [0]
  for (let index = text.indexOf('\n'); index >= 0; index = text.indexOf('\n', index + 1))
    offsets.push(index + 1)
  return offsets
}

// parseBlocks 把一段原文解析为顶层块，base 为这段原文在全文中的起始位置，env 收集其中的引用定义
function parseBlocks(text: string, base: number, firstKey: number, env: Env): Block[] {
  const tokens = markdown.parse(text, env)
  const offsets = lineOffsets(text)
  const blocks: Block[] = []
  let start = 0
  let depth = 0
  for (let index = 0; index < tokens.length; index++) {
    depth += tokens[index]!.nesting
    if (depth !== 0) continue
    const group = tokens.slice(start, index + 1)
    const line = group[0]?.map?.[0] ?? 0
    blocks.push({
      key: firstKey + blocks.length,
      offset: base + (offsets[line] ?? 0),
      nodes: buildNodes(group, env),
    })
    start = index + 1
  }
  return blocks
}

// IncrementalMarkdown 增量解析流式正文：原文只在末尾增长且不含引用定义时，只重新解析最后一个顶层块之后的部分，之前的块保持同一对象；含引用定义时整篇解析，使其他块中的引用链接得到解析
export class IncrementalMarkdown {
  private text = ''
  private blocks: Block[] = []
  private references = false

  render(text: string): Block[] {
    const last = this.blocks.at(-1)
    if (last !== undefined && !this.references && text.startsWith(this.text)) {
      const env: Env = {}
      const tail = parseBlocks(text.slice(last.offset), last.offset, last.key, env)
      if (env.references === undefined) {
        this.blocks = [...this.blocks.slice(0, -1), ...tail]
        this.text = text
        return this.blocks
      }
    }
    const env: Env = {}
    this.blocks = parseBlocks(text, 0, 0, env)
    this.references = env.references !== undefined
    this.text = text
    return this.blocks
  }
}

// codeRanges 返回原文中围栏与缩进代码块所占的字符区间
function codeRanges(text: string): [number, number][] {
  const offsets = lineOffsets(text)
  return markdown
    .parse(text, {})
    .filter((token) => (token.type === 'fence' || token.type === 'code_block') && token.map)
    .map((token) => [offsets[token.map![0]] ?? 0, offsets[token.map![1]] ?? text.length])
}

// insertCitations 与官网一致，在每条支撑结束处按来源序号插入 [[n](uri)]，代码块内的位置不插入，恰在代码块结尾的移到下一字符
export function insertCitations(text: string, citations: Support[], uris: string[]): string {
  const bytes = new TextEncoder().encode(text)
  const decoder = new TextDecoder()
  const ranges = codeRanges(text)
  const marks: { index: number; label: string }[] = []
  for (const citation of citations) {
    const index = decoder.decode(bytes.subarray(0, citation.end)).length
    if (ranges.some(([start, end]) => index > start && index < end)) continue
    const shifted = ranges.some(([, end]) => index === end) ? index + 1 : index
    for (const chunk of [...citation.chunks].sort((a, b) => a - b)) {
      const uri = uris[chunk]
      if (uri !== undefined) marks.push({ index: shifted, label: `[[${chunk + 1}](${uri})]` })
    }
  }
  let output = ''
  let position = 0
  for (const mark of marks.sort((a, b) => a.index - b.index)) {
    output += text.slice(position, mark.index) + mark.label
    position = mark.index
  }
  return output + text.slice(position)
}
