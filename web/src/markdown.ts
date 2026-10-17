import MarkdownIt from 'markdown-it'

// markdown 渲染 Markdown 正文，原始 HTML 按文本转义，链接在新标签页打开
export const markdown = new MarkdownIt({ linkify: true, breaks: true })
const renderLink =
  markdown.renderer.rules.link_open ??
  ((tokens, index, options, _env, self) => self.renderToken(tokens, index, options))
markdown.renderer.rules.link_open = (tokens, index, options, env, self) => {
  tokens[index]?.attrSet('target', '_blank')
  tokens[index]?.attrSet('rel', 'noreferrer')
  return renderLink(tokens, index, options, env, self)
}
