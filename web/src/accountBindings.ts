// parseAccountBindings 解析逗号分隔的 键:账户|账户，按最后一个冒号分出键，返回键到账户 ID 的映射
export function parseAccountBindings(value: string): Map<string, string[]> {
  const bindings = new Map<string, string[]>()
  for (const item of value.split(',')) {
    const index = item.lastIndexOf(':')
    const ids = item
      .slice(index + 1)
      .split('|')
      .map((id) => id.trim())
      .filter(Boolean)
    if (index > 0 && ids.length > 0) bindings.set(item.slice(0, index).trim(), ids)
  }
  return bindings
}
