import { EventSourceParserStream } from 'eventsource-parser/stream'
import { jsonBody } from '@/api'
import type { Model } from '@/types'
import { base64Bytes } from './bytes'
import { isAgent } from './catalog'
import { errorMessage, streamUpdate, type GenerateRequest, type StreamUpdate } from './request'
import type { Part, Source, Support } from './types'

// Protocol 是 API 试用页发送请求使用的公开协议
export type Protocol =
  | 'gemini'
  | 'openai-chat'
  | 'openai-responses'
  | 'anthropic'
  | 'openai-images'
  | 'interactions'

// protocolOptions 是协议下拉的选项，Gemini API 与官网一致为默认，OpenAI Images 只用于图片模型，Gemini Interactions 只用于 agent
export const protocolOptions: { value: Protocol; label: string }[] = [
  { value: 'gemini', label: 'Gemini API' },
  { value: 'openai-chat', label: 'OpenAI Chat' },
  { value: 'openai-responses', label: 'OpenAI Responses' },
  { value: 'anthropic', label: 'Anthropic Messages' },
  { value: 'openai-images', label: 'OpenAI Images' },
  { value: 'interactions', label: 'Gemini Interactions' },
]

// protocolsFor 返回模型可用的协议选项：agent 与官网一样只经 Interactions 调用
export function protocolsFor(model: Model | undefined): typeof protocolOptions {
  if (isAgent(model)) return protocolOptions.filter((item) => item.value === 'interactions')
  return protocolOptions.filter(
    (item) =>
      item.value !== 'interactions' &&
      (item.value !== 'openai-images' || model?.capabilities?.image_route === true),
  )
}

// ProtocolSetting 是只有部分协议能表达的运行设置
export type ProtocolSetting =
  | 'topK'
  | 'stopSequences'
  | 'safety'
  | 'mediaResolution'
  | 'structuredOutput'
  | 'thinkingBudget'
  | 'videoSettings'

// unsupported 是各协议无法表达的运行设置
const unsupported: Record<Protocol, readonly ProtocolSetting[]> = {
  gemini: [],
  'openai-chat': ['topK', 'safety', 'mediaResolution', 'videoSettings'],
  'openai-responses': [
    'topK',
    'stopSequences',
    'safety',
    'mediaResolution',
    'thinkingBudget',
    'videoSettings',
  ],
  anthropic: ['safety', 'mediaResolution', 'structuredOutput', 'videoSettings'],
  'openai-images': [
    'topK',
    'stopSequences',
    'safety',
    'mediaResolution',
    'structuredOutput',
    'thinkingBudget',
    'videoSettings',
  ],
  interactions: [
    'topK',
    'stopSequences',
    'safety',
    'mediaResolution',
    'structuredOutput',
    'thinkingBudget',
    'videoSettings',
  ],
}

// protocolSupports 判断协议能否表达某项运行设置
export function protocolSupports(protocol: Protocol, setting: ProtocolSetting): boolean {
  return !unsupported[protocol].includes(setting)
}

// builtinTools 是 Gemini 内置工具在 OpenAI 与 Anthropic 协议中的写法
const builtinTools: Record<string, { openai: string; anthropic: { type: string; name: string } }> =
  {
    googleSearch: {
      openai: 'web_search',
      anthropic: { type: 'web_search_20250305', name: 'web_search' },
    },
    codeExecution: {
      openai: 'code_interpreter',
      anthropic: { type: 'code_execution_20250825', name: 'code_execution' },
    },
    urlContext: {
      openai: 'url_context',
      anthropic: { type: 'web_fetch_20250910', name: 'web_fetch' },
    },
    googleMaps: { openai: 'google_maps', anthropic: { type: 'google_maps', name: 'google_maps' } },
    imageSearch: {
      openai: 'image_search',
      anthropic: { type: 'image_search', name: 'image_search' },
    },
  }

// FunctionDeclaration 是 Gemini 函数声明
interface FunctionDeclaration {
  name: string
  description?: string
  parameters?: unknown
}

// requestTools 拆出请求中的内置工具名与函数声明
function requestTools(request: GenerateRequest): {
  builtin: string[]
  functions: FunctionDeclaration[]
} {
  const builtin: string[] = []
  const functions: FunctionDeclaration[] = []
  for (const tool of request.tools ?? []) {
    const declarations = tool.functionDeclarations as FunctionDeclaration[] | undefined
    if (declarations) functions.push(...declarations)
    else if ('googleSearch' in tool) builtin.push(...searchTools(record(tool.googleSearch)))
    else builtin.push(...Object.keys(tool).filter((key) => key in builtinTools))
  }
  return { builtin, functions }
}

// searchTools 按 searchTypes 返回 Google 搜索对应的 web 与图片搜索工具，未指定时为 web 搜索
function searchTools(search: Record<string, unknown>): string[] {
  const types = record(search.searchTypes)
  if (!('imageSearch' in types)) return ['googleSearch']
  return 'webSearch' in types ? ['googleSearch', 'imageSearch'] : ['imageSearch']
}

// systemText 返回请求的系统指令文本
function systemText(request: GenerateRequest): string {
  return request.systemInstruction?.parts.map((part) => part.text).join('') ?? ''
}

// dataURL 把内联数据写为 data URL
export function dataURL(data: { mimeType: string; data: string }): string {
  return `data:${data.mimeType};base64,${data.data}`
}

// fileName 返回没有文件名的附件在协议请求中的文件名
function fileName(mimeType: string, index: number): string {
  return `file_${index}.${mimeType.split('/')[1]?.split(';')[0] ?? 'bin'}`
}

// callID 返回函数调用与结果配对的 ID
function callID(part: { id?: string; name: string }): string {
  return part.id ?? part.name
}

// thinkingEffort 返回请求中的思考档位，转为 OpenAI 与 Anthropic 的小写写法
function thinkingEffort(request: GenerateRequest): string | undefined {
  const level = (request.generationConfig.thinkingConfig as { thinkingLevel?: string } | undefined)
    ?.thinkingLevel
  return level?.toLowerCase()
}

// thinkingBudget 返回请求中的思考预算，未设置时为 undefined
function thinkingBudget(request: GenerateRequest): number | undefined {
  return (request.generationConfig.thinkingConfig as { thinkingBudget?: number } | undefined)
    ?.thinkingBudget
}

// responseSchema 返回结构化输出的 JSON Schema
function responseSchema(request: GenerateRequest): unknown {
  const config = request.generationConfig
  if (config.responseMimeType !== 'application/json') return undefined
  return config.responseSchema ?? {}
}

// chatContent 把用户片段写为 Chat Completions 的内容数组
function chatContent(parts: readonly Part[]): Record<string, unknown>[] {
  return parts.flatMap((part, index): Record<string, unknown>[] => {
    if (part.text !== undefined && !part.thought) return [{ type: 'text', text: part.text }]
    const inline = part.inlineData
    if (inline?.mimeType.startsWith('image/'))
      return [{ type: 'image_url', image_url: { url: dataURL(inline) } }]
    if (inline?.mimeType.startsWith('audio/'))
      return [
        {
          type: 'input_audio',
          input_audio: { data: inline.data, format: inline.mimeType.split('/')[1] },
        },
      ]
    if (inline)
      return [
        {
          type: 'file',
          file: {
            file_data: dataURL(inline),
            filename: inline.displayName ?? fileName(inline.mimeType, index),
          },
        },
      ]
    if (part.fileData) return [{ type: 'image_url', image_url: { url: part.fileData.fileUri } }]
    return []
  })
}

// chatRequest 把 Gemini 请求换为 Chat Completions 请求
export function chatRequest(
  model: string,
  request: GenerateRequest,
  stream: boolean,
): Record<string, unknown> {
  const messages: Record<string, unknown>[] = []
  const system = systemText(request)
  if (system !== '') messages.push({ role: 'system', content: system })
  for (const content of request.contents) {
    const responses = content.parts.filter((part) => part.functionResponse)
    for (const part of responses)
      messages.push({
        role: 'tool',
        tool_call_id: callID(part.functionResponse!),
        content: JSON.stringify(part.functionResponse!.response),
      })
    if (content.role === 'user') {
      const blocks = chatContent(content.parts)
      if (blocks.length === 0) continue
      const text = blocks.every((block) => block.type === 'text')
      messages.push({
        role: 'user',
        content: text ? blocks.map((block) => block.text).join('') : blocks,
      })
      continue
    }
    const text = content.parts
      .filter((part) => part.text !== undefined && !part.thought)
      .map((part) => part.text)
      .join('')
    const calls = content.parts
      .filter((part) => part.functionCall)
      .map((part) => ({
        id: callID(part.functionCall!),
        type: 'function',
        function: {
          name: part.functionCall!.name,
          arguments: JSON.stringify(part.functionCall!.args ?? {}),
        },
      }))
    if (text === '' && calls.length === 0) continue
    messages.push({
      role: 'assistant',
      content: text === '' ? null : text,
      ...(calls.length > 0 ? { tool_calls: calls } : {}),
    })
  }
  const config = request.generationConfig
  const body: Record<string, unknown> = { model, messages, stream }
  if (stream) body.stream_options = { include_usage: true }
  if (config.temperature !== undefined) body.temperature = config.temperature
  if (config.topP !== undefined) body.top_p = config.topP
  if (config.maxOutputTokens !== undefined) body.max_completion_tokens = config.maxOutputTokens
  if (config.stopSequences !== undefined) body.stop = config.stopSequences
  const effort = thinkingEffort(request)
  if (effort) body.reasoning_effort = effort
  const budget = thinkingBudget(request)
  if (budget !== undefined)
    body.extra_body = { google: { thinking_config: { thinking_budget: budget } } }
  const schema = responseSchema(request)
  if (schema !== undefined)
    body.response_format = { type: 'json_schema', json_schema: { name: 'response', schema } }
  const { builtin, functions } = requestTools(request)
  const tools = [
    ...builtin.map((name) => ({ type: builtinTools[name]!.openai })),
    ...functions.map((declaration) => ({ type: 'function', function: declaration })),
  ]
  if (tools.length > 0) body.tools = tools
  return body
}

// responsesInput 把对话轮次写为 Responses 输入项
function responsesInput(request: GenerateRequest): Record<string, unknown>[] {
  const items: Record<string, unknown>[] = []
  for (const content of request.contents) {
    if (content.role === 'user') {
      const blocks = content.parts.flatMap((part, index): Record<string, unknown>[] => {
        if (part.text !== undefined && !part.thought)
          return [{ type: 'input_text', text: part.text }]
        const inline = part.inlineData
        if (inline?.mimeType.startsWith('image/'))
          return [{ type: 'input_image', image_url: dataURL(inline) }]
        if (inline)
          return [
            {
              type: 'input_file',
              file_data: dataURL(inline),
              filename: inline.displayName ?? fileName(inline.mimeType, index),
            },
          ]
        if (part.fileData) return [{ type: 'input_image', image_url: part.fileData.fileUri }]
        return []
      })
      if (blocks.length > 0) items.push({ role: 'user', content: blocks })
    } else {
      const text = content.parts
        .filter((part) => part.text !== undefined && !part.thought)
        .map((part) => part.text)
        .join('')
      if (text !== '') items.push({ role: 'assistant', content: [{ type: 'output_text', text }] })
    }
    for (const part of content.parts) {
      if (part.functionCall)
        items.push({
          type: 'function_call',
          call_id: callID(part.functionCall),
          name: part.functionCall.name,
          arguments: JSON.stringify(part.functionCall.args ?? {}),
        })
      if (part.functionResponse)
        items.push({
          type: 'function_call_output',
          call_id: callID(part.functionResponse),
          output: JSON.stringify(part.functionResponse.response),
        })
    }
  }
  return items
}

// responsesRequest 把 Gemini 请求换为 Responses 请求，思考摘要随请求开启
export function responsesRequest(
  model: string,
  request: GenerateRequest,
  stream: boolean,
): Record<string, unknown> {
  const config = request.generationConfig
  const body: Record<string, unknown> = { model, input: responsesInput(request), stream }
  const system = systemText(request)
  if (system !== '') body.instructions = system
  if (config.temperature !== undefined) body.temperature = config.temperature
  if (config.topP !== undefined) body.top_p = config.topP
  if (config.maxOutputTokens !== undefined) body.max_output_tokens = config.maxOutputTokens
  if (config.thinkingConfig !== undefined) {
    const effort = thinkingEffort(request)
    body.reasoning = { ...(effort ? { effort } : {}), summary: 'auto' }
  }
  const schema = responseSchema(request)
  if (schema !== undefined)
    body.text = { format: { type: 'json_schema', name: 'response', schema } }
  const { builtin, functions } = requestTools(request)
  const tools = [
    ...builtin.map((name) => ({ type: builtinTools[name]!.openai })),
    ...functions.map((declaration) => ({ type: 'function', ...declaration })),
  ]
  if (tools.length > 0) body.tools = tools
  return body
}

// anthropicContent 把片段写为 Anthropic 内容块
function anthropicContent(parts: readonly Part[]): Record<string, unknown>[] {
  return parts.flatMap((part): Record<string, unknown>[] => {
    if (part.text !== undefined && !part.thought) return [{ type: 'text', text: part.text }]
    const inline = part.inlineData
    if (inline)
      return [
        {
          type: inline.mimeType.startsWith('image/') ? 'image' : 'document',
          source: { type: 'base64', media_type: inline.mimeType, data: inline.data },
          ...(inline.displayName !== undefined && !inline.mimeType.startsWith('image/')
            ? { title: inline.displayName }
            : {}),
        },
      ]
    if (part.fileData)
      return [{ type: 'image', source: { type: 'url', url: part.fileData.fileUri } }]
    if (part.functionCall)
      return [
        {
          type: 'tool_use',
          id: callID(part.functionCall),
          name: part.functionCall.name,
          input: part.functionCall.args ?? {},
        },
      ]
    if (part.functionResponse)
      return [
        {
          type: 'tool_result',
          tool_use_id: callID(part.functionResponse),
          content: JSON.stringify(part.functionResponse.response),
        },
      ]
    return []
  })
}

// anthropicRequest 把 Gemini 请求换为 Messages 请求，max_tokens 未设置时取模型输出上限
function anthropicRequest(
  model: Model | undefined,
  modelID: string,
  request: GenerateRequest,
  stream: boolean,
): Record<string, unknown> {
  const config = request.generationConfig
  const messages = request.contents
    .map((content) => ({
      role: content.role === 'model' ? 'assistant' : 'user',
      content: anthropicContent(content.parts),
    }))
    .filter((message) => message.content.length > 0)
  const body: Record<string, unknown> = {
    model: modelID,
    max_tokens: config.maxOutputTokens ?? model?.output_token_limit ?? 8192,
    messages,
    stream,
  }
  const system = systemText(request)
  if (system !== '') body.system = system
  if (config.temperature !== undefined) body.temperature = config.temperature
  if (config.topP !== undefined) body.top_p = config.topP
  if (config.topK !== undefined) body.top_k = config.topK
  if (config.stopSequences !== undefined) body.stop_sequences = config.stopSequences
  if (config.thinkingConfig !== undefined) {
    const budget = thinkingBudget(request)
    if (budget === 0) body.thinking = { type: 'disabled' }
    else if (budget !== undefined) body.thinking = { type: 'enabled', budget_tokens: budget }
    else body.thinking = { type: 'adaptive' }
    const effort = thinkingEffort(request)
    if (effort) body.output_config = { effort }
  }
  const { builtin, functions } = requestTools(request)
  const tools = [
    ...builtin.map((name) => builtinTools[name]!.anthropic),
    ...functions.map((declaration) => ({
      name: declaration.name,
      description: declaration.description,
      input_schema: declaration.parameters ?? { type: 'object', properties: {} },
    })),
  ]
  if (tools.length > 0) body.tools = tools
  return body
}

// finishReasons 把各协议的结束原因换为 Gemini 写法
const finishReasons: Record<string, string> = {
  stop: 'STOP',
  end_turn: 'STOP',
  stop_sequence: 'STOP',
  tool_calls: 'STOP',
  tool_use: 'STOP',
  pause_turn: 'STOP',
  completed: 'STOP',
  requires_action: 'STOP',
  length: 'MAX_TOKENS',
  max_tokens: 'MAX_TOKENS',
  max_output_tokens: 'MAX_TOKENS',
  content_filter: 'SAFETY',
  refusal: 'SAFETY',
}

// finishReason 返回结束原因的 Gemini 写法，未知值按原样大写
function finishReason(reason: string): string {
  return finishReasons[reason] ?? reason.toUpperCase()
}

// parseArguments 解析函数调用参数，无法解析时保留原文
function parseArguments(text: string): Record<string, unknown> {
  if (text.trim() === '') return {}
  try {
    return JSON.parse(text) as Record<string, unknown>
  } catch {
    return { arguments: text }
  }
}

// record 把未知值视为对象读取
function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' ? (value as Record<string, unknown>) : {}
}

// stringOf 返回字符串字段，其他类型为空字符串
function stringOf(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

// errorOf 读取事件中的错误
function errorOf(value: Record<string, unknown>): string | undefined {
  const error = value.error ?? record(value.response).error
  if (error === undefined || error === null) return undefined
  const message = record(error).message
  return typeof message === 'string' ? message : JSON.stringify(error)
}

// Decoder 把一种协议的事件或完整响应逐步换为片段变化
interface Decoder {
  event: (value: Record<string, unknown>) => StreamUpdate
  done: (value: Record<string, unknown>) => boolean
}

// chatDecoder 解析 Chat Completions：正文、思考、按序号拼接的工具调用与用量
function chatDecoder(): Decoder {
  const calls = new Map<number, { id: string; name: string; arguments: string }>()
  return {
    event(value) {
      const choice = record((value.choices as unknown[] | undefined)?.[0])
      const message = record(choice.delta ?? choice.message)
      const update: StreamUpdate = { parts: [] }
      if (typeof message.reasoning_content === 'string' && message.reasoning_content !== '')
        update.parts.push({ text: message.reasoning_content, thought: true })
      if (typeof message.content === 'string' && message.content !== '')
        update.parts.push({ text: message.content })
      for (const raw of (message.tool_calls as unknown[] | undefined) ?? []) {
        const call = record(raw)
        const index = typeof call.index === 'number' ? call.index : calls.size
        const fn = record(call.function)
        const current = calls.get(index) ?? { id: '', name: '', arguments: '' }
        if (typeof call.id === 'string') current.id = call.id
        if (typeof fn.name === 'string') current.name += fn.name
        if (typeof fn.arguments === 'string') current.arguments += fn.arguments
        calls.set(index, current)
      }
      if (typeof choice.finish_reason === 'string') {
        update.finishReason = finishReason(choice.finish_reason)
        for (const call of calls.values())
          update.parts.push({
            functionCall: {
              name: call.name,
              args: parseArguments(call.arguments),
              ...(call.id ? { id: call.id } : {}),
            },
          })
        calls.clear()
      }
      const usage = record(value.usage)
      if (typeof usage.completion_tokens === 'number') update.tokens = usage.completion_tokens
      const reasoning = record(usage.completion_tokens_details).reasoning_tokens
      if (typeof reasoning === 'number') update.thoughtTokens = reasoning
      if (typeof usage.prompt_tokens === 'number') update.promptTokens = usage.prompt_tokens
      const cached = record(usage.prompt_tokens_details).cached_tokens
      if (typeof cached === 'number') update.cachedTokens = cached
      return update
    },
    done: () => false,
  }
}

// responsesItem 把 Responses 输出项换为片段
function responsesItem(item: Record<string, unknown>): Part[] {
  if (item.type === 'function_call')
    return [
      {
        functionCall: {
          name: stringOf(item.name),
          args: parseArguments(stringOf(item.arguments)),
          id: stringOf(item.call_id ?? item.id),
        },
      },
    ]
  if (item.type === 'reasoning')
    return ((item.summary as unknown[] | undefined) ?? [])
      .map((summary) => record(summary).text)
      .filter((text): text is string => typeof text === 'string' && text !== '')
      .map((text) => ({ text, thought: true }))
  if (item.type === 'message')
    return ((item.content as unknown[] | undefined) ?? [])
      .map((content) => record(content).text)
      .filter((text): text is string => typeof text === 'string' && text !== '')
      .map((text) => ({ text }))
  if (item.type === 'image_generation_call' && typeof item.result === 'string')
    return [{ inlineData: { mimeType: 'image/png', data: item.result } }]
  if (item.type === 'code_interpreter_call')
    return [
      { executableCode: { language: 'PYTHON', code: stringOf(item.code) } },
      ...responsesCodeResult(item),
    ]
  if (item.type === 'web_search_call') {
    const action = record(item.action)
    const id = stringOf(item.id)
    if (action.type === 'open_page')
      return urlContextStep(id, stringOf(action.url), item.status === 'completed')
    const queries = Array.isArray(action.queries) ? action.queries : [action.query]
    return [
      { toolCall: { toolType: 'GOOGLE_SEARCH_WEB', args: { queries }, id } },
      { toolResponse: { toolType: 'GOOGLE_SEARCH_WEB', response: {}, id } },
    ]
  }
  return []
}

// urlContextStep 返回一次网址读取的调用与结果片段，结果写法与 Gemini API 的 url_metadata 相同
function urlContextStep(id: string, url: string, retrieved: boolean): Part[] {
  return [
    { toolCall: { toolType: 'URL_CONTEXT', args: { urls: [url] }, id } },
    {
      toolResponse: {
        toolType: 'URL_CONTEXT',
        response: interactionToolResponse('URL_CONTEXT', [
          { url, status: retrieved ? 'success' : 'error' },
        ]),
        id,
      },
    },
  ]
}

// responsesCodeResult 把代码解释器调用的状态与日志换为代码执行结果，调用未结束时没有结果
function responsesCodeResult(item: Record<string, unknown>): Part[] {
  if (item.status !== 'completed' && item.status !== 'failed') return []
  const output = ((item.outputs as unknown[] | undefined) ?? [])
    .map((entry) => stringOf(record(entry).logs))
    .join('')
  const outcome = item.status === 'completed' ? 'OUTCOME_OK' : 'OUTCOME_FAILED'
  return [{ codeExecutionResult: output === '' ? { outcome } : { outcome, output } }]
}

// responsesUsage 把 Responses 完成对象中的用量与结束原因写入变化
function responsesUsage(response: Record<string, unknown>, update: StreamUpdate): void {
  const usage = record(response.usage)
  if (typeof usage.output_tokens === 'number') update.tokens = usage.output_tokens
  const reasoning = record(usage.output_tokens_details).reasoning_tokens
  if (typeof reasoning === 'number') update.thoughtTokens = reasoning
  if (typeof usage.input_tokens === 'number') update.promptTokens = usage.input_tokens
  const cached = record(usage.input_tokens_details).cached_tokens
  if (typeof cached === 'number') update.cachedTokens = cached
  const incomplete = record(response.incomplete_details).reason
  update.finishReason =
    typeof incomplete === 'string'
      ? finishReason(incomplete)
      : finishReason(String(response.status))
}

// responsesDecoder 解析 Responses：流式取正文与思考增量、写完的代码与完成的输出项，完整响应读取全部输出项
function responsesDecoder(): Decoder {
  return {
    event(value) {
      const update: StreamUpdate = { parts: [] }
      const type = typeof value.type === 'string' ? value.type : ''
      const delta = typeof value.delta === 'string' ? value.delta : ''
      if (type === 'response.output_text.delta') update.parts.push({ text: delta })
      else if (type === 'response.reasoning_summary_text.delta')
        update.parts.push({ text: delta, thought: true })
      else if (type === 'response.code_interpreter_call_code.done')
        update.parts.push({ executableCode: { language: 'PYTHON', code: stringOf(value.code) } })
      else if (type === 'response.output_item.done') {
        const item = record(value.item)
        if (item.type === 'code_interpreter_call') update.parts.push(...responsesCodeResult(item))
        else if (
          item.type === 'function_call' ||
          item.type === 'image_generation_call' ||
          item.type === 'web_search_call'
        )
          update.parts.push(...responsesItem(item))
      } else if (type === 'response.completed' || type === 'response.incomplete')
        responsesUsage(record(value.response), update)
      else if (type === '') {
        for (const item of (value.output as unknown[] | undefined) ?? [])
          update.parts.push(...responsesItem(record(item)))
        responsesUsage(value, update)
      }
      return update
    },
    done: (value) => value.type === 'response.completed' || value.type === 'response.incomplete',
  }
}

// anthropicServerTool 把服务端工具调用块换为步骤：代码执行为可执行代码，网页搜索为搜索调用，网页读取为网址读取调用
function anthropicServerTool(name: string, id: string, input: Record<string, unknown>): Part[] {
  if (name === 'code_execution')
    return [{ executableCode: { language: 'PYTHON', code: stringOf(input.code) } }]
  if (name === 'web_search')
    return [{ toolCall: { toolType: 'GOOGLE_SEARCH_WEB', args: { queries: [input.query] }, id } }]
  if (name === 'web_fetch')
    return [{ toolCall: { toolType: 'URL_CONTEXT', args: { urls: [stringOf(input.url)] }, id } }]
  return []
}

// anthropicCodeResult 把代码执行结果块换为代码执行结果：返回码非零为失败并取 stderr，执行超时错误为超时
function anthropicCodeResult(
  content: Record<string, unknown>,
): NonNullable<Part['codeExecutionResult']> {
  if (content.type === 'code_execution_tool_result_error')
    return {
      outcome:
        content.error_code === 'execution_time_exceeded'
          ? 'OUTCOME_DEADLINE_EXCEEDED'
          : 'OUTCOME_FAILED',
    }
  const ok = content.return_code === 0
  const output = stringOf(ok ? content.stdout : content.stderr)
  const outcome = ok ? 'OUTCOME_OK' : 'OUTCOME_FAILED'
  return output === '' ? { outcome } : { outcome, output }
}

// anthropicDecoder 解析 Messages：按内容块拼接正文、思考、工具输入与服务端工具步骤
function anthropicDecoder(): Decoder {
  const blocks = new Map<number, { type: string; name: string; id: string; input: string }>()
  // fetches 是网页读取调用的网址，读取失败的结果块只带调用 ID
  const fetches = new Map<string, string>()
  let promptTokens: number | undefined
  let cachedTokens: number | undefined
  const serverTool = (name: string, id: string, input: Record<string, unknown>): Part[] => {
    if (name === 'web_fetch') fetches.set(id, stringOf(input.url))
    return anthropicServerTool(name, id, input)
  }
  const block = (raw: unknown): Part[] => {
    const value = record(raw)
    if (value.type === 'text' && typeof value.text === 'string' && value.text !== '')
      return [{ text: value.text }]
    if (value.type === 'thinking' && typeof value.thinking === 'string' && value.thinking !== '')
      return [{ text: value.thinking, thought: true }]
    if (value.type === 'tool_use')
      return [
        {
          functionCall: {
            name: stringOf(value.name),
            args: record(value.input),
            id: stringOf(value.id),
          },
        },
      ]
    if (value.type === 'server_tool_use')
      return serverTool(stringOf(value.name), stringOf(value.id), record(value.input))
    if (value.type === 'code_execution_tool_result')
      return [{ codeExecutionResult: anthropicCodeResult(record(value.content)) }]
    if (value.type === 'web_search_tool_result') {
      const sources = ((value.content as unknown[] | undefined) ?? []).map((result) => ({
        uri: stringOf(record(result).url),
        title: stringOf(record(result).title),
      }))
      const id = stringOf(value.tool_use_id)
      return [{ toolResponse: { toolType: 'GOOGLE_SEARCH_WEB', response: { sources }, id } }]
    }
    if (value.type === 'web_fetch_tool_result') {
      const id = stringOf(value.tool_use_id)
      const content = record(value.content)
      const retrieved = content.type === 'web_fetch_result'
      const url = retrieved ? stringOf(content.url) : (fetches.get(id) ?? '')
      return urlContextStep(id, url, retrieved).slice(1)
    }
    return []
  }
  return {
    event(value) {
      const update: StreamUpdate = { parts: [] }
      const usage = record(value.usage ?? record(value.message).usage)
      if (typeof usage.cache_read_input_tokens === 'number')
        cachedTokens = usage.cache_read_input_tokens
      if (typeof usage.input_tokens === 'number')
        promptTokens = usage.input_tokens + (cachedTokens ?? 0)
      if (promptTokens !== undefined) update.promptTokens = promptTokens
      if (cachedTokens !== undefined) update.cachedTokens = cachedTokens
      if (typeof usage.output_tokens === 'number') update.tokens = usage.output_tokens
      switch (value.type) {
        case 'content_block_start': {
          const start = record(value.content_block)
          const input = record(start.input)
          if (start.type === 'tool_use' || start.type === 'server_tool_use')
            blocks.set(Number(value.index), {
              type: start.type,
              name: stringOf(start.name),
              id: stringOf(start.id),
              input: Object.keys(input).length > 0 ? JSON.stringify(input) : '',
            })
          else update.parts.push(...block(start))
          break
        }
        case 'content_block_delta': {
          const delta = record(value.delta)
          if (delta.type === 'text_delta') update.parts.push({ text: stringOf(delta.text) })
          if (delta.type === 'thinking_delta')
            update.parts.push({ text: stringOf(delta.thinking), thought: true })
          if (delta.type === 'input_json_delta') {
            const tool = blocks.get(Number(value.index))
            if (tool) tool.input += stringOf(delta.partial_json)
          }
          break
        }
        case 'content_block_stop': {
          const tool = blocks.get(Number(value.index))
          if (tool?.type === 'server_tool_use')
            update.parts.push(...serverTool(tool.name, tool.id, parseArguments(tool.input)))
          else if (tool)
            update.parts.push({
              functionCall: { name: tool.name, args: parseArguments(tool.input), id: tool.id },
            })
          blocks.delete(Number(value.index))
          break
        }
        case 'message_delta': {
          const reason = record(value.delta).stop_reason
          if (typeof reason === 'string') update.finishReason = finishReason(reason)
          break
        }
        case 'message': {
          for (const item of (value.content as unknown[] | undefined) ?? [])
            update.parts.push(...block(item))
          if (typeof value.stop_reason === 'string')
            update.finishReason = finishReason(value.stop_reason)
          break
        }
      }
      return update
    },
    done: (value) => value.type === 'message_stop',
  }
}

// geminiDecoder 解析 generateContent 的事件或完整响应
function geminiDecoder(): Decoder {
  return {
    event: (value) => streamUpdate(value),
    done: () => false,
  }
}

// interactionToolSteps 是服务端工具类型对应的 Interactions 调用与结果步骤类型
const interactionToolSteps: Record<string, [string, string]> = {
  GOOGLE_SEARCH_WEB: ['google_search_call', 'google_search_result'],
  GOOGLE_SEARCH_IMAGE: ['google_search_call', 'google_search_result'],
  URL_CONTEXT: ['url_context_call', 'url_context_result'],
  GOOGLE_MAPS: ['google_maps_call', 'google_maps_result'],
  FILE_SEARCH: ['file_search_call', 'file_search_result'],
  MEDIA_PROCESSING: ['processing_call', 'processing_result'],
}

// interactionMedia 把附件写为 Interactions 内容：图片、音频、视频之外为 document，内联数据带 data，文件引用带 uri
function interactionMedia(
  mimeType: string,
  source: { data: string } | { uri: string },
): Record<string, unknown> {
  const kind = mimeType.split('/')[0]!
  const type = ['image', 'audio', 'video'].includes(kind) ? kind : 'document'
  return { type, ...(mimeType !== '' && { mime_type: mimeType }), ...source }
}

// interactionToolResult 把服务端工具结果写为 Interactions 结果数组：网址读取为网址与状态，地图为地点，搜索为搜索建议
function interactionToolResult(toolType: string, response: Record<string, unknown>): unknown[] {
  if (toolType === 'URL_CONTEXT')
    return ((response.url_metadata as unknown[] | undefined) ?? []).map((raw) => {
      const item = record(raw)
      return {
        url: stringOf(item.retrieved_url),
        status: stringOf(item.url_retrieval_status)
          .replace('URL_RETRIEVAL_STATUS_', '')
          .toLowerCase(),
      }
    })
  if (toolType === 'GOOGLE_MAPS')
    return [
      {
        places: ((response.places as unknown[] | undefined) ?? []).map((raw) => {
          const place = record(raw)
          return { place_id: place.place_id, name: place.title, url: place.uri }
        }),
        ...(typeof response.widget_context_token === 'string' && {
          widget_context_token: response.widget_context_token,
        }),
      },
    ]
  return [
    typeof response.search_suggestions === 'string'
      ? { search_suggestions: response.search_suggestions }
      : {},
  ]
}

// interactionToolResponse 把 Interactions 结果数组还原为服务端工具结果，写法与 Gemini API 的 toolResponse.response 相同
function interactionToolResponse(toolType: string, result: unknown): Record<string, unknown> {
  const items = (Array.isArray(result) ? result : []).map(record)
  if (toolType === 'URL_CONTEXT')
    return {
      url_metadata: items.map((item) => ({
        retrieved_url: stringOf(item.url),
        url_retrieval_status: `URL_RETRIEVAL_STATUS_${stringOf(item.status).toUpperCase()}`,
      })),
    }
  if (toolType === 'GOOGLE_MAPS')
    return {
      places: items.flatMap((item) =>
        ((item.places as unknown[] | undefined) ?? []).map((raw) => {
          const place = record(raw)
          return { place_id: place.place_id, title: place.name, uri: place.url }
        }),
      ),
    }
  const suggestions = items.find((item) => typeof item.search_suggestions === 'string')
  return suggestions ? { search_suggestions: suggestions.search_suggestions } : {}
}

// interactionsInput 把 contents 写为 Interactions 输入步骤：正文与附件为 user_input 或 model_output，思考签名为其前的 thought 步骤，函数调用与结果、代码执行与服务端工具为对应步骤，思考正文不发送
function interactionsInput(request: GenerateRequest): Record<string, unknown>[] {
  const steps: Record<string, unknown>[] = []
  let code = ''
  request.contents.forEach((content, turn) => {
    let output: Record<string, unknown>[] | undefined
    // step 写入独立步骤，其后的正文另起内容步骤
    const step = (value: Record<string, unknown>) => {
      output = undefined
      steps.push(value)
    }
    // write 把一项正文或附件并入当前内容步骤
    const write = (value: Record<string, unknown>) => {
      if (output === undefined) {
        output = []
        steps.push({
          type: content.role === 'user' ? 'user_input' : 'model_output',
          content: output,
        })
      }
      output.push(value)
    }
    content.parts.forEach((part, index) => {
      if (part.thoughtSignature) step({ type: 'thought', signature: part.thoughtSignature })
      if (part.thought) return
      if (part.text) write({ type: 'text', text: part.text })
      else if (part.inlineData)
        write(interactionMedia(part.inlineData.mimeType, { data: part.inlineData.data }))
      else if (part.fileData)
        write(interactionMedia(part.fileData.mimeType ?? '', { uri: part.fileData.fileUri }))
      else if (part.functionCall)
        step({
          type: 'function_call',
          id: callID(part.functionCall),
          name: part.functionCall.name,
          arguments: part.functionCall.args ?? {},
        })
      else if (part.functionResponse)
        step({
          type: 'function_result',
          call_id: callID(part.functionResponse),
          name: part.functionResponse.name,
          result: part.functionResponse.response,
        })
      else if (part.executableCode) {
        code = `code_${turn}_${index}`
        step({
          type: 'code_execution_call',
          id: code,
          arguments: {
            code: part.executableCode.code,
            language: part.executableCode.language.toLowerCase(),
          },
        })
      } else if (part.codeExecutionResult)
        step({
          type: 'code_execution_result',
          call_id: code,
          result: part.codeExecutionResult.output ?? '',
          ...(part.codeExecutionResult.outcome !== 'OUTCOME_OK' && { is_error: true }),
        })
      else if (part.toolCall) {
        const names = interactionToolSteps[part.toolCall.toolType ?? '']
        if (names)
          step({
            type: names[0],
            id: part.toolCall.id ?? '',
            arguments: part.toolCall.args ?? {},
            ...(part.toolCall.toolType === 'GOOGLE_SEARCH_IMAGE' && {
              search_type: 'image_search',
            }),
          })
      } else if (part.toolResponse) {
        const toolType = part.toolResponse.toolType ?? ''
        const names = interactionToolSteps[toolType]
        if (names)
          step({
            type: names[1],
            call_id: part.toolResponse.id ?? '',
            result: interactionToolResult(toolType, part.toolResponse.response ?? {}),
          })
      }
    })
  })
  return steps
}

// interactionsRequest 组合 Interactions 创建请求：agent 名称、输入步骤、续接的交互 ID、系统指令、工具与 agent 的 agent_config、environment 字段，工具顺序与官网相同；工具全部关闭时发送空列表，与官网一样不使用工具
function interactionsRequest(
  modelID: string,
  request: GenerateRequest,
  agentFields: Record<string, unknown> | undefined,
  previous: string | undefined,
  stream: boolean,
): Record<string, unknown> {
  const { builtin, functions } = requestTools(request)
  const search = ['googleSearch', 'imageSearch'].filter((name) => builtin.includes(name))
  const tools = [
    ...(builtin.includes('codeExecution') ? [{ type: 'code_execution' }] : []),
    ...functions.map((declaration) => ({
      type: 'function',
      name: declaration.name,
      ...(declaration.description !== undefined && { description: declaration.description }),
      ...(declaration.parameters !== undefined && { parameters: declaration.parameters }),
    })),
    ...(search.length > 0
      ? [
          {
            type: 'google_search',
            ...(search.includes('imageSearch') && {
              search_types: search.map((name) =>
                name === 'imageSearch' ? 'image_search' : 'web_search',
              ),
            }),
          },
        ]
      : []),
    ...(builtin.includes('urlContext') ? [{ type: 'url_context' }] : []),
    ...(builtin.includes('googleMaps') ? [{ type: 'google_maps' }] : []),
  ]
  const system = systemText(request)
  return {
    agent: modelID,
    input: interactionsInput(request),
    ...(previous !== undefined && { previous_interaction_id: previous }),
    ...(system !== '' && { system_instruction: system }),
    tools,
    ...agentFields,
    stream,
  }
}

// interactionToolParts 把函数调用、代码执行与服务端工具步骤换为片段
function interactionToolParts(step: Record<string, unknown>): Part[] {
  const type = stringOf(step.type)
  const args = record(step.arguments)
  if (type === 'function_call')
    return [{ functionCall: { name: stringOf(step.name), args, id: stringOf(step.id) } }]
  if (type === 'code_execution_call')
    return [
      {
        executableCode: {
          language: (stringOf(args.language) || 'python').toUpperCase(),
          code: stringOf(args.code),
        },
      },
    ]
  if (type === 'code_execution_result') {
    const output = stringOf(step.result)
    const outcome = step.is_error === true ? 'OUTCOME_FAILED' : 'OUTCOME_OK'
    return [{ codeExecutionResult: output === '' ? { outcome } : { outcome, output } }]
  }
  const entry = Object.entries(interactionToolSteps).find(([, names]) => names.includes(type))
  if (entry === undefined) return []
  const toolType = step.search_type === 'image_search' ? 'GOOGLE_SEARCH_IMAGE' : entry[0]
  if (type === entry[1][0]) return [{ toolCall: { toolType, args, id: stringOf(step.id) } }]
  return [
    {
      toolResponse: {
        toolType,
        response: interactionToolResponse(toolType, step.result),
        id: stringOf(step.call_id),
      },
    },
  ]
}

// interactionContentPart 把一项正文或媒体内容换为片段，thought 表示它属于思考摘要
function interactionContentPart(raw: unknown, thought: boolean): Part | undefined {
  const content = record(raw)
  const flag = thought ? { thought: true } : {}
  if (content.type === 'text') return { text: stringOf(content.text), ...flag }
  const mimeType = stringOf(content.mime_type)
  if (typeof content.data === 'string')
    return { inlineData: { mimeType, data: content.data }, ...flag }
  if (typeof content.uri === 'string') return { fileData: { mimeType, fileUri: content.uri } }
  return undefined
}

// interactionsDecoder 解析 Interactions：流式按步骤开始、增量与结束取正文、思考摘要、思考签名、引用、媒体、函数调用与工具步骤，完成事件带用量与状态；完整响应依次读取全部步骤；思考签名附到其后第一个非思考片段，引用下标换为全部回答正文中的字节位置
function interactionsDecoder(): Decoder {
  const encoder = new TextEncoder()
  const sources: Source[] = []
  const supports: Support[] = []
  let kind = ''
  let signature = ''
  let answerBytes = 0
  let textStart = 0
  let inText = false
  // emit 写入一个片段：待用的思考签名附到非思考片段，回答正文计入字节位置
  const emit = (update: StreamUpdate, part: Part) => {
    if (signature !== '' && !part.thought) {
      part.thoughtSignature = signature
      signature = ''
    }
    if (part.text !== undefined && !part.thought) answerBytes += encoder.encode(part.text).length
    update.parts.push(part)
  }
  // cite 把一段回答正文的 url_citation 注释写为来源与支撑，base 为该段在回答正文中的起始字节
  const cite = (update: StreamUpdate, annotations: unknown, base: number) => {
    for (const raw of Array.isArray(annotations) ? annotations : []) {
      const annotation = record(raw)
      if (annotation.type !== 'url_citation' || typeof annotation.end_index !== 'number') continue
      const uri = stringOf(annotation.url)
      let index = sources.findIndex((source) => source.uri === uri)
      if (index === -1) {
        index = sources.length
        sources.push({ uri, title: stringOf(annotation.title) || uri })
      }
      supports.push({ end: base + annotation.end_index, chunks: [index] })
    }
    update.sources = [...sources]
    update.supports = [...supports]
  }
  // finish 写入交互 ID、用量与结束原因，仍待用的思考签名附到最后一个片段
  const finish = (update: StreamUpdate, interaction: Record<string, unknown>) => {
    if (signature !== '') update.parts.push({ text: '', thoughtSignature: signature })
    signature = ''
    if (typeof interaction.id === 'string') update.interactionId = interaction.id
    if (typeof interaction.environment_id === 'string')
      update.environmentId = interaction.environment_id
    const usage = record(interaction.usage)
    const { total_output_tokens: output, total_thought_tokens: thoughts } = usage
    if (typeof output === 'number' || typeof thoughts === 'number')
      update.tokens = Number(output ?? 0) + Number(thoughts ?? 0)
    if (typeof thoughts === 'number') update.thoughtTokens = thoughts
    if (typeof usage.total_input_tokens === 'number') update.promptTokens = usage.total_input_tokens
    if (typeof usage.total_cached_tokens === 'number')
      update.cachedTokens = usage.total_cached_tokens
    if (typeof interaction.status === 'string')
      update.finishReason = finishReason(interaction.status)
  }
  // step 读取完整响应中的一个步骤
  const step = (update: StreamUpdate, value: Record<string, unknown>) => {
    if (value.type === 'thought') {
      for (const content of (value.summary as unknown[] | undefined) ?? []) {
        const part = interactionContentPart(content, true)
        if (part) emit(update, part)
      }
      if (typeof value.signature === 'string') signature = value.signature
    } else if (value.type === 'model_output') {
      for (const content of (value.content as unknown[] | undefined) ?? []) {
        const base = answerBytes
        const part = interactionContentPart(content, false)
        if (part) emit(update, part)
        cite(update, record(content).annotations, base)
      }
    } else for (const part of interactionToolParts(value)) emit(update, part)
  }
  return {
    event(value) {
      const update: StreamUpdate = { parts: [] }
      switch (value.event_type) {
        case 'step.start': {
          const start = record(value.step)
          kind = stringOf(start.type)
          inText = false
          if (kind !== 'thought' && kind !== 'model_output')
            for (const part of interactionToolParts(start)) emit(update, part)
          break
        }
        case 'step.delta': {
          const delta = record(value.delta)
          if (delta.type === 'thought_signature') signature = stringOf(delta.signature)
          else if (delta.type === 'thought_summary') {
            const part = interactionContentPart(delta.content, true)
            if (part) emit(update, part)
          } else if (delta.type === 'text_annotation_delta')
            cite(update, delta.annotations, textStart)
          else if (kind === 'model_output') {
            if (delta.type === 'text' && !inText) textStart = answerBytes
            inText = delta.type === 'text'
            const part = interactionContentPart(delta, false)
            if (part) emit(update, part)
          } else if (typeof delta.signature === 'string')
            update.parts.push({ text: '', thoughtSignature: delta.signature })
          break
        }
        case 'interaction.completed':
          finish(update, record(value.interaction))
          break
        case undefined:
          if (value.object !== 'interaction') break
          for (const item of (value.steps as unknown[] | undefined) ?? [])
            step(update, record(item))
          finish(update, value)
          break
      }
      return update
    },
    done: (value) => value.event_type === 'interaction.completed',
  }
}

// ImageOptions 是 OpenAI Images 协议的图片数量、尺寸与质量
export interface ImageOptions {
  count: number
  size: string
  quality: string
}

// imageMime 返回图片的 MIME 类型：有输出格式时按格式，否则按 Base64 开头识别
function imageMime(data: string, format: string): string {
  if (format !== '') return `image/${format === 'jpg' ? 'jpeg' : format}`
  if (data.startsWith('/9j/')) return 'image/jpeg'
  if (data.startsWith('UklGR')) return 'image/webp'
  return 'image/png'
}

// imagesDecoder 解析 OpenAI Images 的完整响应与 image_generation.completed、image_edit.completed 事件
function imagesDecoder(): Decoder {
  return {
    event: (value) => {
      const update: StreamUpdate = { parts: [] }
      const images =
        typeof value.b64_json === 'string' ? [value] : ((value.data as unknown[]) ?? [])
      for (const item of images) {
        const image = record(item)
        const revised = stringOf(image.revised_prompt)
        if (revised !== '') update.parts.push({ text: revised })
        const data = stringOf(image.b64_json)
        if (data !== '')
          update.parts.push({
            inlineData: { mimeType: imageMime(data, stringOf(image.output_format)), data },
          })
      }
      return update
    },
    done: () => false,
  }
}

// imagesCall 组合 OpenAI 图片请求：取最后一个用户轮次的文字为提示词，带参考图片时以 multipart 调用编辑接口
function imagesCall(
  modelID: string,
  request: GenerateRequest,
  options: ImageOptions,
  stream: boolean,
): { path: string; body: Record<string, unknown> | FormData } {
  const parts =
    [...request.contents].reverse().find((content) => content.role === 'user')?.parts ?? []
  const prompt = parts
    .flatMap((part) => (part.text !== undefined && !part.thought ? [part.text] : []))
    .join('\n')
  const images = parts.flatMap((part) =>
    part.inlineData?.mimeType.startsWith('image/') ? [part.inlineData] : [],
  )
  const fields = {
    model: modelID,
    prompt,
    n: options.count,
    size: options.size,
    quality: options.quality,
    response_format: 'b64_json',
    stream,
  }
  if (images.length === 0) return { path: '/v1/images/generations', body: fields }
  const form = new FormData()
  for (const [key, value] of Object.entries(fields)) form.append(key, String(value))
  images.forEach((image, index) =>
    form.append(
      'image[]',
      new Blob([base64Bytes(image.data)], {
        type: image.mimeType,
      }),
      fileName(image.mimeType, index),
    ),
  )
  return { path: '/v1/images/edits', body: form }
}

// ProtocolCall 是一次协议请求：路径、请求头、正文与解析器
interface ProtocolCall {
  path: string
  headers: Headers
  body: Record<string, unknown> | GenerateRequest | FormData
  decoder: Decoder
}

// protocolCall 按协议组合请求，previous 为 Interactions 续接的交互 ID，获取代码以同一请求生成示例
export function protocolCall(
  protocol: Protocol,
  model: Model | undefined,
  modelID: string,
  key: string,
  request: GenerateRequest,
  stream: boolean,
  images: ImageOptions,
  agent: Record<string, unknown> | undefined,
  previous: string | undefined,
): ProtocolCall {
  const headers = new Headers({ 'Content-Type': 'application/json' })
  switch (protocol) {
    case 'interactions':
      if (key !== '') headers.set('x-goog-api-key', key)
      return {
        path: '/v1beta/interactions',
        headers,
        body: interactionsRequest(modelID, request, agent, previous, stream),
        decoder: interactionsDecoder(),
      }
    case 'openai-images': {
      if (key !== '') headers.set('Authorization', `Bearer ${key}`)
      const call = imagesCall(modelID, request, images, stream)
      if (call.body instanceof FormData) headers.delete('Content-Type')
      return { ...call, headers, decoder: imagesDecoder() }
    }
    case 'openai-chat':
      if (key !== '') headers.set('Authorization', `Bearer ${key}`)
      return {
        path: '/v1/chat/completions',
        headers,
        body: chatRequest(modelID, request, stream),
        decoder: chatDecoder(),
      }
    case 'openai-responses':
      if (key !== '') headers.set('Authorization', `Bearer ${key}`)
      return {
        path: '/v1/responses',
        headers,
        body: responsesRequest(modelID, request, stream),
        decoder: responsesDecoder(),
      }
    case 'anthropic':
      if (key !== '') headers.set('x-api-key', key)
      headers.set('anthropic-version', '2023-06-01')
      return {
        path: '/v1/messages',
        headers,
        body: anthropicRequest(model, modelID, request, stream),
        decoder: anthropicDecoder(),
      }
    case 'gemini':
      if (key !== '') headers.set('x-goog-api-key', key)
      return {
        path: `/v1beta/models/${encodeURIComponent(modelID)}:${stream ? 'streamGenerateContent?alt=sse' : 'generateContent'}`,
        headers,
        body: request,
        decoder: geminiDecoder(),
      }
  }
}

// GenerateOptions 是一次试用请求的协议、模型、OpenAI Images 参数、Interactions 的 agent 配置与续接的交互 ID 与回调
interface GenerateOptions {
  protocol: Protocol
  stream: boolean
  model: Model | undefined
  modelID: string
  apiKey: string
  request: GenerateRequest
  images: ImageOptions
  agent: Record<string, unknown> | undefined
  previous: string | undefined
  signal: AbortSignal
  onUpdate: (update: StreamUpdate) => void
}

// generate 以所选协议调用本服务公开接口，把流式事件或完整响应换为片段变化回调
export async function generate(options: GenerateOptions): Promise<void> {
  const call = protocolCall(
    options.protocol,
    options.model,
    options.modelID,
    options.apiKey,
    options.request,
    options.stream,
    options.images,
    options.agent,
    options.previous,
  )
  const response = await fetch(call.path, {
    method: 'POST',
    headers: call.headers,
    body: call.body instanceof FormData ? call.body : jsonBody(call.body),
    signal: options.signal,
  })
  if (!response.ok) throw new Error(await errorMessage(response))
  if (!options.stream) {
    const text = await response.text()
    const value = record(JSON.parse(text))
    const error = errorOf(value)
    if (error !== undefined) throw new Error(error)
    options.onUpdate(call.decoder.event({ ...value, type: value.type ?? '' }))
    return
  }
  const events = response
    .body!.pipeThrough(new TextDecoderStream())
    .pipeThrough(new EventSourceParserStream())
  for await (const { data } of events) {
    if (data === '[DONE]') return
    const value = record(JSON.parse(data))
    const error = errorOf(value)
    if (error !== undefined) throw new Error(error)
    options.onUpdate(call.decoder.event(value))
    if (call.decoder.done(value)) return
  }
}
