import type { Model } from '@/types'
import type { InlineImage } from './api'

// VideoFrame 是视频示例代码中的一张帧图片或参考图
export interface VideoFrame extends InlineImage {
  name: string
}

// VideoSettings 是视频示例代码使用的模型与运行设置
export interface VideoSettings {
  model: string
  aspectRatio: string
  durationSeconds: number
  resolution: string
}

// resolutionOrder 按清晰度排列分辨率
const resolutionOrder = ['720p', '1080p', '4k']

// optionList 读取模型的视频选项
function optionList(model: Model | undefined, key: string): string[] {
  return model?.capability_options?.[key] ?? []
}

// defaultSettings 返回视频示例代码的默认设置：16:9、8 秒、模型的第一档分辨率
export function defaultSettings(model: Model | undefined): VideoSettings {
  const aspects = optionList(model, 'video_aspect_ratios')
  const resolutions = [...optionList(model, 'video_output_resolutions')].sort(
    (a, b) => resolutionOrder.indexOf(a) - resolutionOrder.indexOf(b),
  )
  return {
    model: model?.id ?? '',
    aspectRatio: aspects.includes('16:9') ? '16:9' : (aspects[0] ?? '16:9'),
    durationSeconds: 8,
    resolution: resolutions[0] ?? '720p',
  }
}
