// PcmPlayer 是按到达顺序连续播放 L16 PCM 音频块的播放器
export interface PcmPlayer {
  enqueue(base64: string, mimeType: string): void
  interrupt(): void
  suspend(): Promise<void>
  resume(): Promise<void>
  close(): void
  readonly context: AudioContext | undefined
}

// pcmFormat 从 audio/l16;rate=48000;channels=2 这类 MIME 读取采样率与声道数
function pcmFormat(mimeType: string): { rate: number; channels: number } {
  const parameters = new Map(
    mimeType
      .split(';')
      .slice(1)
      .map((item) => item.trim().split('=') as [string, string]),
  )
  return {
    rate: Number(parameters.get('rate') ?? 24000),
    channels: Number(parameters.get('channels') ?? 1),
  }
}

// createPcmPlayer 创建 PCM 播放器；lead 是开始或打断后首块之前预留的缓冲秒数
export function createPcmPlayer(lead = 0.1): PcmPlayer {
  let context: AudioContext | undefined
  let next = 0
  const sources = new Set<AudioBufferSourceNode>()

  // enqueue 解码一块 16 位小端 PCM 并接在已排队音频之后播放
  function enqueue(base64: string, mimeType: string): void {
    const { rate, channels } = pcmFormat(mimeType)
    context ??= new AudioContext({ sampleRate: rate })
    const binary = atob(base64)
    const samples = new Int16Array(binary.length >> 1)
    for (let index = 0; index < samples.length; index++)
      samples[index] = binary.charCodeAt(index * 2) | (binary.charCodeAt(index * 2 + 1) << 8)
    const frames = Math.floor(samples.length / channels)
    if (frames === 0) return
    const buffer = context.createBuffer(channels, frames, rate)
    for (let channel = 0; channel < channels; channel++) {
      const data = buffer.getChannelData(channel)
      for (let frame = 0; frame < frames; frame++)
        data[frame] = samples[frame * channels + channel]! / 32768
    }
    const source = context.createBufferSource()
    source.buffer = buffer
    source.connect(context.destination)
    const start = next > context.currentTime ? next : context.currentTime + lead
    source.start(start)
    next = start + buffer.duration
    sources.add(source)
    source.onended = () => sources.delete(source)
  }

  // interrupt 立即停止已排队的音频
  function interrupt(): void {
    for (const source of sources) source.stop()
    sources.clear()
    next = 0
  }

  return {
    enqueue,
    interrupt,
    suspend: async () => {
      await context?.suspend()
    },
    resume: async () => {
      await context?.resume()
    },
    close: () => {
      interrupt()
      void context?.close()
      context = undefined
    },
    get context() {
      return context
    },
  }
}
