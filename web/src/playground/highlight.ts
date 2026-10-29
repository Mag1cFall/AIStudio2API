let worker: Worker | undefined
let nextID = 0
// pending 保存等待高亮结果的代码块
const pending = new Map<number, (html: string) => void>()

// highlightCode 在共用 Worker 中高亮完整代码
export function highlightCode(code: string, language: string): Promise<string> {
  if (!worker) {
    worker = new Worker(new URL('./highlight-worker.ts', import.meta.url), { type: 'module' })
    worker.onmessage = (event: MessageEvent<{ id: number; html: string }>) => {
      pending.get(event.data.id)?.(event.data.html)
      pending.delete(event.data.id)
    }
  }
  const id = ++nextID
  return new Promise((resolve) => {
    pending.set(id, resolve)
    worker!.postMessage({ id, code, language })
  })
}
