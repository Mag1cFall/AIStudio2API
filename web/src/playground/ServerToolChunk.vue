<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from '@/i18n'
import type { IconName } from '@/playground/ui/icons'
import UiIcon from '@/playground/ui/UiIcon.vue'
import ToolIoCard from './ToolIoCard.vue'
import ToolStepRow from './ToolStepRow.vue'
import type { Part, Source } from './types'

const props = defineProps<{
  call: NonNullable<Part['toolCall']>
  response?: Part['toolResponse']
  sources: Source[]
  running: boolean
}>()
const { t } = useI18n()
const expanded = ref(false)

// strings 返回参数中的字符串列表
function strings(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === 'string')
    : []
}
// queries 是网页搜索的查询词
const queries = computed(() => strings(props.call.args?.queries))
// links 是搜索步骤列出的来源：本轮的依据来源，没有时取搜索结果自带的来源
const links = computed(() =>
  props.sources.length > 0
    ? props.sources
    : ((props.response?.response?.sources as Source[] | undefined) ?? []),
)
// pages 是网址读取的网址与读取状态，结果到达前只有网址；url_metadata 为数组或数组的 JSON 文本
const pages = computed(() => {
  const metadata = props.response?.response?.url_metadata
  if (metadata === undefined)
    return strings(props.call.args?.urls).map((url) => ({ url, status: '' }))
  const items = (typeof metadata === 'string' ? JSON.parse(metadata) : metadata) as {
    retrieved_url: string
    url_retrieval_status: string
  }[]
  return items.map((item) => ({ url: item.retrieved_url, status: item.url_retrieval_status }))
})
// kind 是步骤类别：网页搜索、网页访问，其余服务端工具以输入输出卡片显示
const kind = computed(() => {
  if (['GOOGLE_SEARCH_WEB', 'GOOGLE_SEARCH_IMAGE'].includes(props.call.toolType ?? ''))
    return 'search'
  return props.call.toolType === 'URL_CONTEXT' ? 'read' : 'other'
})
// view 是步骤行的图标、动作与对象：搜索为查询词，访问为网址，其余为工具名与参数
const view = computed((): { icon: IconName; title: string; summary: string } => {
  if (kind.value === 'search')
    return { icon: 'globe', title: t('pg.searchStep'), summary: queries.value.join(', ') }
  if (kind.value === 'read')
    return {
      icon: 'document',
      title: t('pg.fetchStep'),
      summary: pages.value.map((page) => page.url).join(', '),
    }
  return {
    icon: 'functionCalling',
    title: props.call.toolName ?? props.call.toolType ?? '',
    summary: JSON.stringify(props.call.args ?? {}),
  }
})
// state 是步骤状态：结果未到且仍在生成时为运行中
const state = computed(() =>
  props.response === undefined && props.running ? 'running' : ('ok' as const),
)
</script>

<template>
  <ToolStepRow
    v-model:expanded="expanded"
    :icon="view.icon"
    :title="view.title"
    :summary="view.summary"
    :state="state"
  >
    <ToolIoCard v-if="kind === 'search'" :input="queries.join('\n')">
      <template v-if="links.length > 0" #output>
        <ol class="step-links">
          <li v-for="(source, index) in links" :key="index">
            <a :href="source.uri" target="_blank" rel="noopener noreferrer">{{ source.title }}</a>
          </li>
        </ol>
      </template>
    </ToolIoCard>
    <template v-else-if="kind === 'read'">
      <div v-for="page in pages" :key="page.url" class="web-fetch">
        <a class="fetch-url" :href="page.url" target="_blank" rel="noopener noreferrer"
          ><UiIcon name="globe" :size="14" class="link-icon" />{{ page.url }}</a
        >
        <div v-if="page.status" class="fetch-meta">
          <span
            class="fetch-status"
            :data-error="page.status !== 'URL_RETRIEVAL_STATUS_SUCCESS' || undefined"
            >{{
              page.status === 'URL_RETRIEVAL_STATUS_SUCCESS'
                ? t('pg.urlRetrieved')
                : t('pg.urlFailed')
            }}</span
          >
        </div>
      </div>
    </template>
    <ToolIoCard
      v-else
      :input="JSON.stringify(call.args ?? {}, undefined, 2)"
      :output="response ? JSON.stringify(response.response ?? {}, undefined, 2) : undefined"
    />
  </ToolStepRow>
</template>

<style scoped>
/* step-links 为搜索来源列表：链接色与悬停下划线取 dsh WebBlock，颜色取 AI Studio 主题 */
.step-links {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  margin: 0;
  padding-left: 18px;
}

.step-links a {
  color: var(--color-v3-text-link);
  text-decoration: none;
  word-break: break-word;
}

.step-links a:hover,
.step-links a:focus-visible {
  text-decoration: underline dotted;
  text-underline-offset: 3px;
}

/* web-fetch 为网址读取的结果块：链接一行、状态一行取 dsh WebBlock，边框、底色与圆角取 AI Studio 主题的卡片 */
.web-fetch {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 4px 0 4px 4px;
  padding: 12px 14px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container);
}

.fetch-url {
  color: var(--color-v3-text-link);
  font-family: 'DM Mono', monospace;
  font-size: 13px;
  font-weight: 500;
  line-height: 19px;
  text-decoration: none;
  word-break: break-all;
}

.fetch-url:hover,
.fetch-url:focus-visible {
  text-decoration: underline dotted;
  text-underline-offset: 3px;
}

.link-icon {
  margin-right: 5px;
  vertical-align: -2px;
}

.fetch-meta {
  display: flex;
  align-items: baseline;
  gap: 12px;
}

.fetch-status {
  color: var(--color-v3-text-var);
  font-size: 13px;
  line-height: 19px;
}

.fetch-status[data-error] {
  color: var(--color-error);
}
</style>
