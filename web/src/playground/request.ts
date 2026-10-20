import { useConsole } from '@/console'
import type { Model } from '@/types'
import { hasCode, toolCodes, type Tool } from './catalog'
import type { Part, RunSettings, Source, Support, Turn } from './types'

// GenerateRequest 是一次 Gemini API 生成请求的正文
export interface GenerateRequest {
  contents: { role: 'user' | 'model'; parts: Part[] }[]
  systemInstruction?: { parts: { text: string }[] }
  generationConfig: Record<string, unknown>
  safetySettings?: { category: string; threshold: string }[]
  tools?: Record<string, unknown>[]
}

// toolPayloads 是各工具在 Gemini API tools 中的写法
const toolPayloads: Partial<Record<Tool, Record<string, unknown>>> = {
  code_execution: { codeExecution: {} },
  google_search: { googleSearch: {} },
  url_context: { urlContext: {} },
  google_maps: { googleMaps: {} },
}

// parseJSON 解析编辑器中的 JSON 文本，空文本或无效 JSON 返回 undefined
export function parseJSON(text: string): unknown {
  if (text.trim() === '') return undefined
  try {
    return JSON.parse(text) as unknown
  } catch {
    return undefined
  }
}

// requestContents 把对话轮次写为 contents：去掉思考正文与出错的空轮次
export function requestContents(turns: readonly Turn[]): GenerateRequest['contents'] {
  return turns
    .map((turn) => ({
      role: turn.role,
      parts: turn.parts.filter(
        (part) => !(part.thought && part.text !== undefined && !part.thoughtSignature),
      ),
    }))
    .filter((turn) => turn.parts.length > 0)
}

// buildRequest 按模型能力把运行设置写为 Gemini API 请求，模型不支持的设置不发送
export function buildRequest(
  settings: RunSettings,
  model: Model | undefined,
  turns: readonly Turn[],
): GenerateRequest {
  const config: Record<string, unknown> = {}
  if (hasCode(model, 62) && settings.temperature !== null) config.temperature = settings.temperature
  if (hasCode(model, 23) && settings.topP !== null) config.topP = settings.topP
  if (hasCode(model, 24) && settings.topK !== null) config.topK = settings.topK
  if (hasCode(model, 64) && settings.maxOutputTokens !== null)
    config.maxOutputTokens = settings.maxOutputTokens
  if (hasCode(model, 63) && settings.stopSequences.length > 0)
    config.stopSequences = settings.stopSequences
  if (hasCode(model, 43) && settings.mediaResolution !== '')
    config.mediaResolution = settings.mediaResolution
  if (hasCode(model, 52)) {
    config.thinkingConfig = {
      includeThoughts: true,
      ...(settings.thinkingLevel === null ? {} : { thinkingLevel: settings.thinkingLevel }),
    }
  } else if (hasCode(model, 35)) {
    config.thinkingConfig = {
      includeThoughts: true,
      ...(settings.thinkingBudget === -1 ? {} : { thinkingBudget: settings.thinkingBudget }),
    }
  } else if (hasCode(model, 25)) {
    config.thinkingConfig = { includeThoughts: true }
  }
  if (hasCode(model, 21)) {
    config.responseModalities = settings.imageOutput === 'IMAGE' ? ['IMAGE'] : ['IMAGE', 'TEXT']
    const imageConfig: Record<string, string> = {}
    if (model?.capabilities?.aspect_ratio && settings.aspectRatio !== '')
      imageConfig.aspectRatio = settings.aspectRatio
    if (model?.capabilities?.output_resolution && settings.imageSize !== '')
      imageConfig.imageSize = settings.imageSize
    if (Object.keys(imageConfig).length > 0) config.imageConfig = imageConfig
  }
  if (model?.capability_options?.video_tasks) {
    const video: Record<string, string> = {}
    if (settings.videoAspectRatio !== '') video.aspectRatio = settings.videoAspectRatio
    if (settings.videoDuration !== null) video.duration = `${settings.videoDuration}s`
    if (settings.videoResolution !== '') video.resolution = settings.videoResolution
    config.responseFormat = [{ video }]
    if (settings.videoTask !== '') config.videoConfig = { task: settings.videoTask }
  }
  const enabled = settings.tools.filter((tool) => hasCode(model, toolCodes[tool]))
  if (enabled.includes('structured_output')) {
    config.responseMimeType = 'application/json'
    const schema = parseJSON(settings.responseSchema)
    if (schema !== undefined) config.responseSchema = schema
  }
  const tools: Record<string, unknown>[] = []
  for (const tool of enabled) {
    const payload = toolPayloads[tool]
    if (payload) tools.push({ ...payload })
  }
  if (enabled.includes('image_search')) {
    const searchTypes = {
      ...(enabled.includes('google_search') ? { webSearch: {} } : {}),
      imageSearch: {},
    }
    const search = tools.find((tool) => 'googleSearch' in tool)
    if (search) search.googleSearch = { searchTypes }
    else tools.push({ googleSearch: { searchTypes } })
  }
  if (enabled.includes('function_declarations')) {
    const declarations = parseJSON(settings.functionDeclarations)
    if (Array.isArray(declarations) && declarations.length > 0)
      tools.push({ functionDeclarations: declarations })
  }
  const request: GenerateRequest = { contents: requestContents(turns), generationConfig: config }
  if (settings.systemInstruction.trim() !== '' && hasCode(model, 8)) {
    request.systemInstruction = { parts: [{ text: settings.systemInstruction }] }
  }
  if (hasCode(model, 28)) {
    request.safetySettings = Object.entries(settings.safety).map(([category, threshold]) => ({
      category,
      threshold,
    }))
  }
  if (tools.length > 0) request.tools = tools
  return request
}

// StreamUpdate 是一次流式事件带来的变化
export interface StreamUpdate {
  parts: Part[]
  finishReason?: string
  tokens?: number
  thoughtTokens?: number
  promptTokens?: number
  sources?: Source[]
  supports?: Support[]
  searchQueries?: string[]
}

// StreamError 是请求或流中返回的错误
export class StreamError extends Error {
  status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'StreamError'
    this.status = status
  }
}

// apiKey 返回 Playground 请求使用的本服务 API key
export function apiKey(): string {
  return (useConsole().config.value?.proxy_api_key ?? '').split(',')[0]?.trim() ?? ''
}

// errorMessage 读取 Gemini API 错误正文中的说明
export async function errorMessage(response: Response): Promise<string> {
  const text = await response.text()
  try {
    const value = JSON.parse(text) as { error?: { message?: string } }
    return value.error?.message ?? text
  } catch {
    return text || response.statusText
  }
}

// GeminiChunk 是 streamGenerateContent 的一个事件
interface GeminiChunk {
  error?: { code?: number; message?: string }
  candidates?: {
    content?: { parts?: Part[] }
    finishReason?: string
    groundingMetadata?: {
      groundingChunks?: {
        web?: { uri?: string; title?: string }
        maps?: { uri?: string; title?: string }
      }[]
      groundingSupports?: { segment?: { endIndex?: number }; groundingChunkIndices?: number[] }[]
      webSearchQueries?: string[]
    }
  }[]
  promptFeedback?: { blockReason?: string }
  usageMetadata?: {
    promptTokenCount?: number
    candidatesTokenCount?: number
    thoughtsTokenCount?: number
  }
}

// streamUpdate 提取 generateContent 事件或响应中的片段、结束原因、用量与依据来源
export function streamUpdate(raw: unknown): StreamUpdate {
  const value = raw as GeminiChunk
  const candidate = value.candidates?.[0]
  const update: StreamUpdate = { parts: candidate?.content?.parts ?? [] }
  const reason = candidate?.finishReason ?? value.promptFeedback?.blockReason
  if (reason) update.finishReason = reason
  const usage = value.usageMetadata
  if (usage?.candidatesTokenCount !== undefined || usage?.thoughtsTokenCount !== undefined) {
    update.tokens = (usage.candidatesTokenCount ?? 0) + (usage.thoughtsTokenCount ?? 0)
  }
  if (usage?.thoughtsTokenCount !== undefined) update.thoughtTokens = usage.thoughtsTokenCount
  if (usage?.promptTokenCount !== undefined) update.promptTokens = usage.promptTokenCount
  const grounding = candidate?.groundingMetadata
  if (grounding?.groundingChunks) {
    update.sources = grounding.groundingChunks
      .map((chunk) => chunk.web ?? chunk.maps)
      .map((item) => ({ uri: item?.uri ?? '', title: item?.title ?? item?.uri ?? '' }))
  }
  if (grounding?.groundingSupports) {
    update.supports = grounding.groundingSupports
      .filter((support) => support.segment?.endIndex !== undefined)
      .map((support) => ({
        end: support.segment!.endIndex!,
        chunks: support.groundingChunkIndices ?? [],
      }))
  }
  if (grounding?.webSearchQueries) update.searchQueries = grounding.webSearchQueries
  return update
}

// appendParts 把新片段并入轮次：相邻同类文本合并，思考签名附到前一片段
export function appendParts(parts: Part[], incoming: readonly Part[]): void {
  for (const part of incoming) {
    const last = parts.at(-1)
    if (
      part.text !== undefined &&
      last?.text !== undefined &&
      Boolean(last.thought) === Boolean(part.thought)
    ) {
      last.text += part.text
      if (part.thoughtSignature) last.thoughtSignature = part.thoughtSignature
      continue
    }
    if (part.text === '' && part.thoughtSignature && last) {
      last.thoughtSignature = part.thoughtSignature
      continue
    }
    parts.push({ ...part })
  }
}

// countTokens 调用本服务 countTokens 计算当前对话的输入 token
export async function countTokens(
  modelID: string,
  apiKey: string,
  request: GenerateRequest,
  signal: AbortSignal,
): Promise<number> {
  const headers = new Headers({ 'Content-Type': 'application/json' })
  if (apiKey !== '') headers.set('x-goog-api-key', apiKey)
  const response = await fetch(`/v1beta/models/${encodeURIComponent(modelID)}:countTokens`, {
    method: 'POST',
    headers,
    signal,
    body: JSON.stringify({
      generateContentRequest: { model: `models/${modelID}`, ...request },
    }),
  })
  if (!response.ok) throw new StreamError(await errorMessage(response), response.status)
  const value = (await response.json()) as { totalTokens?: number }
  return value.totalTokens ?? 0
}
