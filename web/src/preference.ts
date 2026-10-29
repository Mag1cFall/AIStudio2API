// readPreference 读取浏览器保存的界面偏好，没有保存或取值不合法时返回默认值
export function readPreference<T>(key: string, fallback: T, valid: (value: unknown) => boolean): T {
  const raw = window.localStorage.getItem(key)
  if (raw === null) return fallback
  const value: unknown = JSON.parse(raw)
  return valid(value) ? (value as T) : fallback
}

// writePreference 保存界面偏好
export function writePreference(key: string, value: unknown): void {
  window.localStorage.setItem(key, JSON.stringify(value))
}
