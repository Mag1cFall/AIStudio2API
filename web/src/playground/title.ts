import { api } from '@/api'

// TitleTarget 是标题所属的会话：owner 在切换到另一个会话时变化，title 为会话当前标题
interface TitleTarget {
  owner: unknown
  title: string
}

// requestTitle 与官网一样以提示词与回答请求会话标题；失败、请求期间切换了会话或会话已有标题时返回空串
export async function requestTitle(
  model: string,
  prompt: string,
  answer: string,
  target: () => TitleTarget,
): Promise<string> {
  const owner = target().owner
  const title = await api.generateTitle(model, prompt, answer).catch(() => '')
  const current = target()
  return current.owner === owner && current.title === '' ? title : ''
}
