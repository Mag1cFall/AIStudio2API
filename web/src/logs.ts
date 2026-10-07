import type { AdminLog } from './types'

// LogRow 保存请求的最新状态与完整事件时间线
export interface LogRow {
  key: string
  entry: AdminLog
  events: AdminLog[]
}

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

// groupLogs 按请求标识合并生命周期并保持服务事件的原始顺序
export function groupLogs(logs: AdminLog[]): LogRow[] {
  const rows: LogRow[] = []
  const requests = new Map<string, LogRow>()
  for (const entry of logs) {
    const id = entry.request?.id
    const row = id ? requests.get(id) : undefined
    if (row) {
      row.events.push(entry)
      row.entry = { ...entry, request: { ...row.entry.request!, ...entry.request! } }
    } else {
      const next = { key: id || entryKey(entry), entry, events: [entry] }
      rows.push(next)
      if (id) requests.set(id, next)
    }
  }
  return rows
}
