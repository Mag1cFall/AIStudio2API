import type { ThinkingLevel } from '@/types'
import type { Tool } from './catalog'
import type { Protocol } from './protocols'

// Part 是 Gemini API 的内容片段
export interface Part {
  text?: string
  thought?: boolean
  thoughtSignature?: string
  inlineData?: { mimeType: string; data: string }
  fileData?: { mimeType?: string; fileUri: string }
  functionCall?: { name: string; args?: Record<string, unknown>; id?: string }
  functionResponse?: { name: string; response: Record<string, unknown>; id?: string }
  executableCode?: { language: string; code: string }
  codeExecutionResult?: { outcome: string; output?: string }
}

// Source 是搜索与地图依据中的一个来源
export interface Source {
  uri: string
  title: string
}

// Support 是依据中的一条支撑：end 为正文中该段结束处的 UTF-8 字节位置，chunks 为来源下标
export interface Support {
  end: number
  chunks: number[]
}

// Turn 是对话中的一轮；模型轮次记录总耗时、首个片段延迟、思考耗时、输出 token 与其中的思考 token、依据来源与出错信息，时长单位为毫秒
export interface Turn {
  id: string
  role: 'user' | 'model'
  parts: Part[]
  created: number
  duration?: number
  latency?: number
  thinking?: number
  tokens?: number
  thoughtTokens?: number
  finishReason?: string
  sources?: Source[]
  supports?: Support[]
  searchQueries?: string[]
  error?: string
}

// SafetyCategory 是官网安全设置的四个类别
export type SafetyCategory =
  | 'HARM_CATEGORY_HARASSMENT'
  | 'HARM_CATEGORY_HATE_SPEECH'
  | 'HARM_CATEGORY_SEXUALLY_EXPLICIT'
  | 'HARM_CATEGORY_DANGEROUS_CONTENT'

// SafetyThreshold 是安全设置滑块的五档
export type SafetyThreshold =
  | 'OFF'
  | 'BLOCK_NONE'
  | 'BLOCK_ONLY_HIGH'
  | 'BLOCK_MEDIUM_AND_ABOVE'
  | 'BLOCK_LOW_AND_ABOVE'

// MediaResolution 是媒体分辨率，空字符串为模型默认
export type MediaResolution =
  | ''
  | 'MEDIA_RESOLUTION_LOW'
  | 'MEDIA_RESOLUTION_MEDIUM'
  | 'MEDIA_RESOLUTION_HIGH'

// ImageOutput 是图片模型的输出格式：图文或仅图片
export type ImageOutput = 'IMAGE_TEXT' | 'IMAGE'

// ImageQuality 是 OpenAI Images 协议的图片质量
export type ImageQuality = 'auto' | 'low' | 'medium' | 'high'

// RunSettings 是 Playground 运行设置，数值项为 null 时使用模型默认值；thinkingBudget 为 -1 时由模型决定、0 时不思考；protocol 与 stream 决定请求使用的公开协议与是否流式
export interface RunSettings {
  model: string
  protocol: Protocol
  stream: boolean
  systemInstruction: string
  temperature: number | null
  topP: number | null
  topK: number | null
  maxOutputTokens: number | null
  stopSequences: string[]
  thinkingLevel: ThinkingLevel | null
  thinkingBudget: number
  mediaResolution: MediaResolution
  safety: Record<SafetyCategory, SafetyThreshold>
  tools: Tool[]
  responseSchema: string
  functionDeclarations: string
  imageOutput: ImageOutput
  aspectRatio: string
  imageSize: string
  videoAspectRatio: string
  videoDuration: number | null
  videoResolution: string
  videoTask: string
  imageCount: number
  openAISize: string
  imageQuality: ImageQuality
}

// PaneData 是一个窗格的运行设置与对话
export interface PaneData {
  settings: RunSettings
  turns: Turn[]
}

// CompareData 是对比模式的右侧窗格，sync 表示两侧系统指令同步
export interface CompareData extends PaneData {
  sync: boolean
}

// PromptData 是保存到对话库的对话内容，compare 是对比模式的右侧窗格
export interface PromptData extends PaneData {
  compare?: CompareData
}
