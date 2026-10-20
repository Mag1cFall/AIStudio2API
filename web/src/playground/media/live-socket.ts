import { apiKey } from '../request'

// LiveServerMessage 是本服务 /v1/live 的服务端帧
export interface LiveServerMessage {
  type: string
  model?: string
  text?: string
  mime_type?: string
  data?: string
  transcription?: { text?: string; finished?: boolean; language_code?: string }
  tool_call?: { id: string; name: string; arguments: Record<string, unknown> }
  tool_call_ids?: string[]
  session_token?: string
  resumable?: boolean
  error?: string
  code?: string
}

// openLiveSocket 连接本服务的 /v1/live，API key 经 URL 参数传递
export function openLiveSocket(): WebSocket {
  const url = new URL('/v1/live', window.location.href)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  const key = apiKey()
  if (key !== '') url.searchParams.set('key', key)
  return new WebSocket(url)
}
