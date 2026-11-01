<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import type { Part } from './types'

const props = defineProps<{
  call: NonNullable<Part['functionCall']>
  response: string
  awaiting: boolean
}>()
const emit = defineEmits<{ respond: [text: string] }>()
const { t } = useI18n()
const expanded = ref(props.awaiting)
const reply = ref('')
// 流结束后轮到用户填写响应时展开卡片
watch(
  () => props.awaiting,
  (awaiting) => {
    if (awaiting) expanded.value = true
  },
)

// summary 是折叠行里的参数摘要：第一层参数按 key: value 以逗号连接
const summary = computed(() =>
  Object.entries(props.call.args ?? {})
    .map(([key, value]) => `${key}: ${JSON.stringify(value)}`)
    .join(', '),
)
// input 是展开后的完整参数 JSON
const input = computed(() => (props.call.args ? JSON.stringify(props.call.args, undefined, 2) : ''))

function onKey(event: KeyboardEvent): void {
  if (event.key !== 'Enter' && event.key !== ' ') return
  event.preventDefault()
  expanded.value = !expanded.value
}

function send(): void {
  if (reply.value.trim() === '') return
  emit('respond', reply.value)
  reply.value = ''
}
</script>

<template>
  <div class="function-call" :data-open="expanded || undefined">
    <div
      class="call-row"
      role="button"
      tabindex="0"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
      @keydown="onKey"
    >
      <span class="leading">
        <UiIcon name="functionCalling" :size="14" class="call-icon" />
        <UiIcon name="chevronDown" :size="14" class="chevron" />
      </span>
      <span class="title" :title="call.name">{{ call.name }}</span>
      <template v-if="summary">
        <span class="sep" aria-hidden="true" />
        <span class="summary">{{ summary }}</span>
      </template>
    </div>
    <div v-if="expanded" class="io-card">
      <div v-if="input" class="io-section">
        <span class="io-label">{{ t('pg.functionInput') }}</span>
        <span class="io-text">{{ input }}</span>
      </div>
      <span v-if="input" class="io-divider" aria-hidden="true" />
      <div class="io-section">
        <span class="io-label">{{ t('pg.functionOutput') }}</span>
        <form v-if="awaiting" class="io-form" @submit.prevent="send">
          <input
            v-model="reply"
            ms-input
            class="io-input"
            :aria-label="t('pg.functionResponse')"
            :placeholder="t('pg.functionResponse')"
          />
          <button type="submit" ms-button class="ms-button-primary" :disabled="reply.trim() === ''">
            {{ t('pg.send') }}
          </button>
        </form>
        <span v-else class="io-text">{{ response }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.function-call {
  display: flex;
  flex-direction: column;
  margin: 4px 0;
}

.call-row {
  display: flex;
  align-items: center;
  height: 24px;
  min-width: 0;
  color: var(--color-v3-text-var);
  cursor: pointer;
  font-size: 13px;
  line-height: 24px;
  outline: none;
  transition: color 100ms ease;
}

.call-row:hover,
.call-row:focus-visible,
.function-call[data-open] .call-row {
  color: var(--color-v3-text);
}

.leading {
  position: relative;
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  margin-right: 6px;
}

.chevron {
  position: absolute;
  inset: 0;
  margin: auto;
  opacity: 0;
  transition:
    opacity 100ms ease,
    transform 100ms ease;
}

.call-row:hover .call-icon,
.function-call[data-open] .call-icon {
  opacity: 0;
}

.call-row:hover .chevron,
.function-call[data-open] .chevron {
  opacity: 1;
}

.function-call[data-open] .chevron {
  transform: rotate(180deg);
}

.title {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sep {
  flex: none;
  width: 2px;
  height: 2px;
  margin: 0 8px;
  border-radius: 1px;
  background: currentcolor;
  opacity: 0.6;
}

.summary {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

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

.io-form {
  display: flex;
  gap: 8px;
  align-items: center;
  min-width: 0;
  font-family: inherit;
}

.io-input {
  flex: 1;
  min-width: 0;
}
</style>
