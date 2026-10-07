import { EventSourceParserStream } from 'eventsource-parser/stream'
import type {
  Account,
  AccountDraft,
  AccountLoginInput,
  AdminEvent,
  ChromeImportInput,
  ChromeImportProfile,
  PairingToken,
  Model,
  PlaygroundInput,
  PlaygroundMedia,
  PlaygroundMode,
  PlaygroundProtocol,
  PlaygroundTool,
  Cooldown,
  RequestBody,
  RequestSummary,
  ServiceConfig,
  ServiceStatus,
  UsageRecordPage,
  UsageReport,
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

export interface AdminSession {
  enabled: boolean
  authenticated: boolean
  username: string
}

// checkSessionResponse 通知页面清理失效会话中的管理数据
function checkSessionResponse(path: string, response: Response): void {
  if (response.status === 401 && !path.startsWith('/api/auth/')) {
    window.dispatchEvent(new Event('admin-session-expired'))
  }
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
  if (!response.headers.get('content-type')?.includes('application/json')) return body

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
  checkSessionResponse(path, response)
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
  checkSessionResponse(path, response)
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
  session: () => requestJSON<AdminSession>('/api/auth/session'),
  login: (username: string, password: string) =>
    requestJSON<AdminSession>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  logout: () => requestCommand('/api/auth/logout', { method: 'POST' }),
  status: () => requestJSON<ServiceStatus>('/api/status'),
  accounts: async () => (await requestJSON<AccountsResponse>('/api/accounts')).accounts,
  models: async () => (await requestJSON<ModelsResponse>('/api/models')).models,
  cooldowns: async () => (await requestJSON<CooldownsResponse>('/api/cooldowns')).cooldowns,
  requests: async () => (await requestJSON<RequestsResponse>('/api/requests')).requests,
  config: () => requestJSON<ServiceConfig>('/api/config'),
  usage: (query: URLSearchParams, signal?: AbortSignal) =>
    requestJSON<UsageReport>(`/api/usage?${query.toString()}`, { signal: signal ?? null }),
  usageRecords: (query: URLSearchParams, signal?: AbortSignal) =>
    requestJSON<UsageRecordPage>(`/api/usage/records?${query.toString()}`, {
      signal: signal ?? null,
    }),
  usageExportURL: (query: URLSearchParams) => `/api/usage/records.csv?${query.toString()}`,
  requestBody: (id: string) =>
    requestJSON<RequestBody>(`/api/requests/${encodeURIComponent(id)}/body`),
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
  createPairing: () => requestJSON<PairingToken>('/api/pairing', { method: 'POST' }),
  updateAccount: (id: string, draft: AccountDraft) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(draft),
    }),
  deleteAccount: (id: string, keepalive = false) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}`, { method: 'DELETE', keepalive }),
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
  source.onerror = () => {
    void api
      .session()
      .then((session) => {
        if (!session.authenticated) window.dispatchEvent(new Event('admin-session-expired'))
      })
      .catch(() => {})
  }
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

// PlaygroundSetting 是运行设置面板中的一项参数
export type PlaygroundSetting =
  | 'system'
  | 'stream'
  | 'temperature'
  | 'topP'
  | 'topK'
  | 'maxOutputTokens'
  | 'stopSequences'
  | 'seed'
  | 'reasoning'
  | 'tools'
  | 'structuredOutput'
  | 'functionCalling'
  | 'imageSize'
  | 'aspectRatio'
  | 'resolution'
  | 'videoSeconds'
  | 'voice'
  | 'speakers'
  | 'safety'
  | 'mediaResolution'
  | 'imageOutput'

const textSettings: PlaygroundSetting[] = [
  'system',
  'stream',
  'temperature',
  'topP',
  'maxOutputTokens',
  'reasoning',
  'tools',
  'functionCalling',
]

// playgroundSettings 返回输出类型与协议在公开 API 中生效的运行设置
export function playgroundSettings(
  mode: PlaygroundMode,
  protocol: PlaygroundProtocol,
): ReadonlySet<PlaygroundSetting> {
  if (mode === 'text') {
    if (protocol === 'openai-chat')
      return new Set([...textSettings, 'stopSequences', 'seed', 'structuredOutput'])
    if (protocol === 'openai-responses') return new Set([...textSettings, 'structuredOutput'])
    if (protocol === 'anthropic') return new Set([...textSettings, 'topK', 'stopSequences'])
    return new Set([
      ...textSettings,
      'topK',
      'stopSequences',
      'seed',
      'structuredOutput',
      'safety',
      'mediaResolution',
    ])
  }
  if (mode === 'image') {
    if (protocol === 'openai-images') return new Set(['imageSize'])
    return new Set<PlaygroundSetting>([
      'imageOutput',
      'system',
      'temperature',
      'topP',
      'maxOutputTokens',
      'stopSequences',
      'reasoning',
      'tools',
      'aspectRatio',
      'resolution',
    ])
  }
  if (mode === 'speech') {
    if (protocol === 'openai-speech') return new Set(['system', 'voice'])
    return new Set(['temperature', 'voice', 'speakers'])
  }
  if (mode === 'video') return new Set(['videoSeconds', 'aspectRatio', 'resolution'])
  return new Set()
}

// playgroundProtocols 返回输出类型可用的公开协议，首项为默认值
export function playgroundProtocols(mode: PlaygroundMode): PlaygroundProtocol[] {
  if (mode === 'text') return ['openai-chat', 'openai-responses', 'anthropic', 'gemini']
  if (mode === 'image') return ['gemini', 'openai-images']
  if (mode === 'speech') return ['openai-speech', 'gemini']
  if (mode === 'video') return ['gemini', 'openai-videos']
  return ['gemini']
}

// playgroundPath 返回试用请求的公开端点路径
export function playgroundPath(
  input: Pick<PlaygroundInput, 'mode' | 'protocol' | 'model' | 'stream'>,
): string {
  if (input.protocol === 'openai-chat') return '/v1/chat/completions'
  if (input.protocol === 'openai-responses') return '/v1/responses'
  if (input.protocol === 'anthropic') return '/v1/messages'
  if (input.protocol === 'openai-images') return '/v1/images/generations'
  if (input.protocol === 'openai-speech') return '/v1/audio/speech'
  if (input.protocol === 'openai-videos') return '/v1/videos'
  const model = input.model === '' ? '{model}' : encodeURIComponent(input.model)
  if (input.mode === 'video') return `/v1beta/models/${model}:predictLongRunning`
  const method = input.mode === 'text' && input.stream ? 'streamGenerateContent' : 'generateContent'
  return `/v1beta/models/${model}:${method}`
}

// effectiveInput 清除当前输出类型与协议不使用的参数
function effectiveInput(input: PlaygroundInput): PlaygroundInput {
  const settings = playgroundSettings(input.mode, input.protocol)
  const keep = <T>(setting: PlaygroundSetting, value: T, empty: T): T =>
    settings.has(setting) ? value : empty
  return {
    ...input,
    system: keep('system', input.system, ''),
    stream: keep('stream', input.stream, false),
    temperature: keep('temperature', input.temperature, null),
    topP: keep('topP', input.topP, null),
    topK: keep('topK', input.topK, null),
    maxOutputTokens: keep('maxOutputTokens', input.maxOutputTokens, null),
    stopSequences: keep('stopSequences', input.stopSequences, []),
    seed: keep('seed', input.seed, null),
    reasoning: keep('reasoning', input.reasoning, ''),
    tools: keep('tools', input.tools, []),
    structuredOutput: keep('structuredOutput', input.structuredOutput, false),
    functionCalling: keep('functionCalling', input.functionCalling, false),
    speakers: keep('speakers', input.speakers, []),
    safety: keep('safety', input.safety, {}),
    mediaResolution: keep('mediaResolution', input.mediaResolution, ''),
    imageOnly: keep('imageOutput', input.imageOnly, false),
  }
}

const geminiToolNames: Record<PlaygroundTool, string> = {
  web_search: 'googleSearch',
  image_search: 'imageSearch',
  code_interpreter: 'codeExecution',
  url_context: 'urlContext',
  google_maps: 'googleMaps',
}

const anthropicTools: Record<PlaygroundTool, { type: string; name: string }> = {
  web_search: { type: 'web_search_20250305', name: 'web_search' },
  image_search: { type: 'image_search', name: 'image_search' },
  code_interpreter: { type: 'code_execution_20250825', name: 'code_execution' },
  url_context: { type: 'web_fetch_20250910', name: 'web_fetch' },
  google_maps: { type: 'google_maps', name: 'google_maps' },
}

// FunctionDeclaration 是试用页声明的函数，参数为 JSON Schema
interface FunctionDeclaration {
  name: string
  description?: string
  parameters?: unknown
}

// parseJSONField 解析运行设置中的 JSON 文本，失败时指出字段
function parseJSONField(text: string, label: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    throw new Error(`${label} is not valid JSON`)
  }
}

// functionDeclarations 读取函数声明，单个对象视为一个函数
function functionDeclarations(input: PlaygroundInput): FunctionDeclaration[] {
  if (!input.functionCalling || input.functions.trim() === '') return []
  const value = parseJSONField(input.functions, 'Function declarations')
  return (Array.isArray(value) ? value : [value]) as FunctionDeclaration[]
}

// responseSchema 返回结构化输出的 JSON Schema，未开启时省略
function responseSchema(input: PlaygroundInput): unknown {
  if (!input.structuredOutput || input.jsonSchema.trim() === '') return undefined
  return parseJSONField(input.jsonSchema, 'JSON schema')
}

// toolPayload 按协议组合内置工具与函数声明
function toolPayload(input: PlaygroundInput): unknown[] | undefined {
  const functions = functionDeclarations(input)
  let tools: unknown[]
  if (input.protocol === 'gemini') {
    tools = input.tools.map((tool) => ({ [geminiToolNames[tool]]: {} }))
    if (functions.length > 0) tools.push({ functionDeclarations: functions })
  } else if (input.protocol === 'anthropic') {
    tools = [
      ...input.tools.map((tool) => anthropicTools[tool]),
      ...functions.map((item) => ({
        name: item.name,
        description: item.description,
        input_schema: item.parameters ?? { type: 'object', properties: {} },
      })),
    ]
  } else if (input.protocol === 'openai-responses') {
    tools = [
      ...input.tools.map((type) => ({ type })),
      ...functions.map((item) => ({ type: 'function', ...item })),
    ]
  } else {
    tools = [
      ...input.tools.map((type) => ({ type })),
      ...functions.map((item) => ({ type: 'function', function: item })),
    ]
  }
  return tools.length > 0 ? tools : undefined
}

// optional 把未设置的数值参数省略
function optional<T>(value: T | null): T | undefined {
  return value === null ? undefined : value
}

// stopSequences 返回停止序列，没有时省略
function stopSequences(input: PlaygroundInput): string[] | undefined {
  return input.stopSequences.length > 0 ? input.stopSequences : undefined
}

// geminiGenerationConfig 组合 Gemini 生成参数，extra 为输出类型专属字段
function geminiGenerationConfig(
  input: PlaygroundInput,
  extra: Record<string, unknown> = {},
): Record<string, unknown> {
  const schema = responseSchema(input)
  return {
    temperature: optional(input.temperature),
    topP: optional(input.topP),
    topK: optional(input.topK),
    maxOutputTokens: optional(input.maxOutputTokens),
    stopSequences: stopSequences(input),
    seed: optional(input.seed),
    thinkingConfig: input.reasoning ? { thinkingLevel: input.reasoning } : undefined,
    responseMimeType: schema === undefined ? undefined : 'application/json',
    responseJsonSchema: schema,
    mediaResolution: input.mediaResolution || undefined,
    ...extra,
  }
}

// geminiSpeechConfig 返回单声音或多说话人配置
function geminiSpeechConfig(input: PlaygroundInput): unknown {
  const voice = (name: string) => ({ prebuiltVoiceConfig: { voiceName: name } })
  if (input.speakers.length > 1) {
    return {
      multiSpeakerVoiceConfig: {
        speakerVoiceConfigs: input.speakers.map((speaker) => ({
          speaker: speaker.name.trim(),
          voiceConfig: voice(speaker.voice),
        })),
      },
    }
  }
  return input.voice === '' ? undefined : { voiceConfig: voice(input.voice) }
}

// openAIVideoSizes 是宽高比与分辨率对应的 OpenAI 视频尺寸
const openAIVideoSizes: Record<string, string> = {
  '16:9/720p': '1280x720',
  '9:16/720p': '720x1280',
  '16:9/1080p': '1920x1080',
  '9:16/1080p': '1080x1920',
}

// buildPlaygroundRequest 将输出类型、协议与运行设置映射为公开 API 请求
function buildPlaygroundRequest(raw: PlaygroundInput): PlaygroundRequest {
  const input = effectiveInput(raw)
  const headers = new Headers({ 'Content-Type': 'application/json' })
  if (input.apiKey !== '') {
    headers.set('Authorization', `Bearer ${input.apiKey}`)
  }
  const path = playgroundPath(input)
  const request = (body: unknown, responseType: 'json' | 'audio' = 'json'): PlaygroundRequest => ({
    path,
    headers,
    responseType,
    body: JSON.stringify(body),
  })
  const system = input.system || undefined
  const contents = [{ role: 'user', parts: [{ text: input.prompt }] }]

  switch (input.protocol) {
    case 'openai-videos':
      return request({
        model: input.model,
        prompt: input.prompt,
        seconds: input.videoSeconds || undefined,
        size: openAIVideoSizes[`${input.aspectRatio || '16:9'}/${input.resolution || '720p'}`],
      })
    case 'openai-images':
      return request({
        model: input.model,
        prompt: input.prompt,
        n: 1,
        size: input.imageSize,
        quality: input.imageQuality,
      })
    case 'openai-speech':
      return request(
        {
          model: input.model,
          input: input.prompt,
          instructions: system,
          voice: input.voice || undefined,
          response_format: 'wav',
        },
        'audio',
      )
    case 'openai-chat': {
      const schema = responseSchema(input)
      return request({
        model: input.model,
        messages: system
          ? [
              { role: 'system', content: system },
              { role: 'user', content: input.prompt },
            ]
          : [{ role: 'user', content: input.prompt }],
        stream: input.stream,
        reasoning_effort: input.reasoning || undefined,
        temperature: optional(input.temperature),
        top_p: optional(input.topP),
        max_completion_tokens: optional(input.maxOutputTokens),
        stop: stopSequences(input),
        seed: optional(input.seed),
        response_format:
          schema === undefined
            ? undefined
            : { type: 'json_schema', json_schema: { name: 'response', schema } },
        tools: toolPayload(input),
      })
    }
    case 'openai-responses': {
      const schema = responseSchema(input)
      return request({
        model: input.model,
        input: input.prompt,
        instructions: system,
        stream: input.stream,
        reasoning: input.reasoning ? { effort: input.reasoning } : undefined,
        temperature: optional(input.temperature),
        top_p: optional(input.topP),
        max_output_tokens: optional(input.maxOutputTokens),
        text:
          schema === undefined
            ? undefined
            : { format: { type: 'json_schema', name: 'response', schema } },
        tools: toolPayload(input),
      })
    }
    case 'anthropic':
      headers.set('anthropic-version', '2023-06-01')
      if (input.apiKey !== '') headers.set('x-api-key', input.apiKey)
      return request({
        model: input.model,
        max_tokens: input.maxOutputTokens ?? 1024,
        messages: [{ role: 'user', content: input.prompt }],
        system,
        stream: input.stream,
        output_config: input.reasoning ? { effort: input.reasoning } : undefined,
        temperature: optional(input.temperature),
        top_p: optional(input.topP),
        top_k: optional(input.topK),
        stop_sequences: stopSequences(input),
        tools: toolPayload(input),
      })
  }

  if (input.mode === 'video') {
    return request({
      instances: [{ prompt: input.prompt }],
      parameters: {
        aspectRatio: input.aspectRatio || undefined,
        durationSeconds: input.videoSeconds === '' ? undefined : Number(input.videoSeconds),
        resolution: input.resolution || undefined,
      },
    })
  }
  if (input.mode === 'music') {
    return request({ contents, generationConfig: { responseModalities: ['AUDIO'] } })
  }
  if (input.mode === 'speech') {
    return request({
      contents,
      generationConfig: {
        responseModalities: ['AUDIO'],
        temperature: optional(input.temperature),
        speechConfig: geminiSpeechConfig(input),
      },
    })
  }
  const imageConfig =
    input.mode === 'image'
      ? {
          responseModalities: input.imageOnly ? ['IMAGE'] : undefined,
          imageConfig: {
            aspectRatio: input.aspectRatio || undefined,
            imageSize: input.resolution || undefined,
          },
        }
      : {}
  const safetySettings = Object.entries(input.safety)
    .filter(([, threshold]) => threshold !== 'OFF')
    .map(([category, threshold]) => ({ category, threshold }))
  return request({
    contents,
    systemInstruction: system ? { role: 'system', parts: [{ text: system }] } : undefined,
    generationConfig: geminiGenerationConfig(input, imageConfig),
    tools: toolPayload(input),
    safetySettings: safetySettings.length > 0 ? safetySettings : undefined,
  })
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
  return downloadVideo(`/v1/videos/${encodeURIComponent(state.id)}/content`, headers, signal, value)
}

// completeGeminiVideo 轮询 Gemini 视频操作并下载完成后的视频
async function completeGeminiVideo(
  response: Response,
  headers: Headers,
  signal: AbortSignal,
): Promise<{ status: number; chunk: PlaygroundChunk; raw: string }> {
  let value: unknown = await response.json()
  const name = pathValue(value, ['name'])
  if (typeof name !== 'string' || name === '') throw new Error('Invalid video operation')
  while (pathValue(value, ['done']) !== true) {
    await waitForVideoPoll(signal)
    const poll = await fetch(`/v1beta/${name}`, { headers, signal })
    if (!poll.ok) {
      throw new ApiError(await responseErrorMessage(poll), poll.status)
    }
    value = await poll.json()
  }
  const uri = pathValue(value, [
    'response',
    'generateVideoResponse',
    'generatedSamples',
    0,
    'video',
    'uri',
  ])
  if (typeof uri !== 'string') {
    throw new ApiError('Veo generation failed', response.status)
  }
  return downloadVideo(uri, headers, signal, value)
}

// downloadVideo 下载生成的视频并保留最后一次操作响应
async function downloadVideo(
  url: string,
  headers: Headers,
  signal: AbortSignal,
  value: unknown,
): Promise<{ status: number; chunk: PlaygroundChunk; raw: string }> {
  const content = await fetch(url, { headers, signal })
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

// pcmAudioWAV 把 Gemini 返回的 audio/L16 片段合并封装为 16 位 WAV
function pcmAudioWAV(media: PlaygroundMedia[]): PlaygroundMedia[] {
  const pcm = media.filter((item) => item.mime.toLowerCase().startsWith('audio/l16'))
  if (pcm.length === 0) return media
  const parameters = new Map(
    pcm[0]!.mime
      .split(';')
      .slice(1)
      .map((part) => part.trim().split('=') as [string, string]),
  )
  const rate = Number(parameters.get('rate'))
  const channels = Number(parameters.get('channels') ?? 1)
  const chunks = pcm.map((item) =>
    Uint8Array.from(atob(item.url.slice(item.url.indexOf(',') + 1)), (char) => char.charCodeAt(0)),
  )
  const length = chunks.reduce((total, chunk) => total + chunk.length, 0)
  const header = new DataView(new ArrayBuffer(44))
  const text = (offset: number, value: string) => {
    for (let index = 0; index < value.length; index++) {
      header.setUint8(offset + index, value.charCodeAt(index))
    }
  }
  text(0, 'RIFF')
  header.setUint32(4, 36 + length, true)
  text(8, 'WAVEfmt ')
  header.setUint32(16, 16, true)
  header.setUint16(20, 1, true)
  header.setUint16(22, channels, true)
  header.setUint32(24, rate, true)
  header.setUint32(28, rate * channels * 2, true)
  header.setUint16(32, channels * 2, true)
  header.setUint16(34, 16, true)
  text(36, 'data')
  header.setUint32(40, length, true)
  const blob = new Blob([header, ...chunks], { type: 'audio/wav' })
  return [
    ...media.filter((item) => !pcm.includes(item)),
    { mime: 'audio/wav', url: URL.createObjectURL(blob) },
  ]
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
  if (!isRecord(response) || !Array.isArray(response.output)) return chunk
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
  if (input.protocol === 'openai-images') return imageChunk(value)
  if (input.protocol === 'gemini') return geminiChunk(value)
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
  if (response.body === null) throw new Error('Empty event stream')

  const events = response.body
    .pipeThrough(new TextDecoderStream())
    .pipeThrough(new EventSourceParserStream())
  for await (const { data } of events) {
    if (input.protocol === 'openai-chat' && data === '[DONE]') return
    const value: unknown = JSON.parse(data)
    const error = pathValue(value, ['error']) ?? pathValue(value, ['response', 'error'])
    if (error != null) {
      const message = pathValue(error, ['message'])
      throw new ApiError(typeof message === 'string' ? message : data, response.status)
    }
    onDelta(responseChunk(input, value), data)
    if (streamFinished(input, value)) return
  }
  throw new Error('Event stream ended before the protocol completion event')
}

// streamFinished 按各公开协议的结束事件确认请求完成
function streamFinished(input: PlaygroundInput, value: unknown): boolean {
  const type = pathValue(value, ['type'])
  switch (input.protocol) {
    case 'openai-chat':
      return false
    case 'openai-responses':
      return type === 'response.completed' || type === 'response.incomplete'
    case 'anthropic':
      return type === 'message_stop'
    case 'gemini': {
      const reason = pathValue(value, ['candidates', 0, 'finishReason'])
      return typeof reason === 'string' && reason !== ''
    }
    default:
      return false
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

  if (input.protocol === 'openai-videos') {
    return completeVideo(response, request.headers, signal)
  }
  if (input.mode === 'video') {
    return completeGeminiVideo(response, request.headers, signal)
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
  const chunk = responseChunk(input, value)
  chunk.media = pcmAudioWAV(chunk.media)
  return {
    status: response.status,
    chunk,
    raw: JSON.stringify(value, null, 2),
  }
}
