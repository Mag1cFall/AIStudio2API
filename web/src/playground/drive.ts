import { reactive } from 'vue'
import { apiKey } from './request'

// DriveFile 是本服务 Files API 列出的已上传文件，文件存放在账户的 Drive 中
export interface DriveFile {
  name: string
  displayName?: string
  mimeType: string
  sizeBytes?: string
  createTime?: string
  uri: string
}

// driveNames 记录从 Drive 选择的文件 URI 对应的文件名
export const driveNames = reactive(new Map<string, string>())

// listDriveFiles 经本服务 Files API 列出已上传的文件
export async function listDriveFiles(): Promise<DriveFile[]> {
  const headers = new Headers()
  const key = apiKey()
  if (key !== '') headers.set('x-goog-api-key', key)
  const response = await fetch('/v1beta/files?pageSize=100', { headers })
  if (!response.ok) throw new Error(await response.text())
  return ((await response.json()) as { files?: DriveFile[] }).files ?? []
}
