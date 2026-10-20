import { computed, reactive } from 'vue'
import { api, ApiError } from '@/api'
import { useConsole } from '@/console'
import type { Model } from '@/types'
import { newTurnID } from '../session'
import {
  base64Bytes,
  createOpenAIVideo,
  createVideo,
  waitOpenAIVideo,
  waitVideo,
  type InlineImage,
} from './api'
import { pageKindOf } from './kinds'

// VideoProtocol 是视频页发送请求使用的公开协议
export type VideoProtocol = 'gemini' | 'openai-videos'

// videoProtocols 是视频页协议下拉的选项
export const videoProtocols: { value: VideoProtocol; label: string }[] = [
  { value: 'gemini', label: 'Gemini API' },
  { value: 'openai-videos', label: 'OpenAI Videos' },
]

// VideoFrame 是视频请求中的一张帧图片或参考图
export interface VideoFrame extends InlineImage {
  name: string
}

// VideoSettings 是视频页运行设置
export interface VideoSettings {
  model: string
  protocol: VideoProtocol
  aspectRatio: string
  durationSeconds: number
  resolution: string
}

// VideoTurn 是视频页的一轮：用户轮次记录提示词与图片，模型轮次记录生成的视频地址
export interface VideoTurn {
  id: string
  role: 'user' | 'model'
  created: number
  prompt?: string
  start?: VideoFrame | undefined
  last?: VideoFrame | undefined
  references?: VideoFrame[] | undefined
  model?: string
  aspectRatio?: string
  videos?: string[]
  duration?: number
  error?: string
}

// VideoData 是保存到对话库的视频会话
export interface VideoData {
  kind: 'video'
  settings: VideoSettings
  turns: VideoTurn[]
}

// VideoOption 是运行设置下拉中的一项，disabled 时附官网提示
export interface VideoOption<T> {
  value: T
  disabled: boolean
}

// maxReferences 是官网参考图上限
export const maxReferences = 3

// resolutionOrder 按清晰度排列分辨率
const resolutionOrder = ['720p', '1080p', '4k']

// models 返回实时目录中的视频模型
function models(): Model[] {
  return useConsole().models.value.filter((model) => pageKindOf(model) === 'video')
}

// optionList 读取模型的视频选项
function optionList(model: Model | undefined, key: string): string[] {
  const value = model?.capability_options?.[key]
  return Array.isArray(value) ? value.map(String) : []
}

// defaultSettings 返回官网默认设置：16:9、8 秒、模型的第一档分辨率
function defaultSettings(model: Model | undefined): VideoSettings {
  const aspects = optionList(model, 'video_aspect_ratios')
  const resolutions = sortedResolutions(model)
  return {
    model: model?.id ?? '',
    protocol: 'gemini',
    aspectRatio: aspects.includes('16:9') ? '16:9' : (aspects[0] ?? '16:9'),
    durationSeconds: 8,
    resolution: resolutions[0] ?? '720p',
  }
}

// sortedResolutions 返回按清晰度排列的分辨率
function sortedResolutions(model: Model | undefined): string[] {
  return optionList(model, 'video_output_resolutions').sort(
    (a, b) => resolutionOrder.indexOf(a) - resolutionOrder.indexOf(b),
  )
}

const state = reactive({
  id: null as string | null,
  title: '',
  settings: defaultSettings(undefined),
  turns: [] as VideoTurn[],
  draft: '',
  start: undefined as VideoFrame | undefined,
  last: undefined as VideoFrame | undefined,
  references: [] as VideoFrame[],
  running: false,
  started: 0,
  loadError: '',
})
let controller: AbortController | undefined

const currentModel = computed(() => models().find((model) => model.id === state.settings.model))

// aspectRatios 是模型支持的宽高比
const aspectRatios = computed(() => optionList(currentModel.value, 'video_aspect_ratios'))

// durations 按官网规则禁用：有尾帧或分辨率高于 720p 时只能选 8 秒
const durations = computed<VideoOption<number>[]>(() =>
  optionList(currentModel.value, 'video_durations_seconds')
    .map(Number)
    .sort((a, b) => a - b)
    .map((value) => ({
      value,
      disabled: value !== 8 && (state.last !== undefined || state.settings.resolution !== '720p'),
    })),
)

// resolutions 按官网规则禁用：时长不是 8 秒时只能选 720p
const resolutions = computed<VideoOption<string>[]>(() =>
  sortedResolutions(currentModel.value).map((value) => ({
    value,
    disabled: value !== '720p' && state.settings.durationSeconds !== 8,
  })),
)

// ensureModel 在目录变化后选定模型：地址中的模型优先，其次保留当前模型，否则用第一个视频模型
function ensureModel(requested?: string): void {
  const list = models()
  const wanted = list.find((model) => model.id === requested)
  if (wanted && wanted.id !== state.settings.model) {
    setModel(wanted.id)
    return
  }
  if (list.some((model) => model.id === state.settings.model)) return
  if (list[0]) setModel(list[0].id)
}

// setModel 切换模型，保留仍然可用的设置
function setModel(id: string): void {
  const model = models().find((item) => item.id === id)
  const defaults = defaultSettings(model)
  const settings = state.settings
  state.settings = {
    model: defaults.model,
    protocol: settings.protocol,
    aspectRatio: optionList(model, 'video_aspect_ratios').includes(settings.aspectRatio)
      ? settings.aspectRatio
      : defaults.aspectRatio,
    durationSeconds: optionList(model, 'video_durations_seconds').includes(
      String(settings.durationSeconds),
    )
      ? settings.durationSeconds
      : defaults.durationSeconds,
    resolution: sortedResolutions(model).includes(settings.resolution)
      ? settings.resolution
      : defaults.resolution,
  }
}

// reset 恢复当前模型的默认设置
function reset(): void {
  state.settings = defaultSettings(currentModel.value)
}

// setLast 设置或清除尾帧，有尾帧时时长固定为 8 秒
function setLast(frame: VideoFrame | undefined): void {
  state.last = frame
  if (frame !== undefined) state.settings.durationSeconds = 8
}

// newSession 清空当前会话
function newSession(): void {
  controller?.abort()
  Object.assign(state, {
    id: null,
    title: '',
    turns: [],
    draft: '',
    start: undefined,
    last: undefined,
    references: [],
    running: false,
    loadError: '',
  })
}

// load 载入对话库中的视频会话
function load(id: string, title: string, data: VideoData): void {
  controller?.abort()
  Object.assign(state, {
    id,
    title,
    settings: { ...defaultSettings(undefined), ...data.settings },
    turns: data.turns,
    draft: '',
    start: undefined,
    last: undefined,
    references: [],
    running: false,
    loadError: '',
  })
}

// promptTitle 以首个提示词的第一行作为会话标题
function promptTitle(): string {
  const text =
    state.turns
      .find((turn) => turn.role === 'user')
      ?.prompt?.trim()
      .split('\n')[0] ?? ''
  return text.length > 60 ? `${text.slice(0, 60)}…` : text
}

// save 把会话写入对话库
async function save(): Promise<void> {
  if (state.turns.length === 0) return
  state.id ??= newTurnID()
  if (state.title === '') state.title = promptTitle()
  await api.savePrompt<VideoData>(state.id, {
    title: state.title,
    model: state.settings.model,
    description: promptTitle(),
    data: {
      kind: 'video',
      settings: state.settings,
      turns: state.turns,
    },
  })
}

// imagePayload 把帧图片写为 Gemini API 的图片对象
function imagePayload(frame: VideoFrame): { bytesBase64Encoded: string; mimeType: string } {
  return { bytesBase64Encoded: frame.data, mimeType: frame.mimeType }
}

// requestBody 按当前输入与设置组合 predictLongRunning 请求
function requestBody(): Record<string, unknown> {
  const instance: Record<string, unknown> = { prompt: state.draft }
  if (state.start) instance.image = imagePayload(state.start)
  if (state.last) instance.lastFrame = imagePayload(state.last)
  if (state.references.length > 0)
    instance.referenceImages = state.references.map((frame) => ({
      image: imagePayload(frame),
      referenceType: 'asset',
    }))
  return {
    instances: [instance],
    parameters: {
      sampleCount: 1,
      aspectRatio: state.settings.aspectRatio,
      durationSeconds: state.settings.durationSeconds,
      resolution: state.settings.resolution,
    },
  }
}

// openAISizes 是 OpenAI Videos size 与宽高比、分辨率的对应
const openAISizes: Record<string, string> = {
  '16:9 720p': '1280x720',
  '9:16 720p': '720x1280',
  '16:9 1080p': '1920x1080',
  '9:16 1080p': '1080x1920',
  '16:9 4k': '3840x2160',
  '9:16 4k': '2160x3840',
}

// openAIForm 按当前输入与设置组合 OpenAI Videos 请求，起始帧作为 input_reference
function openAIForm(modelID: string): FormData {
  const form = new FormData()
  form.set('model', modelID)
  form.set('prompt', state.draft)
  form.set('seconds', String(state.settings.durationSeconds))
  const size = openAISizes[`${state.settings.aspectRatio} ${state.settings.resolution}`]
  if (size) form.set('size', size)
  if (state.start)
    form.set(
      'input_reference',
      new Blob([base64Bytes(state.start.data)], { type: state.start.mimeType }),
      state.start.name,
    )
  return form
}

// openAIFrames 表示 OpenAI Videos 协议无法表达的尾帧或参考图
const openAIFrames = computed(
  () =>
    state.settings.protocol === 'openai-videos' &&
    (state.last !== undefined || state.references.length > 0),
)

// servicePath 把服务返回的绝对地址换成当前页面同源路径
function servicePath(uri: string): string {
  try {
    const url = new URL(uri, window.location.origin)
    return `${url.pathname}${url.search}`
  } catch {
    return uri
  }
}

// run 以输入框内容生成视频；输入保留在输入框，便于继续修改
async function run(): Promise<void> {
  const model = currentModel.value
  if (state.running || model === undefined || state.draft.trim() === '' || openAIFrames.value)
    return
  const openAI = state.settings.protocol === 'openai-videos'
  const body = openAI ? openAIForm(model.id) : requestBody()
  state.turns.push({
    id: newTurnID(),
    role: 'user',
    created: Date.now(),
    prompt: state.draft,
    start: state.start,
    last: state.last,
    references: state.references.length > 0 ? [...state.references] : undefined,
  })
  const turn = reactive<VideoTurn>({
    id: newTurnID(),
    role: 'model',
    created: Date.now(),
    model: model.name,
    aspectRatio: state.settings.aspectRatio,
  })
  state.turns.push(turn)
  state.running = true
  state.started = performance.now()
  controller = new AbortController()
  const signal = controller.signal
  try {
    await save()
    turn.videos = openAI
      ? [await waitOpenAIVideo(await createOpenAIVideo(body as FormData, signal), signal)]
      : (await waitVideo(await createVideo(model.id, body, signal), signal)).map(servicePath)
  } catch (error) {
    turn.error = signal.aborted
      ? 'Generation stopped'
      : error instanceof Error
        ? error.message
        : String(error)
  } finally {
    turn.duration = Math.round(performance.now() - state.started)
    state.running = false
    controller = undefined
    await save().catch(() => undefined)
  }
}

// stop 停止等待当前视频
function stop(): void {
  controller?.abort()
}

// rename 修改会话标题并保存
async function rename(title: string): Promise<void> {
  state.title = title.trim() || promptTitle()
  await save()
}

// remove 从对话库删除当前会话并清空页面
async function remove(): Promise<void> {
  if (state.id !== null) await api.deletePrompt(state.id)
  newSession()
}

// open 打开对话库中的视频会话
async function open(id: string): Promise<void> {
  if (state.id === id) return
  try {
    const prompt = await api.prompt<VideoData>(id)
    load(id, prompt.title, prompt.data)
  } catch (error) {
    state.loadError =
      error instanceof ApiError && error.status === 404 ? 'not_found' : String(error)
  }
}

// useVideo 返回视频页状态与操作
export function useVideo() {
  return {
    state,
    models: computed(models),
    currentModel,
    openAIFrames,
    aspectRatios,
    durations,
    resolutions,
    ensureModel,
    setModel,
    reset,
    setLast,
    newSession,
    load,
    open,
    run,
    stop,
    rename,
    remove,
  }
}
