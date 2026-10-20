import type { Model } from '@/types'
import { hasCategory, hasCode } from '../catalog'

// PageKind 是 Playground 的页面类型
export type PageKind = 'chat' | 'live' | 'image' | 'video' | 'speech' | 'music'

// pageRoutes 是各页面类型的新会话路径
export const pageRoutes: Record<PageKind, string> = {
  chat: '/prompts/new_chat',
  live: '/live',
  image: '/prompts/new_image',
  video: '/prompts/new_video',
  speech: '/generate-speech',
  music: '/new_music',
}

// unsavedKinds 是内容不保存到历史记录的页面类型，切换模型时提示内容会丢失
export const unsavedKinds: readonly PageKind[] = ['live', 'speech', 'music']

// pageKindOf 按官网规则返回模型所属页面：能力码 26 实时、19 图片、20 视频、37 语音、53 且属音乐分类为音乐，只有实时音乐方法的模型为音乐，其余为对话
export function pageKindOf(model: Model | undefined): PageKind {
  if (model === undefined) return 'chat'
  if (hasCode(model, 26)) return 'live'
  if (hasCode(model, 19)) return 'image'
  if (hasCode(model, 20)) return 'video'
  if (hasCode(model, 37)) return 'speech'
  if (hasCode(model, 53) && hasCategory(model, 16)) return 'music'
  if (model.methods.includes('bidiGenerateMusic')) return 'music'
  return 'chat'
}

// pageKindOfPath 返回路径所属的页面类型
export function pageKindOfPath(path: string): PageKind {
  if (path.startsWith('/live')) return 'live'
  if (path.startsWith('/generate-speech')) return 'speech'
  if (path.startsWith('/new_music')) return 'music'
  if (path.startsWith('/prompts/new_image')) return 'image'
  if (path.startsWith('/prompts/new_video')) return 'video'
  return 'chat'
}
