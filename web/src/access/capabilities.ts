import type { TranslationKey } from '@/i18n'
import type { IconName } from '@/icons'
import { agentTools, isAgent } from '@/playground/catalog'
import { codeOptions, generateCode, type CodeLanguage, type CodeOption } from '@/playground/code'
import { mediaCodeOptions, videoCode } from '@/playground/media/code'
import { liveCode, liveCodeOptions } from '@/playground/media/live-code'
import { musicCode, musicCodeOptions } from '@/playground/media/music-code'
import { speechCode } from '@/playground/media/speech-code'
import { defaultSettings } from '@/playground/media/video'
import type { Protocol } from '@/playground/protocols'
import { toolPayloads, type GenerateRequest } from '@/playground/request'
import type { Model } from '@/types'
import {
  anthropicPython,
  anthropicTypescript,
  decisionCode,
  embeddingCode,
  filesCode,
  transcriptionCode,
} from './snippets'

// CodeInput 是生成能力示例所需的服务地址、所选协议、语言与模型
export interface CodeInput {
  origin: string
  protocol: string
  language: string
  model: Model | undefined
  modelID: string
}

// Capability 是接入页的一种能力：名称、说明与提醒的文案键、图标、所用端点、实时目录中可用模型的条件、协议与语言选项和示例代码
export interface Capability {
  id: string
  label: TranslationKey
  description: TranslationKey
  notice?: TranslationKey
  icon: IconName
  endpoints: [method: string, path: string][]
  matches: (model: Model) => boolean
  protocols: { value: string; label: string }[]
  languages: (protocol: string) => CodeOption[]
  code: (input: CodeInput) => string
}

// option 从已有的语言选项中按值取出若干项
function option(options: CodeOption[], ...values: string[]): CodeOption[] {
  return values.map((value) => options.find((item) => item.value === value)!)
}

// restOption 是 cURL 示例的语言选项
const restOption: CodeOption = {
  value: 'rest',
  label: 'cURL',
  highlight: 'bash',
  file: 'request.sh',
}

// googleOptions 是 Google Gen AI SDK 与 cURL 的语言选项
const googleOptions = [...option(codeOptions, 'python', 'typescript'), restOption]

// openAIOptions 是 OpenAI SDK 与 cURL 的语言选项
const openAIOptions = [...option(codeOptions, 'openai-python', 'openai-typescript'), restOption]

// anthropicOptions 是 Anthropic SDK 与 cURL 的语言选项
const anthropicOptions: CodeOption[] = [
  { value: 'anthropic-python', label: 'Anthropic Python', highlight: 'python', file: 'main.py' },
  {
    value: 'anthropic-typescript',
    label: 'Anthropic TypeScript',
    highlight: 'typescript',
    file: 'main.ts',
  },
  restOption,
]

// prompt 是只含一段用户文字的 Gemini API 请求，config 为生成参数
function prompt(text: string, config: Record<string, unknown> = {}): GenerateRequest {
  return { contents: [{ role: 'user', parts: [{ text }] }], generationConfig: config }
}

// generated 用 API 试用页获取代码的生成器写出示例，与试用页以同一协议发送的请求相同
function generated(input: CodeInput, request: GenerateRequest, stream: boolean): string {
  return generateCode(input.language as CodeLanguage, {
    model: input.model,
    modelID: input.modelID,
    baseURL: input.origin,
    request,
    protocol: input.protocol as Protocol,
    stream,
    images: { count: 1, size: 'auto', quality: 'auto' },
    agent: undefined,
    previous: undefined,
  })
}

// chatModel 表示模型是普通对话模型：经 generateContent 生成文字，不是图片、语音、音乐、转写模型或 agent
export function chatModel(model: Model): boolean {
  const capabilities = model.capabilities ?? {}
  return (
    capabilities.chat_model === true &&
    model.methods.includes('generateContent') &&
    !capabilities.image_route &&
    !capabilities.transcription_output &&
    !isAgent(model)
  )
}

// capabilities 是接入页按能力列出的用法，模型来自实时目录
export const capabilities: Capability[] = [
  {
    id: 'chat',
    label: 'access.cap.chat',
    description: 'access.cap.chatHelp',
    icon: 'playground',
    endpoints: [
      ['POST', '/v1/chat/completions'],
      ['POST', '/v1/responses'],
      ['POST', '/v1/messages'],
      ['POST', '/v1beta/models/{model}:generateContent'],
    ],
    matches: chatModel,
    protocols: [
      { value: 'openai-chat', label: 'OpenAI Chat' },
      { value: 'openai-responses', label: 'OpenAI Responses' },
      { value: 'anthropic', label: 'Anthropic Messages' },
      { value: 'gemini', label: 'Gemini API' },
    ],
    languages: (protocol) =>
      protocol === 'gemini'
        ? googleOptions
        : protocol === 'anthropic'
          ? anthropicOptions
          : openAIOptions,
    code: (input) => {
      if (input.language === 'anthropic-python') return anthropicPython(input)
      if (input.language === 'anthropic-typescript') return anthropicTypescript(input)
      return generated(input, prompt('Explain how AI works in a few words'), false)
    },
  },
  {
    id: 'image',
    label: 'access.cap.image',
    description: 'access.cap.imageHelp',
    icon: 'image',
    endpoints: [
      ['POST', '/v1/images/generations'],
      ['POST', '/v1/images/edits'],
      ['POST', '/v1beta/models/{model}:generateContent'],
    ],
    matches: (model) => model.capabilities?.image_route === true,
    protocols: [
      { value: 'openai-images', label: 'OpenAI Images' },
      { value: 'gemini', label: 'Gemini API' },
    ],
    languages: (protocol) => (protocol === 'gemini' ? googleOptions : openAIOptions),
    code: (input) =>
      generated(
        input,
        prompt('Create a picture of a nano banana dish in a fancy restaurant with a Gemini theme', {
          responseModalities: ['IMAGE', 'TEXT'],
        }),
        false,
      ),
  },
  {
    id: 'speech',
    label: 'access.cap.speech',
    description: 'access.cap.speechHelp',
    icon: 'speech',
    endpoints: [
      ['POST', '/v1/audio/speech'],
      ['POST', '/v1beta/models/{model}:generateContent'],
    ],
    matches: (model) => model.capabilities?.speech_route === true,
    protocols: [],
    languages: () => mediaCodeOptions,
    code: (input) =>
      speechCode(input.language as Parameters<typeof speechCode>[0], {
        baseURL: input.origin,
        model: input.modelID,
        prompt: 'Say cheerfully: Have a wonderful day!',
        temperature: 1,
        speakers: [{ alias: 'Speaker 1', voice: 'Kore' }],
        openAI: { input: 'Have a wonderful day!', voice: 'Kore' },
      }),
  },
  {
    id: 'transcription',
    label: 'access.cap.transcription',
    description: 'access.cap.transcriptionHelp',
    icon: 'transcription',
    endpoints: [
      ['POST', '/v1/audio/transcriptions'],
      ['POST', '/v1/audio/translations'],
    ],
    matches: (model) =>
      model.capabilities?.transcription_output === true &&
      model.methods.includes('generateContent'),
    protocols: [],
    languages: () => openAIOptions,
    code: (input) => transcriptionCode(input.language, input),
  },
  {
    id: 'music',
    label: 'access.cap.music',
    description: 'access.cap.musicHelp',
    icon: 'music',
    endpoints: [['POST', '/v1beta/models/{model}:generateContent']],
    matches: (model) => model.capabilities?.music_route === true,
    protocols: [],
    languages: () => musicCodeOptions,
    code: (input) =>
      musicCode(input.language as Parameters<typeof musicCode>[0], {
        baseURL: input.origin,
        model: input.modelID,
        prompt: '',
        images: [],
      }),
  },
  {
    id: 'video',
    label: 'access.cap.video',
    description: 'access.cap.videoHelp',
    icon: 'video',
    endpoints: [
      ['POST', '/v1/videos'],
      ['GET', '/v1/videos/{id}/content'],
      ['POST', '/v1beta/models/{model}:predictLongRunning'],
    ],
    matches: (model) =>
      (model.capability_options?.video_output_resolutions?.length ?? 0) > 0 ||
      model.methods.includes('predictLongRunning'),
    protocols: [],
    languages: () => mediaCodeOptions,
    code: (input) => {
      const options = input.model?.capability_options ?? {}
      return videoCode(input.language as Parameters<typeof videoCode>[0], {
        baseURL: input.origin,
        prompt: 'A drone flying over a forest at sunrise',
        settings: { ...defaultSettings(input.model), model: input.modelID },
        references: [],
        aspectRatios: options.video_aspect_ratios ?? [],
        durations: (options.video_durations_seconds ?? []).map(Number),
        resolutions: options.video_output_resolutions ?? [],
      })
    },
  },
  {
    id: 'embedding',
    label: 'access.cap.embedding',
    description: 'access.cap.embeddingHelp',
    icon: 'embedding',
    endpoints: [
      ['POST', '/v1/embeddings'],
      ['POST', '/v1beta/models/{model}:embedContent'],
      ['POST', '/v1beta/models/{model}:batchEmbedContents'],
    ],
    matches: (model) => model.methods.includes('embedContent'),
    protocols: [],
    languages: () => openAIOptions,
    code: (input) => embeddingCode(input.language, input),
  },
  {
    id: 'decision',
    label: 'access.cap.decision',
    description: 'access.cap.decisionHelp',
    notice: 'models.decisionHiddenHelp',
    icon: 'decision',
    endpoints: [['POST', '/v1beta/decisions']],
    matches: (model) => model.methods.includes('createDecision'),
    protocols: [],
    languages: () => googleOptions,
    code: (input) => decisionCode(input.language, input),
  },
  {
    id: 'live',
    label: 'access.cap.live',
    description: 'access.cap.liveHelp',
    icon: 'live',
    endpoints: [
      ['GET', '/v1/live'],
      ['GET', '/v1/robotics/stream'],
    ],
    matches: (model) =>
      model.methods.includes('bidiGenerateContent') &&
      !model.capabilities?.speech_translation &&
      !model.capabilities?.transcription_output,
    protocols: [],
    languages: () => liveCodeOptions,
    code: (input) =>
      liveCode(input.language as Parameters<typeof liveCode>[0], {
        baseURL: input.origin,
        setup: {
          type: 'setup',
          model: input.modelID,
          input_modalities: ['text', 'audio'],
          output_modalities: ['audio'],
        },
        prompt: 'Hello, how are you today?',
      }),
  },
  {
    id: 'agent',
    label: 'access.cap.agent',
    description: 'access.cap.agentHelp',
    icon: 'agent',
    endpoints: [
      ['POST', '/v1beta/interactions'],
      ['GET', '/v1beta/interactions/{id}'],
    ],
    matches: isAgent,
    protocols: [],
    languages: () => googleOptions,
    code: (input) => {
      const request = prompt('Research the history of the transistor and summarize it')
      const tools = input.model ? agentTools(input.model).map((tool) => toolPayloads[tool]!) : []
      if (tools.length > 0) request.tools = tools
      return generated({ ...input, protocol: 'interactions' }, request, true)
    },
  },
  {
    id: 'files',
    label: 'access.cap.files',
    description: 'access.cap.filesHelp',
    icon: 'file',
    endpoints: [
      ['POST', '/v1/files'],
      ['POST', '/upload/v1beta/files'],
    ],
    matches: chatModel,
    protocols: [],
    languages: () => [
      option(codeOptions, 'python')[0]!,
      option(codeOptions, 'openai-python')[0]!,
      restOption,
    ],
    code: (input) => filesCode(input.language, input),
  },
]
