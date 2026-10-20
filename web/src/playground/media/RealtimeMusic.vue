<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, onUnmounted } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSlider from '@/playground/ui/UiSlider.vue'
import { promptSuggestions, useRealtimeMusic } from './realtime-music'

const props = defineProps<{ model: string }>()
const { t } = useI18n()
const music = useRealtimeMusic()
const { state } = music
const playing = computed(() => ['connecting', 'loading', 'playing'].includes(state.status))
const canPlay = computed(() => state.prompts.some((prompt) => prompt.text.trim() !== ''))
const suggestions = computed(() =>
  promptSuggestions.filter((text) => !state.prompts.some((prompt) => prompt.text === text)),
)
const statusText = computed(
  () =>
    ({
      idle: t('pg.realtimeIdle'),
      connecting: t('pg.realtimeConnecting'),
      loading: t('pg.realtimeLoading'),
      playing: t('pg.realtimePlaying'),
      paused: t('pg.realtimePaused'),
    })[state.status],
)

// toggle 播放或暂停
function toggle(): void {
  if (playing.value) void music.pause()
  else void music.play(props.model)
}

// setWeight 修改提示词权重
function setWeight(index: number, value: number): void {
  state.prompts[index]!.weight = Math.round(value * 100) / 100
  music.promptsChanged()
}

if (state.prompts.length === 0) music.addPrompt()
onUnmounted(() => music.stop())
</script>

<template>
  <div class="realtime-music">
    <section class="prompts-card">
      <div class="prompts-header">
        <div class="header-label">
          <UiIcon name="modeMusic" :size="16" />
          <span>{{ t('pg.realtimePrompts') }}</span>
        </div>
        <button
          type="button"
          ms-button
          class="ms-button-filter-chip"
          :disabled="state.prompts.length >= 10"
          @click="music.addPrompt()"
        >
          <UiIcon name="add" :size="18" />
          {{ t('pg.addPrompt') }}
        </button>
      </div>
      <div v-for="(prompt, index) in state.prompts" :key="prompt.id" class="prompt-row">
        <input
          v-model="prompt.text"
          ms-input
          class="prompt-text"
          :placeholder="t('pg.realtimePromptPlaceholder')"
          :aria-label="t('pg.realtimePrompt')"
          @input="music.promptsChanged()"
          @keydown.enter.prevent="toggle"
        />
        <div v-tooltip.below="t('pg.promptWeightTip')" class="weight">
          <UiSlider
            class="weight-slider"
            :model-value="prompt.weight"
            :min="0"
            :max="2"
            :step="0.05"
            :label="t('pg.promptWeight')"
            @update:model-value="setWeight(index, $event)"
          />
          <span class="weight-value">{{ prompt.weight.toFixed(2) }}</span>
        </div>
        <button
          v-tooltip.below="t('pg.removePrompt')"
          type="button"
          ms-button
          class="ms-button-borderless ms-button-icon"
          :aria-label="t('pg.removePrompt')"
          :disabled="state.prompts.length <= 1"
          @click="music.removePrompt(index)"
        >
          <UiIcon name="close" :size="18" />
        </button>
      </div>
      <div class="suggestions" :aria-label="t('pg.promptsToTry')">
        <button
          v-for="text in suggestions"
          :key="text"
          type="button"
          ms-button
          class="ms-button-filter-chip suggestion"
          :disabled="state.prompts.length >= 10"
          @click="music.addPrompt(text)"
        >
          {{ text }}
        </button>
      </div>
    </section>

    <div class="transport">
      <button
        v-tooltip="playing ? t('pg.pause') : t('pg.play')"
        type="button"
        class="play-pause-button"
        :aria-label="playing ? t('pg.pause') : t('pg.play')"
        :disabled="!playing && !canPlay"
        @click="toggle"
      >
        <UiIcon
          :name="
            state.status === 'connecting' || state.status === 'loading'
              ? 'spinner'
              : playing
                ? 'pauseCircleFill'
                : 'playCircleFill'
          "
          :size="40"
        />
      </button>
      <button
        v-tooltip="t('pg.stop')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.stop')"
        :disabled="state.status === 'idle'"
        @click="music.stop()"
      >
        <UiIcon name="stopFill" :size="22" />
      </button>
      <button
        v-tooltip="t('pg.resetContextTip')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-small"
        :disabled="state.status === 'idle'"
        @click="music.resetContext()"
      >
        <UiIcon name="refresh" :size="18" />
        {{ t('pg.resetContext') }}
      </button>
      <span class="status" role="status">{{ statusText }}</span>
    </div>
    <p v-if="state.error" role="alert" class="realtime-error">{{ state.error }}</p>
  </div>
</template>

<style scoped>
.realtime-music {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}

.prompts-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: calc(100% - 40px);
  max-width: 1000px;
  margin: 28px auto 16px;
  padding: 20px 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  transition: border-color 0.2s ease;
}

.prompts-card:focus-within {
  border-color: var(--color-v3-outline-accent);
}

.prompts-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.header-label {
  display: flex;
  align-items: center;
  gap: 4px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.prompt-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.prompt-text {
  flex: 1;
  min-width: 0;
}

.weight {
  display: flex;
  align-items: center;
  gap: 8px;
}

.weight-slider {
  width: 140px;
}

.weight-value {
  width: 34px;
  color: var(--color-v3-text-var);
  font-family: 'DM Mono', monospace;
  font-size: 12px;
  text-align: right;
}

.suggestions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 4px;
}

.suggestion {
  padding: 4px 10px;
}

.transport {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: auto;
  padding: 12px;
  border-top: 1px solid var(--color-v3-outline-var);
}

.play-pause-button {
  display: inline-flex;
  padding: 0;
  border: none;
  background: none;
  color: var(--color-v3-text);
  cursor: pointer;
}

.play-pause-button:disabled {
  color: var(--color-v3-text-disable);
  cursor: not-allowed;
}

.status {
  color: var(--color-v3-text-var);
  font-size: 14px;
  line-height: 21px;
}

.realtime-error {
  margin: 0 12px 8px;
  color: var(--color-error);
  font-size: 14px;
  line-height: 21px;
}

@media screen and (max-width: 600px) {
  .prompts-card {
    width: calc(100% - 32px);
  }

  .prompt-row {
    flex-wrap: wrap;
  }

  .prompt-text {
    flex-basis: 100%;
  }
}
</style>
