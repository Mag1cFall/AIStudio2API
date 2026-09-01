import type {
  Account,
  AccountDraft,
  AccountLoginInput,
  AdminEvent,
  ChromeImportInput,
  ChromeImportProfile,
  Model,
  PlaygroundInput,
  PlaygroundMedia,
  Cooldown,
  RequestSummary,
  ServiceConfig,
  ServiceStatus,
} from '@/types'

interface AccountsResponse {
  accounts: Account[]
}

interface AccountResponse {
  account: Account
}

interface ChromeProfilesResponse {
  profiles: ChromeImportProfile[]
}

interface ModelsResponse {
  models: Model[]
}

interface CooldownsResponse {
  cooldowns: Cooldown[]
}

interface RequestsResponse {
  requests: RequestSummary[]
}

export interface EventConnection {
  close: () => void
}

export class ApiError extends Error {
  readonly status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

// responseErrorMessage 提取四套公开协议共享的错误消息
async function responseErrorMessage(response: Response): Promise<string> {
  const body = await response.text()
  if (body === '') return response.statusText

  const value: unknown = JSON.parse(body)
  const message = pathValue(value, ['error', 'message'])
  return typeof message === 'string' ? message : body
}

// requestJSON 执行管理端 JSON 请求并保留服务端错误语义
async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  if (init?.body !== undefined) {
    headers.set('Content-Type', 'application/json')
  }

  const response = await fetch(path, { ...init, headers })
  if (!response.ok) {
    throw new ApiError(await responseErrorMessage(response), response.status)
  }

  return (await response.json()) as T
}

// requestCommand 执行无需响应体的管理写操作
async function requestCommand(path: string, init: RequestInit): Promise<void> {
  const headers = new Headers(init.headers)
  if (init.body !== undefined) {
    headers.set('Content-Type', 'application/json')
  }

  const response = await fetch(path, { ...init, headers })
  if (!response.ok) {
    throw new ApiError(await responseErrorMessage(response), response.status)
  }
}

// parseAdminEvent 校验单一事件流的事件外壳
function parseAdminEvent(raw: string): AdminEvent | undefined {
  const value: unknown = JSON.parse(raw)
  if (typeof value !== 'object' || value === null || !('type' in value) || !('data' in value)) {
    return undefined
  }

  const type = value.type
  if (
    type !== 'status' &&
    type !== 'log' &&
    type !== 'accounts' &&
    type !== 'models' &&
    type !== 'cooldowns' &&
    type !== 'request'
  ) {
    return undefined
  }

  return value as AdminEvent
}

export const api = {
  status: () => requestJSON<ServiceStatus>('/api/status'),
  accounts: async () => (await requestJSON<AccountsResponse>('/api/accounts')).accounts,
  models: async () => (await requestJSON<ModelsResponse>('/api/models')).models,
  cooldowns: async () => (await requestJSON<CooldownsResponse>('/api/cooldowns')).cooldowns,
  requests: async () => (await requestJSON<RequestsResponse>('/api/requests')).requests,
  config: () => requestJSON<ServiceConfig>('/api/config'),
  createAccount: (input: AccountLoginInput) =>
    requestJSON<AccountResponse>('/api/accounts', {
      method: 'POST',
      body: JSON.stringify(input),
    }),
  chromeImportProfiles: async () =>
    (await requestJSON<ChromeProfilesResponse>('/api/accounts/import/chrome')).profiles,
  importChromeAccounts: (input: ChromeImportInput) =>
    requestJSON<AccountsResponse>('/api/accounts/import/chrome', {
      method: 'POST',
      body: JSON.stringify(input),
    }),
  updateAccount: (id: string, draft: AccountDraft) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(draft),
    }),
  deleteAccount: (id: string) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  loginAccount: (id: string) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}/login`, { method: 'POST' }),
  verifyAccount: (id: string) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}/verify`, { method: 'POST' }),
  startService: () => requestJSON<ServiceStatus>('/api/control/start', { method: 'POST' }),
  stopService: () => requestJSON<ServiceStatus>('/api/control/stop', { method: 'POST' }),
  clearLogs: () => requestCommand('/api/logs', { method: 'DELETE' }),
  saveConfig: (config: ServiceConfig) =>
    requestJSON<ServiceConfig>('/api/config', {
      method: 'PUT',
      body: JSON.stringify(config),
    }),
  cancelRequest: (id: string) =>
    requestCommand(`/api/requests/${encodeURIComponent(id)}/cancel`, {
      method: 'POST',
    }),
}

// openAdminEvents 建立唯一的管理状态 SSE 连接
export function openAdminEvents(
  onEvent: (event: AdminEvent) => void,
  onOpen: () => void,
): EventConnection {
  const source = new EventSource('/api/events')
  source.onopen = onOpen
  source.onmessage = (message) => {
    const event = parseAdminEvent(message.data)
    if (event !== undefined) {
      onEvent(event)
    }
  }

  return {
    close: () => source.close(),
  }
}

interface PlaygroundRequest {
  path: string
  headers: Headers
  body: string
  responseType: 'json' | 'audio'
}

export interface PlaygroundChunk {
  text: string
  reasoning: string
  tools: string
  media: PlaygroundMedia[]
}

function toolPayload(input: PlaygroundInput): unknown[] | undefined {
  if (input.tool === '') return undefined

  if (input.protocol === 'gemini') {
    const names: Record<Exclude<PlaygroundInput['tool'], ''>, string> = {
      web_search: 'googleSearch',
      image_search: 'imageSearch',
      code_interpreter: 'codeExecution',
      url_context: 'urlContext',
      google_maps: 'googleMaps',
    }
    return [{ [names[input.tool]]: {} }]
  }

  if (input.protocol === 'anthropic') {
    const definitions: Record<
      Exclude<PlaygroundInput['tool'], ''>,
      { type: string; name: string }
    > = {
      web_search: { type: 'web_search_20250305', name: 'web_search' },
      image_search: { type: 'image_search', name: 'image_search' },
      code_interpreter: {
        type: 'code_execution_20250825',
        name: 'code_execution',
      },
      url_context: { type: 'web_fetch_20250910', name: 'web_fetch' },
      google_maps: { type: 'google_maps', name: 'google_maps' },
    }
    return [definitions[input.tool]]
  }

  return [{ type: input.tool }]
}

// buildPlaygroundRequest 将输出类型和协议映射为公开 API 请求
function buildPlaygroundRequest(input: PlaygroundInput): PlaygroundRequest {
  const headers = new Headers({ 'Content-Type': 'application/json' })
  if (input.apiKey !== '') {
    headers.set('Authorization', `Bearer ${input.apiKey}`)
  }

  if (input.mode === 'video') {
    return {
      path: '/v1/videos',
      headers,
      responseType: 'json',
      body: JSON.stringify({ model: input.model, prompt: input.prompt }),
    }
  }

  if (input.mode === 'image') {
    return {
      path: '/v1/images/generations',
      headers,
      responseType: 'json',
      body: JSON.stringify({
        model: input.model,
        prompt: input.prompt,
        n: 1,
        size: input.imageSize,
        quality: input.imageQuality,
      }),
    }
  }

  if (input.mode === 'speech') {
    return {
      path: '/v1/audio/speech',
      headers,
      responseType: 'audio',
      body: JSON.stringify({
        model: input.model,
        input: input.prompt,
        instructions: input.system || undefined,
        voice: input.voice || undefined,
        response_format: 'wav',
      }),
    }
  }

  if (input.mode === 'music') {
    return {
      path: `/v1beta/models/${encodeURIComponent(input.model)}:generateContent`,
      headers,
      responseType: 'json',
      body: JSON.stringify({
        contents: [{ role: 'user', parts: [{ text: input.prompt }] }],
        generationConfig: { responseModalities: ['AUDIO'] },
      }),
    }
  }

  const tools = toolPayload(input)

  if (input.protocol === 'openai-chat') {
    const messages = input.system
      ? [
          { role: 'system', content: input.system },
          { role: 'user', content: input.prompt },
        ]
      : [{ role: 'user', content: input.prompt }]
    return {
      path: '/v1/chat/completions',
      headers,
      responseType: 'json',
      body: JSON.stringify({
        model: input.model,
        messages,
        stream: input.stream,
        reasoning_effort: input.reasoning || undefined,
        tools,
      }),
    }
  }

  if (input.protocol === 'openai-responses') {
    return {
      path: '/v1/responses',
      headers,
      responseType: 'json',
      body: JSON.stringify({
        model: input.model,
        input: input.prompt,
        instructions: input.system || undefined,
        stream: input.stream,
        reasoning: input.reasoning ? { effort: input.reasoning } : undefined,
        tools,
      }),
    }
  }

  if (input.protocol === 'anthropic') {
    headers.set('anthropic-version', '2023-06-01')
    if (input.apiKey !== '') {
      headers.set('x-api-key', input.apiKey)
    }
    return {
      path: '/v1/messages',
      headers,
      responseType: 'json',
      body: JSON.stringify({
        model: input.model,
        max_tokens: 1024,
        messages: [{ role: 'user', content: input.prompt }],
        system: input.system || undefined,
        stream: input.stream,
        output_config: input.reasoning ? { effort: input.reasoning } : undefined,
        tools,
      }),
    }
  }

  const suffix = input.stream ? 'streamGenerateContent' : 'generateContent'
  return {
    path: `/v1beta/models/${encodeURIComponent(input.model)}:${suffix}`,
    headers,
    responseType: 'json',
    body: JSON.stringify({
      contents: [{ role: 'user', parts: [{ text: input.prompt }] }],
      systemInstruction: input.system
        ? { role: 'system', parts: [{ text: input.system }] }
        : undefined,
      generationConfig: input.reasoning
        ? { thinkingConfig: { thinkingLevel: input.reasoning } }
        : undefined,
      tools,
    }),
  }
}

// isRecord 收窄公开协议响应中的动态对象
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

// pathValue 读取公开协议中已知的嵌套字段
function pathValue(value: unknown, path: readonly (string | number)[]): unknown {
  let current = value
  for (const part of path) {
    if (typeof part === 'number') {
      if (!Array.isArray(current)) return undefined
      current = current[part]
      continue
    }
    if (!isRecord(current)) return undefined
    current = current[part]
  }
  return current
}

interface VideoState {
  id: string
  status: 'queued' | 'completed' | 'failed'
}

function videoState(value: unknown): VideoState {
  const id = isRecord(value) && typeof value.id === 'string' ? value.id : ''
  const status = isRecord(value) && typeof value.status === 'string' ? value.status : ''
  if (id === '' || (status !== 'queued' && status !== 'completed' && status !== 'failed')) {
    throw new Error('Invalid video response')
  }
  return { id, status }
}

function waitForVideoPoll(signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(new DOMException('Aborted', 'AbortError'))
      return
    }
    const onAbort = () => {
      window.clearTimeout(timer)
      reject(new DOMException('Aborted', 'AbortError'))
    }
    const timer = window.setTimeout(() => {
      signal.removeEventListener('abort', onAbort)
      resolve()
    }, 2000)
    signal.addEventListener('abort', onAbort, { once: true })
  })
}

async function completeVideo(
  response: Response,
  headers: Headers,
  signal: AbortSignal,
): Promise<{ status: number; chunk: PlaygroundChunk; raw: string }> {
  let value: unknown = await response.json()
  let state = videoState(value)
  while (state.status === 'queued') {
    await waitForVideoPoll(signal)
    const poll = await fetch(`/v1/videos/${encodeURIComponent(state.id)}`, { headers, signal })
    if (!poll.ok) {
      throw new ApiError(await responseErrorMessage(poll), poll.status)
    }
    value = await poll.json()
    state = videoState(value)
  }
  if (state.status === 'failed') {
    throw new ApiError('Veo generation failed', response.status)
  }
  const content = await fetch(`/v1/videos/${encodeURIComponent(state.id)}/content`, {
    headers,
    signal,
  })
  if (!content.ok) {
    throw new ApiError(await responseErrorMessage(content), content.status)
  }
  const blob = await content.blob()
  return {
    status: content.status,
    chunk: {
      ...emptyChunk(),
      media: [{ mime: blob.type || 'video/mp4', url: URL.createObjectURL(blob) }],
    },
    raw: JSON.stringify(value, null, 2),
  }
}

function emptyChunk(): PlaygroundChunk {
  return { text: '', reasoning: '', tools: '', media: [] }
}

function mediaFrom(value: unknown): PlaygroundMedia | undefined {
  if (!isRecord(value)) return undefined
  const mime = typeof value.mimeType === 'string' ? value.mimeType : ''
  const data = typeof value.data === 'string' ? value.data : ''
  const url = typeof value.fileUri === 'string' ? value.fileUri : ''
  if (mime === '' || (data === '' && url === '')) return undefined
  return { mime, url: data === '' ? url : `data:${mime};base64,${data}` }
}

function toolLine(value: unknown): string {
  if (!isRecord(value)) return ''
  const name = typeof value.name === 'string' ? value.name : ''
  const argumentsValue = value.arguments ?? value.args ?? value.input
  const argumentsText =
    typeof argumentsValue === 'string' ? argumentsValue : JSON.stringify(argumentsValue ?? {})
  return name === '' ? argumentsText : `${name} ${argumentsText}`
}

function geminiChunk(value: unknown): PlaygroundChunk {
  const chunk = emptyChunk()
  const parts = pathValue(value, ['candidates', 0, 'content', 'parts'])
  if (!Array.isArray(parts)) return chunk
  for (const part of parts) {
    if (!isRecord(part)) continue
    if (typeof part.text === 'string') {
      if (part.thought === true) chunk.reasoning += part.text
      else chunk.text += part.text
    }
    if (part.functionCall !== undefined) chunk.tools += `${toolLine(part.functionCall)}\n`
    if (part.executableCode !== undefined) {
      chunk.tools += `${JSON.stringify(part.executableCode, null, 2)}\n`
    }
    if (part.codeExecutionResult !== undefined) {
      chunk.tools += `${JSON.stringify(part.codeExecutionResult, null, 2)}\n`
    }
    const media = mediaFrom(part.inlineData ?? part.fileData)
    if (media !== undefined) chunk.media.push(media)
  }
  return chunk
}

function chatChunk(value: unknown): PlaygroundChunk {
  const chunk = emptyChunk()
  const message =
    pathValue(value, ['choices', 0, 'delta']) ?? pathValue(value, ['choices', 0, 'message'])
  if (!isRecord(message)) return chunk
  if (typeof message.content === 'string') chunk.text = message.content
  if (typeof message.reasoning_content === 'string') chunk.reasoning = message.reasoning_content
  if (Array.isArray(message.tool_calls)) {
    for (const call of message.tool_calls) {
      const value = pathValue(call, ['function'])
      chunk.tools += `${toolLine(value)}\n`
    }
  }
  return chunk
}

function responsesChunk(value: unknown): PlaygroundChunk {
  const chunk = emptyChunk()
  const type = isRecord(value) && typeof value.type === 'string' ? value.type : ''
  const delta = isRecord(value) && typeof value.delta === 'string' ? value.delta : ''
  if (type === 'response.output_text.delta') chunk.text = delta
  if (type === 'response.reasoning_summary_text.delta') chunk.reasoning = delta
  if (type.includes('function_call') && delta !== '') chunk.tools = delta
  if (type === 'response.output_item.added') {
    const item = pathValue(value, ['item'])
    if (isRecord(item) && item.type === 'function_call') chunk.tools = `${toolLine(item)}\n`
  }
  if (type !== '') return chunk
  const response = isRecord(value) && isRecord(value.response) ? value.response : value
  if (!isRecord(response)) return chunk
  if (typeof response.output_text === 'string') chunk.text += response.output_text
  if (!Array.isArray(response.output)) return chunk
  for (const item of response.output) {
    if (!isRecord(item)) continue
    if (item.type === 'reasoning' && Array.isArray(item.summary)) {
      for (const summary of item.summary) {
        const text = pathValue(summary, ['text'])
        if (typeof text === 'string') chunk.reasoning += text
      }
    }
    if (item.type === 'message' && Array.isArray(item.content)) {
      for (const content of item.content) {
        const text = pathValue(content, ['text'])
        if (typeof text === 'string') chunk.text += text
      }
    }
    if (item.type === 'function_call') chunk.tools += `${toolLine(item)}\n`
    if (item.type === 'image_generation_call' && typeof item.result === 'string') {
      chunk.media.push({ mime: 'image/png', url: `data:image/png;base64,${item.result}` })
    }
  }
  return chunk
}

function anthropicChunk(value: unknown): PlaygroundChunk {
  const chunk = emptyChunk()
  const deltaText = pathValue(value, ['delta', 'text'])
  const thinking = pathValue(value, ['delta', 'thinking'])
  const partialJSON = pathValue(value, ['delta', 'partial_json'])
  if (typeof deltaText === 'string') chunk.text = deltaText
  if (typeof thinking === 'string') chunk.reasoning = thinking
  if (typeof partialJSON === 'string') chunk.tools = partialJSON
  const blocks = isRecord(value) && Array.isArray(value.content) ? value.content : []
  for (const block of blocks) {
    if (!isRecord(block)) continue
    if (block.type === 'text' && typeof block.text === 'string') chunk.text += block.text
    if (block.type === 'thinking' && typeof block.thinking === 'string') {
      chunk.reasoning += block.thinking
    }
    if (block.type === 'tool_use') chunk.tools += `${toolLine(block)}\n`
  }
  const startBlock = pathValue(value, ['content_block'])
  if (isRecord(startBlock) && startBlock.type === 'tool_use') {
    chunk.tools += `${toolLine(startBlock)}\n`
  }
  return chunk
}

function imageChunk(value: unknown): PlaygroundChunk {
  const chunk = emptyChunk()
  const data = isRecord(value) && Array.isArray(value.data) ? value.data : []
  for (const item of data) {
    if (!isRecord(item)) continue
    if (typeof item.revised_prompt === 'string') chunk.text += item.revised_prompt
    if (typeof item.url === 'string') {
      const mime = /^data:([^;,]+)/.exec(item.url)?.[1] ?? 'image/png'
      chunk.media.push({ mime, url: item.url })
    }
    if (typeof item.b64_json === 'string') {
      chunk.media.push({ mime: 'image/png', url: `data:image/png;base64,${item.b64_json}` })
    }
  }
  return chunk
}

// responseChunk 提取公开协议的正文、思考、工具与媒体结果
function responseChunk(input: PlaygroundInput, value: unknown): PlaygroundChunk {
  if (input.mode === 'image') return imageChunk(value)
  if (input.mode === 'music' || input.protocol === 'gemini') return geminiChunk(value)
  if (input.protocol === 'openai-chat') return chatChunk(value)
  if (input.protocol === 'openai-responses') return responsesChunk(value)
  return anthropicChunk(value)
}

// readEventStream 逐帧读取公开协议流式响应
async function readEventStream(
  response: Response,
  input: PlaygroundInput,
  onDelta: (chunk: PlaygroundChunk, raw: string) => void,
): Promise<void> {
  if (response.body === null) return

  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader()
  let buffer = ''
  while (true) {
    const { done, value } = await reader.read()
    buffer += value ?? ''

    let boundary = buffer.indexOf('\n\n')
    while (boundary >= 0) {
      const frame = buffer.slice(0, boundary)
      buffer = buffer.slice(boundary + 2)
      const data = frame
        .split('\n')
        .filter((line) => line.startsWith('data:'))
        .map((line) => line.slice(5).trimStart())
        .join('\n')

      if (data !== '' && data !== '[DONE]') {
        const value: unknown = JSON.parse(data)
        onDelta(responseChunk(input, value), data)
      }
      boundary = buffer.indexOf('\n\n')
    }

    if (done) break
  }
}

// runPlayground 执行一次公开协议试用并返回取消控制器
export async function runPlayground(
  input: PlaygroundInput,
  signal: AbortSignal,
  onDelta: (chunk: PlaygroundChunk, raw: string) => void,
): Promise<{ status: number; chunk?: PlaygroundChunk; raw?: string }> {
  const request = buildPlaygroundRequest(input)
  const response = await fetch(request.path, {
    method: 'POST',
    headers: request.headers,
    body: request.body,
    signal,
  })

  if (!response.ok) {
    throw new ApiError(await responseErrorMessage(response), response.status)
  }

  if (input.mode === 'video') {
    return completeVideo(response, request.headers, signal)
  }

  if (request.responseType === 'audio') {
    const blob = await response.blob()
    return {
      status: response.status,
      chunk: {
        ...emptyChunk(),
        media: [{ mime: blob.type || 'audio/wav', url: URL.createObjectURL(blob) }],
      },
      raw: JSON.stringify({ type: blob.type || 'audio/wav', size: blob.size }, null, 2),
    }
  }

  if (input.mode === 'text' && input.stream) {
    await readEventStream(response, input, onDelta)
    return { status: response.status }
  }

  const value: unknown = await response.json()
  return {
    status: response.status,
    chunk: responseChunk(input, value),
    raw: JSON.stringify(value, null, 2),
  }
}
