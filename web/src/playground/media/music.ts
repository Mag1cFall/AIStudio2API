import { computed, reactive } from 'vue'
import { useConsole } from '@/console'
import type { Model } from '@/types'
import { newTurnID } from '../session'
import { audioBlob, generateContent, responseText, type InlineImage } from './api'
import { pageKindOf } from './kinds'

// MusicImage 是音乐提示中的参考图片
export interface MusicImage extends InlineImage {
  name: string
}

// MusicAudio 是播放栏中的音频，sample 表示官网示例音频
export interface MusicAudio {
  url: string
  name: string
  sample?: boolean
}

// MusicView 是音乐编辑器的视图：文字或分段编排
export type MusicView = 'text' | 'composer'

// SectionType 是分段编排中的段落类型
export const sectionTypes = ['Intro', 'Verse', 'Chorus', 'Bridge', 'Outro', 'Build'] as const
export type SectionType = (typeof sectionTypes)[number]

// MusicSection 是分段编排中的一段：类型、起止秒数、强度与描述
export interface MusicSection {
  id: string
  type: SectionType
  description: string
  startTime: number
  endTime: number
  volume: number
}

// maxSections 是官网分段编排的段落上限
export const maxSections = 10

// defaultModel 是官网音乐页的默认模型
const defaultModel = 'lyria-3-pro-preview'

// sectionPattern 与官网一致匹配 [m:ss - m:ss] Type: Intensity: n/10. 描述
const sectionPattern =
  /\[(\d+:\d{2})\s*-\s*(\d+:\d{2})\]\s*(\w+):\s*Intensity:\s*(\d+)\/10\.[ ]*([\s\S]*?)(?=\s*\[\d+:\d{2}\s*-\s*\d+:\d{2}\]|$)/g

// clock 把秒数写为 m:ss
export function clock(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}

// seconds 把 m:ss 解析为秒数
export function seconds(value: string): number {
  const [minutes, rest] = value.split(':').map(Number)
  return (minutes ?? 0) * 60 + (rest ?? 0)
}

// serializeSections 与官网一致把分段写为提示词
export function serializeSections(sections: readonly MusicSection[]): string {
  return sections
    .map(
      (section) =>
        `[${clock(section.startTime)} - ${clock(section.endTime)}] ${section.type}: Intensity: ${section.volume}/10. ${section.description}`,
    )
    .join('\n\n')
}

// parseSections 与官网一致从提示词解析分段，没有分段格式时返回 null
export function parseSections(text: string): MusicSection[] | null {
  const matches = [...text.matchAll(sectionPattern)]
  if (matches.length === 0) return null
  const sections: MusicSection[] = []
  let cursor = 0
  for (const match of matches) {
    const between = text.slice(cursor, match.index).trim()
    let description = match[5] ?? ''
    const last = sections.at(-1)
    if (between) {
      if (last) last.description = last.description ? `${last.description}\n\n${between}` : between
      else description = description ? `${between}\n\n${description}` : between
    }
    const type = sectionTypes.find((item) => item === match[3]) ?? 'Intro'
    sections.push({
      id: newTurnID(),
      type,
      description: description.trim(),
      startTime: seconds(match[1]!),
      endTime: seconds(match[2]!),
      volume: Number(match[4]),
    })
    cursor = match.index + match[0].length
  }
  const tail = text.slice(cursor).trim()
  const last = sections.at(-1)
  if (tail && last) last.description = last.description ? `${last.description}\n\n${tail}` : tail
  return sections
}

// models 返回实时目录中的音乐模型
function models(): Model[] {
  return useConsole().models.value.filter((model) => pageKindOf(model) === 'music')
}

const state = reactive({
  model: '',
  editing: false,
  view: 'text' as MusicView,
  prompt: '',
  sections: [] as MusicSection[],
  images: [] as MusicImage[],
  audio: undefined as MusicAudio | undefined,
  title: '',
  lyrics: '',
  running: false,
  error: '',
})
let controller: AbortController | undefined

const currentModel = computed(() => models().find((model) => model.id === state.model))

// ensureModel 选定模型：地址中的模型优先，其次保留当前模型，再次为官网默认模型
function ensureModel(requested?: string): void {
  const list = models()
  const wanted = list.find((model) => model.id === requested)
  if (wanted) state.model = wanted.id
  else if (!list.some((model) => model.id === state.model))
    state.model = (list.find((model) => model.id === defaultModel) ?? list[0])?.id ?? ''
}

// setAudio 替换播放栏音频并释放上一段生成结果
function setAudio(audio: MusicAudio | undefined): void {
  if (state.audio?.url.startsWith('blob:')) URL.revokeObjectURL(state.audio.url)
  state.audio = audio
}

// edit 打开编辑器，可带示例提示词与示例音频
function edit(prompt = '', sample?: MusicAudio): void {
  state.editing = true
  state.prompt = prompt
  state.error = ''
  if (state.view === 'composer') openComposer()
  setAudio(sample)
}

// newSession 回到空态
function newSession(): void {
  controller?.abort()
  Object.assign(state, {
    editing: false,
    view: 'text',
    prompt: '',
    sections: [],
    images: [],
    title: '',
    lyrics: '',
    running: false,
    error: '',
  })
  setAudio(undefined)
}

// openComposer 与官网一致切换到分段编排：能解析的提示词拆成分段，其余作为一段 Intro
function openComposer(): void {
  state.view = 'composer'
  const text = state.prompt
  state.sections = text
    ? (parseSections(text) ?? [
        { id: newTurnID(), type: 'Intro', description: text, startTime: 0, endTime: 10, volume: 5 },
      ])
    : []
}

// setView 切换文字与分段编排
function setView(view: MusicView): void {
  if (view === 'composer') openComposer()
  else state.view = 'text'
}

// syncSections 把分段写回提示词
function syncSections(): void {
  state.prompt = serializeSections(state.sections)
}

// addSection 与官网一致在末尾加入一段，起点接上一段终点，时长 10 秒
function addSection(type: SectionType): void {
  if (state.sections.length >= maxSections) return
  const start = state.sections.at(-1)?.endTime ?? 0
  state.sections.push({
    id: newTurnID(),
    type,
    description: '',
    startTime: start,
    endTime: start + 10,
    volume: 5,
  })
  syncSections()
}

// moveSection 与官网一致移动段落，各位置保留原有的起止时间
function moveSection(from: number, to: number): void {
  if (to < 0 || to >= state.sections.length) return
  const times = state.sections.map(({ startTime, endTime }) => ({ startTime, endTime }))
  const [section] = state.sections.splice(from, 1)
  state.sections.splice(to, 0, section!)
  state.sections.forEach((item, index) => Object.assign(item, times[index]))
  syncSections()
}

// removeSection 删除一段
function removeSection(index: number): void {
  state.sections.splice(index, 1)
  syncSections()
}

// requestBody 按官网运行请求组合 generateContent：图片在前，提示词在后，输出音频
function requestBody(model: Model | undefined = currentModel.value): Record<string, unknown> {
  return {
    contents: [
      {
        role: 'user',
        parts: [
          ...state.images.map((image) => ({
            inlineData: { mimeType: image.mimeType, data: image.data },
          })),
          { text: state.prompt },
        ],
      },
    ],
    generationConfig: {
      ...(model?.top_p === undefined ? {} : { topP: model.top_p }),
      ...(model?.top_k === undefined ? {} : { topK: model.top_k }),
      responseModalities: ['AUDIO'],
    },
  }
}

// run 生成音乐，结果放入播放栏，文字部分作为歌词与段落说明
async function run(): Promise<void> {
  const model = currentModel.value
  if (state.running || model === undefined || state.prompt.trim() === '') return
  state.running = true
  state.error = ''
  controller = new AbortController()
  const signal = controller.signal
  try {
    const response = await generateContent(model.id, requestBody(model), signal)
    const candidate = response.candidates?.[0]
    const parts = candidate?.content?.parts ?? []
    const blob = audioBlob(parts)
    state.lyrics = responseText(parts)
    if (blob === undefined) {
      state.error = candidate?.finishReason ?? response.promptFeedback?.blockReason ?? 'No audio'
    } else {
      const extension = blob.type.includes('wav') ? 'wav' : 'mp3'
      setAudio({ url: URL.createObjectURL(blob), name: `${model.id}.${extension}` })
    }
  } catch (error) {
    if (!signal.aborted) state.error = error instanceof Error ? error.message : String(error)
  } finally {
    state.running = false
    controller = undefined
  }
}

// stop 停止当前生成
function stop(): void {
  controller?.abort()
}

// useMusic 返回音乐页状态与操作
export function useMusic() {
  return {
    state,
    models: computed(models),
    currentModel,
    ensureModel,
    edit,
    newSession,
    setView,
    syncSections,
    addSection,
    moveSection,
    removeSection,
    requestBody,
    run,
    stop,
  }
}
