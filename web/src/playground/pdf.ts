import workerURL from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import { base64Bytes } from './bytes'

// pdfThumbnail 把 Base64 PDF 的第一页按给定的 CSS 宽度画成图片，返回 PNG data URL；pdf.js 在首次调用时才加载
export async function pdfThumbnail(data: string, width: number): Promise<string> {
  const { GlobalWorkerOptions, getDocument } = await import('pdfjs-dist')
  GlobalWorkerOptions.workerSrc = workerURL
  const task = getDocument({ data: base64Bytes(data) })
  try {
    const pdf = await task.promise
    const page = await pdf.getPage(1)
    const scale = (width * window.devicePixelRatio) / page.getViewport({ scale: 1 }).width
    const viewport = page.getViewport({ scale })
    const canvas = document.createElement('canvas')
    canvas.width = Math.ceil(viewport.width)
    canvas.height = Math.ceil(viewport.height)
    await page.render({ canvas, viewport }).promise
    return canvas.toDataURL('image/png')
  } finally {
    await task.destroy()
  }
}
