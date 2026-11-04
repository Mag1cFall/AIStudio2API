<script setup lang="ts">
import { computed } from 'vue'

const model = defineModel<number>({ required: true })
const props = defineProps<{
  min: number
  max: number
  step: number
  disabled?: boolean
  label: string
}>()
// fill 是当前值在滑轨上所占的比例，滑轨左段按它着色
const fill = computed(
  () =>
    `${Math.min(100, Math.max(0, ((model.value - props.min) / (props.max - props.min)) * 100))}%`,
)
</script>

<template>
  <input
    v-model.number="model"
    type="range"
    class="ms-slider-input"
    :min="min"
    :max="max"
    :step="step"
    :disabled="disabled"
    :aria-label="label"
    :style="{ '--slider-fill': fill }"
  />
</template>

<style scoped>
/* --slider-primary 与 --slider-surface-variant 为官网 mat-slider 所用的 --mat-sys-primary 与 --mat-sys-surface-variant */
.ms-slider-input {
  --slider-primary: #777779;
  --slider-surface-variant: #e2e2e6;
  --slider-track: color-mix(in srgb, var(--slider-surface-variant) 24%, transparent);
  display: block;
  width: 100%;
  height: 24px;
  margin: 0;
  background: none;
  cursor: pointer;
  outline: none;
  appearance: none;
}

:root[data-theme='dark'] .ms-slider-input {
  --slider-primary: #ababab;
  --slider-surface-variant: #444748;
}

.ms-slider-input:disabled {
  opacity: 0.38;
  cursor: default;
}

.ms-slider-input::-webkit-slider-runnable-track {
  height: 4px;
  border-radius: 2px;
  background: linear-gradient(
    to right,
    var(--slider-primary) var(--slider-fill),
    var(--slider-track) var(--slider-fill)
  );
}

.ms-slider-input::-moz-range-track {
  height: 4px;
  border-radius: 2px;
  background: var(--slider-track);
}

.ms-slider-input::-moz-range-progress {
  height: 4px;
  border-radius: 2px;
  background: var(--slider-primary);
}

.ms-slider-input::-webkit-slider-thumb {
  width: 14px;
  height: 14px;
  margin-top: -5px;
  border-radius: 50%;
  background: var(--slider-primary);
  appearance: none;
}

.ms-slider-input::-moz-range-thumb {
  width: 14px;
  height: 14px;
  border: 0;
  border-radius: 50%;
  background: var(--slider-primary);
}

.ms-slider-input:enabled:hover::-webkit-slider-thumb {
  box-shadow: 0 0 0 17px color-mix(in srgb, var(--slider-primary) 5%, transparent);
}

.ms-slider-input:enabled:hover::-moz-range-thumb {
  box-shadow: 0 0 0 17px color-mix(in srgb, var(--slider-primary) 5%, transparent);
}

.ms-slider-input:focus::-webkit-slider-thumb {
  box-shadow: 0 0 0 17px color-mix(in srgb, var(--slider-primary) 20%, transparent);
}

.ms-slider-input:focus::-moz-range-thumb {
  box-shadow: 0 0 0 17px color-mix(in srgb, var(--slider-primary) 20%, transparent);
}
</style>
