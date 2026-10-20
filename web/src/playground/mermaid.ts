import type { MermaidRequest } from './mermaid-worker'

let worker: Worker | undefined
let nextID = 0
// pending 是等待 Worker 应答的渲染请求
const pending = new Map<number, (svg: string) => void>()

// createWorker 创建渲染 Mermaid 的 Worker，首次渲染时才加载
function createWorker(): Worker {
  const created = new Worker(new URL('./mermaid-worker.ts', import.meta.url), { type: 'module' })
  created.onmessage = (event: MessageEvent<{ id: number; svg: string }>) => {
    pending.get(event.data.id)?.(event.data.svg)
    pending.delete(event.data.id)
  }
  return created
}

// renderMermaid 在共用的 Worker 中渲染 Mermaid 源码，返回 SVG 字符串，渲染失败时为空
export function renderMermaid(request: Omit<MermaidRequest, 'id'>): Promise<string> {
  const target = (worker ??= createWorker())
  const id = ++nextID
  return new Promise((resolve) => {
    pending.set(id, resolve)
    target.postMessage({ ...request, id })
  })
}
