export type Locale = 'zh-CN' | 'zh-TW' | 'en' | 'ja' | 'ko' | 'fr' | 'de'

export type TabID = 'logs' | 'accounts' | 'models' | 'requests' | 'settings' | 'playground'

export type AccountState =
  'ready' | 'busy' | 'cooldown' | 'auth_required' | 'unavailable' | 'disabled'

export interface Account {
  id: string
  label: string
  enabled: boolean
  state: AccountState
  proxy: string
  locale: string
  timezone: string
  models: string[]
  benefit_tier: string
  message: string
}

export interface AccountDraft {
  label: string
  enabled: boolean
  proxy: string
  locale: string
  timezone: string
}

export interface AccountLoginInput {
  proxy: string
  locale: string
  timezone: string
}

export interface ChromeImportProfile {
  profile: string
  display_name: string
  email: string
  locale: string
}

export interface ChromeImportInput extends AccountLoginInput {
  profiles: string[]
}

export interface AccountCounters {
  total: number
  ready: number
  busy: number
  cooldown: number
  auth_required: number
}

export interface ServiceStatus {
  state: 'STOPPED' | 'LAUNCHING' | 'RUNNING'
  running: boolean
  ready: boolean
  version: string
  active_requests: number
  accounts: AccountCounters
}

export interface AdminLog {
  time: string
  level: string
  source: string
  message: string
}

export interface Model {
  id: string
  name: string
  description?: string
  methods: string[]
  input_token_limit?: number
  output_token_limit?: number
  capabilities?: Record<string, boolean>
  capability_options?: Record<string, string[]>
  access_modes?: number[]
  paid?: boolean
}

export interface Cooldown {
  account_id: string
  account_label: string
  model_id: string
  until: string
  reason?: string
}

export type RequestState = 'queued' | 'running' | 'completed' | 'cancelled' | 'failed'

export interface RequestSummary {
  id: string
  model: string
  account_id: string
  account_label: string
  state: RequestState
  started_at: string
}

export interface ServiceConfig {
  auth_states: string
  listen_addr: string
  proxy_api_key: string
  active_listen_addr: string
  active_proxy_api_key: string
  management_restart_required: boolean
  service_restart_required: boolean
  proxy: string
  init_timeout: string
  request_timeout: string
  warm_worker_limit: number
  max_active_workers: number
  warm_startup_concurrency: number
  per_account_concurrency: number
  temporary_chat: boolean
}

export type AdminEvent =
  | { type: 'status'; data: ServiceStatus }
  | { type: 'log'; data: AdminLog }
  | { type: 'accounts'; data: { accounts: Account[] } }
  | { type: 'models'; data: { models: Model[] } }
  | { type: 'cooldowns'; data: Cooldown[] }
  | { type: 'request'; data: RequestSummary }

export type PlaygroundProtocol = 'openai-chat' | 'openai-responses' | 'anthropic' | 'gemini'

export type PlaygroundMode = 'text' | 'image' | 'speech' | 'music' | 'video'

export type PlaygroundReasoning = '' | 'low' | 'medium' | 'high'

export type PlaygroundTool =
  '' | 'web_search' | 'image_search' | 'code_interpreter' | 'url_context' | 'google_maps'

export interface PlaygroundInput {
  mode: PlaygroundMode
  protocol: PlaygroundProtocol
  model: string
  prompt: string
  system: string
  stream: boolean
  reasoning: PlaygroundReasoning
  tool: PlaygroundTool
  imageSize: 'auto' | '1024x1024' | '1536x1024' | '1024x1536'
  imageQuality: 'auto' | 'low' | 'medium' | 'high'
  voice: string
  apiKey: string
}

export interface PlaygroundMedia {
  mime: string
  url: string
}

export interface PlaygroundResult {
  text: string
  reasoning: string
  tools: string
  media: PlaygroundMedia[]
  raw: string
  durationMs: number
  status: number
}
