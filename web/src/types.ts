export type Locale = 'zh-CN' | 'zh-TW' | 'en' | 'ja' | 'ko' | 'fr' | 'de'

export type TabID =
  | 'logs'
  | 'accounts'
  | 'models'
  | 'requests'
  | 'usage'
  | 'settings'
  | 'playground'
  | 'version'

export type AccountState =
  | 'ready'
  | 'busy'
  | 'cooldown'
  | 'auth_required'
  | 'unavailable'
  | 'disabled'

export interface Account {
  id: string
  label: string
  enabled: boolean
  state: AccountState
  proxy: string
  egress: string
  locale: string
  timezone: string
  models: string[]
  benefit_tier: string
  message: string
}

export interface ProxyPoolEntry {
  proxy: string
  available: boolean
  accounts: number
  checked_at?: string
  error?: string
}

export interface AccountRefresh {
  account: Account
  previous_tier: string
  tier: string
  previous_models: number
  models: number
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
  id: string
  profile: string
  display_name: string
  email: string
  locale: string
}

export interface PairingToken {
  token: string
  expires_at: string
}

export interface ChromeImportInput extends AccountLoginInput {
  account_ids: string[]
}

export interface CookieImportInput extends AccountLoginInput {
  label: string
  cookies: string
  cdp: string
}

export interface CookieBrowser {
  address: string
  browser: string
  email: string
  locale: string
  timezone: string
  imported: boolean
  signed_out: boolean
  error: string
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

export interface UpdateStatus {
  current: string
  latest?: string
  available: boolean
  mode: 'binary' | 'download' | 'container' | 'development' | 'managed'
  managed_by?: string
  notes?: string
  url?: string
  published_at?: string
  checked_at?: string
  error?: string
  updating: boolean
}

export interface AdminLog {
  time: string
  level: string
  source: string
  message: string
  event: string
  request?: RequestLog
}

// RequestLog 对应请求日志的结构化载荷
export interface RequestLog {
  id: string
  state: 'running' | 'completed' | 'tool_calls' | 'limited' | 'blocked' | 'failed' | 'cancelled'
  model?: string
  method?: string
  path?: string
  status?: number
  duration_ms?: number
  tool_calls?: number
  finish_reason?: string
  error?: string
  input_messages?: number
  input_text_chars?: number
  input_media?: number
  input_media_bytes?: number
  input_files?: number
  parameters?: Record<string, string>
  first_event_ms?: number
  upstream_bytes?: number
  channel?: UpstreamChannel
  usage?: {
    input_tokens: number
    cached_tokens: number
    reasoning_tokens: number
    reply_tokens: number
    output_tokens: number
    total_tokens: number
    average_tokens_per_second: number
  }
}

// ModelPrice 是模型价格表中的一行，input 与 output 为每百万 token 美元，按次计价时对应 per_request 为真
export interface ModelPrice {
  text?: string
  label?: string
  up_to?: number
  thinking?: boolean
  input?: number
  input_per_request?: boolean
  output?: number
  output_per_request?: boolean
}

// ModelDetails 是官网模型卡片中的说明、别名、提醒、价格与日期，日期为 Unix 秒
export interface ModelDetails {
  summary?: string
  alias?: string
  warning?: string
  docs_url?: string
  model_card_url?: string
  release_date?: number
  knowledge_cutoff?: number
  pricing?: ModelPrice[]
  pricing_url?: string
  pricing_note?: string
}

// Language 是模型可选语言的代码与显示名
export interface Language {
  code: string
  name: string
}

// VoiceDetail 是音色的试听地址、音高、描述、口音、性别、特质与用途
export interface VoiceDetail {
  name: string
  sample_url?: string
  pitch?: string
  descriptions?: string[]
  locale?: string
  accent?: string
  gender?: string
  trait?: string
  use_case?: string
}

export interface Model {
  id: string
  name: string
  description?: string
  methods: string[]
  input_token_limit?: number
  output_token_limit?: number
  temperature?: number
  max_temperature?: number
  top_p?: number
  top_k?: number
  thinking_levels?: ThinkingLevel[]
  default_thinking_level?: ThinkingLevel
  thinking_budget?: ThinkingBudget
  tool_compatibility?: [string, string][]
  details?: ModelDetails
  capabilities?: Record<string, boolean>
  capability_options?: Record<string, string[]>
  voice_details?: VoiceDetail[]
  languages?: Language[]
  access_modes?: number[]
  paid?: boolean
  channels?: UpstreamChannel[]
}

export type ThinkingLevel = 'MINIMAL' | 'LOW' | 'MEDIUM' | 'HIGH'

// ThinkingBudget 是可设思考预算的模型的预算默认值、上下限与能否关闭思考
export interface ThinkingBudget {
  default?: number
  max?: number
  min?: number
  can_disable?: boolean
}

export type UpstreamChannel = 'playground' | 'build' | 'assistant'

export interface Cooldown {
  account_id: string
  account_label: string
  channel: UpstreamChannel
  model_id: string
  scope: string
  until: string
  reason?: string
}

export type RequestState = 'queued' | 'running' | 'completed' | 'cancelled' | 'failed'

export interface RequestSummary {
  id: string
  model: string
  account_id: string
  account_label: string
  channel?: UpstreamChannel
  state: RequestState
  started_at: string
}

export interface ServiceConfig {
  auto_start: boolean
  request_body_log: boolean
  log_file: boolean
  log_max_total_mb: number
  admin_auth_enabled: boolean
  admin_username: string
  admin_password?: string
  admin_password_set: boolean
  build_native_nonstream: boolean
  auth_states: string
  listen_addr: string
  proxy_api_key: string
  active_listen_addr: string
  active_proxy_api_key: string
  management_restart_required: boolean
  service_restart_required: boolean
  proxy: string
  active_proxy: string
  proxy_pool: ProxyPoolEntry[]
  init_timeout: string
  request_timeout: string
  warm_worker_limit: number
  max_active_workers: number
  warm_startup_concurrency: number
  per_account_concurrency: number
  routing_strategy: 'round-robin' | 'fill-first'
  upstream_channels: UpstreamChannel[]
  model_mapping: string
  model_channels: string
  waa_backend: 'camoufox' | 'go'
  temporary_chat: boolean
  media_output: 'inline' | 'url'
  media_base_url: string
}

export type UsageDimension = 'model' | 'account' | 'channel' | 'protocol' | 'state'

export type UsageFilters = Partial<Record<UsageDimension, string[]>>

export interface UsageLatency {
  avg_ms: number
  p50_ms: number
  p95_ms: number
  p99_ms: number
}

export interface UsageStats {
  requests: number
  succeeded: number
  failed: number
  canceled: number
  rate_limited: number
  input_tokens: number
  cached_tokens: number
  reasoning_tokens: number
  reply_tokens: number
  total_tokens: number
  duration: UsageLatency
  first_event: UsageLatency
  queue_avg_ms: number
  last_at?: string
}

export interface UsageBucket extends UsageStats {
  at: string
  cost: number
}

export interface UsageGroup extends UsageStats {
  key: string
}

export interface UsagePair extends UsageStats {
  account: string
  model: string
}

export interface UsageSeries {
  key: string
  other?: boolean
  requests: number[]
  tokens: number[]
}

export interface UsageReport {
  from: string
  to: string
  bucket_seconds: number
  generated_at: string
  latest_at?: string
  totals: UsageStats
  previous: UsageStats
  cost: number
  previous_cost: number
  unpriced_models: string[]
  recent: { minutes: number; requests: number; tokens: number }
  buckets: UsageBucket[]
  stack_by: UsageDimension
  series: UsageSeries[]
  groups: Record<UsageDimension | 'status', UsageGroup[]>
  pairs: UsagePair[]
  options: Record<UsageDimension, string[]>
}

export interface RequestAttempt {
  account: string
  channel: string
  error: string
  duration_ms: number
}

export interface UsageRecord {
  id: string
  time: string
  protocol: string
  path: string
  model: string
  account: string
  channel: string
  status: number
  state: string
  duration_ms: number
  first_event_ms: number
  queue_ms: number
  input_tokens: number
  cached_tokens: number
  reasoning_tokens: number
  reply_tokens: number
  total_tokens: number
  tool_calls: number
  error: string
  attempts: RequestAttempt[]
  has_body: boolean
}

export interface UsageRecordPage {
  items: UsageRecord[]
  next_cursor?: string
}

export interface RequestBody {
  id: string
  time: string
  request: string
  request_size: number
  response: string
  response_size: number
}

export type AdminEvent =
  | { type: 'status'; data: ServiceStatus }
  | { type: 'log'; data: AdminLog }
  | { type: 'accounts'; data: { accounts: Account[] } }
  | { type: 'models'; data: { models: Model[] } }
  | { type: 'cooldowns'; data: Cooldown[] }
  | { type: 'request'; data: RequestSummary }

// PromptSummary 是对话库列表中的一项
export interface PromptSummary {
  id: string
  title: string
  model: string
  description: string
  created_at: string
  updated_at: string
}

// PromptInput 是保存对话时提交的内容
export interface PromptInput<T> {
  title: string
  model: string
  description: string
  data: T
}

// SavedPrompt 是从对话库读取的完整对话
export interface SavedPrompt<T> extends PromptSummary {
  data: T
}
