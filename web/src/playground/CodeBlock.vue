<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import hljs from 'highlight.js/lib/common'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import type { IconName } from '@/playground/ui/icons'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { vTooltip } from '@/playground/ui/tooltip'
import { previewOrigin } from '@/playground/preview'

const props = withDefaults(
  defineProps<{
    code: string
    language?: string
    headerText?: string
    icon?: IconName
    expandable?: boolean
    showDownload?: boolean
    stickyHeader?: boolean
    showDivider?: boolean
  }>(),
  {
    language: '',
    headerText: '',
    icon: 'code',
    expandable: true,
    showDownload: true,
    stickyHeader: true,
    showDivider: false,
  },
)
const { t } = useI18n()
const { showNotice, config } = useConsole()

// languageNames 是官网代码块标题中与语言标识不同写法的显示名
const languageNames: Record<string, string> = {
  c: 'C',
  'c++': 'C++',
  cpp: 'C++',
  csharp: 'C#',
  css: 'CSS',
  csv: 'CSV',
  javascript: 'JavaScript',
  json: 'JSON',
  none: '',
  'objective-c': 'Objective-C',
  php: 'PHP',
  plaintext: '',
  sql: 'SQL',
  typescript: 'TypeScript',
  'visual basic': 'Visual Basic',
}
// downloadTypes 是下载文件的扩展名与 MIME 类型，未列出的语言按纯文本下载
const downloadTypes: Record<string, [string, string]> = {
  'c++': ['.cpp', 'text/x-c++src'],
  cpp: ['.cpp', 'text/x-c++src'],
  csharp: ['.cs', 'text/x-csharp'],
  javascript: ['.js', 'text/javascript'],
  'objective-c': ['.m', 'text/x-objective-c'],
  php: ['.php', 'text/x-php'],
  plaintext: ['.txt', 'text/plain'],
  sql: ['.sql', 'text/x-sql'],
  typescript: ['.ts', 'text/typescript'],
  'visual basic': ['.vb', 'text/x-vb'],
  python: ['.py', 'text/x-python'],
  java: ['.java', 'text/x-java'],
  go: ['.go', 'text/x-go'],
  bash: ['.sh', 'text/x-bash'],
  html: ['.html', 'text/html'],
  c: ['.c', 'text/x-c'],
  kotlin: ['.kt', 'text/x-kotlin'],
  css: ['.css', 'text/css'],
  csv: ['.csv', 'text/plain'],
}

const expanded = ref(true)
const previewing = ref(false)
// frame 是预览框，指向预览来源的 shim 页
const frame = ref<HTMLIFrameElement>()
// preview 是 HTML 预览的独立来源
const preview = computed(() => previewOrigin(config.value?.active_preview_listen_addr ?? ''))
const shim = computed(
  () => `${preview.value}/shim.html?origin=${encodeURIComponent(location.origin)}`,
)
const trimmed = computed(() => props.code.trim())
// title 是标题栏文字：指定标题优先，其次为语言显示名，都为空时为 Code
const title = computed(() => {
  const language = props.language
  const name =
    language in languageNames
      ? languageNames[language]
      : language.charAt(0).toUpperCase() + language.slice(1)
  return props.headerText || name || t('pg.code')
})

// hasAtLeastLines 判断文本是否至少包含 count 行，不分配数组
function hasAtLeastLines(text: string, count: number): boolean {
  let lines = 1
  for (let index = text.indexOf('\n'); index !== -1; index = text.indexOf('\n', index + 1)) {
    if (++lines >= count) return true
  }
  return false
}

// autoHighlightLimit 是未指定语言时执行自动识别高亮的最大字符数
const autoHighlightLimit = 16_384
// incrementalChunkSize 是长代码增量高亮的分块字符数
const incrementalChunkSize = 4_096
// largeCodeThreshold 是跳过网格折叠动画并在折叠后卸载 DOM 的字符阈值
const largeCodeThreshold = 4_096

const isLargeCode = computed(() => props.code.length >= largeCodeThreshold)
const bodyMounted = ref(true)
let unmountTimer = 0
let cachedLanguage = ''
let cachedPrefix = ''
let cachedHTML = ''

// highlightSegment 高亮单段代码
function highlightSegment(text: string, language: string): string {
  if (text === '') return ''
  if (language === 'none') return escapeHTML(text)
  if (hljs.getLanguage(language))
    return hljs.highlight(text, { language, ignoreIllegals: true }).value
  if (text.length > autoHighlightLimit) return escapeHTML(text)
  return hljs.highlightAuto(text).value
}

// highlightedParts 把长代码拆为稳定的已高亮前缀与流式变化的小尾部，避免每帧重写整块 innerHTML
const highlightedParts = computed<{ prefix: string; tail: string }>(() => {
  const text = trimmed.value
  const language = props.language
  if (language === 'none') return { prefix: '', tail: escapeHTML(text) }
  if (!hljs.getLanguage(language)) {
    const html =
      text.length <= autoHighlightLimit ? hljs.highlightAuto(text).value : escapeHTML(text)
    return { prefix: '', tail: html }
  }
  if (text.length < incrementalChunkSize) {
    cachedLanguage = language
    cachedPrefix = ''
    cachedHTML = ''
    return { prefix: '', tail: highlightSegment(text, language) }
  }
  if (language !== cachedLanguage || !text.startsWith(cachedPrefix)) {
    cachedLanguage = language
    cachedPrefix = ''
    cachedHTML = ''
  }
  const rest = text.slice(cachedPrefix.length)
  const boundary = rest.lastIndexOf('\n')
  if (boundary >= incrementalChunkSize) {
    const chunk = rest.slice(0, boundary + 1)
    cachedPrefix += chunk
    cachedHTML += highlightSegment(chunk, language)
  }
  return {
    prefix: cachedHTML,
    tail: highlightSegment(text.slice(cachedPrefix.length), language),
  }
})
// previewable 与官网一致：15 行以上的 HTML 或以 <svg 开头的代码可以预览
const previewable = computed(
  () =>
    (props.language === 'html' && hasAtLeastLines(props.code, 15)) ||
    props.code.startsWith('<svg '),
)

// sendPreview 在预览页就绪后把代码发给它，由它以 blob 文档打开
function sendPreview(event: MessageEvent): void {
  const target = frame.value?.contentWindow
  if (!target || event.source !== target || event.origin !== preview.value) return
  if (event.data?.type === 'preview-ready') target.postMessage({ html: props.code }, preview.value)
}

onMounted(() => window.addEventListener('message', sendPreview))
onUnmounted(() => {
  window.removeEventListener('message', sendPreview)
  window.clearTimeout(unmountTimer)
})

// escapeHTML 转义文本中的 HTML 特殊字符
function escapeHTML(text: string): string {
  return text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
}

// toggle 展开或折叠代码；长代码块立即切换并卸载内部节点以避免整页重排卡顿
function toggle(): void {
  if (!props.expandable) return
  window.clearTimeout(unmountTimer)
  const next = !expanded.value
  if (next) {
    bodyMounted.value = true
    expanded.value = true
    return
  }
  expanded.value = false
  if (isLargeCode.value) {
    bodyMounted.value = false
  } else {
    unmountTimer = window.setTimeout(() => {
      if (!expanded.value) bodyMounted.value = false
    }, 225)
  }
}

// copy 复制代码并以提示条告知结果
async function copy(): Promise<void> {
  try {
    await navigator.clipboard.writeText(trimmed.value)
    showNotice(t('pg.copied'), 'success')
  } catch {
    showNotice(t('pg.copyFailed'), 'error')
  }
}

// download 以官网文件名 ai_studio_code 加语言扩展名下载代码
function download(): void {
  const [extension, type] = downloadTypes[props.language] ?? ['.txt', 'text/plain']
  const url = URL.createObjectURL(new Blob([props.code], { type }))
  const link = document.createElement('a')
  link.href = url
  link.download = `ai_studio_code${extension}`
  link.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <div class="ms-code-block" :data-test-language="language">
    <div class="container">
      <div class="panel" :class="{ 'sticky-header': stickyHeader, expanded }">
        <div
          class="code-header"
          :class="{ sticky: stickyHeader, disabled: !expandable }"
          role="button"
          :tabindex="expandable ? 0 : -1"
          :aria-expanded="expanded"
          :aria-disabled="!expandable"
          @click="toggle"
          @keydown.enter.self.prevent="toggle"
          @keydown.space.self.prevent="toggle"
        >
          <div class="header-content">
            <UiIcon :name="icon" :size="20" class="title-icon" />
            <span v-tooltip="title" class="title-text">{{ title }}</span>
          </div>
          <div class="actions-container">
            <div
              class="projected-actions-wrapper"
              @click.stop
              @keydown.enter.stop
              @keydown.space.stop
            >
              <button
                v-if="previewable"
                v-tooltip="t('pg.preview')"
                type="button"
                ms-button
                class="ms-button-borderless ms-button-icon"
                :aria-label="previewing ? t('pg.stopPreview') : t('pg.showPreview')"
                @click="previewing = !previewing"
              >
                <UiIcon :name="previewing ? 'stopGeneration' : 'playCircle'" :size="20" />
              </button>
              <button
                v-if="showDownload"
                v-tooltip="t('pg.download')"
                type="button"
                ms-button
                class="ms-button-borderless ms-button-icon"
                :aria-label="t('pg.downloadCode')"
                @click="download"
              >
                <UiIcon name="download" :size="20" />
              </button>
              <button
                v-tooltip="t('pg.copyToClipboard')"
                type="button"
                ms-button
                class="ms-button-borderless ms-button-icon"
                :aria-label="t('pg.copyCode')"
                @click="copy"
              >
                <UiIcon name="copy" :size="20" />
              </button>
            </div>
            <button
              v-if="expandable"
              v-tooltip="expanded ? t('pg.collapseCode') : t('pg.expandCode')"
              type="button"
              ms-button
              tabindex="-1"
              class="ms-button-borderless ms-button-icon"
              :aria-label="expanded ? t('pg.collapseCode') : t('pg.expandCode')"
            >
              <UiIcon :name="expanded ? 'expandLess' : 'chevronDown'" :size="20" />
            </button>
          </div>
        </div>
        <div class="code-body" :class="{ 'no-transition': isLargeCode }" :inert="!expanded">
          <div v-if="bodyMounted" class="code-body-inner">
            <div v-if="previewing" class="preview">
              <iframe
                :key="code"
                ref="frame"
                :title="t('pg.preview')"
                sandbox="allow-same-origin allow-scripts allow-popups allow-forms"
                :src="shim"
              ></iframe>
            </div>
            <template v-else>
              <pre><code class="hljs"><span v-if="highlightedParts.prefix" v-html="highlightedParts.prefix"></span><span v-html="highlightedParts.tail"></span></code></pre>
              <div v-if="showDivider" class="code-content-divider"></div>
              <slot></slot>
            </template>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ms-code-block {
  position: relative;
  display: block;
}

.container {
  margin-top: 8px;
  margin-bottom: 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container);
  box-shadow: var(--shadow-v3-xs);
}

.panel {
  overflow: hidden;
  border-radius: 12px;
  background: var(--color-v3-surface-container);
}

.panel.sticky-header {
  overflow: visible clip;
}

.code-header {
  display: flex;
  align-items: center;
  height: 36px;
  padding-inline: 12px 6px;
  border-radius: 12px;
  background-color: var(--color-v3-surface-container);
  color: var(--color-v3-text);
  cursor: pointer;
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  outline: none;
}

.code-header.disabled {
  cursor: default;
}

.code-header:focus-visible {
  background-color: var(--color-v3-surface-container-high);
}

.code-header.sticky {
  position: sticky;
  z-index: 1;
  top: calc(var(--ms-autoscroll-container-padding-top, 20px) * -1);
}

.header-content {
  display: flex;
  flex: 1 1 0;
  align-items: center;
  min-width: 0;
  margin-right: 16px;
  overflow: hidden;
}

.title-icon {
  flex-shrink: 0;
  margin-right: 4px;
  color: var(--color-v3-text-var);
}

.title-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.actions-container,
.projected-actions-wrapper {
  display: flex;
  align-items: center;
  gap: 8px;
}

.actions-container {
  justify-content: end;
}

.code-body {
  display: grid;
  grid-template-rows: 0fr;
  visibility: hidden;
  transition:
    grid-template-rows 225ms cubic-bezier(0.4, 0, 0.2, 1),
    visibility 225ms;
}

.code-body.no-transition {
  transition: none;
}

.panel.expanded .code-body {
  grid-template-rows: 1fr;
  visibility: visible;
}

.code-body-inner {
  min-height: 0;
  overflow: hidden;
}

pre {
  display: inline-flex;
  max-width: calc(100cqw - 32px);
  margin: 0;
  padding: 0 16px 12px 12px;
  overflow-x: auto;
  contain: content;
  color: var(--color-v3-text);
  line-height: 22px;
}

code {
  font-family: monospace;
  font-size: 13px;
  text-wrap: wrap;
}

.code-content-divider {
  margin: 0 6px;
  border-top: 1px solid var(--color-v3-outline-var);
}

.preview {
  width: 100%;
  height: 50vh;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  box-shadow: var(--shadow-v3-xs);
  overflow: hidden;
}

.preview iframe {
  width: 100%;
  height: 100%;
  border: none;
  background: #fff;
}
</style>
