<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from '@/i18n'
import MarkdownView from './MarkdownView.vue'
import ToolStepRow from './ToolStepRow.vue'

const props = defineProps<{ text: string; running: boolean; raw: boolean }>()
const { t } = useI18n()
const expanded = ref(false)

// latestLine 返回首行已写完的最后一个段落的首行
function latestLine(text: string): string {
  const paragraphs = text.split(/\r?\n(?:[\t ]*\r?\n)+/)
  const done = paragraphs.at(-1)!.includes('\n') ? paragraphs : paragraphs.slice(0, -1)
  return (
    done
      .map((paragraph) => paragraph.split('\n')[0]!.trim())
      .filter(Boolean)
      .at(-1) ?? ''
  )
}
// summary 与 dsh ReasoningRow 一样：生成中取最近写完的段落首行，结束后取首行，去掉粗体标记
const summary = computed(() =>
  (props.running ? latestLine(props.text) : props.text.split('\n')[0]!).replaceAll('**', ''),
)
</script>

<template>
  <ToolStepRow
    v-model:expanded="expanded"
    icon="spark"
    :title="t('pg.thinkStep')"
    :summary="summary"
    :state="running ? 'running' : 'ok'"
  >
    <div class="think-body"><MarkdownView :text="text" :raw="raw" /></div>
  </ToolStepRow>
</template>

<style scoped>
/* think-body 为展开的思考正文：缩进与次级字号取 dsh ReasoningRow，颜色取 AI Studio 次要文字 */
.think-body {
  min-width: 0;
  padding: 4px 0 4px 22px;
  color: var(--color-v3-text-var);
}

.think-body :deep(.cmark-node :is(p, li, td, th, blockquote, dt)) {
  font-size: 13px;
  line-height: 20px;
}
</style>
