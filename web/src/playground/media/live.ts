import { computed, reactive } from 'vue'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import { readPreference, writePreference } from '@/preference'
import type { Model, ThinkingLevel } from '@/types'
import { defaultChatModel, hasCode } from '../catalog'
import { apiKey, errorMessage } from '../request'
import { parseFunctions } from '../schema'
import { audioInput } from '../shell/preferences'
import { openLiveSocket, type LiveServerMessage } from './live-socket'
import { createPcmPlayer } from './pcm-player'
import { bytesToBase64, pcmToWav, startPcmCapture, type PcmCapture } from './pcm-capture'

// LiveVariant 是 Live 模型的会话形态：对话、实时翻译或实时转录
export type LiveVariant = 'conversation' | 'translation' | 'transcription'

// liveVariantOf 按能力码返回模型的会话形态：46 为实时翻译，77 为实时转录
export function liveVariantOf(model: Model | undefined): LiveVariant {
  if (hasCode(model, 46)) return 'translation'
  if (model?.capabilities?.transcription_output) return 'transcription'
  return 'conversation'
}

// LiveSource 是会话的实时输入：麦克风、摄像头、屏幕或浏览器标签页音频
export type LiveSource = 'mic' | 'camera' | 'screen' | 'tab'

// LiveSettings 是 Live 页的运行设置
export interface LiveSettings {
  systemInstruction: string
  voice: string
  mediaResolution: 'low' | 'medium'
  thinkingLevel: ThinkingLevel | ''
  maxContext: number
  targetContext: number
  functionCalling: boolean
  functionDeclarations: string
  automaticResponse: boolean
  googleSearch: boolean
  targetLanguage: string
  echoTargetLanguage: boolean
  languages: string[]
  customVocabulary: string[]
  smartTranscription: boolean
}

// LivePart 是一轮中的内容：文本、思考、带转写的音频或函数调用
export type LivePart =
  | { type: 'text'; text: string }
  | { type: 'thought'; text: string }
  | { type: 'audio'; transcript: string; url: string; interrupted: boolean }
  | {
      type: 'call'
      id: string
      name: string
      args: Record<string, unknown>
      response: string
      generating: boolean
      sent: boolean
      cancelled: boolean
      error: string
      behavior: string
      willContinue: boolean
      responses: string[]
    }

// LiveTranscript 是翻译与转录面板中的一段转写，language 为大写的基础语言代码
export interface LiveTranscript {
  text: string
  language: string
}

// baseLanguage 返回语言代码的大写基础部分，例如 en-US 为 EN
function baseLanguage(code: string): string {
  return code.split('-')[0]!.toUpperCase()
}

// groupByLanguage 合并相邻同语言的转写片段，语言变化时另起一段
function groupByLanguage(chunks: readonly LiveTranscript[]): LiveTranscript[] {
  const paragraphs: LiveTranscript[] = []
  let language = ''
  let text = ''
  for (const chunk of chunks) {
    const next = chunk.language ? baseLanguage(chunk.language) : ''
    if (next !== language && language !== '') {
      paragraphs.push({ language, text })
      text = ''
    }
    language = next
    text += chunk.text
  }
  if (text || language) paragraphs.push({ language, text })
  return paragraphs
}

// firstLanguage 返回片段中第一个语言代码
function firstLanguage(chunks: readonly LiveTranscript[]): string {
  const chunk = chunks.find((item) => item.language)
  return chunk ? baseLanguage(chunk.language) : ''
}

// segments 按起点下标把片段切成连续的几段
function segments<T>(items: readonly T[], starts: readonly number[]): T[][] {
  return starts.map((start, index) => items.slice(start, starts[index + 1] ?? items.length))
}

// inputParagraphs 返回输入转写面板的段落：按模型输出段切分后再按语言分段
export function inputParagraphs(): LiveTranscript[] {
  return segments(state.input, state.inputStarts).flatMap(groupByLanguage)
}

// outputParagraphs 返回输出转写面板的段落：每个输出段一段，对应输入含多种语言时按比例切分
export function outputParagraphs(): LiveTranscript[] {
  const inputs = segments(state.input, state.inputStarts)
  return segments(state.output, state.outputStarts).flatMap((output, index) => {
    const input = inputs[index] ?? []
    if (output.length === 0) return []
    if (input.length === 0 || groupByLanguage(input).length <= 1)
      return [{ language: firstLanguage(output), text: output.map((item) => item.text).join('') }]
    const cuts = [0]
    let language = ''
    input.forEach((chunk, position) => {
      const next = chunk.language ? baseLanguage(chunk.language) : ''
      if (next !== language && language !== '') cuts.push(position)
      language = next
    })
    return cuts.flatMap((cut, position) => {
      const end = position < cuts.length - 1 ? cuts[position + 1]! : input.length
      const part = output.slice(
        Math.round((cut / input.length) * output.length),
        Math.round((end / input.length) * output.length),
      )
      return part.length
        ? [{ language: firstLanguage(part), text: part.map((item) => item.text).join('') }]
        : []
    })
  })
}

// LiveTurn 是一轮用户输入或模型输出
export interface LiveTurn {
  id: number
  role: 'user' | 'model'
  parts: LivePart[]
}

// LiveStatus 是会话状态：未连接、连接中、进行中或已结束
export type LiveStatus = 'idle' | 'connecting' | 'live' | 'ended'

// minContext 与 defaultMaxContext、defaultTargetContext 是官网 Session Context 滑块的下限与默认值
export const minContext = 16000
const defaultMaxContext = 104857
const defaultTargetContext = 52428

// defaultSettings 返回模型的默认运行设置
function defaultSettings(model: Model | undefined): LiveSettings {
  const voices = model?.voice_details?.map((voice) => voice.name) ?? []
  return {
    systemInstruction: '',
    voice: voices.includes('Zephyr') ? 'Zephyr' : (voices[0] ?? 'Zephyr'),
    mediaResolution: 'medium',
    thinkingLevel: model?.capabilities?.thinking_level
      ? (model.default_thinking_level ?? 'MINIMAL')
      : '',
    maxContext: defaultMaxContext,
    targetContext: defaultTargetContext,
    functionCalling: false,
    functionDeclarations: '',
    automaticResponse: false,
    googleSearch: false,
    targetLanguage: model?.languages?.[0]?.code ?? 'en',
    echoTargetLanguage: false,
    languages: [],
    customVocabulary: [],
    smartTranscription: false,
  }
}

const state = reactive({
  model: '',
  settings: defaultSettings(undefined),
  turns: [] as LiveTurn[],
  input: [] as LiveTranscript[],
  output: [] as LiveTranscript[],
  inputStarts: [0],
  outputStarts: [0],
  status: 'idle' as LiveStatus,
  sources: [] as LiveSource[],
  preview: undefined as MediaStream | undefined,
  interim: '',
  draft: '',
  error: '',
  disclaimer: false,
})
// disclaimerKey 是浏览器记住已确认 Live 首次使用提示的偏好键
const disclaimerKey = 'live.disclaimerAcknowledged'
let disclaimerAcknowledged = readPreference(disclaimerKey, false, (value) => value === true)
let socket: WebSocket | undefined
let ready: Promise<boolean> | undefined
// settleReady 结束正在建立的会话的等待，disconnect 时以 false 结束
let settleReady: ((connected: boolean) => void) | undefined
// sessionEpoch 在每次 disconnect 时递增，取得采集流期间会话已结束时据此释放采集流
let sessionEpoch = 0
let sessionToken = ''
let reconnect = false
let turnID = 0
let outputOpen = false
let modelAudio: Uint8Array<ArrayBuffer>[] = []
let audioRate = 24000
let capture: PcmCapture | undefined
let captureStream: MediaStream | undefined
let videoStream: MediaStream | undefined
let frameTimer = 0
const player = createPcmPlayer()

const currentModel = computed(() =>
  useConsole().models.value.find((model) => model.id === state.model),
)
const variant = computed(() => liveVariantOf(currentModel.value))

// selectModel 切换模型并恢复该模型的默认运行设置
function selectModel(id: string): void {
  disconnect()
  state.model = id
  state.settings = defaultSettings(currentModel.value)
  state.turns = []
  clearTranscripts()
  state.error = ''
}

// resetSettings 恢复当前模型的默认运行设置
function resetSettings(): void {
  state.settings = defaultSettings(currentModel.value)
}

// lastTurn 返回最后一轮，角色不同时新建一轮
function lastTurn(role: LiveTurn['role']): LiveTurn {
  const last = state.turns.at(-1)
  if (last?.role === role) return last
  const turn: LiveTurn = { id: ++turnID, role, parts: [] }
  state.turns.push(turn)
  return state.turns.at(-1)!
}

// lastPart 返回最后一轮中指定类型的最后一个部分，没有时新建
function lastPart<T extends LivePart['type']>(
  role: LiveTurn['role'],
  type: T,
  create: () => Extract<LivePart, { type: T }>,
): Extract<LivePart, { type: T }> {
  const turn = lastTurn(role)
  const last = turn.parts.at(-1)
  if (last?.type === type) return last as Extract<LivePart, { type: T }>
  turn.parts.push(create())
  return turn.parts.at(-1) as Extract<LivePart, { type: T }>
}

// modelAudioPart 返回模型当前轮的音频部分
function modelAudioPart(): Extract<LivePart, { type: 'audio' }> {
  return lastPart('model', 'audio', () => ({
    type: 'audio',
    transcript: '',
    url: '',
    interrupted: false,
  }))
}

// finishModelAudio 把当前轮收到的模型音频封装为可回放的 WAV
function finishModelAudio(interrupted = false): void {
  if (modelAudio.length === 0) return
  const part = modelAudioPart()
  part.url = URL.createObjectURL(pcmToWav(modelAudio, audioRate))
  part.interrupted = interrupted
  modelAudio = []
}

// transcriptOf 返回服务端帧中的转写片段
function transcriptOf(message: LiveServerMessage): LiveTranscript {
  return {
    text: message.transcription?.text ?? message.text ?? '',
    language: message.transcription?.language_code ?? '',
  }
}

// closeOutput 结束当前模型输出段，之后的输入转写归入下一段
function closeOutput(): void {
  if (!outputOpen) return
  outputOpen = false
  state.inputStarts.push(state.input.length)
}

// clearTranscripts 清空翻译与转录面板
function clearTranscripts(): void {
  state.input = []
  state.output = []
  state.inputStarts = [0]
  state.outputStarts = [0]
  outputOpen = false
}

// send 发送一帧客户端消息
function send(frame: Record<string, unknown>): void {
  if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(frame))
}

// setupFrame 按运行设置生成 /v1/live 的 setup 帧
function setupFrame(): Record<string, unknown> {
  const settings = state.settings
  const frame: Record<string, unknown> = { type: 'setup', model: state.model }
  if (sessionToken) frame.session_token = sessionToken
  if (variant.value === 'translation') {
    frame.input_modalities = ['audio']
    frame.output_modalities = ['audio']
    frame.translation = {
      target_language_code: settings.targetLanguage,
      echo_target_language: settings.echoTargetLanguage,
    }
  } else if (variant.value === 'transcription') {
    frame.input_modalities = ['audio']
    frame.output_modalities = ['text']
    frame.transcription = {
      language_codes: settings.languages,
      custom_vocabulary: settings.customVocabulary,
      smart: settings.smartTranscription,
    }
    return frame
  } else {
    frame.input_modalities = ['text', 'audio', 'image']
    frame.output_modalities = ['audio']
    frame.voice = settings.voice
    frame.media_resolution = settings.mediaResolution
    if (settings.thinkingLevel) frame.thinking_level = settings.thinkingLevel.toLowerCase()
    frame.context_window_compression = {
      trigger_tokens: settings.maxContext,
      target_tokens: settings.targetContext,
    }
    if (settings.googleSearch) frame.google_search = true
    if (settings.functionCalling) frame.tools = JSON.parse(settings.functionDeclarations || '[]')
  }
  if (settings.systemInstruction.trim()) frame.system_instruction = settings.systemInstruction
  return frame
}

// onMessage 处理服务端帧
function onMessage(message: LiveServerMessage): void {
  if (variant.value !== 'conversation') {
    onTranscriptMessage(message)
    return
  }
  switch (message.type) {
    case 'session_resumption':
      if (message.session_token) sessionToken = message.session_token
      break
    case 'media':
      if (!message.data || !message.mime_type?.startsWith('audio/')) break
      player.enqueue(message.data, message.mime_type)
      audioRate = Number(/rate=(\d+)/.exec(message.mime_type)?.[1] ?? 24000)
      modelAudio.push(Uint8Array.from(atob(message.data), (char) => char.charCodeAt(0)))
      modelAudioPart()
      break
    case 'output_transcription':
      modelAudioPart().transcript += message.transcription?.text ?? message.text ?? ''
      break
    case 'input_transcription':
      state.interim = ''
      lastPart('user', 'text', () => ({ type: 'text', text: '' })).text +=
        message.transcription?.text ?? message.text ?? ''
      break
    case 'interim_input_transcription':
      state.interim = message.transcription?.text ?? message.text ?? ''
      break
    case 'text':
      lastPart('model', 'text', () => ({ type: 'text', text: '' })).text += message.text ?? ''
      break
    case 'provider': {
      const raw = (message as { raw?: { kind?: string; text?: string } }).raw
      if (raw?.kind === 'reasoning' && raw.text)
        lastPart('model', 'thought', () => ({ type: 'thought', text: '' })).text += raw.text
      break
    }
    case 'tool_call':
      if (message.tool_call) addCall(message.tool_call)
      break
    case 'tool_call_cancellation':
      for (const turn of state.turns)
        for (const part of turn.parts)
          if (part.type === 'call' && message.tool_call_ids?.includes(part.id))
            part.cancelled = true
      break
    case 'interrupted':
      player.interrupt()
      finishModelAudio(true)
      break
    case 'generation_complete':
    case 'turn_complete':
      finishModelAudio()
      break
    case 'go_away':
      reconnect = sessionToken !== ''
      break
    case 'error':
      state.error = message.error ?? message.code ?? 'error'
      break
  }
}

// onTranscriptMessage 处理实时翻译与转录会话的服务端帧：音频只播放，转写写入面板
function onTranscriptMessage(message: LiveServerMessage): void {
  switch (message.type) {
    case 'session_resumption':
      if (message.session_token) sessionToken = message.session_token
      break
    case 'media':
      if (message.data && message.mime_type?.startsWith('audio/'))
        player.enqueue(message.data, message.mime_type)
      break
    case 'input_transcription':
      state.interim = ''
      state.input.push(transcriptOf(message))
      break
    case 'interim_input_transcription':
      state.interim = message.transcription?.text ?? message.text ?? ''
      break
    case 'output_transcription':
    case 'text':
      if (!outputOpen) {
        if (state.output.length > 0) state.outputStarts.push(state.output.length)
        outputOpen = true
      }
      state.output.push(transcriptOf(message))
      break
    case 'interrupted':
      player.interrupt()
      closeOutput()
      break
    case 'generation_complete':
    case 'turn_complete':
      closeOutput()
      break
    case 'go_away':
      reconnect = sessionToken !== ''
      break
    case 'error':
      state.error = message.error ?? message.code ?? 'error'
      break
  }
}

// addCall 显示模型的函数调用，开启自动函数响应时生成响应填入输入框；behavior 取自同名函数声明
function addCall(call: NonNullable<LiveServerMessage['tool_call']>): void {
  const declaration = parseFunctions(state.settings.functionDeclarations).find(
    (item) => item.name === call.name,
  )
  const behavior = declaration?.extra?.behavior
  lastTurn('model').parts.push({
    type: 'call',
    id: call.id,
    name: call.name,
    args: call.arguments,
    response: '',
    generating: state.settings.automaticResponse,
    sent: false,
    cancelled: false,
    error: '',
    behavior: typeof behavior === 'string' ? behavior.toUpperCase() : '',
    willContinue: false,
    responses: [],
  })
  const part = state.turns.at(-1)!.parts.at(-1) as Extract<LivePart, { type: 'call' }>
  if (state.settings.automaticResponse) void answerCall(part)
}

// answerCall 用文本模型按函数声明为调用生成一份 JSON 响应
async function answerCall(part: Extract<LivePart, { type: 'call' }>): Promise<void> {
  const declaration = parseFunctions(state.settings.functionDeclarations).find(
    (item) => item.name === part.name,
  )
  const generator = defaultChatModel(useConsole().models.value)
  try {
    const response = await fetch(
      `/v1beta/models/${generator?.id ?? 'gemini-flash-latest'}:generateContent`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'x-goog-api-key': apiKey() },
        body: JSON.stringify({
          contents: [
            {
              role: 'user',
              parts: [
                {
                  text: `Generate a realistic response for this function call as a JSON object.\nFunction declaration: ${JSON.stringify(declaration ?? { name: part.name })}\nCall arguments: ${JSON.stringify(part.args)}`,
                },
              ],
            },
          ],
          generationConfig: { responseMimeType: 'application/json' },
        }),
      },
    )
    if (!response.ok) throw new Error(await errorMessage(response))
    const result = (await response.json()) as {
      candidates?: { content?: { parts?: { text?: string; thought?: boolean }[] } }[]
    }
    part.response =
      result.candidates?.[0]?.content?.parts
        ?.filter((item) => !item.thought)
        .map((item) => item.text ?? '')
        .join('') ?? '{}'
  } catch (error) {
    part.error = error instanceof Error ? error.message : String(error)
  } finally {
    part.generating = false
  }
}

// respond 把函数调用的响应发送给模型；Will continue 打开时保留输入框，之后可再次响应同一调用
function respond(part: Extract<LivePart, { type: 'call' }>): void {
  let content: unknown
  try {
    const value: unknown = JSON.parse(part.response)
    content =
      typeof value === 'object' && value !== null && !Array.isArray(value)
        ? value
        : { response: value }
  } catch {
    content = { response: part.response }
  }
  const willContinue = part.behavior === 'NON_BLOCKING' && part.willContinue
  send({
    type: 'tool_response',
    tool_responses: [
      { id: part.id, name: part.name, content, ...(willContinue ? { will_continue: true } : {}) },
    ],
  })
  if (willContinue) part.responses.push(part.response)
  else part.sent = true
}

// connect 打开会话并在 setup_complete 后返回 true
function connect(): Promise<boolean> {
  if (ready) return ready
  state.error = ''
  state.status = 'connecting'
  const frame = setupFrame()
  ready = new Promise<boolean>((resolve) => {
    settleReady = resolve
    const current = openLiveSocket()
    socket = current
    current.onopen = () => current.send(JSON.stringify(frame))
    current.onmessage = (event) => {
      const message = JSON.parse(String(event.data)) as LiveServerMessage
      if (message.type === 'setup_complete') {
        state.status = 'live'
        resolve(true)
      }
      onMessage(message)
    }
    current.onclose = () => {
      if (socket !== current) return
      socket = undefined
      ready = undefined
      finishModelAudio()
      resolve(false)
      if (reconnect && state.status === 'live') {
        reconnect = false
        void connect()
        return
      }
      stopSources()
      state.status = state.status === 'connecting' && !state.error ? 'idle' : 'ended'
    }
  })
  return ready
}

// disconnect 结束会话与全部实时输入
function disconnect(): void {
  const current = socket
  socket = undefined
  ready = undefined
  sessionEpoch++
  settleReady?.(false)
  settleReady = undefined
  reconnect = false
  sessionToken = ''
  if (current) {
    if (current.readyState === WebSocket.OPEN) current.send(JSON.stringify({ type: 'close' }))
    current.close()
  }
  stopSources()
  player.interrupt()
  finishModelAudio()
  if (state.status !== 'idle') state.status = 'ended'
}

// newSession 清空对话并结束会话
function newSession(): void {
  disconnect()
  state.turns = []
  clearTranscripts()
  state.interim = ''
  state.error = ''
  state.status = 'idle'
}

// requireDisclaimer 尚未确认 Live 首次使用提示时打开确认对话框并返回 true
function requireDisclaimer(): boolean {
  if (disclaimerAcknowledged) return false
  state.disclaimer = true
  return true
}

// acknowledgeDisclaimer 记住确认并关闭对话框，与官网一样由用户再次运行
function acknowledgeDisclaimer(): void {
  disclaimerAcknowledged = true
  writePreference(disclaimerKey, true)
  state.disclaimer = false
}

// sendText 建立会话后发送一条文本
async function sendText(text: string): Promise<void> {
  const value = text.trim()
  if (value === '' || requireDisclaimer()) return
  if (!(await connect())) return
  await player.resume()
  lastTurn('user').parts.push({ type: 'text', text: value })
  send({ type: 'text', text: value })
}

// stopSources 停止全部实时输入
function stopSources(): void {
  capture?.stop()
  capture = undefined
  for (const stream of [captureStream, videoStream])
    stream?.getTracks().forEach((track) => track.stop())
  captureStream = undefined
  videoStream = undefined
  window.clearInterval(frameTimer)
  state.sources = []
  state.preview = undefined
}

// stopSource 停止一路实时输入
function stopSource(source: LiveSource): void {
  if (source === 'mic' || source === 'tab') {
    capture?.stop()
    capture = undefined
    captureStream?.getTracks().forEach((track) => track.stop())
    captureStream = undefined
    send({ type: 'media_end' })
  } else {
    window.clearInterval(frameTimer)
    videoStream?.getTracks().forEach((track) => track.stop())
    videoStream = undefined
    state.preview = undefined
  }
  state.sources = state.sources.filter((item) =>
    source === 'mic' || source === 'tab'
      ? item !== 'mic' && item !== 'tab'
      : item !== 'camera' && item !== 'screen',
  )
}

// startAudio 采集麦克风或标签页音频并按 16 kHz PCM 发送
async function startAudio(stream: MediaStream, source: 'mic' | 'tab'): Promise<void> {
  if (stream.getAudioTracks().length === 0) {
    stream.getTracks().forEach((track) => track.stop())
    throw new Error(useI18n().t('pg.tabNoAudio'))
  }
  captureStream = stream
  capture = await startPcmCapture(new MediaStream(stream.getAudioTracks()), (data) =>
    send({ type: 'audio', mime_type: 'audio/pcm', data }),
  )
  stream.getAudioTracks()[0]?.addEventListener('ended', () => stopSource(source))
  state.sources.push(source)
}

// startVideo 预览摄像头或屏幕，每秒发送一帧 JPEG
function startVideo(stream: MediaStream, source: 'camera' | 'screen'): void {
  videoStream = stream
  state.preview = stream
  const video = document.createElement('video')
  video.muted = true
  video.srcObject = stream
  void video.play()
  const canvas = document.createElement('canvas')
  frameTimer = window.setInterval(() => {
    if (video.videoWidth === 0) return
    const scale = Math.min(1, 1024 / Math.max(video.videoWidth, video.videoHeight))
    canvas.width = Math.round(video.videoWidth * scale)
    canvas.height = Math.round(video.videoHeight * scale)
    canvas.getContext('2d')?.drawImage(video, 0, 0, canvas.width, canvas.height)
    canvas.toBlob(
      (blob) =>
        void blob?.arrayBuffer().then((buffer) =>
          send({
            type: 'image',
            mime_type: 'image/jpeg',
            data: bytesToBase64(new Uint8Array(buffer)),
          }),
        ),
      'image/jpeg',
      0.8,
    )
  }, 1000)
  stream.getVideoTracks()[0]?.addEventListener('ended', () => stopSource(source))
  state.sources.push(source)
}

// toggleSource 打开或关闭一路实时输入，打开前先建立会话
async function toggleSource(source: LiveSource): Promise<void> {
  if (state.sources.includes(source)) {
    stopSource(source)
    return
  }
  if (requireDisclaimer()) return
  state.error = ''
  const epoch = sessionEpoch
  try {
    let stream: MediaStream
    if (source === 'mic')
      stream = await navigator.mediaDevices.getUserMedia({ audio: audioInput() })
    else if (source === 'camera')
      stream = await navigator.mediaDevices.getUserMedia({ video: true })
    else
      stream = await navigator.mediaDevices.getDisplayMedia({
        video: true,
        audio: source === 'tab',
      })
    const connected = epoch === sessionEpoch && (await connect())
    if (connected) await player.resume()
    if (!connected || epoch !== sessionEpoch) {
      stream.getTracks().forEach((track) => track.stop())
      return
    }
    if (source === 'mic' || source === 'tab') {
      if (state.sources.includes('mic')) stopSource('mic')
      if (state.sources.includes('tab')) stopSource('tab')
      await startAudio(stream, source)
    } else {
      if (state.sources.includes('camera')) stopSource('camera')
      if (state.sources.includes('screen')) stopSource('screen')
      startVideo(stream, source)
    }
  } catch (error) {
    state.error = error instanceof Error ? error.message : String(error)
  }
}

// useLive 返回 Live 页状态与操作
export function useLive() {
  return {
    state,
    currentModel,
    variant,
    selectModel,
    resetSettings,
    sendText,
    requireDisclaimer,
    acknowledgeDisclaimer,
    toggleSource,
    disconnect,
    newSession,
    respond,
    setupFrame,
  }
}
