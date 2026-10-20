import type { RenderOptions } from 'beautiful-mermaid'

// MermaidRequest 是一次渲染请求：id 对应应答，code 为图的源码
export interface MermaidRequest {
  id: number
  code: string
  options: RenderOptions
}

// scope 是 Worker 的全局对象
const scope = self as unknown as {
  document?: object
  onmessage: ((event: MessageEvent<MermaidRequest>) => void) | null
  postMessage(message: { id: number; svg: string }): void
}

// document 占位使 elkjs 加载页面内的同步布局器，没有它时 elkjs 把自身当作 Worker 脚本接管消息
scope.document = {}
// self 定义为可写的自有属性，beautiful-mermaid 初始化 elkjs 后会回写 self
Object.defineProperty(scope, 'self', { value: scope, writable: true, configurable: true })
// library 是在占位之后加载的 beautiful-mermaid
const library = import('beautiful-mermaid')

// onmessage 在 Worker 内渲染 SVG，渲染失败时应答空字符串
scope.onmessage = async (event) => {
  const { id, code, options } = event.data
  const { renderMermaidSVG } = await library
  try {
    scope.postMessage({ id, svg: renderMermaidSVG(code, options) })
  } catch {
    scope.postMessage({ id, svg: '' })
  }
}
