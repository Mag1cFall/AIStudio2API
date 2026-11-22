import { reactive, watch } from 'vue'
import { driveNames } from './drive'
import { attachmentURL, loadAttachmentBlob } from './media-store'
import type { Part } from './types'

export {
  attachmentURL,
  blobToBase64,
  cacheAttachmentBlob,
  copyAttachmentBlob,
  hasCachedAttachmentBlob,
  hasSameAttachmentBlob,
  loadAttachmentBlob,
  localAttachment,
  promptMediaURL,
  revokeAttachmentURL,
  revokeTurnURLs,
  uploadAttachment,
} from './media-store'

// MediaKind 是预览对话框显示的媒体类型
export type MediaKind = 'image' | 'video' | 'audio' | 'text' | 'pdf'

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
