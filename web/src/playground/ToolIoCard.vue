<script setup lang="ts">
import { computed, useSlots } from 'vue'
import { useI18n } from '@/i18n'

const props = defineProps<{ input?: string; output?: string | undefined; error?: boolean }>()
const slots = useSlots()
const { t } = useI18n()
// hasOutput 表示显示输出段：有输出文本或调用方提供了输出段内容
const hasOutput = computed(() => props.output !== undefined || slots.output !== undefined)
</script>

<template>
  <div class="io-card">
    <div v-if="input" class="io-section">
      <span class="io-label">{{ t('pg.functionInput') }}</span>
      <span class="io-text">{{ input }}</span>
    </div>
    <span v-if="input && hasOutput" class="io-divider" aria-hidden="true" />
    <div v-if="hasOutput" class="io-section">
      <span class="io-label">{{ t('pg.functionOutput') }}</span>
      <slot name="output">
        <span class="io-text" :data-error="error || undefined">{{ output }}</span>
      </slot>
    </div>
  </div>
</template>

<style scoped>
/* 输入输出卡片取 dsh ToolRow 的 ioCard 结构，颜色取 AI Studio 主题 */
.io-card {
  display: flex;
  flex-direction: column;
  margin: 4px 0 4px 4px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container);
  font-family: 'DM Mono', monospace;
  font-size: 12px;
  line-height: 18px;
}

.io-section {
  display: grid;
  grid-template-columns: max-content 1fr;
  column-gap: 14px;
  align-items: baseline;
  max-height: 150px;
  padding: 12px 16px;
  overflow-y: auto;
}

.io-label {
  position: sticky;
  top: 0;
  align-self: start;
  color: var(--color-v3-text-var);
}

.io-divider {
  flex: none;
  height: 1px;
  background: var(--color-v3-outline-var);
}

.io-text {
  min-width: 0;
  color: var(--color-v3-text);
  white-space: pre-wrap;
  word-break: break-word;
}

.io-text[data-error] {
  color: var(--color-error);
}
</style>
