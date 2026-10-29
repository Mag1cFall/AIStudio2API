import { onDeactivated, onUnmounted, ref } from 'vue'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import { apiKey, failureText } from './request'
import { audioInput } from './shell/preferences'

// useAudioRecording 管理麦克风录音与转写，录音结果交给组件处理
export function useAudioRecording(onAudio: (audio: Blob) => void | Promise<void>) {
  const { t } = useI18n()
  const { showNotice } = useConsole()
  const recording = ref(false)
  const opening = ref(false)
  const transcribing = ref(false)
  let recorder: MediaRecorder | undefined

  // start 打开所选麦克风并开始收集录音
  async function start(): Promise<boolean> {
    opening.value = true
    let stream: MediaStream
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: audioInput() })
    } catch {
      showNotice(t('pg.microphoneDenied'), 'error')
      return false
    } finally {
      opening.value = false
    }
    const chunks: Blob[] = []
    recorder = new MediaRecorder(stream)
    recorder.ondataavailable = (event) => chunks.push(event.data)
    recorder.onstop = () => {
      stream.getTracks().forEach((track) => track.stop())
      recording.value = false
      void onAudio(new Blob(chunks, { type: recorder?.mimeType || 'audio/webm' }))
    }
    recording.value = true
    recorder.start()
    return true
  }

  // stop 停止录音并交付音频
  function stop(): void {
    recorder?.stop()
  }

  // transcribe 上传录音并返回去除首尾空白的转写正文
  async function transcribe(audio: Blob): Promise<string> {
    transcribing.value = true
    try {
      const form = new FormData()
      form.append('file', audio, 'speech.webm')
      const headers = new Headers()
      const key = apiKey()
      if (key !== '') headers.set('Authorization', `Bearer ${key}`)
      const response = await fetch('/v1/audio/transcriptions', {
        method: 'POST',
        headers,
        body: form,
      })
      const value = (await response.json()) as { text?: string; error?: { message?: string } }
      if (!response.ok) throw new Error(value.error?.message ?? response.statusText)
      return value.text?.trim() ?? ''
    } catch (error) {
      showNotice(failureText(error, t('pg.transcribeUnreachable')), 'error')
      return ''
    } finally {
      transcribing.value = false
    }
  }

  // release 在组件卸载或随 KeepAlive 停用时结束进行中的录音
  function release(): void {
    if (recorder?.state === 'recording') stop()
  }
  onDeactivated(release)
  onUnmounted(release)

  return { recording, opening, transcribing, start, stop, transcribe }
}
