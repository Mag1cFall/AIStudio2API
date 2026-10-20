import type {
  Account,
  AccountDraft,
  AccountLoginInput,
  AccountRefresh,
  AdminEvent,
  ChromeImportInput,
  CookieBrowser,
  CookieImportInput,
  ChromeImportProfile,
  PairingToken,
  Model,
  PromptInput,
  PromptSummary,
  SavedPrompt,
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

export interface EventConnection {
  close: () => void
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

// responseError 提取四套公开协议与管理接口共享的错误码与错误消息
async function responseError(response: Response): Promise<ApiError> {
  const body = await response.text()
  if (body === '') return new ApiError(response.statusText, response.status)
  if (!response.headers.get('content-type')?.includes('application/json')) {
    return new ApiError(body, response.status)
  }

  const value: unknown = JSON.parse(body)
  const message = pathValue(value, ['error', 'message'])
  const code = pathValue(value, ['error', 'code'])
  return new ApiError(
    typeof message === 'string' ? message : body,
    response.status,
    typeof code === 'string' ? code : '',
  )
}

// requestJSON 执行管理端 JSON 请求并保留服务端错误语义
async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  if (init?.body !== undefined) {
    headers.set('Content-Type', 'application/json')
  }

  const response = await fetch(path, { ...init, headers })
  checkSessionResponse(path, response)
  if (!response.ok) {
    throw await responseError(response)
  }

  return (await response.json()) as T
}

// requestCommand 执行无需响应体的管理写操作
async function requestCommand(path: string, init: RequestInit): Promise<void> {
  const headers = new Headers(init.headers)
  if (init.body !== undefined) {
    headers.set('Content-Type', 'application/json')
  }

  const response = await fetch(path, { ...init, headers })
  checkSessionResponse(path, response)
  if (!response.ok) {
    throw await responseError(response)
  }
}

// parseAdminEvent 校验单一事件流的事件外壳
function parseAdminEvent(raw: string): AdminEvent | undefined {
  const value: unknown = JSON.parse(raw)
  if (typeof value !== 'object' || value === null || !('type' in value) || !('data' in value)) {
    return undefined
  }

  const type = value.type
  if (
    type !== 'status' &&
    type !== 'log' &&
    type !== 'accounts' &&
    type !== 'models' &&
    type !== 'cooldowns' &&
    type !== 'request'
  ) {
    return undefined
  }

  return value as AdminEvent
}

export const api = {
  session: () => requestJSON<AdminSession>('/api/auth/session'),
  login: (username: string, password: string, remember: boolean) =>
    requestJSON<AdminSession>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password, remember }),
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
  usage: (query: URLSearchParams, signal?: AbortSignal) =>
    requestJSON<UsageReport>(`/api/usage?${query.toString()}`, { signal: signal ?? null }),
  usageRecords: (query: URLSearchParams, signal?: AbortSignal) =>
    requestJSON<UsageRecordPage>(`/api/usage/records?${query.toString()}`, {
      signal: signal ?? null,
    }),
  usageExportURL: (query: URLSearchParams) => `/api/usage/records.csv?${query.toString()}`,
  requestBody: (id: string) =>
    requestJSON<RequestBody>(`/api/requests/${encodeURIComponent(id)}/body`),
  createAccount: (input: AccountLoginInput, view: string, signal: AbortSignal) =>
    requestJSON<AccountResponse>('/api/accounts', {
      method: 'POST',
      body: JSON.stringify({ ...input, view }),
      signal,
    }),
  chromeImportProfiles: async () =>
    (await requestJSON<ChromeProfilesResponse>('/api/accounts/import/chrome')).profiles,
  importChromeAccounts: (input: ChromeImportInput) =>
    requestJSON<AccountsResponse>('/api/accounts/import/chrome', {
      method: 'POST',
      body: JSON.stringify(input),
    }),
  cookieBrowsers: async () =>
    (await requestJSON<{ browsers: CookieBrowser[] }>('/api/accounts/import/browsers')).browsers,
  importCookieAccount: (input: CookieImportInput) =>
    requestJSON<AccountResponse>('/api/accounts/import/cookies', {
      method: 'POST',
      body: JSON.stringify(input),
    }),
  createPairing: () => requestJSON<PairingToken>('/api/pairing', { method: 'POST' }),
  updateAccount: (id: string, draft: AccountDraft) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(draft),
    }),
  deleteAccount: (id: string, keepalive = false) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}`, { method: 'DELETE', keepalive }),
  loginAccount: (id: string, view: string, signal: AbortSignal) =>
    requestCommand(`/api/accounts/${encodeURIComponent(id)}/login`, {
      method: 'POST',
      body: JSON.stringify({ view }),
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
  saveConfig: (config: ServiceConfig) =>
    requestJSON<ServiceConfig>('/api/config', {
      method: 'PUT',
      body: JSON.stringify(config),
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
  prompt: <T>(id: string) => requestJSON<SavedPrompt<T>>(`/api/prompts/${encodeURIComponent(id)}`),
  savePrompt: <T>(id: string, prompt: PromptInput<T>, keepalive = false) =>
    requestJSON<PromptSummary>(`/api/prompts/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(prompt),
      keepalive,
    }),
  deletePrompt: (id: string) =>
    requestCommand(`/api/prompts/${encodeURIComponent(id)}`, { method: 'DELETE' }),
}

// openAdminEvents 建立唯一的管理状态 SSE 连接
export function openAdminEvents(
  onEvent: (event: AdminEvent) => void,
  onOpen: () => void,
): EventConnection {
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
  source.onmessage = (message) => {
    const event = parseAdminEvent(message.data)
    if (event !== undefined) {
      onEvent(event)
    }
  }

  return {
    close: () => source.close(),
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

// pathValue 读取公开协议中已知的嵌套字段
function pathValue(value: unknown, path: readonly (string | number)[]): unknown {
  let current = value
  for (const part of path) {
    if (typeof part === 'number') {
      if (!Array.isArray(current)) return undefined
      current = current[part]
      continue
    }
    if (!isRecord(current)) return undefined
    current = current[part]
  }
  return current
}
