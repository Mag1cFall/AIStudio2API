<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from '@/i18n'
import CodeBlock from './CodeBlock.vue'
import { dataURL } from './protocols'
import ToolIoCard from './ToolIoCard.vue'
import ToolStepRow from './ToolStepRow.vue'
import type { Part } from './types'

const props = defineProps<{
  code?: Part['executableCode']
  result?: Part['codeExecutionResult']
  images: NonNullable<Part['inlineData']>[]
  running: boolean
}>()
const { t } = useI18n()
const expanded = ref(false)

// state 是步骤状态：结果未到且仍在生成时为运行中，失败或超时为出错
const state = computed(() => {
  if (props.result === undefined) return props.running ? 'running' : 'ok'
  return props.result.outcome === 'OUTCOME_OK' ? 'ok' : 'error'
})
// timedOut 表示代码运行超时
const timedOut = computed(() => props.result?.outcome === 'OUTCOME_DEADLINE_EXCEEDED')
// failure 是出错时的说明：超时或失败
const failure = computed(() => (timedOut.value ? t('pg.codeTimeout') : t('pg.codeFailed')))
// badge 是出错时的状态徽标：超时或失败
const badge = computed(() => {
  if (state.value !== 'error') return undefined
  return timedOut.value ? t('pg.stepTimedOut') : t('pg.stepFailed')
})
// summary 是折叠行的摘要：运行失败时为错误输出的最后一行或失败说明，其余为代码的第一个非空行
const summary = computed(() => {
  const lines = (text: string) => text.split('\n').filter((line) => line.trim() !== '')
  if (state.value === 'error' && !timedOut.value)
    return lines(props.result?.output ?? '').at(-1) ?? failure.value
  return lines(props.code?.code ?? '')[0] ?? ''
})
// output 是输出段的文本：出错时以失败说明开头，没有输出时不显示输出段
const output = computed(() => {
  if (props.result === undefined) return undefined
  const text = props.result.output ?? ''
  if (state.value === 'error') return text === '' ? failure.value : `${failure.value}\n${text}`
  return text === '' ? undefined : text
})
</script>

<template>
  <ToolStepRow
    v-model:expanded="expanded"
    icon="code"
    :title="t('pg.codeStep')"
    :summary="summary"
    :state="state"
    :badge="badge"
  >
    <div v-if="code" class="step-code">
      <CodeBlock :code="code.code" :language="code.language.toLowerCase()" />
    </div>
    <ToolIoCard v-if="output !== undefined" :output="output" :error="state === 'error'" />
    <div v-if="images.length > 0" class="step-images">
      <img v-for="(image, index) in images" :key="index" :src="dataURL(image)" alt="" />
    </div>
  </ToolStepRow>
</template>

<style scoped>
/* step-code 为代码段：最高 260 px，超出时在段内滚动，取 dsh 代码步骤 */
.step-code {
  max-height: 260px;
  margin: 4px 0 4px 4px;
  overflow-y: auto;
}

.step-code :deep(.container) {
  margin: 0;
}

/* step-images 为代码运行生成的图片：缩进与间距取 dsh 图片步骤，圆角与尺寸上限取 AI Studio 回答中的图片 */
.step-images {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 4px 0 4px 4px;
}

.step-images img {
  max-width: 100%;
  max-height: min(30vh, 358px);
  border-radius: 8px;
  object-fit: cover;
}
</style>
