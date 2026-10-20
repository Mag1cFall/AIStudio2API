import { computed, reactive } from 'vue'
import { useConsole } from '@/console'
import type { Model, VoiceDetail } from '@/types'
import { apiKey, errorMessage, StreamError } from '../request'
import { newTurnID } from '../session'
import { audioBlob, generateContent } from './api'
import { pageKindOf } from './kinds'
import type { SpeechTemplate } from './speech-templates'

// SpeechProtocol 是语音页发送请求使用的公开协议
export type SpeechProtocol = 'gemini' | 'openai-speech'

// speechProtocols 是语音页协议下拉的选项
export const speechProtocols: { value: SpeechProtocol; label: string }[] = [
  { value: 'gemini', label: 'Gemini API' },
  { value: 'openai-speech', label: 'OpenAI Speech' },
]

// SpeechBlock 是一段台词：说话人序号、风格与正文
export interface SpeechBlock {
  id: string
  speaker: number
  style: string
  text: string
}

// Speaker 是一位说话人：请求中的名称、音色，以及旧版 TTS 模型的音频画像、风格、语速与口音
export interface Speaker {
  alias: string
  voice: string
  persona: string
  style: string
  pace: string
  accent: string
}

// SpeechSettings 是语音页运行设置；conversational 对应官网 Filler words
export interface SpeechSettings {
  model: string
  protocol: SpeechProtocol
  temperature: number | null
  conversational: boolean
}

// StylePreset 是风格下拉的一项预设
export interface StylePreset {
  label: string
  value: string
}

// daikonTags 是支持分段风格的 TTS 模型的表情与音效标签，插入为 <tag>
const daikonTags =
  'argh;breath;cackle;cheer;chuckle;chuckles;cough;cry;exhales;gasp;giggle;groan;growl;grr;grunt;heavy breath;hiss;laugh;laughter;long pause;moan;pant;pff;phew;scream;short pause;shout;shriek;sigh;sighs;sneeze;snicker;snort;sob;throat-clearing;tsk;whimper;whispering;whispers;yawn'.split(
    ';',
  )

// emotionTags 是其他 TTS 模型的情绪标签，插入为 [tag]
const emotionTags =
  'admiration adoration aggression agitation amusement anger annoyance awe confusion curiosity determination enthusiasm excitement frustration hope interest laughs negative nervousness neutral positive tension whispers'.split(
    ' ',
  )

// daikonStyles 是支持分段风格的 TTS 模型的风格预设
const daikonStyles: StylePreset[] = [
  { label: 'Whisper', value: 'whisper quietly' },
  { label: 'Friendly', value: 'Warm and neutral delivery with micro-pauses' },
  { label: 'Narration', value: 'commanding, rich vocal modulation, dramatic pauses' },
  { label: 'Promote', value: 'High energy, elongated vowels' },
  { label: 'Podcast', value: 'Calm, measured delivery' },
]

// classicStyles 是其他 TTS 模型的风格预设
const classicStyles: StylePreset[] = [
  {
    label: 'Vocal Smile',
    value:
      'The "Vocal Smile": The soft palate is raised to keep the tone bright, sunny, and explicitly inviting.',
  },
  {
    label: 'Newscaster',
    value: 'Professional, authoritative, clear articulation with standard broadcast cadence.',
  },
  { label: 'Whisper', value: 'Intimate, breathy, close-to-mic proximity effect.' },
  { label: 'Empathetic', value: 'Warm, understanding, soft tone with gentle inflections.' },
  {
    label: 'Promo/Hype',
    value: 'High energy, punchy consonants, elongated vowels on excitement words.',
  },
  { label: 'Deadpan', value: 'Flat affect, minimal pitch variation, dry delivery.' },
]

// paceOptions 是旧版 TTS 模型说话人语速的预设
export const paceOptions: StylePreset[] = [
  { label: 'Natural', value: 'Natural conversational pace.' },
  { label: 'Rapid Fire', value: 'Fast, energetic, no dead air. Sentences overlap slightly.' },
  { label: 'The Drift', value: 'Slow, liquid, zero urgency. Long pauses for breath.' },
  { label: 'Staccato', value: 'Short, clipped sentences with distinct pauses between words.' },
]

// accentOptions 是旧版 TTS 模型说话人口音的预设
export const accentOptions: StylePreset[] = [
  { label: 'Neutral', value: 'Neutral' },
  { label: 'American (Gen)', value: 'American (General)' },
  { label: 'American (Valley)', value: 'American (Valley Girl)' },
  { label: 'American (South)', value: 'Southern US' },
  { label: 'British (RP)', value: 'British (RP)' },
  { label: 'British (Brixton)', value: 'British (Brixton)' },
  { label: 'Transatlantic', value: 'Transatlantic' },
  { label: 'Australian', value: 'Australian' },
]

// newSpeaker 返回第 index 位说话人的初始设置
function newSpeaker(index: number, voice: string): Speaker {
  return { alias: `Speaker ${index + 1}`, voice, persona: '', style: '', pace: '', accent: '' }
}

// models 返回实时目录中的语音模型
function models(): Model[] {
  return useConsole().models.value.filter((model) => pageKindOf(model) === 'speech')
}

// voicesOf 返回模型的音色介绍，目录没有介绍时只有名称
function voicesOf(model: Model | undefined): VoiceDetail[] {
  if (model?.voice_details?.length) return model.voice_details
  return (model?.capability_options?.voices ?? []).map((name) => ({ name }))
}

const state = reactive({
  settings: {
    model: '',
    protocol: 'gemini',
    temperature: null,
    conversational: false,
  } as SpeechSettings,
  speakers: [newSpeaker(0, ''), newSpeaker(1, '')],
  blocks: [] as SpeechBlock[],
  scene: '',
  context: '',
  running: false,
  audio: '',
  error: '',
  previewing: '',
  previewLoading: '',
})
let controller: AbortController | undefined
let previewController: AbortController | undefined
let previewAudio: HTMLAudioElement | undefined

const currentModel = computed(() => models().find((model) => model.id === state.settings.model))
const voices = computed(() => voicesOf(currentModel.value))
// daikon 表示模型支持分段风格，官网对这类模型使用 <tag> 标签、风格下拉与 Filler words
const daikon = computed(() => Boolean(currentModel.value?.capabilities?.speech_metadata))
const tags = computed(() => [...(daikon.value ? daikonTags : emotionTags)].sort())
const styles = computed(() => (daikon.value ? daikonStyles : classicStyles))
// multi 与官网一致：任一台词块属于第二位说话人时为多人模式
const multi = computed(() => state.blocks.some((block) => block.speaker !== 0))
// openAIMulti 表示 OpenAI Speech 协议无法表达的多人对话
const openAIMulti = computed(() => state.settings.protocol === 'openai-speech' && multi.value)
const canRun = computed(
  () =>
    currentModel.value !== undefined &&
    !openAIMulti.value &&
    state.blocks.some((block) => block.text.trim() !== ''),
)
// usedSpeakers 是运行设置中列出的说话人序号
const usedSpeakers = computed(() => (multi.value ? [0, 1] : [0]))

// voiceDetail 返回音色的介绍
function voiceDetail(name: string): VoiceDetail | undefined {
  return voices.value.find((voice) => voice.name === name)
}

// defaultVoice 返回第 index 位说话人的默认音色：与官网相同优先 Fola、Rami 或 Zephyr、Puck
function defaultVoice(index: number): string {
  const preferred = daikon.value ? ['Fola', 'Rami'] : ['Zephyr', 'Puck']
  const name = preferred[index] ?? preferred[0]!
  if (voiceDetail(name)) return name
  return voices.value[index]?.name ?? voices.value[0]?.name ?? ''
}

// ensureVoices 把不在当前模型中的音色换成默认音色
function ensureVoices(): void {
  state.speakers.forEach((speaker, index) => {
    if (!voiceDetail(speaker.voice)) speaker.voice = defaultVoice(index)
  })
}

// ensureModel 在目录变化后选定模型：地址中的模型优先，其次保留当前模型，否则用第一个语音模型
function ensureModel(requested?: string): void {
  const list = models()
  const wanted = list.find((model) => model.id === requested)
  if (wanted) setModel(wanted.id)
  else if (!list.some((model) => model.id === state.settings.model) && list[0]) setModel(list[0].id)
  else ensureVoices()
}

// setModel 切换模型，音色不在新模型中时换成默认音色
function setModel(id: string): void {
  const wasDaikon = daikon.value
  state.settings.model = id
  ensureVoices()
  if (wasDaikon !== daikon.value)
    state.blocks.forEach((block) => (block.text = convertTags(block.text, daikon.value)))
}

// convertTags 与官网一致在两类标签写法之间转换：<tag> 与 [tag]
function convertTags(text: string, toDaikon: boolean): string {
  return toDaikon
    ? text.replaceAll(/\[([a-zA-Z][a-zA-Z0-9_\s,-]{0,49})\]/g, '<$1>')
    : text.replaceAll(/<([a-zA-Z][a-zA-Z0-9_\s,-]{0,49})>/g, '[$1]')
}

// reset 恢复模型默认设置
function reset(): void {
  state.settings.temperature = null
  state.settings.conversational = false
}

// stopPreview 停止单段试听
function stopPreview(): void {
  previewController?.abort()
  previewAudio?.pause()
  state.previewing = ''
  state.previewLoading = ''
}

// clearAudio 释放上一次生成的音频
function clearAudio(): void {
  if (state.audio) URL.revokeObjectURL(state.audio)
  state.audio = ''
}

// clear 停止生成与试听并清空结果
function clear(): void {
  controller?.abort()
  stopPreview()
  clearAudio()
  state.error = ''
}

// newDialog 打开空白对话：一段第一位说话人的空台词
function newDialog(): void {
  clear()
  state.speakers = [newSpeaker(0, defaultVoice(0)), newSpeaker(1, defaultVoice(1))]
  state.blocks = [{ id: newTurnID(), speaker: 0, style: '', text: '' }]
  state.scene = ''
  state.context = ''
}

// close 回到语音页空态
function close(): void {
  clear()
  state.blocks = []
}

// useTemplate 载入官网示例对话的说话人、风格、台词与 Filler words
function useTemplate(template: SpeechTemplate): void {
  clear()
  state.speakers = [0, 1].map((index) => {
    const voice = template.speakers[index]?.voice ?? ''
    return newSpeaker(index, voiceDetail(voice) ? voice : defaultVoice(index))
  })
  state.scene = ''
  state.context = ''
  if (daikon.value) state.settings.conversational = template.conversational
  state.blocks = template.turns.map((turn) => ({
    id: newTurnID(),
    speaker: turn.speaker,
    style: turn.style,
    text: daikon.value ? turn.text : convertTags(turn.text, false),
  }))
  state.audio = template.audio
}

// addBlock 与官网一致在末尾加入一段台词，说话人与上一段交替
function addBlock(): void {
  const last = state.blocks.at(-1)
  state.blocks.push({ id: newTurnID(), speaker: last?.speaker === 0 ? 1 : 0, style: '', text: '' })
}

// removeBlock 删除一段台词，至少保留一段
function removeBlock(index: number): void {
  if (state.blocks.length > 1) state.blocks.splice(index, 1)
}

// part 把一段台词写为请求片段：多人模式带说话人名称，有风格时带风格
function part(block: SpeechBlock, withSpeaker: boolean): Record<string, unknown> {
  const metadata: Record<string, string> = {}
  if (withSpeaker) metadata.speaker = state.speakers[block.speaker]!.alias
  if (block.style.trim() !== '') metadata.style = block.style.trim()
  return Object.keys(metadata).length > 0
    ? { text: block.text, speechMetadata: metadata }
    : { text: block.text }
}

// direction 返回旧版 TTS 模型说话人的导演备注：风格、语速与口音
function direction(speaker: Speaker): string {
  const items = [
    speaker.style && `Style: ${speaker.style.replace(/\.+$/, '')}`,
    speaker.pace && `Pace: ${speaker.pace.replace(/\.+$/, '')}`,
    speaker.accent && `Accent: ${speaker.accent.replace(/\.+$/, '')}`,
  ].filter(Boolean)
  if (items.length === 0) return ''
  const value = Array.from(`${items.join('. ')}.`)
  return value.length > 250 ? `${value.slice(0, 247).join('').trimEnd()}...` : value.join('')
}

// legacyPrompt 按官网旧版 TTS 模型的写法把场景、音频画像、导演备注与台词合为一段文字
function legacyPrompt(blocks: readonly SpeechBlock[], isMulti: boolean): string {
  const header = [
    state.scene.trim() && `## Scene:\n${state.scene.trim()}`,
    state.context.trim() && `## Sample Context:\n${state.context.trim()}`,
  ]
    .filter(Boolean)
    .join('\n\n')
  const lines = blocks.filter((block) => block.text.trim() !== '')
  const transcript = isMulti
    ? lines.map((block) => `${state.speakers[block.speaker]!.alias}: ${block.text}`).join('\n')
    : lines.map((block) => block.text).join('\n')
  const body = `${header ? `${header}\n\n` : ''}## Transcript:\n${transcript}`
  const used = isMulti
    ? state.speakers.filter((_, index) => lines.some((block) => block.speaker === index))
    : [state.speakers[0]!]
  const label = (speaker: Speaker, value: string) =>
    isMulti ? (value ? `For ${speaker.alias}: ${value}` : '') : value
  const profile = used
    .map((speaker) => label(speaker, speaker.persona.trim()))
    .filter(Boolean)
    .join('\n')
  const note = used
    .map((speaker) => label(speaker, direction(speaker)))
    .filter(Boolean)
    .join('\n')
  const lead =
    profile && note
      ? "Read the following transcript based on the audio profile and director's note."
      : profile
        ? 'Read the following transcript based on the audio profile.'
        : note
          ? "Read the following transcript based on the director's note."
          : ''
  return [
    lead,
    profile && `# Audio Profile\n${profile}`,
    note && `# Director's note\n${note}`,
    body,
  ]
    .filter(Boolean)
    .join('\n\n')
}

// requestBody 按官网运行请求组合 generateContent：分段风格模型每段台词为一个带说话人与风格的片段，旧版模型为一段整理后的文字；试听单段时沿用整段对话的说话人设置
function requestBody(
  model: Model,
  blocks: readonly SpeechBlock[] = state.blocks,
): Record<string, unknown> {
  const isMulti = multi.value
  const speakers = isMulti ? state.speakers : [state.speakers[0]!]
  const voice = (name: string) => ({ prebuiltVoiceConfig: { voiceName: name } })
  const speechConfig = isMulti
    ? {
        multiSpeakerVoiceConfig: {
          ...(daikon.value
            ? { mode: state.settings.conversational ? 'CONVERSATIONAL' : 'VERBATIM' }
            : {}),
          speakerVoiceConfigs: speakers.map((speaker) => ({
            speaker: speaker.alias,
            voiceConfig: voice(speaker.voice),
          })),
        },
      }
    : { voiceConfig: voice(speakers[0]!.voice) }
  return {
    contents: [
      {
        role: 'user',
        parts: daikon.value
          ? blocks.filter((block) => block.text.trim() !== '').map((block) => part(block, isMulti))
          : [{ text: legacyPrompt(blocks, isMulti) }],
      },
    ],
    generationConfig: {
      temperature: state.settings.temperature ?? model.temperature ?? 1,
      ...(model.top_p === undefined ? {} : { topP: model.top_p }),
      ...(model.top_k === undefined ? {} : { topK: model.top_k }),
      responseModalities: ['AUDIO'],
      speechConfig,
    },
  }
}

// openAISpeechBody 把单人对话写为 OpenAI Speech 请求：台词合为 input，首个风格为 instructions
function openAISpeechBody(
  modelID: string,
  blocks: readonly SpeechBlock[] = state.blocks,
): Record<string, unknown> {
  const input = blocks
    .map((block) => block.text)
    .filter((value) => value.trim() !== '')
    .join('\n')
  const style = blocks.find((block) => block.style.trim() !== '')?.style.trim()
  return {
    model: modelID,
    input,
    voice: state.speakers[blocks[0]?.speaker ?? 0]!.voice,
    response_format: 'wav',
    ...(style ? { instructions: style } : {}),
  }
}

// openAISpeech 调用 /v1/audio/speech 返回音频
async function openAISpeech(body: unknown, signal: AbortSignal): Promise<Blob> {
  const headers = new Headers({ 'Content-Type': 'application/json' })
  const key = apiKey()
  if (key !== '') headers.set('Authorization', `Bearer ${key}`)
  const response = await fetch('/v1/audio/speech', {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
    signal,
  })
  if (!response.ok) throw new StreamError(await errorMessage(response), response.status)
  return response.blob()
}

// synthesize 按所选协议生成台词的音频
async function synthesize(
  model: Model,
  blocks: readonly SpeechBlock[],
  signal: AbortSignal,
): Promise<Blob> {
  if (state.settings.protocol === 'openai-speech')
    return openAISpeech(openAISpeechBody(model.id, blocks), signal)
  const response = await generateContent(model.id, requestBody(model, blocks), signal)
  const blob = audioBlob(response.candidates?.[0]?.content?.parts ?? [])
  if (blob) return blob
  const reason = response.candidates?.[0]?.finishReason ?? response.promptFeedback?.blockReason
  throw new Error(reason ? `No audio returned (${reason})` : 'No audio returned')
}

// run 生成整段对话的音频
async function run(): Promise<void> {
  const model = currentModel.value
  if (state.running || model === undefined || !canRun.value) return
  stopPreview()
  clearAudio()
  state.error = ''
  state.running = true
  controller = new AbortController()
  const signal = controller.signal
  try {
    state.audio = URL.createObjectURL(await synthesize(model, state.blocks, signal))
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

// preview 与官网一致试听单段台词，再次点击停止
async function preview(block: SpeechBlock): Promise<void> {
  const model = currentModel.value
  if (state.previewing === block.id) {
    stopPreview()
    return
  }
  stopPreview()
  if (model === undefined || block.text.trim() === '') return
  state.previewing = block.id
  state.previewLoading = block.id
  previewController = new AbortController()
  const signal = previewController.signal
  try {
    const blob = await synthesize(model, [block], signal)
    if (state.previewing !== block.id) return
    previewAudio ??= new Audio()
    previewAudio.onended = () => (state.previewing = '')
    previewAudio.onerror = () => (state.previewing = '')
    if (previewAudio.src) URL.revokeObjectURL(previewAudio.src)
    previewAudio.src = URL.createObjectURL(blob)
    state.previewLoading = ''
    await previewAudio.play()
  } catch (error) {
    if (!signal.aborted) state.error = error instanceof Error ? error.message : String(error)
    if (state.previewing === block.id) stopPreview()
  }
}

// codePrompt 返回获取代码使用的台词文字：分段风格模型为官网的 Transcript 写法，旧版模型与运行请求相同
function codePrompt(): string {
  if (!daikon.value) return legacyPrompt(state.blocks, multi.value)
  const lines = state.blocks.filter((block) => block.text.trim() !== '')
  const transcript = multi.value
    ? lines.map((block) => `${state.speakers[block.speaker]!.alias}: ${block.text}`).join('\n')
    : lines.map((block) => block.text).join('\n')
  return `## Transcript:\n${transcript}`
}

// useSpeech 返回语音页状态与操作
export function useSpeech() {
  return {
    state,
    models: computed(models),
    currentModel,
    voices,
    daikon,
    tags,
    styles,
    multi,
    openAIMulti,
    canRun,
    usedSpeakers,
    voiceDetail,
    ensureModel,
    setModel,
    reset,
    newDialog,
    close,
    useTemplate,
    addBlock,
    removeBlock,
    requestBody,
    openAISpeechBody,
    codePrompt,
    run,
    stop,
    preview,
    stopPreview,
  }
}
