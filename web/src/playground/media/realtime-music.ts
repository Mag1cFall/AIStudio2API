import { reactive } from 'vue'
import { newTurnID } from '../session'
import { openLiveSocket, type LiveServerMessage } from './live-socket'
import { createPcmPlayer } from './pcm-player'

// WeightedPrompt 是实时音乐的一条带权重提示词
export interface WeightedPrompt {
  id: string
  text: string
  weight: number
}

// MusicConfig 是 Gemini API LiveMusicGenerationConfig 的字段，空值不发送
export interface MusicConfig {
  temperature: number
  topK: number
  guidance: number
  seed: number | null
  bpm: number | null
  density: number | null
  brightness: number | null
  scale: string
  muteBass: boolean
  muteDrums: boolean
  onlyBassAndDrums: boolean
  musicGenerationMode: string
}

// RealtimeStatus 是实时音乐会话状态
export type RealtimeStatus = 'idle' | 'connecting' | 'loading' | 'playing' | 'paused'

// musicScales 是 LiveMusicGenerationConfig.scale 的取值
export const musicScales = [
  'C_MAJOR_A_MINOR',
  'D_FLAT_MAJOR_B_FLAT_MINOR',
  'D_MAJOR_B_MINOR',
  'E_FLAT_MAJOR_C_MINOR',
  'E_MAJOR_D_FLAT_MINOR',
  'F_MAJOR_D_MINOR',
  'G_FLAT_MAJOR_E_FLAT_MINOR',
  'G_MAJOR_E_MINOR',
  'A_FLAT_MAJOR_F_MINOR',
  'A_MAJOR_G_FLAT_MINOR',
  'B_FLAT_MAJOR_G_MINOR',
  'B_MAJOR_A_FLAT_MINOR',
] as const

// musicModes 是 LiveMusicGenerationConfig.musicGenerationMode 的取值
export const musicModes = ['QUALITY', 'DIVERSITY', 'VOCALIZATION'] as const

// promptSuggestions 是 Lyria RealTime 示例应用 PromptDJ 的提示词
export const promptSuggestions = [
  'Bossa Nova',
  'Chillwave',
  'Drum and Bass',
  'Post Punk',
  'Shoegaze',
  'Funk',
  'Chiptune',
  'Lush Strings',
  'Sparkling Arpeggios',
  'Staccato Rhythms',
  'Punchy Kick',
  'Dubstep',
  'K Pop',
  'Neo Soul',
  'Trip Hop',
  'Thrash',
]

// defaultConfig 返回 Lyria RealTime 的默认生成参数
function defaultConfig(): MusicConfig {
  return {
    temperature: 1.1,
    topK: 40,
    guidance: 4,
    seed: null,
    bpm: null,
    density: null,
    brightness: null,
    scale: '',
    muteBass: false,
    muteDrums: false,
    onlyBassAndDrums: false,
    musicGenerationMode: 'QUALITY',
  }
}

const state = reactive({
  prompts: [] as WeightedPrompt[],
  config: defaultConfig(),
  status: 'idle' as RealtimeStatus,
  error: '',
})
let socket: WebSocket | undefined
let ready = false
let promptTimer = 0
let configTimer = 0
let sentConfig = ''
const player = createPcmPlayer(1)

// send 发送一帧客户端消息
function send(frame: Record<string, unknown>): void {
  if (ready && socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(frame))
}

// activePrompts 返回非空提示词
function activePrompts(): { text: string; weight: number }[] {
  return state.prompts
    .filter((prompt) => prompt.text.trim() !== '')
    .map((prompt) => ({ text: prompt.text.trim(), weight: prompt.weight }))
}

// configFrame 返回只含已设置字段的 music_config
function configFrame(): Record<string, unknown> {
  const config: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(state.config))
    if (value !== null && value !== '') config[key] = value
  return config
}

// sendPrompts 发送当前提示词
function sendPrompts(): void {
  const prompts = activePrompts()
  if (prompts.length > 0) send({ type: 'music_prompts', weighted_prompts: prompts })
}

// sendConfig 发送生成参数；BPM 或调式变化时按 Lyria RealTime 规则重置上下文后生效
function sendConfig(): void {
  const config = configFrame()
  const previous = sentConfig ? (JSON.parse(sentConfig) as Record<string, unknown>) : {}
  sentConfig = JSON.stringify(config)
  send({ type: 'music_config', music_config: config })
  if (previous.bpm !== config.bpm || previous.scale !== config.scale) {
    if (state.status === 'playing' || state.status === 'loading') {
      player.interrupt()
      send({ type: 'playback', playback_control: 'reset_context' })
    }
  }
}

// promptsChanged 在提示词编辑后短暂合并再发送
function promptsChanged(): void {
  window.clearTimeout(promptTimer)
  promptTimer = window.setTimeout(sendPrompts, 300)
}

// configChanged 在参数编辑后短暂合并再发送
function configChanged(): void {
  window.clearTimeout(configTimer)
  configTimer = window.setTimeout(sendConfig, 300)
}

// onMessage 处理服务端帧
function onMessage(message: LiveServerMessage): void {
  if (message.type === 'setup_complete') {
    ready = true
    sendPrompts()
    sentConfig = JSON.stringify(configFrame())
    send({ type: 'music_config', music_config: configFrame() })
    send({ type: 'playback', playback_control: 'play' })
    state.status = 'loading'
  } else if (message.type === 'media' && message.data && message.mime_type) {
    if (state.status === 'paused') return
    player.enqueue(message.data, message.mime_type)
    state.status = 'playing'
  } else if (message.type === 'error') {
    state.error = message.error ?? message.code ?? 'error'
  }
}

// connect 打开实时音乐会话
function connect(model: string): void {
  state.error = ''
  state.status = 'connecting'
  ready = false
  socket = openLiveSocket()
  socket.onopen = () =>
    socket?.send(
      JSON.stringify({
        type: 'setup',
        model,
        input_modalities: ['text'],
        output_modalities: ['audio'],
      }),
    )
  socket.onmessage = (event) => onMessage(JSON.parse(String(event.data)) as LiveServerMessage)
  socket.onclose = () => {
    socket = undefined
    ready = false
    if (state.status !== 'idle') state.status = 'idle'
  }
}

// play 开始或继续播放；没有提示词时不连接
async function play(model: string): Promise<void> {
  if (activePrompts().length === 0) return
  await player.resume()
  if (socket && ready) {
    send({ type: 'playback', playback_control: 'play' })
    state.status = 'loading'
    return
  }
  connect(model)
}

// pause 暂停播放，会话保持
async function pause(): Promise<void> {
  send({ type: 'playback', playback_control: 'pause' })
  player.interrupt()
  state.status = 'paused'
  await player.suspend()
}

// stop 停止播放并关闭会话
function stop(): void {
  send({ type: 'playback', playback_control: 'stop' })
  send({ type: 'close' })
  socket?.close()
  socket = undefined
  ready = false
  player.interrupt()
  state.status = 'idle'
}

// resetContext 清空模型已生成的上下文，从当前提示词重新开始
function resetContext(): void {
  player.interrupt()
  send({ type: 'playback', playback_control: 'reset_context' })
}

// addPrompt 加入一条提示词
function addPrompt(text = ''): void {
  state.prompts.push({ id: newTurnID(), text, weight: 1 })
  promptsChanged()
}

// removePrompt 删除一条提示词
function removePrompt(index: number): void {
  state.prompts.splice(index, 1)
  promptsChanged()
}

// resetConfig 恢复默认生成参数
function resetConfig(): void {
  state.config = defaultConfig()
  configChanged()
}

// useRealtimeMusic 返回实时音乐状态与操作
export function useRealtimeMusic() {
  return {
    state,
    play,
    pause,
    stop,
    resetContext,
    addPrompt,
    removePrompt,
    promptsChanged,
    configChanged,
    resetConfig,
  }
}
