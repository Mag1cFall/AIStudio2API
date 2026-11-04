// splitKeys 返回逗号分隔的 API key 列表中去掉首尾空白的各个非空 key
export function splitKeys(value: string): string[] {
  return value
    .split(',')
    .map((key) => key.trim())
    .filter(Boolean)
}

// maskKey 以圆点加末 4 位显示 API key，同一列表中的各个 key 由末 4 位区分
export function maskKey(key: string): string {
  return `••••${key.slice(-4)}`
}
