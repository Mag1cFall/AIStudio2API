import type { AdminLog } from './types'

// LogRow 保存请求或阶段的最新状态与完整事件时间线
export interface LogRow {
  key: string
  entry: AdminLog
  events: AdminLog[]
  stage?: LogStage
}

// LogStageState 是分阶段运行日志的进行状态
export type LogStageState = 'running' | 'completed' | 'failed' | 'cancelled'

// LogStage 是同一来源分阶段运行日志合并后的进度
export interface LogStage {
  name: string
  step: number
  steps: number
  detail: string
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

// formatLogNumber 按语言格式化日志统计数值
export function formatLogNumber(value: number, locale: string, digits = 0): string {
  const key = `${locale}:${digits}`
  let format = numberFormats.get(key)
  if (format === undefined) {
    format = new Intl.NumberFormat(locale, { maximumFractionDigits: digits })
    numberFormats.set(key, format)
  }
  return format.format(value)
}

// stagePattern 匹配“阶段名 | i/N | 详情”形式的阶段步骤消息
const stagePattern = /^(.+?) \| (\d+)\/(\d+)(?: \| ([\s\S]*))?$/

// stageReadyTitles 是就绪消息标题对应的阶段名
const stageReadyTitles: Record<string, string> = {
  协议运行时就绪: '运行时装配',
  生成服务就绪: '生成服务启动',
  'WAA Worker 就绪': 'WAA Worker 启动',
}

// stageEndSuffixes 是阶段名加后缀构成的结束消息标题对应的状态
const stageEndSuffixes: [string, LogStageState][] = [
  ['完成', 'completed'],
  ['失败', 'failed'],
  ['已取消', 'cancelled'],
]

// stageEnd 返回结束消息标题所属的阶段名与结束状态
function stageEnd(title: string): [string, LogStageState] | undefined {
  const ready = stageReadyTitles[title]
  if (ready !== undefined) return [ready, 'completed']
  for (const [suffix, state] of stageEndSuffixes)
    if (title.endsWith(suffix)) return [title.slice(0, -suffix.length), state]
  return undefined
}

// levelRanks 是日志级别的严重程度
const levelRanks: Record<string, number> = { INFO: 1, WARN: 2, ERROR: 3 }

// severerLevel 返回两个日志级别中更严重的一个
function severerLevel(current: string, next: string): string {
  return (levelRanks[next.toUpperCase()] ?? 0) > (levelRanks[current.toUpperCase()] ?? 0)
    ? next
    : current
}

// mergeStage 把一条运行日志并入阶段卡片，卡片保持首条消息的时间并取最严重的级别
function mergeStage(row: StageRow, entry: AdminLog, detail: string): void {
  row.events.push(entry)
  row.entry = { ...row.events[0]!, level: severerLevel(row.entry.level, entry.level) }
  row.stage.detail = detail
}

// groupStage 按来源与阶段名把运行日志并入阶段卡片，不属于阶段卡片时返回 false
function groupStage(rows: LogRow[], stages: Map<string, StageRow>, entry: AdminLog): boolean {
  const step = stagePattern.exec(entry.message)
  if (step) {
    const [, name = '', index, total, detail = ''] = step
    const key = `${entry.source}\n${name}`
    const open = stages.get(key)
    if (open && Number(index) > open.stage.step) {
      mergeStage(open, entry, detail)
      open.stage.step = Number(index)
      open.stage.steps = Number(total)
      return true
    }
    const row: StageRow = {
      key: entryKey(entry),
      entry,
      events: [entry],
      stage: { name, step: Number(index), steps: Number(total), detail, state: 'running' },
    }
    rows.push(row)
    stages.set(key, row)
    return true
  }
  const separator = entry.message.indexOf(' | ')
  const title = separator < 0 ? entry.message : entry.message.slice(0, separator)
  const open = stages.get(`${entry.source}\n${title}`)
  if (open) {
    mergeStage(open, entry, separator < 0 ? '' : entry.message.slice(separator + 3))
    return true
  }
  const end = stageEnd(title)
  if (end === undefined) return false
  const key = `${entry.source}\n${end[0]}`
  const ended = stages.get(key)
  if (ended === undefined) return false
  mergeStage(ended, entry, entry.message)
  ended.stage.state = end[1]
  stages.delete(key)
  return true
}

// groupLogs 按请求标识合并生命周期、按来源与阶段名合并分阶段运行日志，并保持服务事件的原始顺序
export function groupLogs(logs: AdminLog[]): LogRow[] {
  const rows: LogRow[] = []
  const requests = new Map<string, LogRow>()
  const stages = new Map<string, StageRow>()
  for (const entry of logs) {
    const id = entry.request?.id
    const row = id ? requests.get(id) : undefined
    if (row) {
      row.events.push(entry)
      row.entry = { ...entry, request: { ...row.entry.request!, ...entry.request! } }
    } else if (id || entry.event !== 'runtime.message' || !groupStage(rows, stages, entry)) {
      const next = { key: id || entryKey(entry), entry, events: [entry] }
      rows.push(next)
      if (id) requests.set(id, next)
    }
  }
  return rows
}
