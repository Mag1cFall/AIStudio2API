export type Locale = 'zh-CN' | 'zh-TW' | 'en' | 'ja' | 'ko' | 'fr' | 'de'

export type TabID =
  | 'logs'
  | 'accounts'
  | 'access'
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
  // pairing_renewed_at 是远程添加的账户最近一次由原电脑续签的时间
  pairing_renewed_at?: string
  // transition 是账户启停的后台收尾阶段：draining 等待在途请求结束并关闭 Worker，syncing 同步模型目录
  transition?: 'draining' | 'syncing'
  // active_requests 是账户上进行中的请求数
  active_requests: number
}

interface ProxyPoolEntry {
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
  accounts: { id: string; email: string }[]
}

// CookieAccountItem 是一次 Cookie 导入中的一个账户：识别出的邮箱、Cookie 文本或调试浏览器地址与会话序号
export interface CookieAccountItem {
  email: string
  cookies: string
  cdp: string
  auth_user: number
}

// CookieAccountsInput 是一次导入的 Cookie 账户与共用的账户环境，语言与时区为空时调试浏览器账户沿用浏览器的设置
export interface CookieAccountsInput extends AccountLoginInput {
  items: CookieAccountItem[]
}

// AccountTaskItem 是账户任务中的一个账户，label 为邮箱，浏览器登录完成前为空
export interface AccountTaskItem {
  label: string
  state: 'pending' | 'running' | 'done' | 'failed'
  code?: string
  error?: string
}

// AccountTask 是在服务端运行的添加或重新登录账户任务，view 为显示登录画面的登录窗口 ID
export interface AccountTask {
  id: string
  kind: 'login' | 'relogin' | 'chrome' | 'cookies' | 'browser'
  view?: string
  state: 'running' | 'done' | 'failed'
  items: AccountTaskItem[]
  started_at: string
}

// CookieImportInput 是识别 Cookie 中账户时提交的 Cookie 文本与出口代理
export interface CookieImportInput {
  cookies: string
  proxy: string
}

export interface CookieBrowser {
  address: string
  browser: string
  email: string
  auth_user: number
  locale: string
  timezone: string
  imported: boolean
  signed_out: boolean
  // device_bound 为真时 Cookie 带 __Secure-1PSIDRTS，会话绑定原浏览器所在的设备
  device_bound: boolean
  error: string
}

interface AccountCounters {
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
  chrome_import: boolean
}

export interface UpdateStatus {
  current: string
  latest?: string
  available: boolean
  mode: 'binary' | 'container' | 'development' | 'managed'
  managed_by?: string
  notes?: string
  url?: string
  published_at?: string
  checked_at?: string
  error?: string
  updating: boolean
  progress?: UpdateProgress
}

// UpdateProgress 是进行中的在线更新的阶段与发行包的已下载和总字节数，总字节数未知时为 0
export interface UpdateProgress {
  stage: 'download' | 'verify' | 'install' | 'restart'
  downloaded: number
  total: number
}

export interface AdminLog {
  time: string
  level: 'INFO' | 'WARN' | 'ERROR'
  source: string
  message: string
  event: string
  request?: RequestLog
}

// RequestLog 对应请求日志的结构化载荷
interface RequestLog {
  id: string
  state: 'running' | 'completed' | 'tool_calls' | 'limited' | 'blocked' | 'failed' | 'cancelled'
  started_at?: string
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
interface ModelDetails {
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
interface Language {
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
  // agent_model 是 agent 默认使用的模型 ID
  agent_model?: string
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

// FirstEventLatency 是一个模型与通道近期原请求首事件等待的 p95 与样本数
export interface FirstEventLatency {
  model: string
  channel: UpstreamChannel
  samples: number
  p95_ms: number
}

// PathListing 是路径选择器当前位置的内容，path 为空表示文件系统根列表
export interface PathListing {
  path: string
  parent: string
  entries: { name: string; path: string; dir: boolean }[]
}

export interface ServiceConfig {
  auto_start: boolean
  hide_dock_icon: boolean
  request_body_log: boolean
  log_file: boolean
  log_max_total_mb: number
  admin_auth_enabled: boolean
  admin_username: string
  admin_password?: string
  admin_password_set: boolean
  build_native_nonstream: boolean
  nonstream_keepalive: boolean
  fake_stream: boolean
  youtube_links: boolean
  search_model_aliases: boolean
  google_search: boolean
  continuation_rewrite: boolean
  continuation_prompt: string
  strip_thought_signatures: boolean
  first_event_hedge: string
  first_event_hedge_models: string
  auth_states: string
  listen_addr: string
  preview_listen_addr: string
  proxy_api_key: string
  active_listen_addr: string
  reserved_listen_addr: string
  active_preview_listen_addr: string
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
  account_order: string
  upstream_channels: UpstreamChannel[]
  model_mapping: string
  model_channels: string
  model_accounts: string
  api_key_accounts: string
  waa_backend: 'camoufox' | 'spidermonkey'
  media_output: 'inline' | 'url'
  media_base_url: string
  environment: string[]
  invalid: InvalidConfigValue[]
}

// InvalidConfigValue 是加载时取值无效、按默认值生效的配置项
export interface InvalidConfigValue {
  key: string
  value: string
  reason: string
}

export type UsageDimension = 'model' | 'account' | 'channel' | 'protocol' | 'state'

export type UsageFilters = Partial<Record<UsageDimension, string[]>>

interface UsageLatency {
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

interface UsageBucket extends UsageStats {
  at: string
  cost: number
}

interface UsageGroup extends UsageStats {
  key: string
}

interface UsagePair extends UsageStats {
  account: string
  model: string
}

interface UsageSeries {
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

interface RequestAttempt {
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
  | { type: 'account_tasks'; data: { tasks: AccountTask[] } }
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

// SystemInstructionPreset 是保存在服务端对话库中的系统指令预设
export interface SystemInstructionPreset {
  id: string
  title: string
  text: string
}
