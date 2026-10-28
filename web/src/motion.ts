// standardDuration 是展开与收起过渡的时长，单位毫秒
export const standardDuration = 225

// standardEasing 是展开与收起过渡的缓动曲线
export const standardEasing = 'cubic-bezier(0.4, 0, 0.2, 1)'

// bezier 返回控制点为 a、b 的三次贝塞尔分量在参数 t 处的值
function bezier(a: number, b: number, t: number): number {
  return 3 * (1 - t) ** 2 * t * a + 3 * (1 - t) * t ** 2 * b + t ** 3
}

// standardEase 返回 standardEasing 在时间进度 progress 处的位移进度
export function standardEase(progress: number): number {
  let t = progress
  for (let i = 0; i < 8; i++) {
    const error = bezier(0.4, 0.2, t) - progress
    if (Math.abs(error) < 1e-5) break
    const slope = 3 * (1 - t) ** 2 * 0.4 + 6 * (1 - t) * t * (0.2 - 0.4) + 3 * t ** 2 * (1 - 0.2)
    if (slope === 0) break
    t = Math.min(1, Math.max(0, t - error / slope))
  }
  return bezier(0, 1, t)
}

// reducedMotion 判断系统是否要求减少动态效果
export function reducedMotion(): boolean {
  return matchMedia('(prefers-reduced-motion: reduce)').matches
}
