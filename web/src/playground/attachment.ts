import { reactive, toRaw, watch } from 'vue'
import { base64Bytes } from './bytes'
import { driveNames } from './drive'
import type { Part } from './types'

// MediaKind 是预览对话框显示的媒体类型
export type MediaKind = 'image' | 'video' | 'audio' | 'text' | 'pdf'

// mediaBlobs 与 mediaURLs 按原始 inlineData 对象缓存 Blob 与 Object URL
const mediaBlobs = new WeakMap<object, Blob>()
const mediaURLs = new WeakMap<object, string>()
const mediaRegistry = new FinalizationRegistry<string>((url) => URL.revokeObjectURL(url))

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

// attachmentBlob 返回内联附件的 Blob：有缓存时取缓存，否则解码 Base64
export function attachmentBlob(part: Part): Blob {
  const raw = toRaw(part.inlineData!)
  return mediaBlobs.get(raw) ?? new Blob([base64Bytes(raw.data)], { type: raw.mimeType })
}

// attachmentURL 返回内联附件的 Object URL，首次显示时以 attachmentBlob 创建并缓存其 Blob
export function attachmentURL(part: Part): string {
  if (!part.inlineData) return ''
  const raw = toRaw(part.inlineData)
  let url = mediaURLs.get(raw)
  if (url === undefined) {
    const blob = attachmentBlob(part)
    cacheAttachmentBlob(raw, blob)
    url = URL.createObjectURL(blob)
    mediaURLs.set(raw, url)
    mediaRegistry.register(raw, url)
  }
  return url
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
export function attachmentText(part: Part): Promise<string> {
  return attachmentBlob(part).text()
}

// downloadAttachment 把内联附件按文件名存为文件
export function downloadAttachment(part: Part): void {
  const link = document.createElement('a')
  link.href = attachmentURL(part)
  link.download = attachmentName(part)
  link.click()
}

// AttachmentPreview 是预览对话框的状态：打开的附件、类型、Blob 地址与文本全文
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
