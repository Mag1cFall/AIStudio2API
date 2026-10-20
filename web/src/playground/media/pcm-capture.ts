// captureProcessor 是把输入音频按 100 ms 切成 16 位小端 PCM 的 AudioWorklet 处理器
const captureProcessor = `
class PcmCapture extends AudioWorkletProcessor {
  constructor() {
    super()
    this.buffer = new Int16Array(1600)
    this.length = 0
  }
  process(inputs) {
    const input = inputs[0][0]
    if (input) {
      for (let index = 0; index < input.length; index++) {
        const value = Math.max(-1, Math.min(1, input[index]))
        this.buffer[this.length++] = value < 0 ? value * 0x8000 : value * 0x7fff
        if (this.length === this.buffer.length) {
          this.port.postMessage(this.buffer.slice().buffer)
          this.length = 0
        }
      }
    }
    return true
  }
}
registerProcessor('pcm-capture', PcmCapture)
`

// bytesToBase64 把字节编码为 Base64
export function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  for (let index = 0; index < bytes.length; index += 0x8000)
    binary += String.fromCharCode(...bytes.subarray(index, index + 0x8000))
  return btoa(binary)
}

// PcmCapture 是一路正在采集的音频
export interface PcmCapture {
  stop(): void
}

// startPcmCapture 把音频流转为 16 kHz 单声道 16 位 PCM，每 100 ms 回调一次 Base64 数据
export async function startPcmCapture(
  stream: MediaStream,
  onChunk: (base64: string) => void,
): Promise<PcmCapture> {
  const context = new AudioContext({ sampleRate: 16000 })
  const url = URL.createObjectURL(new Blob([captureProcessor], { type: 'application/javascript' }))
  try {
    await context.audioWorklet.addModule(url)
  } finally {
    URL.revokeObjectURL(url)
  }
  const source = context.createMediaStreamSource(stream)
  const node = new AudioWorkletNode(context, 'pcm-capture')
  node.port.onmessage = (event: MessageEvent<ArrayBuffer>) =>
    onChunk(bytesToBase64(new Uint8Array(event.data)))
  source.connect(node)
  return {
    stop() {
      node.port.onmessage = null
      source.disconnect()
      node.disconnect()
      void context.close()
    },
  }
}

// pcmToWav 把 16 位小端单声道 PCM 片段封装为 WAV
export function pcmToWav(chunks: readonly Uint8Array<ArrayBuffer>[], rate: number): Blob {
  const size = chunks.reduce((total, chunk) => total + chunk.length, 0)
  const header = new DataView(new ArrayBuffer(44))
  const text = (offset: number, value: string) => {
    for (let index = 0; index < value.length; index++)
      header.setUint8(offset + index, value.charCodeAt(index))
  }
  text(0, 'RIFF')
  header.setUint32(4, 36 + size, true)
  text(8, 'WAVE')
  text(12, 'fmt ')
  header.setUint32(16, 16, true)
  header.setUint16(20, 1, true)
  header.setUint16(22, 1, true)
  header.setUint32(24, rate, true)
  header.setUint32(28, rate * 2, true)
  header.setUint16(32, 2, true)
  header.setUint16(34, 16, true)
  text(36, 'data')
  header.setUint32(40, size, true)
  return new Blob([header, ...chunks], { type: 'audio/wav' })
}
