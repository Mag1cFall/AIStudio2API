import { computed, reactive } from 'vue'
import { api } from '@/api'
import { useConsole } from '@/console'
import { keepPage } from '@/desktop'
import { useI18n } from '@/i18n'
import type { Model } from '@/types'
import { base64Bytes } from '../bytes'
import { failureText } from '../request'
import { newTurnID } from '../session'
import { createOpenAIVideo, createVideo, waitOpenAIVideo, waitVideo, type InlineImage } from './api'
import { modelMatches } from '../catalog'
import { pageKindOf } from './kinds'
import { requestTitle } from '../title'

// VideoProtocol 是视频页发送请求使用的公开协议
type VideoProtocol = 'gemini' | 'openai-videos'

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
interface VideoTurn {
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
  task?: VideoTask
  duration?: number
  stopped?: boolean
  error?: string
}

// VideoTask 是模型轮次在服务端的视频任务：Gemini API 为 operation 名，OpenAI Videos 为视频 ID
interface VideoTask {
  protocol: VideoProtocol
  id: string
}

// VideoData 是保存到对话库的视频会话
export interface VideoData {
  kind: 'video'
  settings: VideoSettings
  turns: VideoTurn[]
}

// VideoOption 是运行设置下拉中的一项，disabled 时附官网提示
interface VideoOption<T> {
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
  return model?.capability_options?.[key] ?? []
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
  return [...optionList(model, 'video_output_resolutions')].sort(
    (a, b) => resolutionOrder.indexOf(a) - resolutionOrder.indexOf(b),
  )
}

const state = reactive({
  id: null as string | null,
  title: '',
  description: '',
  settings: defaultSettings(undefined),
  turns: [] as VideoTurn[],
  draft: '',
  start: undefined as VideoFrame | undefined,
  last: undefined as VideoFrame | undefined,
  references: [] as VideoFrame[],
  running: false,
  started: 0,
})
let controller: AbortController | undefined

// 关闭桌面窗口时，生成中的视频保留页面
keepPage(() => state.running)

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

// imageInputs 按实时目录返回模型接受的图片输入：video_inputs 的 frames 为首尾帧、reference_images 为参考图，video_tasks 的 image_to_video 为起始图、reference_to_video 为参考图；两项都未列出时只接受起始图
function imageInputs(model: Model | undefined): {
  start: boolean
  last: boolean
  references: boolean
} {
  const inputs = optionList(model, 'video_inputs')
  const tasks = optionList(model, 'video_tasks')
  return {
    start:
      (inputs.length === 0 && tasks.length === 0) ||
      inputs.includes('frames') ||
      tasks.includes('image_to_video'),
    last: inputs.includes('frames'),
    references: inputs.includes('reference_images') || tasks.includes('reference_to_video'),
  }
}

// videoInputs 是当前模型接受的图片输入
const videoInputs = computed(() => imageInputs(currentModel.value))

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
  const wanted = list.find((model) => modelMatches(model, requested))
  if (wanted && wanted.id !== state.settings.model) {
    setModel(wanted.id)
    return
  }
  if (list.some((model) => model.id === state.settings.model)) return
  if (list[0]) setModel(list[0].id)
}

// setModel 切换模型，保留仍然可用的设置与图片，移除新模型不接受的图片
function setModel(id: string): void {
  const model = models().find((item) => item.id === id)
  const defaults = defaultSettings(model)
  const settings = state.settings
  const inputs = imageInputs(model)
  if (!inputs.start) state.start = undefined
  if (!inputs.last) state.last = undefined
  if (!inputs.references) state.references = []
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

// leave 离开当前会话：生成中先保存会话与已创建的视频任务，再停止本页的等待
function leave(): void {
  if (state.running) void save().catch(() => undefined)
  controller?.abort()
}

// newSession 清空当前会话
function newSession(): void {
  leave()
  Object.assign(state, {
    id: null,
    title: '',
    description: '',
    turns: [],
    draft: '',
    start: undefined,
    last: undefined,
    references: [],
    running: false,
  })
}

// load 载入对话库中的视频会话
function load(id: string, prompt: { title: string; description: string; data: VideoData }): void {
  leave()
  Object.assign(state, {
    id,
    title: prompt.title,
    description: prompt.description,
    settings: { ...prompt.data.settings },
    turns: prompt.data.turns,
    draft: '',
    start: undefined,
    last: undefined,
    references: [],
    running: false,
  })
  resume()
}

// resume 在会话最后一轮的视频任务尚无结果时继续等待，没有任务的未完成轮次记为已停止
function resume(): void {
  const turn = state.turns.at(-1)
  if (
    turn?.role !== 'model' ||
    turn.videos !== undefined ||
    turn.stopped ||
    turn.error !== undefined
  )
    return
  const task = turn.task
  if (task === undefined) {
    turn.stopped = true
    return
  }
  state.started = performance.now() - (Date.now() - turn.created)
  void follow(turn, async (signal) =>
    task.protocol === 'openai-videos'
      ? [await waitOpenAIVideo({ id: task.id, status: 'queued' }, signal)]
      : (await waitVideo({ name: task.id }, signal)).map(servicePath),
  )
}

// generateTitle 与官网一样在会话未命名时于提交时以提示词生成标题，失败时保持未命名
async function generateTitle(model: string, prompt: string): Promise<void> {
  const title = await requestTitle(model, prompt, '', () => ({
    owner: state.turns,
    title: state.title,
  }))
  if (title === '') return
  state.title = title
  await save().catch(() => undefined)
}

// save 把会话写入对话库，empty 为 true 时没有轮次的会话也写入
async function save(empty = false): Promise<void> {
  if (state.turns.length === 0 && !empty) return
  state.id ??= newTurnID()
  await api.savePrompt<VideoData>(state.id, {
    title: state.title,
    model: state.settings.model,
    description: state.description,
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
  const url = new URL(uri, window.location.origin)
  return `${url.pathname}${url.search}`
}

// canRun 表示可以运行：有模型，有提示词、起始图或参考图，且 OpenAI Videos 没有它无法表达的尾帧或参考图
const canRun = computed(
  () =>
    currentModel.value !== undefined &&
    (state.draft.trim() !== '' || state.start !== undefined || state.references.length > 0) &&
    !openAIFrames.value,
)

// run 以输入框内容生成视频，与官网一样有图片时提示词可以为空
async function run(): Promise<void> {
  const model = currentModel.value
  if (state.running || model === undefined || !canRun.value) return
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
  if (state.title === '' && state.draft.trim() !== '') void generateTitle(model.id, state.draft)
  state.started = performance.now()
  const owner = state.turns
  await follow(turn, async (signal) => {
    await save()
    if (openAI) {
      const video = await createOpenAIVideo(body as FormData, signal)
      await keepTask(turn, { protocol: 'openai-videos', id: video.id }, owner)
      return [await waitOpenAIVideo(video, signal)]
    }
    const operation = await createVideo(model.id, body, signal)
    await keepTask(turn, { protocol: 'gemini', id: operation.name }, owner)
    return (await waitVideo(operation, signal)).map(servicePath)
  })
}

// keepTask 记下模型轮次的视频任务，会话仍是 owner 时立即保存，离开后重新打开可继续等待
async function keepTask(turn: VideoTurn, task: VideoTask, owner: VideoTurn[]): Promise<void> {
  turn.task = task
  if (state.turns === owner) await save()
}

// follow 等待模型轮次的视频并写入结果；等待中离开了该会话时，结果留给重新打开时继续等待，不改动当前会话
async function follow(
  turn: VideoTurn,
  wait: (signal: AbortSignal) => Promise<string[]>,
): Promise<void> {
  const owner = state.turns
  state.running = true
  controller = new AbortController()
  const signal = controller.signal
  try {
    turn.videos = await wait(signal)
  } catch (error) {
    if (state.turns === owner) {
      if (signal.aborted) turn.stopped = true
      else turn.error = failureText(error, useI18n().t('pg.videoUnreachable'))
    }
  } finally {
    if (state.turns === owner) {
      turn.duration = Math.round(performance.now() - state.started)
      controller = undefined
      await save().catch(() => undefined)
      if (state.turns === owner) state.running = false
    }
  }
}

// stop 停止等待当前视频
function stop(): void {
  controller?.abort()
}

// rename 修改会话标题与描述并保存
async function rename(title: string, description: string): Promise<void> {
  state.title = title.trim()
  state.description = description.trim()
  await save()
}

// remove 停止当前等待并清空页面，再从对话库删除该会话
async function remove(): Promise<void> {
  const id = state.id
  state.running = false
  newSession()
  if (id !== null) await api.deletePrompt(id)
}

// useVideo 返回视频页状态与操作
export function useVideo() {
  return {
    state,
    models: computed(models),
    currentModel,
    videoInputs,
    openAIFrames,
    canRun,
    aspectRatios,
    durations,
    resolutions,
    ensureModel,
    setModel,
    reset,
    setLast,
    newSession,
    load,
    run,
    stop,
    rename,
    remove,
    save,
  }
}
