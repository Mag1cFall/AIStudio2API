<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import ToolIoCard from './ToolIoCard.vue'
import ToolStepRow from './ToolStepRow.vue'
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

function send(): void {
  if (reply.value.trim() === '') return
  emit('respond', reply.value)
  reply.value = ''
}
</script>

<template>
  <ToolStepRow
    v-model:expanded="expanded"
    icon="functionCalling"
    :title="call.name"
    :summary="summary"
  >
    <ToolIoCard :input="input" :output="awaiting ? undefined : response">
      <template v-if="awaiting" #output>
        <form class="io-form" @submit.prevent="send">
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
      </template>
    </ToolIoCard>
  </ToolStepRow>
</template>

<style scoped>
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
