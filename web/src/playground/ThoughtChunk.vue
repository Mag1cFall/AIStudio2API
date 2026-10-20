<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { vTooltip } from '@/playground/ui/tooltip'
import { formatDuration } from './duration'
import LoadingIndicator from './LoadingIndicator.vue'
import MarkdownView from './MarkdownView.vue'

const props = defineProps<{
  text: string
  streaming: boolean
  shown?: string
  raw?: boolean
  duration?: number | undefined
  tokens?: number | undefined
}>()
const { t, locale } = useI18n()
const expanded = ref(false)
const outline = ref<HTMLElement>()
// bodyMounted 表示思考正文已渲染；折叠动画结束后卸载，生成中折叠时不解析正文
const bodyMounted = ref(false)

watch(expanded, (open) => {
  if (open) bodyMounted.value = true
  else
    window.setTimeout(() => {
      if (!expanded.value) bodyMounted.value = false
    }, 225)
})

// headings 按官网取小标题的规则列出思考中每个含粗体的行的粗体文字，相邻重复的只保留一个
const headings = computed(() =>
  props.text
    .split('\n')
    .map(
      (line) =>
        line
          .trim()
          .match(/\*\*(.*)\*\*/)?.[1]
          ?.trim() ?? '',
    )
    .filter(Boolean)
    .filter((heading, index, list) => heading !== list[index - 1]),
)
// toggleText 是底栏的展开或折叠提示
const toggleText = computed(() =>
  expanded.value ? t('pg.collapseThoughts') : t('pg.expandThoughts'),
)
// showOutline 表示折叠的底栏列出思考小标题，没有小标题时提示展开
const showOutline = computed(() => !expanded.value && headings.value.length > 0)
// meta 是思考结束后标题行右侧的思考耗时与思考 token 数
const meta = computed(() =>
  [
    props.duration === undefined ? '' : formatDuration(props.duration / 1000),
    props.tokens === undefined
      ? ''
      : t('pg.tokens').replace('{count}', new Intl.NumberFormat(locale.value).format(props.tokens)),
  ]
    .filter(Boolean)
    .join(' · '),
)

// 生成中新小标题出现时列表滚到最新一行
watch(
  () => headings.value.length,
  async () => {
    if (!props.streaming) return
    await nextTick()
    if (outline.value) outline.value.scrollTop = outline.value.scrollHeight
  },
)

// onKey 以回车或空格切换展开
function onKey(event: KeyboardEvent): void {
  if (event.key !== 'Enter' && event.key !== ' ') return
  event.preventDefault()
  expanded.value = !expanded.value
}
</script>

<template>
  <div class="thought-chunk">
    <div class="thought-panel" :class="{ expanded }">
      <div
        class="top-panel-header"
        role="button"
        tabindex="0"
        :aria-expanded="expanded"
        @click="expanded = !expanded"
        @keydown="onKey"
      >
        <UiIcon
          name="spark"
          :size="16"
          class="thinking-progress-icon"
          :class="{ 'in-progress': streaming }"
        />
        <LoadingIndicator v-if="streaming" />
        <template v-else>{{ t('pg.thoughts') }}</template>
        <span v-if="!streaming && meta" class="thought-meta">{{ meta }}</span>
      </div>
      <div class="thought-body" :inert="!expanded">
        <div class="thought-body-inner">
          <div v-if="bodyMounted" class="thought-content">
            <MarkdownView :text="shown ?? text" :raw="raw" />
          </div>
        </div>
      </div>
      <div
        v-tooltip="toggleText"
        class="thought-panel-footer"
        role="button"
        tabindex="0"
        :aria-expanded="expanded"
        @click="expanded = !expanded"
        @keydown="onKey"
      >
        <div class="thought-collapsed-text-container">
          <ol v-if="showOutline" ref="outline" class="thought-outline">
            <TransitionGroup name="fade-slide">
              <li
                v-for="(heading, index) in headings"
                :key="index"
                class="thought-collapsed-text"
                :class="{ latest: streaming && index === headings.length - 1 }"
              >
                {{ heading }}
              </li>
            </TransitionGroup>
          </ol>
          <p v-else class="thought-collapsed-text">{{ toggleText }}</p>
        </div>
        <UiIcon name="chevronRight" :size="20" class="footer-icon" :class="{ expanded }" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.thought-chunk {
  display: block;
  width: 100%;
  margin-top: 12px;
  margin-bottom: 4px;
}

.thought-panel {
  overflow: hidden;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container);
  box-shadow: var(--shadow-v3-xs);
}

.top-panel-header {
  display: flex;
  align-items: center;
  min-height: 45px;
  padding: 12px 12px 12px 16px;
  color: var(--color-v3-text);
  cursor: pointer;
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  outline: none;
  transition: margin-bottom 225ms cubic-bezier(0.4, 0, 0.2, 1);
}

.thought-panel.expanded .top-panel-header {
  margin-bottom: 16px;
}

.top-panel-header:focus-visible,
.thought-panel-footer:focus-visible {
  background-color: var(--color-v3-surface-container-high);
}

.thinking-progress-icon {
  margin-right: 4px;
  color: var(--color-v3-text-link);
}

.thinking-progress-icon.in-progress {
  animation: rotate 3s linear infinite;
}

.thought-body {
  display: grid;
  grid-template-rows: 0fr;
  visibility: hidden;
  transition:
    grid-template-rows 225ms cubic-bezier(0.4, 0, 0.2, 1),
    visibility 225ms;
}

.thought-panel.expanded .thought-body {
  grid-template-rows: 1fr;
  visibility: visible;
}

.thought-body-inner {
  min-height: 0;
  overflow: hidden;
}

.thought-content {
  padding: 0 24px 16px;
}

.thought-panel-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 38px;
  padding: 0 16px;
  border-top: 1px solid var(--color-v3-outline-var);
  color: var(--color-v3-text);
  cursor: pointer;
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  outline: none;
}

.thought-collapsed-text-container {
  flex: 1;
  width: 0;
  overflow: hidden;
}

.thought-collapsed-text {
  margin: 0 16px 0 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.thought-meta {
  margin-left: auto;
  padding-left: 16px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-weight: 400;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.thought-outline {
  max-height: 100px;
  margin: 0;
  padding: 8px 0;
  overflow-y: auto;
  list-style: none;
  scrollbar-width: thin;
}

.thought-outline .thought-collapsed-text {
  color: var(--color-v3-text-var);
  font-weight: 400;
}

.thought-outline .thought-collapsed-text.latest {
  color: var(--color-v3-text);
  font-weight: 500;
}

.footer-icon {
  flex: 0 0 auto;
  transform: rotate(90deg);
}

.footer-icon.expanded {
  transform: rotate(270deg);
  transition: transform 0.3s;
}

.fade-slide-enter-active {
  transition:
    opacity 300ms ease-out,
    transform 300ms ease-out;
}

.fade-slide-leave-active {
  transition: opacity 200ms ease-in;
}

.fade-slide-enter-from {
  opacity: 0;
  transform: translateY(100%);
}

.fade-slide-leave-to {
  opacity: 0;
}

@keyframes rotate {
  to {
    transform: rotate(1turn);
  }
}
</style>
