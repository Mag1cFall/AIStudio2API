import { markRaw, reactive, toRaw, watch } from 'vue'
import { api } from '@/api'
import { base64Bytes } from './bytes'
import { driveNames } from './drive'
import type { Part, Turn } from './types'

// MediaKind 是预览对话框显示的媒体类型
export type MediaKind = 'image' | 'video' | 'audio' | 'text' | 'pdf'

// mediaBlobs 与 mediaURLs 按原始 inlineData 对象缓存磁盘 File 与临时 Object URL
const mediaBlobs = new WeakMap<object, Blob>()
const mediaURLs = new WeakMap<object, string>()
const mediaRegistry = new FinalizationRegistry<string>((url) => URL.revokeObjectURL(url))

// promptMediaURL 返回服务端保存的对话附件媒体流地址
export function promptMediaURL(id: string): string {
  return `/api/prompts/media/${encodeURIComponent(id)}`
}

// cacheAttachmentBlob 绑定内联附件的原始 Blob
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
  if (file instanceof File) cacheAttachmentBlob(inlineData, file)
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

// loadAttachmentBlob 返回附件的 Blob：本地 File 缓存优先，否则从对话媒体库读取或解码内联 Base64
export async function loadAttachmentBlob(
  part: Part,
  signal: AbortSignal | null = null,
): Promise<Blob> {
  const raw = toRaw(part.inlineData!)
  const cached = mediaBlobs.get(raw)
  if (cached !== undefined) return cached
  if (raw.mediaId) {
    const response = await fetch(promptMediaURL(raw.mediaId), { signal })
    if (!response.ok) throw new Error(`${response.status} ${response.statusText}`)
    return response.blob()
  }
  return new Blob([base64Bytes(raw.data)], { type: raw.mimeType })
}

// blobToBase64 使用浏览器原生 FileReader 把 Blob 编码为 Base64
export function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result as string
      resolve(result.slice(result.indexOf(',') + 1))
    }
    reader.onerror = () => reject(reader.error)
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

// attachmentKind 返回内联附件在预览对话框中的类型，Drive 与 YouTube 引用和无法预览的文件为空
export function attachmentKind(part: Part): MediaKind | '' {
  const mime = part.inlineData?.mimeType ?? ''
  if (/^(image|video|audio)\//.test(mime)) return mime.split('/')[0] as MediaKind
  if (mime.startsWith('text/')) return 'text'
  if (mime === 'application/pdf') return 'pdf'
  return ''
}

// attachmentName 返回附件显示的文件名：记录的文件名、Drive 文件名、链接或 MIME 类型
export function attachmentName(part: Part): string {
  const uri = part.fileData?.fileUri
  return (
    part.fileName ??
    (uri === undefined ? undefined : (driveNames.get(uri) ?? uri)) ??
    part.inlineData?.mimeType ??
    ''
  )
}

// attachmentText 返回文本附件的全文
export async function attachmentText(part: Part): Promise<string> {
  return (await loadAttachmentBlob(part)).text()
}

// downloadAttachment 把内联附件按文件名存为文件
export function downloadAttachment(part: Part): void {
  const link = document.createElement('a')
  link.href = attachmentURL(part)
  link.download = attachmentName(part)
  link.click()
}

// AttachmentPreview 是预览对话框的状态：打开的附件、类型、媒体地址与文本全文
export interface AttachmentPreview {
  open: boolean
  part?: Part
  kind: MediaKind
  src: string
  text: string
}

// useAttachmentPreview 返回附件预览对话框的状态与打开、关闭方法
export function useAttachmentPreview() {
  const preview = reactive<AttachmentPreview>({ open: false, kind: 'image', src: '', text: '' })
  // show 在对话框中打开附件，无法预览的附件不打开
  async function show(part: Part): Promise<void> {
    const kind = attachmentKind(part)
    if (kind === '') return
    release()
    preview.text = kind === 'text' ? await attachmentText(part) : ''
    preview.src = kind === 'text' ? '' : attachmentURL(part)
    Object.assign(preview, { part, kind, open: true })
  }
  // release 清空预览地址
  function release(): void {
    preview.src = ''
  }
  watch(
    () => preview.open,
    (open) => {
      if (open) return
      release()
      delete preview.part
      preview.text = ''
    },
  )
  return { preview, show, release }
}
