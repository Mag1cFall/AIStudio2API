export interface CooldownMetric {
  account_id: string
  model_id: string
  until: string
}

export interface ModelCooldown {
  model: string
  accounts: number
  until: string
}

// groupCooldowns 按模型汇总冷却中的账号数与最早恢复时间，冷却账号多的模型排前面
export function groupCooldowns(cooldowns: CooldownMetric[]): ModelCooldown[] {
  const groups = new Map<string, { accounts: Set<string>; until: string }>()
  for (const cooldown of cooldowns) {
    const model = cooldown.model_id || '*'
    const group = groups.get(model) ?? { accounts: new Set<string>(), until: cooldown.until }
    group.accounts.add(cooldown.account_id)
    if (Date.parse(cooldown.until) < Date.parse(group.until)) group.until = cooldown.until
    groups.set(model, group)
  }
  return [...groups.entries()]
    .map(([model, group]) => ({ model, accounts: group.accounts.size, until: group.until }))
    .sort((left, right) => right.accounts - left.accounts || left.model.localeCompare(right.model))
}

// pacificDayStart 返回给定时刻所在的太平洋时区当天零点
export function pacificDayStart(now: Date): Date {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'America/Los_Angeles',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(now)
  const value = (type: string) => Number(parts.find((part) => part.type === type)?.value ?? 0)
  const midnightUTC = Date.UTC(value('year'), value('month') - 1, value('day'))
  return new Date(midnightUTC - zoneOffset(new Date(midnightUTC), 'America/Los_Angeles'))
}

// zoneOffset 返回指定时刻在该时区相对 UTC 的毫秒偏移
function zoneOffset(date: Date, timeZone: string): number {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hour12: false,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).formatToParts(date)
  const value = (type: string) => Number(parts.find((part) => part.type === type)?.value ?? 0)
  const asUTC = Date.UTC(
    value('year'),
    value('month') - 1,
    value('day'),
    value('hour') % 24,
    value('minute'),
    value('second'),
  )
  return asUTC - date.getTime()
}
