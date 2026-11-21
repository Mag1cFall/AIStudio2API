// splitReason 把冷却原因拆成冷却类型与上游错误原文
export function splitReason(reason: string | undefined): { category: string; text: string } {
  const value = reason ?? ''
  const separator = value.indexOf(': ')
  if (separator <= 0) return { category: '', text: value }
  return { category: value.slice(0, separator), text: value.slice(separator + 2) }
}
