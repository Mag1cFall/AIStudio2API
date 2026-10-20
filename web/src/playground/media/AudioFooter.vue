<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import RunKeyHint from '../RunKeyHint.vue'
import { useRunTooltip } from '../run-keys'

const props = defineProps<{
  audio: string
  running: boolean
  canRun: boolean
  runTooltip?: string
  runLabel?: string
  fileName: string
}>()
const emit = defineEmits<{ run: []; stop: [] }>()
const { t } = useI18n()
// defaultTooltip 是运行按钮的悬停提示，带自定文字的按钮（音乐页 Generate）与官网一样不显示提示
const defaultTooltip = useRunTooltip(() => props.running)
const element = ref<HTMLAudioElement>()
const playing = ref(false)
const current = ref(0)
const duration = ref(0)

// clock 把秒数写为 m:ss
function clock(seconds: number): string {
  const value = Number.isFinite(seconds) ? Math.floor(seconds) : 0
  return `${Math.floor(value / 60)}:${String(value % 60).padStart(2, '0')}`
}

// toggle 播放或暂停
function toggle(): void {
  const audio = element.value
  if (!audio) return
  if (audio.paused) void audio.play()
  else audio.pause()
}

// seek 跳到进度条位置
function seek(event: Event): void {
  if (element.value) element.value.currentTime = Number((event.target as HTMLInputElement).value)
}

// submit 运行或停止
function submit(): void {
  if (props.running) emit('stop')
  else if (props.canRun) emit('run')
}

watch(
  () => props.audio,
  (audio) => {
    current.value = 0
    duration.value = 0
    playing.value = false
    if (audio.startsWith('blob:'))
      void Promise.resolve().then(() => element.value?.play().catch(() => undefined))
  },
)
</script>

<template>
  <div class="footer">
    <div class="footer-controls">
      <slot name="title" />
      <div v-if="audio" class="player-container">
        <audio
          ref="element"
          :src="audio"
          preload="auto"
          @play="playing = true"
          @pause="playing = false"
          @ended="playing = false"
          @timeupdate="current = ($event.target as HTMLAudioElement).currentTime"
          @loadedmetadata="duration = ($event.target as HTMLAudioElement).duration"
        ></audio>
        <button
          type="button"
          class="play-pause-button"
          :aria-label="playing ? t('pg.pause') : t('pg.play')"
          @click="toggle"
        >
          <UiIcon :name="playing ? 'pauseCircleFill' : 'playCircleFill'" :size="40" />
        </button>
        <div class="progress-container">
          <span class="time">{{ clock(current) }}</span>
          <input
            class="seek-slider"
            type="range"
            min="0"
            :max="duration || 0"
            step="0.01"
            :value="current"
            :aria-label="t('pg.seek')"
            :style="{ '--progress': `${duration ? (current / duration) * 100 : 0}%` }"
            @input="seek"
          />
          <span class="time">{{ clock(duration) }}</span>
        </div>
        <a
          v-tooltip="t('pg.download')"
          ms-button
          class="ms-button-borderless ms-button-icon download-button"
          :href="audio"
          :download="fileName"
          :aria-label="t('pg.download')"
        >
          <UiIcon name="download" :size="20" />
        </a>
      </div>
      <div class="key-and-run">
        <button
          v-tooltip="runLabel ? '' : runTooltip || defaultTooltip"
          type="button"
          ms-button
          class="run-button ms-button-primary"
          :aria-label="running ? t('pg.stop') : runLabel || t('pg.run')"
          :aria-disabled="!running && !canRun"
          @click="submit"
        >
          <template v-if="running">
            <UiIcon name="refresh" :size="18" class="spin" />
            <span class="run-button-label">{{ t('pg.stop') }}</span>
          </template>
          <template v-else>
            <span class="run-button-label">{{ runLabel || t('pg.run') }}</span>
            <RunKeyHint v-if="!runLabel" />
          </template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.footer {
  width: 100%;
  margin-top: auto;
  border-top: 1px solid var(--color-v3-outline-var);
  background-color: var(--color-v3-surface);
}

.footer-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  box-sizing: border-box;
  width: 100%;
  margin: 0 auto;
  padding: 12px;
}

.player-container {
  display: flex;
  flex-grow: 1;
  align-items: center;
  gap: 16px;
}

.play-pause-button {
  display: inline-flex;
  padding: 0;
  border: none;
  background: none;
  color: var(--color-v3-text);
  cursor: pointer;
}

.play-pause-button :deep(svg) {
  transition: transform 0.15s cubic-bezier(0.4, 0, 0.2, 1);
}

.play-pause-button:hover :deep(svg),
.play-pause-button:focus-visible :deep(svg) {
  transform: scale(1.05);
}

.progress-container {
  display: flex;
  flex-grow: 1;
  align-items: center;
  gap: 12px;
}

.time {
  padding: 0 2px;
  color: var(--color-v3-text-var);
  font-family: 'DM Mono', monospace;
  font-size: 10px;
}

.seek-slider {
  flex-grow: 1;
  height: 4px;
  border-radius: 2px;
  background: linear-gradient(
    90deg,
    var(--color-v3-text) var(--progress),
    var(--color-v3-outline-var) var(--progress)
  );
  cursor: pointer;
  appearance: none;
}

.seek-slider:hover {
  background: linear-gradient(
    90deg,
    var(--color-v3-text-link) var(--progress),
    var(--color-v3-outline-var) var(--progress)
  );
}

.seek-slider::-webkit-slider-thumb {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: transparent;
  appearance: none;
}

.seek-slider:hover::-webkit-slider-thumb,
.seek-slider:focus-visible::-webkit-slider-thumb {
  background: var(--color-v3-text-link);
}

.seek-slider::-moz-range-thumb {
  width: 14px;
  height: 14px;
  border: none;
  border-radius: 50%;
  background: transparent;
}

.seek-slider:hover::-moz-range-thumb {
  background: var(--color-v3-text-link);
}

.download-button {
  margin-left: 12px;
}

.key-and-run {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-left: 12px;
}

@media screen and (max-width: 480px) {
  .footer-controls {
    justify-content: flex-start;
  }

  .player-container {
    display: contents;
  }

  .progress-container {
    order: -1;
    flex-basis: 100%;
    padding-inline-start: 6px;
  }

  .key-and-run {
    margin-inline-start: auto;
  }
}

.run-button .run-button-label {
  min-width: 28px;
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
