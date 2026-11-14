import type { AdminLog, LogField } from './types'

// LogRow 保存请求或阶段的最新状态与完整事件时间线
export interface LogRow {
  key: string
  entry: AdminLog
  events: AdminLog[]
  stage?: LogStage
}

// LogStageState 是分阶段运行日志的进行状态
export type LogStageState = 'running' | 'completed' | 'failed' | 'cancelled'

// LogStage 是同一来源分阶段运行日志合并后的进度，steps 为 0 表示阶段没有编号的步骤
interface LogStage {
  name: string
  step: number
  steps: number
  state: LogStageState
}

// StageRow 是带阶段进度的日志行
type StageRow = LogRow & { stage: LogStage }

// entryKeys 为非请求日志分配不随数组截断变化的行标识
const entryKeys = new WeakMap<AdminLog, string>()
let nextEntryKey = 0

// entryKey 返回日志对象的稳定行标识
function entryKey(entry: AdminLog): string {
  let key = entryKeys.get(entry)
  if (key === undefined) {
    key = `log:${nextEntryKey++}`
    entryKeys.set(entry, key)
  }
  return key
}

// numberFormats 按语言与小数位缓存日志数值格式器
const numberFormats = new Map<string, Intl.NumberFormat>()

// formatLogNumber 按语言格式化日志统计数值，给出 unit 时带上该语言的时间单位
export function formatLogNumber(
  value: number,
  locale: string,
  digits = 0,
  unit?: 'millisecond' | 'second' | 'minute',
): string {
  const key = `${locale}:${digits}:${unit}`
  let format = numberFormats.get(key)
  if (format === undefined) {
    format = new Intl.NumberFormat(locale, {
      maximumFractionDigits: digits,
      ...(unit && { style: 'unit', unit }),
    })
    numberFormats.set(key, format)
  }
  return format.format(value)
}

// goDurationUnits 是 Go 时长文字各单位的毫秒数
const goDurationUnits: Record<string, number> = {
  h: 3_600_000,
  m: 60_000,
  s: 1000,
  ms: 1,
  µs: 0.001,
  us: 0.001,
  ns: 0.000_001,
}

// parseGoDuration 把 Go 时长文字（如 23ms、3.644s、1m30s）换算为毫秒，不是时长时返回 undefined
export function parseGoDuration(text: string): number | undefined {
  if (!/^(?:\d+(?:\.\d+)?(?:ms|µs|us|ns|h|m|s))+$/.test(text)) return undefined
  let total = 0
  for (const [, value, unit] of text.matchAll(/(\d+(?:\.\d+)?)(ms|µs|us|ns|h|m|s)/g))
    total += Number(value) * goDurationUnits[unit!]!
  return total
}

// formatLogDuration 按语言格式化日志时长：不足 1 秒用毫秒，不足 1 分钟用秒，其余用分钟
export function formatLogDuration(ms: number, locale: string): string {
  if (ms < 1000) return formatLogNumber(ms, locale, 0, 'millisecond')
  if (ms < 60_000) return formatLogNumber(ms / 1000, locale, 2, 'second')
  return formatLogNumber(ms / 60_000, locale, 1, 'minute')
}

// prettyBody 缩进 JSON 正文，非 JSON 保持原文
export function prettyBody(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

// byteLength 返回文本的 UTF-8 字节数
export function byteLength(text: string): number {
  return new TextEncoder().encode(text).length
}

// logField 返回日志中指定字段的取值
export function logField(entry: AdminLog, key: string): string | undefined {
  return entry.fields?.find((field) => field.key === key)?.value
}

// attemptMessages 是上游尝试失败后换账户重试的日志消息
const attemptMessages = new Set(['账号切换', '续写轮账号切换'])

// isAttempt 判断日志是否记录一次失败后换账户的上游尝试
export function isAttempt(entry: AdminLog): boolean {
  return attemptMessages.has(entry.message)
}

// TimelineItem 是请求时间线的一行：event 为来源、级别与消息相同的连续进度事件，attempt 为一次失败后换账户的尝试及其全部事件
export type TimelineItem =
  | { kind: 'event'; events: AdminLog[] }
  | { kind: 'attempt'; attempt: AdminLog; events: AdminLog[] }

// requestTimeline 把请求事件整理为时间线：每次换账户前的事件并入该次尝试，其余连续的同类进度事件合为一行
export function requestTimeline(events: AdminLog[]): TimelineItem[] {
  const items: TimelineItem[] = []
  let pending: AdminLog[] = []
  const flush = () => {
    for (const event of pending) appendEvent(items, event)
    pending = []
  }
  for (const event of events) {
    if (event.event !== 'request.progress') {
      flush()
      appendEvent(items, event)
    } else if (isAttempt(event)) {
      items.push({ kind: 'attempt', attempt: event, events: [...pending, event] })
      pending = []
    } else {
      pending.push(event)
    }
  }
  flush()
  return items
}

// appendEvent 把事件追加到时间线，与上一行同为来源、级别与消息相同的进度事件时并入该行
function appendEvent(items: TimelineItem[], event: AdminLog): void {
  const last = items.at(-1)
  const previous = last?.kind === 'event' ? last.events.at(-1)! : undefined
  if (
    previous?.event === 'request.progress' &&
    event.event === 'request.progress' &&
    previous.source === event.source &&
    previous.level === event.level &&
    previous.message === event.message
  ) {
    last!.events.push(event)
    return
  }
  items.push({ kind: 'event', events: [event] })
}

// blockFieldKeys 是另起一行显示的长字段
const blockFieldKeys = new Set(['error', 'reason'])

// isBlockField 判断日志字段是否另起一行显示
export function isBlockField(field: LogField): boolean {
  return blockFieldKeys.has(field.key)
}

// levelRanks 是日志级别的严重程度
const levelRanks: Record<AdminLog['level'], number> = { INFO: 1, WARN: 2, ERROR: 3 }

// severerLevel 返回两个日志级别中更严重的一个
function severerLevel(current: AdminLog['level'], next: AdminLog['level']): AdminLog['level'] {
  return levelRanks[next] > levelRanks[current] ? next : current
}

// mergeStage 把一条运行日志并入阶段卡片，卡片保持首条消息的时间并取最严重的级别
function mergeStage(row: StageRow, entry: AdminLog): void {
  row.events.push(entry)
  row.entry = { ...row.events[0]!, level: severerLevel(row.entry.level, entry.level) }
}

// groupStage 按来源与阶段名把运行日志并入阶段卡片：结束日志结束进行中的卡片，步骤号不大于当前步骤的日志开始新卡片，不属于阶段卡片时返回 false
function groupStage(rows: LogRow[], stages: Map<string, StageRow>, entry: AdminLog): boolean {
  const stage = entry.stage
  if (stage === undefined) return false
  const key = `${entry.source}\n${stage.name}`
  const open = stages.get(key)
  if (stage.state !== undefined) {
    if (open === undefined) return false
    mergeStage(open, entry)
    open.stage.state = stage.state
    stages.delete(key)
    return true
  }
  if (open !== undefined && (stage.step === undefined || stage.step > open.stage.step)) {
    mergeStage(open, entry)
    if (stage.step !== undefined) {
      open.stage.step = stage.step
      open.stage.steps = stage.steps ?? 0
    }
    return true
  }
  const row: StageRow = {
    key: entryKey(entry),
    entry,
    events: [entry],
    stage: { name: stage.name, step: stage.step ?? 0, steps: stage.steps ?? 0, state: 'running' },
  }
  rows.push(row)
  stages.set(key, row)
  return true
}

// requestSources 返回请求依次用到的账户，还没有账户事件时返回请求自身的来源
export function requestSources(row: LogRow): string[] {
  const sources = new Set(
    row.events.filter((event) => event.source !== 'request').map((event) => event.source),
  )
  return sources.size > 0 ? [...sources] : [row.entry.source]
}

// groupLogs 按请求标识合并生命周期、按来源与阶段名合并分阶段运行日志，并保持服务事件的原始顺序；previous 中事件相同的行沿用原对象

export function groupLogs(logs: AdminLog[], previous: ReadonlyMap<string, LogRow>): LogRow[] {
  const rows: LogRow[] = []
  const requests = new Map<string, LogRow>()
  const stages = new Map<string, StageRow>()
  for (const entry of logs) {
    const id = entry.request?.id
    const row = id ? requests.get(id) : undefined
    if (row) {
      row.events.push(entry)
      row.entry = { ...entry, request: { ...row.entry.request!, ...entry.request! } }
    } else if (id || !groupStage(rows, stages, entry)) {
      const next = { key: id || entryKey(entry), entry, events: [entry] }
      rows.push(next)
      if (id) requests.set(id, next)
    }
  }
  return rows.map((row) => {
    const old = previous.get(row.key)
    return old !== undefined &&
      old.events.length === row.events.length &&
      old.events[0] === row.events[0] &&
      old.events.at(-1) === row.events.at(-1)
      ? old
      : row
  })
}
