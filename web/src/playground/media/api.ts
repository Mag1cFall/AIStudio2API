import { apiKey, errorMessage, StreamError } from '../request'
import type { Part } from '../types'

// InlineImage 是请求中的内联图片
export interface InlineImage {
  mimeType: string
  data: string
}

// GenerateResponse 是 generateContent 的响应
export interface GenerateResponse {
  candidates?: { content?: { parts?: Part[] }; finishReason?: string }[]
  promptFeedback?: { blockReason?: string }
}

// VideoOperation 是 Gemini 视频长任务的状态
export interface VideoOperation {
  name: string
  done?: boolean
  error?: { message?: string }
  response?: {
    generateVideoResponse?: { generatedSamples?: { video?: { uri?: string } }[] }
  }
}

// headers 返回带本服务 API key 的请求头
function headers(json = true): Headers {
  const result = new Headers()
  if (json) result.set('Content-Type', 'application/json')
  const key = apiKey()
  if (key !== '') result.set('x-goog-api-key', key)
  return result
}

// post 以 JSON 调用本服务公开接口，非 2xx 时抛出上游说明
async function post<T>(path: string, body: unknown, signal: AbortSignal): Promise<T> {
  const response = await fetch(path, {
    method: 'POST',
    headers: headers(),
    body: JSON.stringify(body),
    signal,
  })
  if (!response.ok) throw new StreamError(await errorMessage(response), response.status)
  return (await response.json()) as T
}

// generateContent 调用 generateContent
export function generateContent(
  modelID: string,
  body: unknown,
  signal: AbortSignal,
): Promise<GenerateResponse> {
  return post(`/v1beta/models/${encodeURIComponent(modelID)}:generateContent`, body, signal)
}

// PredictImagesInput 是 Imagen predict 请求的提示词与参数
export interface PredictImagesInput {
  prompt: string
  sampleCount: number
  aspectRatio: string
}

// predictImages 调用 Imagen 模型的 predict，返回生成图片的 data 地址
export async function predictImages(
  modelID: string,
  input: PredictImagesInput,
  signal: AbortSignal,
): Promise<string[]> {
  const result = await post<{
    predictions?: { bytesBase64Encoded?: string; mimeType?: string; raiFilteredReason?: string }[]
  }>(
    `/v1beta/models/${encodeURIComponent(modelID)}:predict`,
    {
      instances: [{ prompt: input.prompt }],
      parameters: { sampleCount: input.sampleCount, aspectRatio: input.aspectRatio },
    },
    signal,
  )
  const predictions = result.predictions ?? []
  const images = predictions
    .filter((item) => item.bytesBase64Encoded)
    .map((item) => `data:${item.mimeType ?? 'image/png'};base64,${item.bytesBase64Encoded}`)
  if (images.length === 0)
    throw new Error(
      predictions.find((item) => item.raiFilteredReason)?.raiFilteredReason ?? 'No images',
    )
  return images
}

// OpenAIVideo 是 OpenAI Videos 视频对象
export interface OpenAIVideo {
  id: string
  status: string
  error?: { message?: string } | null
}

// createOpenAIVideo 以 multipart 调用 /v1/videos 创建视频任务
export async function createOpenAIVideo(form: FormData, signal: AbortSignal): Promise<OpenAIVideo> {
  const response = await fetch('/v1/videos', {
    method: 'POST',
    headers: headers(false),
    body: form,
    signal,
  })
  if (!response.ok) throw new StreamError(await errorMessage(response), response.status)
  return (await response.json()) as OpenAIVideo
}

// waitOpenAIVideo 每 5 秒轮询 OpenAI 视频对象直到完成，返回结果视频地址
export async function waitOpenAIVideo(video: OpenAIVideo, signal: AbortSignal): Promise<string> {
  let current = video
  while (current.status !== 'completed' && current.status !== 'failed') {
    await new Promise<void>((resolve, reject) => {
      const timer = window.setTimeout(resolve, 5000)
      signal.addEventListener(
        'abort',
        () => {
          window.clearTimeout(timer)
          reject(new DOMException('Aborted', 'AbortError'))
        },
        { once: true },
      )
    })
    const response = await fetch(`/v1/videos/${encodeURIComponent(video.id)}`, {
      headers: headers(false),
      signal,
    })
    if (!response.ok) throw new StreamError(await errorMessage(response), response.status)
    current = (await response.json()) as OpenAIVideo
  }
  if (current.status === 'failed')
    throw new StreamError(current.error?.message ?? 'Video generation failed', 500)
  return `/v1/videos/${encodeURIComponent(video.id)}/content`
}

// createVideo 调用 predictLongRunning 创建视频任务
export function createVideo(
  modelID: string,
  body: unknown,
  signal: AbortSignal,
): Promise<VideoOperation> {
  return post(`/v1beta/models/${encodeURIComponent(modelID)}:predictLongRunning`, body, signal)
}

// waitVideo 每 5 秒轮询视频任务直到完成，返回生成的视频地址
export async function waitVideo(operation: VideoOperation, signal: AbortSignal): Promise<string[]> {
  let current = operation
  while (current.done !== true) {
    await new Promise<void>((resolve, reject) => {
      const timer = window.setTimeout(resolve, 5000)
      signal.addEventListener(
        'abort',
        () => {
          window.clearTimeout(timer)
          reject(new DOMException('Aborted', 'AbortError'))
        },
        { once: true },
      )
    })
    const response = await fetch(`/v1beta/${current.name}`, { headers: headers(false), signal })
    if (!response.ok) throw new StreamError(await errorMessage(response), response.status)
    current = (await response.json()) as VideoOperation
  }
  if (current.error) throw new StreamError(current.error.message ?? 'Video generation failed', 500)
  const samples = current.response?.generateVideoResponse?.generatedSamples ?? []
  return samples.map((sample) => sample.video?.uri ?? '').filter((uri) => uri !== '')
}

// downloadMedia 下载本服务返回的媒体地址
export async function downloadMedia(url: string, signal?: AbortSignal): Promise<Blob> {
  const response = await fetch(url, { headers: headers(false), signal: signal ?? null })
  if (!response.ok) throw new StreamError(await errorMessage(response), response.status)
  return response.blob()
}

// readImage 把图片文件读为内联图片
export async function readImage(file: Blob): Promise<InlineImage> {
  const buffer = new Uint8Array(await file.arrayBuffer())
  let binary = ''
  for (let index = 0; index < buffer.length; index += 0x8000) {
    binary += String.fromCharCode(...buffer.subarray(index, index + 0x8000))
  }
  return { mimeType: file.type || 'image/png', data: btoa(binary) }
}

// base64Bytes 把 base64 文本解码为字节
export function base64Bytes(data: string): Uint8Array<ArrayBuffer> {
  return Uint8Array.from(atob(data), (char) => char.charCodeAt(0))
}

// audioBlob 把响应中的音频片段合成一个可播放文件：audio/L16 PCM 封装为 16 位 WAV，其余格式按原样拼接
export function audioBlob(parts: readonly Part[]): Blob | undefined {
  const audio = parts.filter((part) => part.inlineData?.mimeType.startsWith('audio/'))
  if (audio.length === 0) return undefined
  const mime = audio[0]!.inlineData!.mimeType
  const chunks = audio.map((part) => base64Bytes(part.inlineData!.data))
  if (!mime.toLowerCase().startsWith('audio/l16')) return new Blob(chunks, { type: mime })
  const parameters = new Map(
    mime
      .split(';')
      .slice(1)
      .map((item) => item.trim().split('=') as [string, string]),
  )
  const rate = Number(parameters.get('rate') ?? 24000)
  const channels = Number(parameters.get('channels') ?? 1)
  const length = chunks.reduce((total, chunk) => total + chunk.length, 0)
  const header = new DataView(new ArrayBuffer(44))
  const text = (offset: number, value: string): void => {
    for (let index = 0; index < value.length; index++)
      header.setUint8(offset + index, value.charCodeAt(index))
  }
  text(0, 'RIFF')
  header.setUint32(4, 36 + length, true)
  text(8, 'WAVEfmt ')
  header.setUint32(16, 16, true)
  header.setUint16(20, 1, true)
  header.setUint16(22, channels, true)
  header.setUint32(24, rate, true)
  header.setUint32(28, rate * channels * 2, true)
  header.setUint16(32, channels * 2, true)
  header.setUint16(34, 16, true)
  text(36, 'data')
  header.setUint32(40, length, true)
  return new Blob([header, ...chunks], { type: 'audio/wav' })
}

// responseText 返回响应正文中的文本
export function responseText(parts: readonly Part[]): string {
  return parts
    .filter((part) => part.text !== undefined && !part.thought)
    .map((part) => part.text)
    .join('')
}
