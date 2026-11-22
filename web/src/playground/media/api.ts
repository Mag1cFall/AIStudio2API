import { useI18n } from '@/i18n'
import { base64Bytes, bytesToBase64, pcmToWav } from '../bytes'
import { apiKey, errorMessage } from '../request'
import type { Part } from '../types'
import { pcmFormat } from './pcm-player'

const { t } = useI18n()

// InlineImage 是请求中的内联图片
export interface InlineImage {
  mimeType: string
  data: string
}

// GenerateResponse 是 generateContent 的响应
interface GenerateResponse {
  candidates?: { content?: { parts?: Part[] }; finishReason?: string }[]
  promptFeedback?: { blockReason?: string }
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
  if (!response.ok) throw new Error(await errorMessage(response))
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
interface PredictImagesInput {
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
      predictions.find((item) => item.raiFilteredReason)?.raiFilteredReason ?? t('pg.noImages'),
    )
  return images
}

// downloadMedia 下载本服务返回的媒体地址
export async function downloadMedia(url: string, signal?: AbortSignal): Promise<Blob> {
  const response = await fetch(url, { headers: headers(false), signal: signal ?? null })
  if (!response.ok) throw new Error(await errorMessage(response))
  return response.blob()
}

// readImage 把图片文件读为内联图片
export async function readImage(file: Blob): Promise<InlineImage> {
  return {
    mimeType: file.type || 'image/png',
    data: bytesToBase64(new Uint8Array(await file.arrayBuffer())),
  }
}

// audioBlob 把响应中的音频片段合成一个可播放文件：audio/L16 PCM 封装为 16 位 WAV，其余格式按原样拼接
export function audioBlob(parts: readonly Part[]): Blob | undefined {
  const audio = parts.filter((part) => part.inlineData?.mimeType.startsWith('audio/'))
  if (audio.length === 0) return undefined
  const mime = audio[0]!.inlineData!.mimeType
  const chunks = audio.map((part) => base64Bytes(part.inlineData!.data))
  if (!mime.toLowerCase().startsWith('audio/l16')) return new Blob(chunks, { type: mime })
  const { rate, channels } = pcmFormat(mime)
  return pcmToWav(chunks, rate, channels)
}

// responseText 返回响应正文中的文本
export function responseText(parts: readonly Part[]): string {
  return parts
    .filter((part) => part.text !== undefined && !part.thought)
    .map((part) => part.text)
    .join('')
}
