import { jsonBody } from '@/api'
import { useConsole } from '@/console'
import type { Model } from '@/types'
import { Capability, hasCode, isAgent, isOmniModel, toolCodes, type Tool } from './catalog'
import type { Protocol } from './protocols'
import type { AgentEnvironment, Part, RunSettings, Source, Support, Turn } from './types'

// GenerateRequest 是一次 Gemini API 生成请求的正文
export interface GenerateRequest {
  contents: { role: 'user' | 'model'; parts: Part[] }[]
  systemInstruction?: { parts: { text: string }[] }
  generationConfig: Record<string, unknown>
  safetySettings?: { category: string; threshold: string }[]
  tools?: Record<string, unknown>[]
  toolConfig?: { includeServerSideToolInvocations: boolean }
}

// toolPayloads 是各工具在 Gemini API tools 中的写法
export const toolPayloads: Partial<Record<Tool, Record<string, unknown>>> = {
  code_execution: { codeExecution: {} },
  google_search: { googleSearch: {} },
  url_context: { urlContext: {} },
  google_maps: { googleMaps: {} },
}

// parseJSON 解析编辑器中的 JSON 文本，空文本或无效 JSON 返回 undefined
function parseJSON(text: string): unknown {
  if (text.trim() === '') return undefined
  try {
    return JSON.parse(text) as unknown
  } catch {
    return undefined
  }
}

// requestContents 把对话轮次写为 contents：去掉思考正文、出错的空轮次与附件的显示字段，Omni 与官网一样不发送视频设置
function requestContents(turns: readonly Turn[], omni: boolean): GenerateRequest['contents'] {
  return turns
    .map((turn) => ({
      role: turn.role,
      parts: turn.parts
        .filter((part) => !(part.thought && part.text !== undefined && !part.thoughtSignature))
        .map((part) => (omni ? omitVideoSettings(apiPart(part)) : apiPart(part))),
    }))
    .filter((turn) => turn.parts.length > 0)
}

// omitVideoSettings 返回去掉视频截取、帧率与处理方式的片段
function omitVideoSettings({
  videoMetadata: _metadata,
  mediaProcessing: _mode,
  ...part
}: Part): Part {
  return part
}

// apiPart 返回去掉试用页显示字段的片段：已上传 Drive 的附件只发送文件引用，内联数据只用于本页预览，内联附件的文件名写为 inlineData.displayName
export function apiPart({
  fileName,
  fileExpires: _fileExpires,
  tokenCount: _tokenCount,
  ...part
}: Part): Part {
  if (part.fileData !== undefined && part.inlineData !== undefined) {
    const { inlineData: _preview, ...uploaded } = part
    return uploaded
  }
  if (fileName === undefined || part.inlineData === undefined) return part
  return { ...part, inlineData: { ...part.inlineData, displayName: fileName } }
}

// buildRequest 按模型能力把运行设置写为 Gemini API 请求，模型不支持的设置不发送，采样参数按所设值发送
export function buildRequest(
  settings: RunSettings,
  model: Model | undefined,
  turns: readonly Turn[],
): GenerateRequest {
  const config: Record<string, unknown> = {}
  if (settings.temperature !== null) config.temperature = settings.temperature
  if (settings.topP !== null) config.topP = settings.topP
  if (settings.topK !== null) config.topK = settings.topK
  if (hasCode(model, Capability.MaxOutputTokens) && settings.maxOutputTokens !== null)
    config.maxOutputTokens = settings.maxOutputTokens
  if (hasCode(model, Capability.StopSequences) && settings.stopSequences.length > 0)
    config.stopSequences = settings.stopSequences
  if (hasCode(model, Capability.MediaResolution) && settings.mediaResolution !== '')
    config.mediaResolution = settings.mediaResolution
  if (hasCode(model, Capability.ThinkingLevel)) {
    config.thinkingConfig = {
      includeThoughts: true,
      ...(settings.thinkingLevel === null ? {} : { thinkingLevel: settings.thinkingLevel }),
    }
  } else if (hasCode(model, Capability.ThinkingBudget)) {
    config.thinkingConfig = {
      includeThoughts: true,
      ...(settings.thinkingBudget === -1 ? {} : { thinkingBudget: settings.thinkingBudget }),
    }
  } else if (hasCode(model, Capability.Thinking)) {
    config.thinkingConfig = { includeThoughts: true }
  }
  if (hasCode(model, Capability.ImageRoute)) {
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
  const request: GenerateRequest = {
    contents: requestContents(turns, isOmniModel(model)),
    generationConfig: config,
  }
  if (settings.systemInstruction.trim() !== '' && hasCode(model, Capability.SystemInstruction)) {
    request.systemInstruction = { parts: [{ text: settings.systemInstruction }] }
  }
  if (hasCode(model, Capability.SafetySettings)) {
    request.safetySettings = Object.entries(settings.safety).map(([category, threshold]) => ({
      category,
      threshold,
    }))
  }
  if (tools.length > 0) request.tools = tools
  // Agent 模式要求返回搜索、网址读取等服务端工具步骤，按发生顺序显示
  if (enabled.includes('code_execution'))
    request.toolConfig = { includeServerSideToolInvocations: true }
  return request
}

// agentFields 返回 agent 请求的 agent_config 与 environment 字段，都不需要时为 undefined
export function agentFields(
  settings: RunSettings,
  model: Model | undefined,
): Record<string, unknown> | undefined {
  const config = agentConfig(settings, model)
  const environment = hasCode(model, Capability.RemoteEnvironment)
    ? agentEnvironment(settings.environment)
    : undefined
  if (config === undefined && environment === undefined) return undefined
  return {
    ...(config !== undefined && { agent_config: config }),
    ...(environment !== undefined && { environment }),
  }
}

// agentConfig 按官方 Interactions REST 写出 agent 的 agent_config：Antigravity 为所选模型与 token 上限，都未设置时不发送；Deep Research 按思考摘要、可视化与协作规划的能力码写出这三项；其余模型不发送
function agentConfig(
  settings: RunSettings,
  model: Model | undefined,
): Record<string, unknown> | undefined {
  if (model === undefined || !isAgent(model)) return undefined
  if (model.agent_model !== undefined) {
    if (settings.agentModel === '' && settings.maxTotalTokens === null) return undefined
    return {
      type: 'antigravity',
      ...(settings.agentModel !== '' && { model: settings.agentModel }),
      ...(settings.maxTotalTokens !== null && {
        max_total_tokens: String(settings.maxTotalTokens),
      }),
    }
  }
  const config: Record<string, unknown> = {}
  if (hasCode(model, Capability.ThinkingSummaries))
    config.thinking_summaries = settings.thinkingSummaries ? 'auto' : 'none'
  if (hasCode(model, Capability.Visualization))
    config.visualization = settings.visualization ? 'auto' : 'off'
  if (hasCode(model, Capability.CollaborativePlanning) && settings.collaborativePlanning)
    config.collaborative_planning = true
  return Object.keys(config).length > 0 ? { type: 'deep-research', ...config } : undefined
}

// agentEnvironment 按官方 Interactions REST 写出运行设置中的环境：已有环境与官网一样只发环境 ID，新环境带源或规则时为 remote 环境对象，没有规则时不发送 network，什么都没设置时不发送
function agentEnvironment(
  environment: AgentEnvironment,
): string | Record<string, unknown> | undefined {
  if (environment.type === 'existing') return environment.id.trim() || undefined
  const sources = environment.sources
  const allowlist = environment.rules
    .filter((rule) => rule.domain.trim() !== '')
    .map((rule) => {
      const headers = rule.headers.filter((header) => header.name.trim() !== '')
      return {
        domain: rule.domain.trim(),
        ...(headers.length > 0 && {
          transform: Object.fromEntries(
            headers.map((header) => [header.name.trim(), header.value]),
          ),
        }),
      }
    })
  if (sources.length === 0 && allowlist.length === 0) return undefined
  return {
    type: 'remote',
    ...(sources.length > 0 && {
      sources: sources.map((source) =>
        source.type === 'inline'
          ? { type: 'inline', target: source.target, content: source.content }
          : { type: source.type, source: source.source, target: source.target },
      ),
    }),
    ...(allowlist.length > 0 && { network: { allowlist } }),
  }
}

// continuation 与官网一样选取 Interactions 续接的起点：以 Interactions 发送且最后一个模型轮带交互 ID 时，以该 ID 续接并只发送其后的轮次，否则发送全部轮次
export function continuation(
  protocol: Protocol,
  turns: readonly Turn[],
): { previous: string | undefined; turns: readonly Turn[] } {
  const index = turns.map((turn) => turn.role).lastIndexOf('model')
  const previous = protocol === 'interactions' ? turns[index]?.interactionId : undefined
  return { previous, turns: previous === undefined ? turns : turns.slice(index + 1) }
}

// StreamUpdate 是一次流式事件带来的变化，interactionId 与 environmentId 是 Interactions 完成时的交互 ID 与远程环境 ID
export interface StreamUpdate {
  parts: Part[]
  interactionId?: string
  environmentId?: string
  finishReason?: string
  tokens?: number
  thoughtTokens?: number
  promptTokens?: number
  sources?: Source[]
  supports?: Support[]
  searchQueries?: string[]
}

// failureText 返回请求失败的说明：未能连接服务时为 unreachable，其余为 failed，其中的 {detail} 换成错误原文
export function failureText(error: unknown, unreachable: string, failed = '{detail}'): string {
  const detail = error instanceof Error ? error.message : String(error)
  return (error instanceof TypeError ? unreachable : failed).replace('{detail}', detail)
}

// apiKey 返回 Playground 请求使用的本服务 API key
export function apiKey(): string {
  return (useConsole().config.value?.proxy_api_key ?? '').split(',')[0]!.trim()
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
    body: jsonBody({
      generateContentRequest: { model: `models/${modelID}`, ...request },
    }),
  })
  if (!response.ok) throw new Error(await errorMessage(response))
  const value = (await response.json()) as { totalTokens?: number }
  return value.totalTokens ?? 0
}
