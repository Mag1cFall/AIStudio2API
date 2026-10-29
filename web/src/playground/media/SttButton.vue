<script setup lang="ts">
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { useAudioRecording } from '../recording'

const emit = defineEmits<{ text: [text: string] }>()
const { t } = useI18n()
const { recording, opening, transcribing, start, stop, transcribe } = useAudioRecording(
  async (audio) => {
    const text = await transcribe(audio)
    if (text !== '') emit('text', text)
  },
)

// toggle 开始录音，再次点击停止并转写
async function toggle(): Promise<void> {
  if (transcribing.value || opening.value) return
  if (recording.value) {
    stop()
    return
  }
  await start()
}
</script>

<template>
  <button
    v-tooltip.below="recording ? t('pg.stopRecording') : t('pg.speechToText')"
    type="button"
    ms-button
    class="stt-button ms-button-filter-chip"
    :class="{ recording }"
    :aria-label="t('pg.speechToText')"
    :aria-disabled="transcribing || opening"
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
</style>
