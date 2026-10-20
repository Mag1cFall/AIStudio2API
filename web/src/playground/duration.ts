// formatDuration 与官网一致：按时分秒显示，秒保留一位小数并去掉末尾的 .0
export function formatDuration(seconds: number): string {
  if (seconds < 0) return ''
  if (seconds === 0) return '0s'
  let hours = Math.floor(seconds / 3600)
  const rest = seconds % 3600
  let minutes = Math.floor(rest / 60)
  let secs = Number((rest % 60).toFixed(1))
  if (secs >= 60) {
    secs = 0
    minutes++
  }
  if (minutes >= 60) {
    minutes = 0
    hours++
  }
  let text = ''
  if (hours > 0) text += `${hours}h`
  if (minutes > 0) text += `${minutes}m`
  if (secs > 0 || text === '') text += `${secs}s`
  return text
}
