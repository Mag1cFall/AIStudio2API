import MarkdownIt from 'markdown-it'

// markdown 渲染 Markdown 正文，原始 HTML 按文本转义，链接在新标签页打开
export const markdown = new MarkdownIt({ linkify: true, breaks: true })
markdown.renderer.rules.link_open = (tokens, index, options, _env, self) => {
  tokens[index]!.attrSet('target', '_blank')
  tokens[index]!.attrSet('rel', 'noreferrer')
  return self.renderToken(tokens, index, options)
}
