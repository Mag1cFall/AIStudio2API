<script setup lang="ts">
import { computed, onUnmounted } from 'vue'
import {
  SelectContent,
  SelectItem,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectTrigger,
  SelectViewport,
} from 'reka-ui'
import { useI18n } from '@/i18n'
import type { VoiceDetail } from '@/types'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { useSamplePlayer } from './sample'
import { voiceTraits } from './speech-voices'

const model = defineModel<string>({ required: true })
const props = defineProps<{ voices: readonly VoiceDetail[]; disabled?: boolean }>()
const { t } = useI18n()
const { playingSample, toggleSample, stopSample } = useSamplePlayer()

// options 是带官网说明的音色：描述与音高
const options = computed(() =>
  props.voices.map((voice) => ({
    name: voice.name,
    sample: voice.sample_url ?? '',
    traits: voiceTraits(voice).join(', '),
  })),
)

onUnmounted(stopSample)
</script>

<template>
  <SelectRoot v-model="model" :disabled="disabled">
    <SelectTrigger class="ms-select-trigger w-full" :aria-label="t('pg.voice')">
      <UiIcon name="voiceSelection" :size="18" />
      <span class="ms-select-value">{{ model }}</span>
      <UiIcon name="chevronDown" :size="20" class="ms-select-arrow" />
    </SelectTrigger>
    <SelectPortal>
      <SelectContent position="popper" :side-offset="0" class="ms-select-panel">
        <SelectViewport>
          <SelectItem
            v-for="option in options"
            :key="option.name"
            :value="option.name"
            class="ms-select-option voice-option"
          >
            <button
              v-if="option.sample"
              type="button"
              ms-button
              class="ms-button-borderless ms-button-icon ms-button-small"
              :aria-label="t('pg.playAudioSample')"
              @pointerdown.stop
              @pointerup.stop
              @click.stop="toggleSample(option.sample)"
            >
              <UiIcon
                :name="playingSample === option.sample ? 'pauseCircle' : 'playCircle'"
                :size="20"
              />
            </button>
            <span class="voice-text">
              <SelectItemText class="voice-name">{{ option.name }}</SelectItemText>
              <span class="voice-traits">{{ option.traits }}</span>
            </span>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>

<style scoped>
.voice-option {
  gap: 8px;
  height: auto;
  min-height: 48px;
}

.voice-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.voice-name {
  font-size: 14px;
  line-height: 21px;
}

.voice-traits {
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}
</style>
