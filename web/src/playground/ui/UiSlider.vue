<script setup lang="ts">
import { computed } from 'vue'
import { SliderRange, SliderRoot, SliderThumb, SliderTrack } from 'reka-ui'

const model = defineModel<number>({ required: true })
defineProps<{ min: number; max: number; step: number; disabled?: boolean; label: string }>()
// value 把单值模型换成 Reka 滑块的数组值
const value = computed({
  get: () => [model.value],
  set: (values: number[]) => {
    if (values[0] !== undefined) model.value = values[0]
  },
})
</script>

<template>
  <SliderRoot
    v-model="value"
    :min="min"
    :max="max"
    :step="step"
    :disabled="disabled"
    class="ms-slider-root"
  >
    <SliderTrack class="ms-slider-track">
      <SliderRange class="ms-slider-range" />
    </SliderTrack>
    <SliderThumb class="ms-slider-thumb" :aria-label="label" />
  </SliderRoot>
</template>

<style scoped>
/* --slider-primary 与 --slider-surface-variant 为官网 mat-slider 所用的 --mat-sys-primary 与 --mat-sys-surface-variant */
.ms-slider-root {
  --slider-primary: #777779;
  --slider-surface-variant: #e2e2e6;
  position: relative;
  display: flex;
  align-items: center;
  width: 100%;
  height: 24px;
  touch-action: none;
  user-select: none;
}

:root[data-theme='dark'] .ms-slider-root {
  --slider-primary: #ababab;
  --slider-surface-variant: #444748;
}

.ms-slider-root[data-disabled] {
  opacity: 0.38;
}

.ms-slider-track {
  position: relative;
  flex-grow: 1;
  height: 4px;
  border-radius: 2px;
  background-color: color-mix(in srgb, var(--slider-surface-variant) 24%, transparent);
}

.ms-slider-range {
  position: absolute;
  height: 100%;
  border-radius: 2px;
  background-color: var(--slider-primary);
}

.ms-slider-thumb {
  display: block;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background-color: var(--slider-primary);
  cursor: pointer;
  outline: none;
}

.ms-slider-thumb:hover {
  box-shadow: 0 0 0 17px color-mix(in srgb, var(--slider-primary) 5%, transparent);
}

.ms-slider-thumb:focus {
  box-shadow: 0 0 0 17px color-mix(in srgb, var(--slider-primary) 20%, transparent);
}
</style>
