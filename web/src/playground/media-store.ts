import { markRaw, toRaw } from 'vue'
import { api } from '@/api'
import { base64Bytes } from './bytes'
import type { Part, Turn } from './types'

// mediaBlobs 与 mediaURLs 按原始 inlineData 对象缓存未落盘 Blob 与临时 Object URL
const mediaBlobs = new WeakMap<object, Blob>()
const mediaURLs = new WeakMap<object, string>()
const mediaRegistry = new FinalizationRegistry<string>((url) => URL.revokeObjectURL(url))

// promptMediaURL 返回服务端保存的对话附件媒体流地址
export function promptMediaURL(id: string): string {
  return `/api/prompts/media/${encodeURIComponent(id)}`
}

// cacheAttachmentBlob 绑定内联附件的本地 Blob
export function cacheAttachmentBlob(inline: NonNullable<Part['inlineData']>, blob: Blob): void {
  mediaBlobs.set(toRaw(inline), blob)
}

// copyAttachmentBlob 为复制出的内联附件复用已缓存的 Blob
export function copyAttachmentBlob(
  source: NonNullable<Part['inlineData']>,
  target: NonNullable<Part['inlineData']>,
): void {
  const blob = mediaBlobs.get(toRaw(source))
  if (blob !== undefined) cacheAttachmentBlob(target, blob)
}

// uploadAttachment 把附件流式上传到对话媒体库，返回带 mediaId 的内联片段
export async function uploadAttachment(
  file: Blob,
  mimeType = file.type || 'application/octet-stream',
): Promise<Part> {
  const mediaId = await api.uploadPromptMedia(file, mimeType)
  const inlineData = markRaw({ mimeType, data: '', mediaId })
  return { inlineData }
}

// localAttachment 创建绑定本地 Blob 的内联附件片段，用于临时对话
export function localAttachment(
  file: Blob,
  mimeType = file.type || 'application/octet-stream',
): Part {
  const inlineData = markRaw({ mimeType, data: '' })
  cacheAttachmentBlob(inlineData, file)
  return { inlineData }
}

// hasCachedAttachmentBlob 判断内联附件是否持有本地缓存的 Blob
export function hasCachedAttachmentBlob(inline: NonNullable<Part['inlineData']>): boolean {
  return mediaBlobs.has(toRaw(inline))
}

// hasSameAttachmentBlob 判断内联附件是否缓存了同一个本地 Blob
export function hasSameAttachmentBlob(
  inline: NonNullable<Part['inlineData']>,
  blob: Blob,
): boolean {
  return mediaBlobs.get(toRaw(inline)) === blob
}

// loadAttachmentBlob 返回附件的 Blob：已落盘附件以服务端副本为准，未落盘附件复用本地缓存或解码 Base64
export async function loadAttachmentBlob(
  part: Part,
  signal: AbortSignal | null = null,
): Promise<Blob> {
  const raw = toRaw(part.inlineData!)
  if (raw.mediaId) {
    const response = await fetch(promptMediaURL(raw.mediaId), { signal })
    if (!response.ok) throw new Error(`${response.status} ${response.statusText}`)
    const blob = await response.blob()
    return raw.mimeType && blob.type !== raw.mimeType
      ? blob.slice(0, blob.size, raw.mimeType)
      : blob
  }
  const cached = mediaBlobs.get(raw)
  if (cached !== undefined) return cached
  const blob = new Blob([base64Bytes(raw.data)], { type: raw.mimeType })
  cacheAttachmentBlob(raw, blob)
  return blob
}

// blobToBase64 使用浏览器原生 FileReader 把 Blob 编码为 Base64，并响应取消信号
export function blobToBase64(blob: Blob, signal: AbortSignal | null = null): Promise<string> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason ?? new DOMException('Aborted', 'AbortError'))
      return
    }
    const reader = new FileReader()
    const onAbort = () => {
      reader.abort()
      reject(signal?.reason ?? new DOMException('Aborted', 'AbortError'))
    }
    const cleanup = () => signal?.removeEventListener('abort', onAbort)
    signal?.addEventListener('abort', onAbort, { once: true })
    reader.onload = () => {
      cleanup()
      const result = reader.result as string
      resolve(result.slice(result.indexOf(',') + 1))
    }
    reader.onerror = () => {
      cleanup()
      reject(reader.error)
    }
    reader.readAsDataURL(blob)
  })
}

// attachmentURL 返回内联附件的播放或下载地址：服务端媒体直接返回 HTTP URL，其余按需创建 Object URL
export function attachmentURL(part: Part): string {
  if (!part.inlineData) return ''
  const raw = toRaw(part.inlineData)
  if (raw.mediaId) return promptMediaURL(raw.mediaId)
  let url = mediaURLs.get(raw)
  if (url === undefined) {
    const blob = mediaBlobs.get(raw) ?? new Blob([base64Bytes(raw.data)], { type: raw.mimeType })
    cacheAttachmentBlob(raw, blob)
    url = URL.createObjectURL(blob)
    mediaURLs.set(raw, url)
    mediaRegistry.register(raw, url, raw)
  }
  return url
}

// revokeAttachmentURL 立即释放内联附件创建的临时 Object URL 与缓存 Blob
export function revokeAttachmentURL(part: Part): void {
  if (!part.inlineData) return
  const raw = toRaw(part.inlineData)
  const url = mediaURLs.get(raw)
  if (url !== undefined) {
    mediaRegistry.unregister(raw)
    URL.revokeObjectURL(url)
    mediaURLs.delete(raw)
  }
  mediaBlobs.delete(raw)
}

// revokeTurnURLs 释放多轮对话中全部内联附件的临时 Object URL 与缓存 Blob
export function revokeTurnURLs(turns: readonly Turn[]): void {
  for (const turn of turns) {
    for (const part of turn.parts) revokeAttachmentURL(part)
  }
}
