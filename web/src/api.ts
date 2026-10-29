import { ref } from 'vue'
import { keepPage } from '@/desktop'
import type {
  Account,
  AccountDraft,
  AccountLoginInput,
  AccountRefresh,
  AdminEvent,
  ChromeImportInput,
  CookieBrowser,
  CookieImportInput,
  FirstEventLatency,
  ChromeImportProfile,
  PairingToken,
  Model,
  PromptInput,
  PromptSummary,
  SavedPrompt,
  SystemInstructionPreset,
  Cooldown,
  RequestBody,
  RequestSummary,
  ServiceConfig,
  ServiceStatus,
  UpdateStatus,
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
  readonly code: string

  constructor(message: string, status: number, code = '') {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

// responseError 提取管理接口的错误码与错误消息
async function responseError(response: Response): Promise<ApiError> {
  const body = await response.text()
  if (body === '') return new ApiError(response.statusText, response.status)
  if (!response.headers.get('content-type')?.includes('application/json')) {
    return new ApiError(body, response.status)
  }
  const { error } = JSON.parse(body) as { error: { message: string; code: string } }
  return new ApiError(error.message, response.status, error.code)
}

// jsonBody 把请求正文序列化为 JSON Blob
export function jsonBody(value: unknown): Blob {
  return new Blob([JSON.stringify(value)], { type: 'application/json' })
}

// writes 是进行中的管理写请求数，关闭桌面窗口时有写请求则保留页面
const writes = ref(0)
keepPage(() => writes.value > 0)

// send 发出管理端请求并保留服务端错误语义，写请求进行中时计入 writes
async function send(path: string, init?: RequestInit): Promise<Response> {
  const write = init?.method !== undefined && init.method !== 'GET'
  if (write) writes.value++
  try {
    const response = await fetch(path, init)
    checkSessionResponse(path, response)
    if (!response.ok) {
      throw await responseError(response)
    }
    return response
  } finally {
    if (write) writes.value--
  }
}

// requestJSON 执行管理端 JSON 请求
async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  return (await (await send(path, init)).json()) as T
}

// requestCommand 执行无需响应体的管理写操作
async function requestCommand(path: string, init: RequestInit): Promise<void> {
  await send(path, init)
}

export const api = {
  session: () => requestJSON<AdminSession>('/api/auth/session'),
  login: (username: string, password: string, remember: boolean) =>
    requestJSON<AdminSession>('/api/auth/login', {
      method: 'POST',
      body: jsonBody({ username, password, remember }),
    }),
  logout: () => requestCommand('/api/auth/logout', { method: 'POST' }),
  status: () => requestJSON<ServiceStatus>('/api/status'),
  updateStatus: (refresh = false) =>
    requestJSON<UpdateStatus>(refresh ? '/api/update?refresh=1' : '/api/update'),
  applyUpdate: () => requestJSON<UpdateStatus>('/api/update', { method: 'POST' }),
  accounts: async () => (await requestJSON<AccountsResponse>('/api/accounts')).accounts,
  models: async () => (await requestJSON<ModelsResponse>('/api/models')).models,
  cooldowns: async () => (await requestJSON<CooldownsResponse>('/api/cooldowns')).cooldowns,
  requests: async () => (await requestJSON<RequestsResponse>('/api/requests')).requests,
  config: () => requestJSON<ServiceConfig>('/api/config'),
  usage: (query: URLSearchParams, signal: AbortSignal) =>
    requestJSON<UsageReport>(`/api/usage?${query.toString()}`, { signal }),
  usageRecords: (query: URLSearchParams, signal: AbortSignal) =>
    requestJSON<UsageRecordPage>(`/api/usage/records?${query.toString()}`, { signal }),
  usageExportURL: (query: URLSearchParams) => `/api/usage/records.csv?${query.toString()}`,
  requestBody: (id: string) =>
    requestJSON<RequestBody>(`/api/requests/${encodeURIComponent(id)}/body`),
  createAccount: (input: AccountLoginInput, view: string, signal: AbortSignal) =>
    requestJSON<AccountResponse>('/api/accounts', {
      method: 'POST',
      body: jsonBody({ ...input, view }),
      signal,
    }),
  chromeImportProfiles: async () =>
    (await requestJSON<ChromeProfilesResponse>('/api/accounts/import/chrome')).profiles,
  importChromeAccounts: (input: ChromeImportInput) =>
    requestJSON<AccountsResponse>('/api/accounts/import/chrome', {
      method: 'POST',
      body: jsonBody(input),
    }),
  cookieBrowsers: async () =>
    (await requestJSON<{ browsers: CookieBrowser[] }>('/api/accounts/import/browsers')).browsers,
  discoverCookieAccounts: async (input: CookieImportInput) =>
    (
      await requestJSON<{ accounts: CookieBrowser[] }>('/api/accounts/import/cookies/discover', {
        method: 'POST',
        body: jsonBody(input),
      })
    ).accounts,
  importCookieAccount: (input: CookieImportInput) =>
    requestJSON<AccountResponse>('/api/accounts/import/cookies', {
      method: 'POST',
      body: jsonBody(input),
    }),
  createPairing: () => requestJSON<PairingToken>('/api/pairing', { method: 'POST' }),
  updateAccount: (id: string, draft: AccountDraft) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: jsonBody(draft),
    }),
  deleteAccount: (id: string, keepalive: boolean) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}`, { method: 'DELETE', keepalive }),
  loginAccount: (id: string, view: string, signal: AbortSignal) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}/login`, {
      method: 'POST',
      body: jsonBody({ view }),
      signal,
    }),
  loginViewURL: (view: string) =>
    `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/api/login-view/${encodeURIComponent(view)}`,
  refreshAccount: (id: string) =>
    requestJSON<AccountRefresh>(`/api/accounts/${encodeURIComponent(id)}/refresh`, {
      method: 'POST',
    }),
  startService: () => requestJSON<ServiceStatus>('/api/control/start', { method: 'POST' }),
  stopService: () => requestJSON<ServiceStatus>('/api/control/stop', { method: 'POST' }),
  clearLogs: () => requestCommand('/api/logs', { method: 'DELETE' }),
  firstEventLatency: async () =>
    (await requestJSON<{ latencies: FirstEventLatency[] }>('/api/first-event-latency')).latencies,
  saveConfig: (config: ServiceConfig) =>
    requestJSON<ServiceConfig>('/api/config', {
      method: 'PUT',
      body: jsonBody(config),
    }),
  clearCooldown: (accountID: string, scope: string) =>
    requestCommand(
      `/api/accounts/${encodeURIComponent(accountID)}/cooldowns/${encodeURIComponent(scope)}`,
      { method: 'DELETE' },
    ),
  cancelRequest: (id: string) =>
    requestCommand(`/api/requests/${encodeURIComponent(id)}/cancel`, {
      method: 'POST',
    }),
  prompts: async () => (await requestJSON<{ prompts: PromptSummary[] }>('/api/prompts')).prompts,
  promptPage: (pageSize: number, pageToken: string) => {
    const query = new URLSearchParams({ page_size: String(pageSize) })
    if (pageToken !== '') query.set('page_token', pageToken)
    return requestJSON<{ prompts: PromptSummary[]; next_page_token?: string }>(
      `/api/prompts?${query.toString()}`,
    )
  },
  prompt: <T>(id: string) => requestJSON<SavedPrompt<T>>(`/api/prompts/${encodeURIComponent(id)}`),
  savePrompt: <T>(id: string, prompt: PromptInput<T>, keepalive = false) =>
    requestJSON<PromptSummary>(`/api/prompts/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: jsonBody(prompt),
      keepalive,
    }),
  renamePrompt: (id: string, title: string, description: string) =>
    requestJSON<PromptSummary>(`/api/prompts/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: jsonBody({ title, description }),
    }),
  copyPrompt: (id: string, copy: string, title: string) =>
    requestJSON<PromptSummary>(`/api/prompts/${encodeURIComponent(id)}/copy`, {
      method: 'POST',
      body: jsonBody({ id: copy, title }),
    }),
  deletePrompt: (id: string) =>
    requestCommand(`/api/prompts/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  generateTitle: async (model: string, prompt: string, answer: string) =>
    (
      await requestJSON<{ title: string }>('/api/prompts/title', {
        method: 'POST',
        body: jsonBody({ model, prompt, answer }),
      })
    ).title,
  systemInstructions: async () =>
    (await requestJSON<{ presets: SystemInstructionPreset[] }>('/api/prompts/system-instructions'))
      .presets,
  saveSystemInstruction: (preset: SystemInstructionPreset) =>
    requestCommand(`/api/prompts/system-instructions/${encodeURIComponent(preset.id)}`, {
      method: 'PUT',
      body: jsonBody(preset),
    }),
  deleteSystemInstruction: (id: string) =>
    requestCommand(`/api/prompts/system-instructions/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),
}

// openAdminEvents 建立唯一的管理状态 SSE 连接
export function openAdminEvents(
  onEvent: (event: AdminEvent) => void,
  onOpen: () => void,
): EventSource {
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
  source.onmessage = (message: MessageEvent<string>) =>
    onEvent(JSON.parse(message.data) as AdminEvent)
  return source
}
