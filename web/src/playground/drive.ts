import { reactive } from 'vue'
import { apiKey } from './request'
import type { Part } from './types'

// DriveFile 是本服务 Files API 列出的已上传文件，文件存放在账户的 Drive 中
export interface DriveFile {
  name: string
  displayName?: string
  mimeType: string
  sizeBytes?: string
  createTime?: string
  expirationTime: string
  uri: string
}

// driveNames 记录从 Drive 选择的文件 URI 对应的文件名
export const driveNames = reactive(new Map<string, string>())

// listDriveFiles 经本服务 Files API 列出已上传的文件
export async function listDriveFiles(): Promise<DriveFile[]> {
  const response = await fetch('/v1beta/files?pageSize=100', { headers: filesHeaders() })
  if (!response.ok) throw new Error(await response.text())
  return ((await response.json()) as { files?: DriveFile[] }).files ?? []
}

// driveFileExists 检查已上传的 Files API 文件是否存在
export async function driveFileExists(fileUri: string): Promise<boolean> {
  const name = fileUri.split('/files/')[1]
  if (!name) return false
  const response = await fetch(`/v1beta/files/${encodeURIComponent(name)}`, {
    headers: filesHeaders(),
  })
  return response.status !== 404
}

// filesHeaders 返回调用本服务 Files API 的请求头，设置了 API 密钥时带上密钥
function filesHeaders(): Headers {
  const headers = new Headers()
  const key = apiKey()
  if (key !== '') headers.set('x-goog-api-key', key)
  return headers
}

// uploadDriveFile 与官网一致在附件加入时经本服务 Files API 把文件上传到账户的 Drive，返回上传后的文件
export async function uploadDriveFile(file: Blob, name: string): Promise<DriveFile> {
  const form = new FormData()
  form.append('metadata', JSON.stringify({ file: { displayName: name } }))
  form.append('file', file, name)
  const response = await fetch('/upload/v1beta/files?uploadType=multipart', {
    method: 'POST',
    headers: filesHeaders(),
    body: form,
  })
  if (!response.ok) throw new Error(await response.text())
  return ((await response.json()) as { file: DriveFile }).file
}

// countVideoFrames 与官网一致以 StreamExtractVideoFrames 统计已上传视频按视频设置抽帧后的 token 数
export async function countVideoFrames(
  model: string,
  fileUri: string,
  videoMetadata: Part['videoMetadata'],
): Promise<number> {
  const response = await fetch('/api/video-tokens', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ model, file: fileUri, videoMetadata }),
  })
  if (!response.ok) throw new Error(await response.text())
  return ((await response.json()) as { totalTokens: number }).totalTokens
}
