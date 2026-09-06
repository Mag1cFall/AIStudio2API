import type { AdminLog } from './types'

// LogRow 保存请求的最新状态与完整事件时间线
export interface LogRow {
  key: string
  entry: AdminLog
  events: AdminLog[]
}

// groupLogs 按请求标识合并生命周期并保持服务事件的原始顺序
export function groupLogs(logs: AdminLog[]): LogRow[] {
  const rows: LogRow[] = []
  const requests = new Map<string, LogRow>()
  for (const [index, entry] of logs.entries()) {
    const id = entry.request?.id
    const row = id ? requests.get(id) : undefined
    if (row) {
      row.events.push(entry)
      row.entry = { ...entry, request: { ...row.entry.request!, ...entry.request! } }
    } else {
      const next = { key: id || `${entry.time}:${index}`, entry, events: [entry] }
      rows.push(next)
      if (id) requests.set(id, next)
    }
  }
  return rows
}
