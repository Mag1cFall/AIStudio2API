<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { useI18n } from '@/i18n'
import UiSelect from '@/playground/ui/UiSelect.vue'
import UiSlider from '@/playground/ui/UiSlider.vue'
import UiSwitch from '@/playground/ui/UiSwitch.vue'
import { musicModes, musicScales, useRealtimeMusic, type MusicConfig } from './realtime-music'

const { t } = useI18n()
const music = useRealtimeMusic()
const { state } = music

// SliderField 是一个滑块参数：字段、范围、步长、说明与是否可留空
interface SliderField {
  key: 'temperature' | 'topK' | 'guidance' | 'bpm' | 'density' | 'brightness'
  label: string
  tip: string
  min: number
  max: number
  step: number
  optional?: boolean
}

const sliders: SliderField[] = [
  {
    key: 'temperature',
    label: 'pg.temperature',
    tip: 'pg.musicTemperatureTip',
    min: 0,
    max: 3,
    step: 0.1,
  },
  {
    key: 'guidance',
    label: 'pg.musicGuidance',
    tip: 'pg.musicGuidanceTip',
    min: 0,
    max: 6,
    step: 0.1,
  },
  { key: 'topK', label: 'pg.topK', tip: 'pg.musicTopKTip', min: 1, max: 1000, step: 1 },
  {
    key: 'bpm',
    label: 'pg.musicBpm',
    tip: 'pg.musicBpmTip',
    min: 60,
    max: 200,
    step: 1,
    optional: true,
  },
  {
    key: 'density',
    label: 'pg.musicDensity',
    tip: 'pg.musicDensityTip',
    min: 0,
    max: 1,
    step: 0.05,
    optional: true,
  },
  {
    key: 'brightness',
    label: 'pg.musicBrightness',
    tip: 'pg.musicBrightnessTip',
    min: 0,
    max: 1,
    step: 0.05,
    optional: true,
  },
]
const toggles: { key: 'muteBass' | 'muteDrums' | 'onlyBassAndDrums'; label: string }[] = [
  { key: 'muteBass', label: 'pg.musicMuteBass' },
  { key: 'muteDrums', label: 'pg.musicMuteDrums' },
  { key: 'onlyBassAndDrums', label: 'pg.musicOnlyBassAndDrums' },
]

// setValue 写入参数并发送
function setValue<K extends keyof MusicConfig>(key: K, value: MusicConfig[K]): void {
  state.config[key] = value
  music.configChanged()
}

// setNumber 从数值输入框写入参数，可留空的参数清空时不发送
function setNumber(field: SliderField | { key: 'seed'; optional: true }, event: Event): void {
  const raw = (event.target as HTMLInputElement).value.trim()
  if (raw === '' && field.optional) setValue(field.key, null)
  else if (raw !== '' && Number.isFinite(Number(raw))) setValue(field.key, Number(raw))
}

// scaleLabel 把调式枚举写为可读文字
function scaleLabel(value: string): string {
  return value
    .split('_')
    .map((word) => (word === 'FLAT' ? '♭' : word.charAt(0) + word.slice(1).toLowerCase()))
    .join(' ')
    .replaceAll(' ♭', '♭')
    .replace(/ (Major|Minor) /, ' $1 / ')
}
</script>

<template>
  <div class="field-group">
    <div
      v-for="field in sliders"
      :key="field.key"
      v-tooltip.left="t(field.tip as 'pg.musicBpmTip')"
      class="settings-item-column"
    >
      <h3 class="item-description-title">{{ t(field.label as 'pg.temperature') }}</h3>
      <div class="ms-slider">
        <UiSlider
          :model-value="state.config[field.key] ?? field.min"
          :min="field.min"
          :max="field.max"
          :step="field.step"
          :label="t(field.label as 'pg.temperature')"
          @update:model-value="setValue(field.key, $event)"
        />
        <input
          ms-input
          class="small slider-number-input"
          type="number"
          :min="field.min"
          :max="field.max"
          :step="field.step"
          :placeholder="field.optional ? t('pg.auto') : ''"
          :aria-label="t(field.label as 'pg.temperature')"
          :value="state.config[field.key] ?? ''"
          @change="setNumber(field, $event)"
        />
      </div>
    </div>
    <div v-tooltip.left="t('pg.musicScaleTip')" class="settings-item-column">
      <h3 class="item-description-title">{{ t('pg.musicScale') }}</h3>
      <UiSelect
        :model-value="state.config.scale"
        class="w-full"
        :aria-label="t('pg.musicScale')"
        @update:model-value="setValue('scale', String($event))"
      >
        <option value="">{{ t('pg.auto') }}</option>
        <option v-for="scale in musicScales" :key="scale" :value="scale">
          {{ scaleLabel(scale) }}
        </option>
      </UiSelect>
    </div>
    <div v-tooltip.left="t('pg.musicModeTip')" class="settings-item-column">
      <h3 class="item-description-title">{{ t('pg.musicMode') }}</h3>
      <UiSelect
        :model-value="state.config.musicGenerationMode"
        class="w-full"
        :aria-label="t('pg.musicMode')"
        @update:model-value="setValue('musicGenerationMode', String($event))"
      >
        <option v-for="mode in musicModes" :key="mode" :value="mode">
          {{ mode.charAt(0) + mode.slice(1).toLowerCase() }}
        </option>
      </UiSelect>
    </div>
    <div v-tooltip.left="t('pg.musicSeedTip')" class="settings-item">
      <h3 class="item-description-title">{{ t('playground.seed') }}</h3>
      <input
        ms-input
        class="small number-input"
        type="number"
        step="1"
        :placeholder="t('pg.auto')"
        :aria-label="t('playground.seed')"
        :value="state.config.seed ?? ''"
        @change="setNumber({ key: 'seed', optional: true }, $event)"
      />
    </div>
    <div v-for="toggle in toggles" :key="toggle.key" class="toggle-row">
      <div class="label-container">
        <p class="label">{{ t(toggle.label as 'pg.musicMuteBass') }}</p>
      </div>
      <UiSwitch
        :model-value="state.config[toggle.key]"
        :label="t(toggle.label as 'pg.musicMuteBass')"
        @update:model-value="setValue(toggle.key, $event)"
      />
    </div>
  </div>
</template>

<style scoped>
.toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  margin-block: 12px;
}

.label-container {
  flex: 1;
  min-width: 0;
}

.label {
  margin: 0;
  font-size: 14px;
  line-height: 21px;
}

.number-input {
  width: 96px;
}
</style>
