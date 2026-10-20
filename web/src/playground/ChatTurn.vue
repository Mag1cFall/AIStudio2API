<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { useRouter } from 'vue-router'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import CodeBlock from './CodeBlock.vue'
import { driveNames } from './drive'
import LoadingIndicator from './LoadingIndicator.vue'
import MarkdownView from './MarkdownView.vue'
import RunStats from './RunStats.vue'
import ThoughtChunk from './ThoughtChunk.vue'
import { insertCitations } from './markdown'
import { useReveal } from './reveal'
import { usePlayground } from './session'
import type { Part, Turn } from './types'

const props = defineProps<{
  turn: Turn
  index: number
  streaming: boolean
  showLabel: boolean
  responses?: Part[] | undefined
}>()
const { t, locale } = useI18n()
const router = useRouter()
const { showNotice } = useConsole()
const playground = usePlayground()
const { session } = playground

const root = ref<HTMLElement>()
const answer = ref<HTMLElement>()
const editing = ref(false)
const draft = ref('')
const editor = ref<HTMLTextAreaElement>()
const functionReply = ref('')
// visible 表示轮次在视口附近；离屏且不在生成时用占位高度代替内容
const visible = ref(true)
const height = ref(0)

// thoughtText 是模型轮次的思考摘要
const thoughtText = computed(() =>
  props.turn.parts
    .filter((part) => part.thought && part.text)
    .map((part) => part.text)
    .join(''),
)
// answerParts 是思考以外的片段
const answerParts = computed(() => props.turn.parts.filter((part) => !part.thought))
// answerText 是回复正文
const answerText = computed(() =>
  answerParts.value
    .filter((part) => part.text !== undefined)
    .map((part) => part.text)
    .join(''),
)
// citedAnswer 是生成完成后按依据支撑插入引用编号的回复正文，与官网一样出现后直接显示全文
const citedAnswer = computed(() => {
  const { supports, sources } = props.turn
  if (!supports?.length || !sources?.length || props.streaming) return undefined
  return insertCitations(
    answerText.value,
    supports,
    sources.map((source) => source.uri),
  )
})
// thinkingRunning 表示正在输出思考，回复片段出现后结束
const thinkingRunning = computed(() => props.streaming && answerParts.value.length === 0)
const shownThought = useReveal(
  () => thoughtText.value,
  () => props.streaming,
)
const revealedAnswer = useReveal(
  () => answerText.value,
  () => props.streaming,
)
// shownAnswer 是当前显示的回复正文
const shownAnswer = computed(() => citedAnswer.value ?? revealedAnswer.value)
// waiting 表示已提交但还没有收到任何内容
const waiting = computed(() => props.streaming && props.turn.parts.length === 0)
const time = computed(() =>
  new Intl.DateTimeFormat(locale.value, { hour: 'numeric', minute: '2-digit' }).format(
    props.turn.created,
  ),
)
// fullTime 与官网一样是悬停时间时提示的日期与时刻
const fullTime = computed(() =>
  new Intl.DateTimeFormat(locale.value, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(props.turn.created),
)
const roleLabel = computed(() => (props.turn.role === 'user' ? t('pg.user') : t('pg.model')))
// hasThoughtSegment 表示模型轮次单独显示思考段
const hasThoughtSegment = computed(() => props.turn.role === 'model' && thoughtText.value !== '')
// hasAnswerSegment 表示需要显示回复段
const hasAnswerSegment = computed(
  () =>
    props.turn.role === 'user' ||
    !hasThoughtSegment.value ||
    answerParts.value.length > 0 ||
    props.turn.error !== undefined ||
    waiting.value ||
    (props.turn.sources?.length ?? 0) > 0,
)
// now 是生成中每 100 ms 更新的时刻，轮次耗时与官网一样随之计数
const now = ref(performance.now())
let ticker = 0
watch(
  () => props.streaming,
  (running) => {
    window.clearInterval(ticker)
    if (running) ticker = window.setInterval(() => (now.value = performance.now()), 100)
  },
  { immediate: true },
)
// elapsed 是生成中从提交起的耗时
const elapsed = computed(() =>
  props.streaming ? Math.max(0, Math.round(now.value - session.started)) : undefined,
)
// thoughtDuration 是思考段的耗时，思考结束前随生成计数
const thoughtDuration = computed(() => props.turn.thinking ?? elapsed.value)
// answerDuration 是模型轮次的总耗时，生成中随之计数
const answerDuration = computed(() =>
  props.turn.role === 'model' ? (props.turn.duration ?? elapsed.value) : undefined,
)
// sources 是依据来源中有标题或地址的条目
const sources = computed(() =>
  (props.turn.sources ?? []).filter((source) => source.title || source.uri),
)
// awaitingResponse 表示最后一轮函数调用还没有函数响应，可在调用块内填写
const awaitingResponse = computed(
  () =>
    props.index === session.turns.length - 1 &&
    !props.streaming &&
    props.turn.role === 'model' &&
    answerParts.value.some((part) => part.functionCall) &&
    !props.responses?.length,
)

// mediaURL 返回内联媒体的 data URL
function mediaURL(part: Part): string {
  return `data:${part.inlineData?.mimeType};base64,${part.inlineData?.data}`
}

// mediaKind 返回内联媒体的类型
function mediaKind(part: Part): 'image' | 'audio' | 'video' | 'file' {
  const mime = part.inlineData?.mimeType ?? part.fileData?.mimeType ?? ''
  if (mime.startsWith('image/')) return 'image'
  if (mime.startsWith('audio/')) return 'audio'
  if (mime.startsWith('video/')) return 'video'
  return 'file'
}

// executionCode 与官网一致：执行结果出错时显示超时或失败说明加错误输出
function executionCode(result: NonNullable<Part['codeExecutionResult']>): string {
  if (result.outcome === 'OUTCOME_OK') return result.output ?? ''
  const reason =
    result.outcome === 'OUTCOME_DEADLINE_EXCEEDED' ? t('pg.codeTimeout') : t('pg.codeFailed')
  return `${reason}\n${result.output ?? ''}`
}

// responseText 返回已发送的函数响应文本
function responseText(call: NonNullable<Part['functionCall']>): string {
  const response = props.responses?.find(
    (part) =>
      part.functionResponse?.name === call.name &&
      (call.id === undefined || part.functionResponse.id === call.id),
  )?.functionResponse?.response
  if (response === undefined) return ''
  const keys = Object.keys(response)
  return keys.length === 1 && keys[0] === 'result' && typeof response.result === 'string'
    ? response.result
    : JSON.stringify(response)
}

// sendResponse 以函数响应继续生成
function sendResponse(call: NonNullable<Part['functionCall']>): void {
  if (functionReply.value.trim() === '') return
  playground.respond(call, functionReply.value)
  functionReply.value = ''
}

// startEdit 进入编辑：文本变为可编辑文本框
async function startEdit(): Promise<void> {
  if (editing.value) {
    finishEdit()
    return
  }
  draft.value =
    props.turn.role === 'user'
      ? (props.turn.parts.find((part) => part.text)?.text ?? '')
      : answerParts.value
          .filter((part) => part.text !== undefined)
          .map((part) => part.text)
          .join('')
  editing.value = true
  await nextTick()
  editor.value?.focus()
  resizeEditor()
}

// finishEdit 保存编辑结果
function finishEdit(): void {
  if (!editing.value) return
  editing.value = false
  playground.editTurn(props.index, draft.value)
}

// resizeEditor 让编辑框随内容增高
function resizeEditor(): void {
  const element = editor.value
  if (element === undefined) return
  element.style.height = 'auto'
  element.style.height = `${element.scrollHeight}px`
}

// plainText 返回本轮正文的 Markdown 原文
function plainText(): string {
  return props.turn.role === 'user'
    ? (props.turn.parts.find((part) => part.text)?.text ?? '')
    : answerParts.value
        .filter((part) => part.text !== undefined)
        .map((part) => part.text)
        .join('')
}

// copyText 复制回复的纯文本
async function copyText(): Promise<void> {
  await navigator.clipboard.writeText(answer.value?.innerText ?? plainText())
  showNotice(t('pg.copied'), 'success')
}

// copyMarkdown 复制回复的 Markdown 原文
async function copyMarkdown(): Promise<void> {
  await navigator.clipboard.writeText(plainText())
  showNotice(t('pg.copied'), 'success')
}

// branch 从这一轮创建分支并打开
async function branch(): Promise<void> {
  const id = await playground.branch(props.index)
  void router.push(`/prompts/${id}`)
}

let intersection: IntersectionObserver | undefined
let resize: ResizeObserver | undefined

onMounted(() => {
  intersection = new IntersectionObserver(
    ([entry]) => {
      visible.value = entry?.isIntersecting ?? true
    },
    { root: root.value?.closest('.chat-scroller') ?? null, rootMargin: '1500px 0px' },
  )
  resize = new ResizeObserver(([entry]) => {
    if (visible.value && entry) height.value = entry.contentRect.height
  })
  if (root.value) {
    intersection.observe(root.value)
    resize.observe(root.value)
  }
})

onUnmounted(() => {
  intersection?.disconnect()
  resize?.disconnect()
  window.clearInterval(ticker)
})
</script>

<template>
  <div
    :id="`turn-${turn.id}`"
    ref="root"
    class="chat-turn"
    :class="{ 'thought-host': hasThoughtSegment }"
  >
    <div v-if="!visible && !streaming && height > 0" :style="{ height: `${height}px` }"></div>
    <template v-else>
      <div
        v-if="hasThoughtSegment"
        tabindex="-1"
        class="chat-turn-container model"
        :class="{ 'menu-open': false }"
      >
        <div class="actions-container">
          <div class="actions hover-or-edit">
            <button
              v-tooltip.below="t('pg.rerun')"
              type="button"
              ms-button
              class="ms-button-borderless ms-button-icon"
              :aria-label="t('pg.rerun')"
              :aria-disabled="session.running"
              @click="playground.rerun(index)"
            >
              <UiIcon name="spark" :size="20" />
            </button>
            <DropdownMenuRoot :modal="false">
              <DropdownMenuTrigger
                v-tooltip.below="t('pg.moreOptions')"
                ms-button
                class="ms-button-borderless ms-button-icon"
                :aria-label="t('pg.moreOptions')"
              >
                <UiIcon name="more" :size="20" />
              </DropdownMenuTrigger>
              <DropdownMenuPortal>
                <DropdownMenuContent align="end" :side-offset="4" class="ms-menu-panel">
                  <DropdownMenuItem class="ms-menu-item" @select="playground.deleteTurn(index)">
                    <UiIcon name="delete" :size="18" />
                    <span class="ms-menu-item-text">{{ t('common.delete') }}</span>
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    class="ms-menu-item"
                    :disabled="session.running"
                    @select="branch"
                  >
                    <UiIcon name="branch" :size="18" />
                    <span class="ms-menu-item-text">{{ t('pg.branch') }}</span>
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenuPortal>
            </DropdownMenuRoot>
          </div>
        </div>
        <div class="turn-content">
          <div v-if="showLabel" role="heading" aria-level="3" class="author-label">
            {{ roleLabel }}<span v-tooltip.below="fullTime" class="timestamp">{{ time }}</span>
          </div>
          <ThoughtChunk
            :text="thoughtText"
            :shown="shownThought"
            :streaming="thinkingRunning"
            :raw="session.rawMode"
            :duration="turn.thinking"
            :tokens="turn.thoughtTokens"
          />
        </div>
        <div v-if="thoughtDuration !== undefined" class="turn-footer">
          <RunStats class="hover-or-edit" :duration="thoughtDuration" />
        </div>
      </div>

      <div
        v-if="hasAnswerSegment"
        tabindex="-1"
        class="chat-turn-container"
        :class="[turn.role, { edit: editing }]"
      >
        <div class="actions-container">
          <div class="actions hover-or-edit">
            <button
              v-if="!waiting && turn.error === undefined"
              v-tooltip.below="editing ? t('pg.stopEditing') : t('pg.edit')"
              type="button"
              ms-button
              class="toggle-edit-button ms-button-borderless ms-button-icon"
              :aria-label="editing ? t('pg.stopEditing') : t('pg.edit')"
              :aria-disabled="session.running"
              @click="!session.running && startEdit()"
            >
              <UiIcon :name="editing ? 'check' : 'edit'" :size="20" />
            </button>
            <button
              v-tooltip.below="t('pg.rerun')"
              type="button"
              ms-button
              class="ms-button-borderless ms-button-icon"
              :aria-label="t('pg.rerun')"
              :aria-disabled="session.running"
              @click="!session.running && playground.rerun(index)"
            >
              <UiIcon name="spark" :size="20" />
            </button>
            <DropdownMenuRoot :modal="false">
              <DropdownMenuTrigger
                v-tooltip.below="t('pg.moreOptions')"
                ms-button
                class="ms-button-borderless ms-button-icon"
                :aria-label="t('pg.moreOptions')"
              >
                <UiIcon name="more" :size="20" />
              </DropdownMenuTrigger>
              <DropdownMenuPortal>
                <DropdownMenuContent align="end" :side-offset="4" class="ms-menu-panel">
                  <DropdownMenuItem
                    class="ms-menu-item"
                    :disabled="session.running"
                    @select="playground.deleteTurn(index)"
                  >
                    <UiIcon name="delete" :size="18" />
                    <span class="ms-menu-item-text">{{ t('common.delete') }}</span>
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    class="ms-menu-item"
                    :disabled="session.running"
                    @select="branch"
                  >
                    <UiIcon name="branch" :size="18" />
                    <span class="ms-menu-item-text">{{ t('pg.branch') }}</span>
                  </DropdownMenuItem>
                  <DropdownMenuItem class="ms-menu-item" @select="copyText">
                    <UiIcon name="copy" :size="18" />
                    <span class="ms-menu-item-text">{{ t('pg.copyText') }}</span>
                  </DropdownMenuItem>
                  <DropdownMenuItem class="ms-menu-item" @select="copyMarkdown">
                    <UiIcon name="markdownCopy" :size="18" />
                    <span class="ms-menu-item-text">{{ t('pg.copyMarkdown') }}</span>
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenuPortal>
            </DropdownMenuRoot>
          </div>
        </div>
        <div class="turn-content">
          <div
            v-if="showLabel && !hasThoughtSegment"
            role="heading"
            aria-level="3"
            class="author-label"
          >
            {{ roleLabel }}<span v-tooltip.below="fullTime" class="timestamp">{{ time }}</span>
          </div>
          <LoadingIndicator v-if="waiting" />
          <div
            v-for="(part, partIndex) in answerParts"
            v-show="!editing || part.text === undefined"
            :key="partIndex"
          >
            <template v-if="part.inlineData || part.fileData">
              <img
                v-if="mediaKind(part) === 'image' && part.inlineData"
                class="media-image"
                :src="mediaURL(part)"
                alt=""
              />
              <audio
                v-else-if="mediaKind(part) === 'audio' && part.inlineData"
                controls
                :src="mediaURL(part)"
              ></audio>
              <video
                v-else-if="mediaKind(part) === 'video' && part.inlineData"
                controls
                :src="mediaURL(part)"
              ></video>
              <a
                v-else-if="part.fileData"
                class="file-chip"
                :href="part.fileData.fileUri"
                target="_blank"
                rel="noreferrer"
              >
                <UiIcon
                  :name="driveNames.has(part.fileData.fileUri) ? 'addToDrive' : 'youtube'"
                  :size="18"
                />{{ driveNames.get(part.fileData.fileUri) ?? part.fileData.fileUri }}
              </a>
              <span v-else class="file-chip"
                ><UiIcon name="upload" :size="18" />{{ part.inlineData?.mimeType }}</span
              >
            </template>
            <CodeBlock
              v-else-if="part.functionCall"
              :code="
                part.functionCall.args ? JSON.stringify(part.functionCall.args, undefined, 2) : ''
              "
              :header-text="part.functionCall.name"
              icon="functionCalling"
              show-divider
            >
              <form class="function-response" @submit.prevent="sendResponse(part.functionCall)">
                <input
                  v-if="awaitingResponse"
                  v-model="functionReply"
                  ms-input
                  class="function-response-input"
                  :aria-label="t('pg.functionResponse')"
                  :placeholder="t('pg.functionResponse')"
                />
                <input
                  v-else
                  ms-input
                  class="function-response-input"
                  :aria-label="t('pg.functionResponse')"
                  :placeholder="t('pg.functionResponse')"
                  :value="responseText(part.functionCall)"
                  disabled
                />
                <button
                  v-if="awaitingResponse"
                  type="submit"
                  ms-button
                  class="ms-button-primary"
                  :disabled="functionReply.trim() === ''"
                >
                  {{ t('pg.send') }}
                </button>
              </form>
            </CodeBlock>
            <CodeBlock
              v-else-if="part.executableCode"
              :code="part.executableCode.code"
              :language="t('pg.executableCode')"
            />
            <CodeBlock
              v-else-if="part.codeExecutionResult"
              :code="executionCode(part.codeExecutionResult)"
              :language="t('pg.codeExecutionResult')"
            />
          </div>
          <div v-if="turn.role === 'user' && !editing" class="user-chunk">
            <MarkdownView :text="plainText()" :raw="session.rawMode" />
          </div>
          <div v-else-if="turn.role === 'model' && !editing && shownAnswer" ref="answer">
            <MarkdownView :text="shownAnswer" :raw="session.rawMode" />
          </div>
          <textarea
            v-if="editing"
            ref="editor"
            v-model="draft"
            class="edit-textarea"
            :aria-label="t('pg.edit')"
            @input="resizeEditor"
            @keydown.escape.prevent="finishEdit"
            @blur="finishEdit"
          ></textarea>
          <div v-if="sources.length && !streaming" class="grounding-sources">
            <p class="sources-title">
              {{ t('pg.citations') }}
              <span
                v-tooltip="t('pg.sourcesTooltip')"
                class="sources-help"
                :aria-label="t('pg.sourcesTooltip')"
                ><UiIcon name="help" :size="16"
              /></span>
            </p>
            <ol>
              <li v-for="(source, sourceIndex) in sources" :key="sourceIndex">
                <a :href="source.uri" target="_blank" rel="noopener noreferrer">{{
                  source.title || source.uri
                }}</a>
              </li>
            </ol>
          </div>
          <div v-if="turn.searchQueries?.length && !streaming" class="search-entry-point">
            <div class="search-entry-container">
              <h5 class="search-entry-container-title">{{ t('pg.searchSuggestions') }}</h5>
              <div class="search-entry-container-description">
                {{ t('pg.searchSuggestionsNote') }}
                <a
                  href="https://ai.google.dev/gemini-api/docs/grounding/search-suggestions"
                  target="_blank"
                  rel="noopener noreferrer"
                  >{{ t('pg.learnMore') }}</a
                >
              </div>
              <div class="search-chips">
                <UiIcon name="search" :size="24" class="search-chips-icon" />
                <ul class="carousel">
                  <li v-for="query in turn.searchQueries" :key="query">
                    <a
                      class="chip"
                      target="_blank"
                      rel="noopener noreferrer"
                      :href="`https://www.google.com/search?q=${encodeURIComponent(query)}&client=app-vertex-grounding&safesearch=active`"
                      >{{ query }}</a
                    >
                  </li>
                </ul>
              </div>
            </div>
          </div>
          <div v-if="turn.error !== undefined" class="model-error">
            <UiIcon name="error" :size="20" />
            <span>{{ turn.error }}</span>
          </div>
          <div
            v-if="
              turn.finishReason && !['STOP', 'MAX_TOKENS'].includes(turn.finishReason) && !streaming
            "
            class="finish-reason"
          >
            {{ t('pg.finishReason').replace('{reason}', turn.finishReason) }}
          </div>
        </div>
        <div v-if="answerDuration !== undefined" class="turn-footer">
          <RunStats
            class="hover-or-edit"
            :duration="answerDuration"
            :latency="turn.latency"
            :tokens="streaming ? undefined : turn.tokens"
          />
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.chat-turn {
  display: flex;
  flex-direction: column;
  width: 100%;
}

.chat-turn + .chat-turn {
  margin-top: 8px;
}

.chat-turn-container {
  position: relative;
  display: flex;
  flex: 1;
  flex-direction: column;
  justify-content: center;
  min-width: 0;
  max-width: 100%;
  margin-bottom: 0;
  padding: 8px 12px 24px;
  border: 1px solid transparent;
  border-radius: 8px;
}

.chat-turn-container + .chat-turn-container {
  margin-top: 8px;
}

.chat-turn-container:focus-visible,
.chat-turn-container:has([aria-expanded='true'][aria-haspopup='menu']):not(.edit) {
  border-color: var(--color-v3-outline);
}

@media (hover: hover), (pointer: none) {
  .chat-turn-container:hover:not(.edit) {
    border-color: var(--color-v3-outline);
  }

  .chat-turn-container:hover .hover-or-edit {
    opacity: 1;
    pointer-events: auto;
  }

  .chat-turn-container:hover .turn-footer {
    background-color: var(--color-v3-surface);
  }
}

.chat-turn-container.edit {
  border-color: var(--color-v3-outline-accent);
}

.actions-container {
  position: sticky;
  z-index: 1;
  top: 20px;
  display: flex;
  justify-content: flex-end;
  width: 100%;
  height: 0;
}

.chat-turn-container:has(.ms-code-block .code-header.sticky) .actions-container {
  top: calc(56px * var(--ms-autoscroll-container-scrolled, 1));
  transition: top 0.6s ease-in-out;
}

.actions {
  position: absolute;
  top: -32px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 4px;
  border-radius: 16px;
  background-color: var(--color-v3-surface-container-high);
}

.chat-turn-container.model:has(.ms-code-block .code-header.sticky) .actions {
  right: 8px;
}

.chat-turn-container.user:has(.ms-code-block .code-header.sticky) .actions {
  right: 20px;
}

.hover-or-edit {
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.3s ease-in-out;
}

.chat-turn-container:focus-within .hover-or-edit,
.chat-turn-container:has([aria-expanded='true'][aria-haspopup='menu']) .hover-or-edit,
.chat-turn-container.edit .hover-or-edit {
  opacity: 1;
  pointer-events: auto;
}

.turn-content {
  display: flex;
  flex-direction: column;
  width: 100%;
  min-width: 0;
}

.author-label {
  margin-top: 20px;
  margin-bottom: 8px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.timestamp {
  margin-left: 8px;
  color: var(--color-v3-text-var);
  cursor: default;
  font-size: 11px;
}

.timestamp::before {
  margin-right: 8px;
  content: '•';
}

.user-chunk {
  border-radius: 16px;
  color: var(--color-on-surface);
}

.edit-textarea {
  width: 100%;
  min-height: 21px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--color-v3-text);
  font-size: 14px;
  line-height: 21px;
  outline: none;
  resize: none;
  white-space: pre-wrap;
}

.model-error {
  display: flex;
  align-items: center;
  gap: 4px;
  color: var(--color-error);
}

.finish-reason {
  margin-top: 8px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.grounding-sources ol {
  margin: 0;
  padding-left: 24px;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.grounding-sources a {
  color: var(--color-v3-text-link);
}

.sources-title {
  display: flex;
  align-items: center;
  gap: 2px;
  margin: 32px 0 14px;
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.sources-help {
  display: inline-flex;
  cursor: default;
}

.search-entry-point {
  max-width: calc(100cqw - 32px);
  margin-top: 22px;
  font-size: 12px;
  line-height: 18px;
}

.search-entry-container {
  padding: 12px 16px;
  border: 1px solid var(--color-search-entry-point-border-color);
  border-radius: 16px;
  background-color: var(--color-search-entry-point-background-color);
  color: var(--color-search-entry-point-color);
}

.search-entry-container-title {
  margin: 0;
  font-size: 14px;
  font-weight: 400;
  line-height: 21px;
}

.search-entry-container-description {
  padding: 8px 0;
}

.search-entry-container-description a {
  color: var(--color-v3-text-link);
}

.search-chips {
  display: flex;
  align-items: center;
  overflow-y: auto;
  padding: 12px;
  border: 1px solid var(--color-search-entry-point-container-border-color);
  border-radius: 8px;
  background-color: var(--color-search-entry-point-container-background-color);
}

.search-chips-icon {
  padding: 4px 0;
}

.carousel {
  display: flex;
  flex-wrap: wrap;
  row-gap: 8px;
  margin: 0;
  padding: 0;
  list-style-type: none;
  scrollbar-width: none;
  white-space: nowrap;
}

.chip {
  display: inline-block;
  min-width: 20px;
  margin: 0 8px;
  padding: 5px 16px;
  border: 1px solid var(--color-search-entry-point-chip-border-color);
  border-radius: 16px;
  background-color: var(--color-search-entry-point-chip-background-color);
  color: var(--color-search-entry-point-chip-color);
  text-align: center;
  text-decoration: none;
  user-select: none;
}

.chip:hover {
  background-color: var(--color-search-entry-point-chip-hover-background-color);
}

.function-response {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px;
}

.function-response-input {
  flex: 1;
}

.media-image {
  max-width: min(100%, 480px);
  max-height: 480px;
  margin: 4px 0 8px;
  border-radius: 8px;
}

.file-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin: 4px 0 8px;
  padding: 4px 8px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  color: var(--color-v3-text);
  font-size: 12px;
  line-height: 18px;
  overflow-wrap: anywhere;
}

.turn-footer {
  position: absolute;
  bottom: -20px;
  display: flex;
  align-items: center;
  gap: 4px;
  height: unset;
  padding: 4px 8px;
  border-radius: 8px;
  background-color: transparent;
}
</style>
