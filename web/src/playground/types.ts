import type { ThinkingLevel } from '@/types'
import type { Tool } from './catalog'
import type { Protocol } from './protocols'

// Part 是 Gemini API 的内容片段；fileName 是附件的文件名，请求中写为 inlineData.displayName；tokenCount 是试用页附件显示的 token 数，不随请求发送
export interface Part {
  text?: string
  thought?: boolean
  thoughtSignature?: string
  inlineData?: { mimeType: string; data: string; displayName?: string }
  fileData?: { mimeType?: string; fileUri: string }
  functionCall?: { name: string; args?: Record<string, unknown>; id?: string }
  functionResponse?: { name: string; response: Record<string, unknown>; id?: string }
  executableCode?: { language: string; code: string }
  codeExecutionResult?: { outcome: string; output?: string }
  toolCall?: { toolType?: string; toolName?: string; args?: Record<string, unknown>; id?: string }
  toolResponse?: { toolType?: string; response?: Record<string, unknown>; id?: string }
  videoMetadata?: { startOffset?: string; endOffset?: string; fps?: number }
  mediaProcessing?: 'STATIC' | 'AGENTIC'
  fileName?: string
  fileExpires?: number
  tokenCount?: number
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

// Turn 是对话中的一轮；模型轮次记录总耗时、首个片段延迟、思考耗时、输出 token 与其中的思考 token、依据来源、出错信息、是否在 Agent 模式下生成与经 Interactions 生成时的交互 ID，时长单位为毫秒
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
  agent?: boolean
  interactionId?: string
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

// EnvironmentSource 是 agent 环境中挂载的一个源：inline 为内联文件，gcs 与 repository 为 Source URL 指向的存储桶与仓库，target 为挂载路径
export interface EnvironmentSource {
  type: 'inline' | 'gcs' | 'repository'
  source: string
  target: string
  content: string
}

// EgressRule 是 agent 环境出站白名单的一条规则，headers 为注入匹配请求的 HTTP 请求头
export interface EgressRule {
  domain: string
  headers: { name: string; value: string }[]
}

// AgentEnvironment 是 agent 的运行环境：new 为按 sources 与 rules 新建的环境，existing 为 id 指向的已有环境，produced 是本对话的运行得到的环境 ID
export interface AgentEnvironment {
  type: 'new' | 'existing'
  id: string
  produced?: string
  sources: EnvironmentSource[]
  rules: EgressRule[]
}

// RunSettings 是 Playground 运行设置，数值项为 null 时使用模型默认值；thinkingBudget 为 -1 时由模型决定、0 时不思考；protocol 与 stream 决定请求使用的公开协议与是否流式；agentModel 与 maxTotalTokens 是 Antigravity 所选的模型与 token 上限，空值为 agent 默认，environment 是带远程环境的 agent 的运行环境；thinkingSummaries、visualization 与 collaborativePlanning 是 Deep Research 的思考摘要、可视化与协作规划
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
  agentModel: string
  maxTotalTokens: number | null
  environment: AgentEnvironment
  thinkingSummaries: boolean
  visualization: boolean
  collaborativePlanning: boolean
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
