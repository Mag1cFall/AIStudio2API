<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { onUnmounted, ref } from 'vue'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { apiKey } from '../request'
import { audioInput } from '../shell/preferences'

const emit = defineEmits<{ text: [text: string] }>()
const { t } = useI18n()
const { showNotice } = useConsole()
const recording = ref(false)
const transcribing = ref(false)
let recorder: MediaRecorder | undefined

// toggle 开始录音，再次点击停止并转写
async function toggle(): Promise<void> {
  if (transcribing.value) return
  if (recording.value) {
    recorder?.stop()
    return
  }
  let stream: MediaStream
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: audioInput() })
  } catch {
    showNotice(t('pg.microphoneDenied'), 'error')
    return
  }
  const chunks: Blob[] = []
  recorder = new MediaRecorder(stream)
  recorder.ondataavailable = (event) => chunks.push(event.data)
  recorder.onstop = () => {
    stream.getTracks().forEach((track) => track.stop())
    recording.value = false
    void transcribe(new Blob(chunks, { type: recorder?.mimeType || 'audio/webm' }))
  }
  recording.value = true
  recorder.start()
}

// transcribe 用本服务的转写接口把录音转为文字
async function transcribe(audio: Blob): Promise<void> {
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
    const text = value.text?.trim() ?? ''
    if (text !== '') emit('text', text)
  } catch (error) {
    showNotice(error instanceof Error ? error.message : String(error), 'error')
  } finally {
    transcribing.value = false
  }
}

onUnmounted(() => {
  if (recorder?.state === 'recording') recorder.stop()
})
</script>

<template>
  <button
    v-tooltip.below="recording ? t('pg.stopRecording') : t('pg.speechToText')"
    type="button"
    ms-button
    class="stt-button ms-button-filter-chip"
    :class="{ recording }"
    :aria-label="t('pg.speechToText')"
    :aria-disabled="transcribing"
    @click.stop="toggle"
  >
    <UiIcon
      :name="transcribing ? 'spinner' : recording ? 'stop' : 'mic'"
      :size="18"
      :class="{ spin: transcribing }"
    />
  </button>
</template>

<style scoped>
.stt-button {
  width: 32px;
  padding: 8px 6px;
  border-color: var(--color-v3-outline);
  background: transparent;
}

.stt-button:hover {
  background: var(--color-v3-button-container-high);
}

.stt-button.recording {
  border-color: var(--color-v3-text-link);
  color: var(--color-v3-text-link);
}

.spin {
  animation: spin 0.66s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(1turn);
  }
}
</style>
