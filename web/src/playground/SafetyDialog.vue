<script setup lang="ts">
import { useI18n, type TranslationKey } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiSlider from '@/playground/ui/UiSlider.vue'
import { safetyCategories, safetyThresholds, usePlayground } from './session'
import type { SafetyCategory, SafetyThreshold } from './types'

const open = defineModel<boolean>('open', { required: true })
const { t } = useI18n()
const { session } = usePlayground()

// categoryLabels 是官网安全类别名
const categoryLabels: Record<SafetyCategory, TranslationKey> = {
  HARM_CATEGORY_HARASSMENT: 'pg.harassment',
  HARM_CATEGORY_HATE_SPEECH: 'pg.hate',
  HARM_CATEGORY_SEXUALLY_EXPLICIT: 'pg.sexuallyExplicit',
  HARM_CATEGORY_DANGEROUS_CONTENT: 'pg.dangerousContent',
}

// thresholdLabels 是滑块五档的名称
const thresholdLabels: Record<SafetyThreshold, TranslationKey> = {
  OFF: 'pg.blockOff',
  BLOCK_NONE: 'pg.blockNone',
  BLOCK_ONLY_HIGH: 'pg.blockFew',
  BLOCK_MEDIUM_AND_ABOVE: 'pg.blockSome',
  BLOCK_LOW_AND_ABOVE: 'pg.blockMost',
}

// setLevel 按滑块位置设置类别的阈值
function setLevel(category: SafetyCategory, index: number): void {
  session.settings.safety[category] = safetyThresholds[index] ?? 'OFF'
}

// resetDefaults 把全部类别恢复为关闭
function resetDefaults(): void {
  for (const category of safetyCategories) session.settings.safety[category] = 'OFF'
}
</script>

<template>
  <UiDialog v-model:open="open" :title="t('pg.safetyTitle')" width="580px">
    <p class="description">{{ t('pg.safetyDescription') }}</p>
    <div v-for="category in safetyCategories" :key="category" class="safety-row">
      <span class="category">{{ t(categoryLabels[category]) }}</span>
      <div class="slider-column">
        <span class="level">{{ t(thresholdLabels[session.settings.safety[category]]) }}</span>
        <UiSlider
          :model-value="safetyThresholds.indexOf(session.settings.safety[category])"
          :min="0"
          :max="4"
          :step="1"
          :label="t(categoryLabels[category])"
          @update:model-value="setLevel(category, $event)"
        />
      </div>
    </div>
    <template #actions>
      <button type="button" ms-button class="ms-button-primary" @click="resetDefaults">
        {{ t('pg.resetDefaults') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.description {
  margin: 0 0 16px;
  line-height: 20px;
}

.safety-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 69px;
  border-top: 1px solid var(--color-v3-outline-var);
}

.safety-row:last-child {
  border-bottom: 1px solid var(--color-v3-outline-var);
}

.category {
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;
}

.slider-column {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 170px;
}

.level {
  font-size: 12px;
  line-height: 18px;
}
</style>
