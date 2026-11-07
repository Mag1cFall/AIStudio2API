<script setup lang="ts">
import {
  computed,
  nextTick,
  onActivated,
  onDeactivated,
  onMounted,
  onUnmounted,
  ref,
  type FunctionalComponent,
} from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { useRouter } from 'vue-router'
import DisclosureChevron from '@/components/DisclosureChevron.vue'
import UiCollapse from '@/components/UiCollapse.vue'
import { useConsole } from '@/console'
import { useI18n, type TranslationKey } from '@/i18n'
import type { IconName } from '@/playground/ui/icons'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { attachmentName, downloadAttachment, useAttachmentPreview } from './attachment'
import { copyToClipboard } from './clipboard'
import { useRunningClock } from './clock'
import CodeExecutionChunk from './CodeExecutionChunk.vue'
import FileChunk from './FileChunk.vue'
import FunctionCallChunk from './FunctionCallChunk.vue'
import { driveNames } from './drive'
import LoadingIndicator from './LoadingIndicator.vue'
import MarkdownView from './MarkdownView.vue'
import RunStats from './RunStats.vue'
import ServerToolChunk from './ServerToolChunk.vue'
import ThoughtChunk from './ThoughtChunk.vue'
import ThoughtStep from './ThoughtStep.vue'
import MediaViewDialog from './media/MediaViewDialog.vue'
import VideoResult from './media/VideoResult.vue'
import { isAgent } from './catalog'
import { insertCitations } from './markdown'
import { dataURL } from './protocols'
import { useReveal } from './reveal'
import { youtubeVideoID } from './sample-media'
import SegmentRun from './SegmentRun.vue'
import { usePlayground } from './session'
import type { Part, Turn } from './types'

const props = defineProps<{
  turn: Turn
  index: number
  streaming: boolean
  showLabel: boolean
  responses?: Part[] | undefined
}>()
const { t, locale, logText, errorText } = useI18n()
const router = useRouter()
const { showNotice } = useConsole()
const playground = usePlayground()
const { session } = playground

const root = ref<HTMLElement>()
const editing = ref(false)
const draft = ref('')
const editor = ref<HTMLTextAreaElement>()
// visible 表示轮次在视口附近；离屏且不在生成时用占位高度代替内容
const visible = ref(true)
const height = ref(0)

// agent 表示模型轮次在 Agent 模式下生成：思考按出现顺序显示为步骤行，完成后过程折叠在耗时标题下
const agent = computed(() => props.turn.role === 'model' && props.turn.agent === true)
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
// Segment 是回复按原顺序分成的一段：连续的文本片段合为一段，start 与 end 为它在回复正文中的位置；Agent 模式下连续的思考片段合为一段；其余片段各成一段，index 为它在 answerParts 中的位置
type Segment =
  | { kind: 'text'; start: number; end: number }
  | { kind: 'thought'; text: string }
  | { kind: 'part'; part: Part; index: number }
// segments 是回复按片段原顺序分成的段
const segments = computed(() => {
  const list: Segment[] = []
  let offset = 0
  let index = 0
  for (const part of props.turn.parts) {
    if (part.thought) {
      if (!agent.value || !part.text) continue
      const last = list.at(-1)
      if (last?.kind === 'thought') last.text += part.text
      else list.push({ kind: 'thought', text: part.text })
      continue
    }
    if (part.text === undefined) {
      list.push({ kind: 'part', part, index: index++ })
      continue
    }
    index++
    const last = list.at(-1)
    if (last?.kind === 'text') last.end += part.text.length
    else list.push({ kind: 'text', start: offset, end: offset + part.text.length })
    offset += part.text.length
  }
  return list
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
// shownSegments 是各段与文本段当前显示的正文：生成中按逐字显示的进度截取，完成后有依据支撑时与官网一样直接显示插入引用编号的全文，key 为段的原序号
const shownSegments = computed(() => {
  const { supports = [], sources = [] } = props.turn
  const cited = !props.streaming && supports.length > 0 && sources.length > 0
  const uris = sources.map((source) => source.uri)
  const encoder = new TextEncoder()
  let startByte = 0
  const shown = segments.value.map((segment, key) => {
    if (segment.kind !== 'text') return { segment, text: '', key }
    if (!cited)
      return { segment, text: revealedAnswer.value.slice(segment.start, segment.end), key }
    const text = answerText.value.slice(segment.start, segment.end)
    const size = encoder.encode(text).length
    const own = supports
      .filter((support) => support.end > startByte && support.end <= startByte + size)
      .map((support) => ({ ...support, end: support.end - startByte }))
    startByte += size
    return { segment, text: insertCitations(text, own, uris), key }
  })
  return shown
})
// isMedia 表示该段为图片、音视频或文件，过程折叠后仍显示
function isMedia(segment: Segment): boolean {
  return (
    segment.kind === 'part' &&
    (segment.part.inlineData !== undefined || segment.part.fileData !== undefined)
  )
}
// isStep 表示该段显示为步骤行：思考、函数调用、可执行代码与执行结果、服务端工具调用与结果
function isStep(segment: Segment): boolean {
  return (
    segment.kind === 'thought' ||
    (segment.kind === 'part' &&
      (segment.part.functionCall !== undefined ||
        segment.part.executableCode !== undefined ||
        segment.part.codeExecutionResult !== undefined ||
        segment.part.toolCall !== undefined ||
        segment.part.toolResponse !== undefined))
  )
}
// processEnd 是 Agent 模式轮次中最后一个步骤行之后的段序号，之前的段为过程，之后的为回答
const processEnd = computed(() =>
  agent.value ? segments.value.map(isStep).lastIndexOf(true) + 1 : 0,
)
// processOpen 表示完成后折叠的过程已展开
const processOpen = ref(false)
// pairedResult 表示该片段与之前的调用合在同一个步骤中显示：紧跟在可执行代码后的执行结果，或同一 ID 的服务端工具调用之后的结果
function pairedResult(index: number): boolean {
  const part = answerParts.value[index]
  if (part?.toolResponse !== undefined) return toolCallIndex(part.toolResponse.id, index) !== -1
  return (
    part?.codeExecutionResult !== undefined &&
    answerParts.value[index - 1]?.executableCode !== undefined
  )
}
// toolCallIndex 返回 before 之前同一 ID 的服务端工具调用在 answerParts 中的位置，没有时为 -1
function toolCallIndex(id: string | undefined, before: number): number {
  return answerParts.value.findIndex(
    (part, index) => index < before && part.toolCall !== undefined && part.toolCall.id === id,
  )
}
// toolResponse 返回 index 处服务端工具调用之后同一 ID 的结果
function toolResponse(index: number): Part['toolResponse'] {
  const id = answerParts.value[index]!.toolCall!.id
  return answerParts.value.find(
    (part, position) =>
      position > index && part.toolResponse !== undefined && part.toolResponse.id === id,
  )?.toolResponse
}
// lastSearch 是最后一个网页搜索步骤在 answerParts 中的位置，本轮的依据来源显示在这一步
const lastSearch = computed(() =>
  answerParts.value
    .map((part) =>
      ['GOOGLE_SEARCH_WEB', 'GOOGLE_SEARCH_IMAGE'].includes(part.toolCall?.toolType ?? ''),
    )
    .lastIndexOf(true),
)
// stepImages 返回 index 处代码运行步骤输出的图片：执行结果之后连续的内联图片
function stepImages(index: number): NonNullable<Part['inlineData']>[] {
  const images: NonNullable<Part['inlineData']>[] = []
  let position = answerParts.value[index]?.codeExecutionResult === undefined ? index + 2 : index + 1
  if (answerParts.value[position - 1]?.codeExecutionResult === undefined) return images
  while (answerParts.value[position]?.inlineData?.mimeType.startsWith('image/')) {
    images.push(answerParts.value[position]!.inlineData!)
    position++
  }
  return images
}
// stepImage 表示 index 处的图片是代码运行步骤的输出：之前连续的图片之前是执行结果
function stepImage(index: number): boolean {
  let position = index
  while (answerParts.value[position]?.inlineData?.mimeType.startsWith('image/')) position--
  return position !== index && answerParts.value[position]?.codeExecutionResult !== undefined
}
// folded 表示 Agent 模式轮次完成后过程已折叠，代码运行生成的图片显示在过程标题之后
const folded = computed(() => turnProcess.value?.foldable === true && !processOpen.value)
// StepKind 是步骤组计数的步骤类别，对应 dsh 过程分组的运行代码、搜索网页、访问网页与调用工具
type StepKind = 'code' | 'search' | 'fetch' | 'tools'
// stepKinds 是各步骤类别的组标题文案、接在后面时的文案与图标
const stepKinds: Record<StepKind, { label: TranslationKey; next: TranslationKey; icon: IconName }> =
  {
    code: { label: 'pg.groupCode', next: 'pg.groupCodeNext', icon: 'code' },
    search: { label: 'pg.groupSearch', next: 'pg.groupSearchNext', icon: 'globe' },
    fetch: { label: 'pg.groupFetch', next: 'pg.groupFetchNext', icon: 'document' },
    tools: { label: 'pg.groupTools', next: 'pg.groupToolsNext', icon: 'functionCalling' },
  }
// stepKind 返回段计入的步骤类别：每次代码运行、服务端工具调用与函数调用各计一次，思考与已配对的结果不计
function stepKind(segment: Segment): StepKind | undefined {
  if (segment.kind !== 'part' || pairedResult(segment.index)) return undefined
  const { part } = segment
  if (part.executableCode || part.codeExecutionResult) return 'code'
  if (part.functionCall) return 'tools'
  const step = part.toolCall ?? part.toolResponse
  if (step === undefined) return undefined
  if (['GOOGLE_SEARCH_WEB', 'GOOGLE_SEARCH_IMAGE'].includes(step.toolType ?? '')) return 'search'
  return step.toolType === 'URL_CONTEXT' ? 'fetch' : 'tools'
}
// groupHeader 与 dsh processTitle 一样取次数最多的前三类步骤组成组标题，次数相同时按首次出现排序，图标取第一类；只有思考时为分析完成
function groupHeader(members: Segment[]): { title: string; icon: IconName } {
  const counts = new Map<StepKind, number>()
  for (const segment of members) {
    const kind = stepKind(segment)
    if (kind) counts.set(kind, (counts.get(kind) ?? 0) + 1)
  }
  const ranked = [...counts].sort((a, b) => b[1] - a[1]).map(([kind]) => stepKinds[kind])
  const [first, second, third] = ranked
  if (first === undefined) return { title: t('pg.groupThinking'), icon: 'spark' }
  const lead = t(first.label)
  if (second === undefined) return { title: lead, icon: first.icon }
  if (third === undefined) {
    const prefix = t('pg.groupPrefix')
    const next = t(second.next)
    const shared = prefix !== '' && lead.startsWith(prefix) && t(second.label).startsWith(prefix)
    const title = t('pg.groupJoinTwo')
      .replace('{first}', lead)
      .replace('{second}', shared ? next.slice(prefix.length) : next)
    return { title, icon: first.icon }
  }
  const title = [lead, t(second.next), t(third.next)].join(t('pg.groupComma'))
  return {
    title: ranked.length > 3 ? t('pg.groupMore').replace('{title}', title) : title,
    icon: first.icon,
  }
}
// StepBlock 是依次显示的一块段：group 为步骤组的标题与图标，没有时各段直接显示
interface StepBlock {
  key: number
  group?: { title: string; icon: IconName }
  items: (typeof shownSegments.value)[number][]
}
// blocks 与 dsh 过程分组一样把已完成轮次过程中连续的思考与工具步骤合为步骤组，正文、媒体与过程结尾打断分组；生成中与等待函数响应时不分组
const blocks = computed(() => {
  const grouping = turnProcess.value !== undefined && !awaitingResponse.value
  const list: StepBlock[] = []
  for (const item of shownSegments.value) {
    const { segment } = item
    const member =
      grouping &&
      item.key < processEnd.value &&
      (isStep(segment) ||
        (segment.kind === 'part' && (pairedResult(segment.index) || stepImage(segment.index))))
    const last = list.at(-1)
    if (
      last !== undefined &&
      (last.group !== undefined) === member &&
      last.key < processEnd.value === item.key < processEnd.value
    )
      last.items.push(item)
    else list.push({ key: item.key, items: [item] })
    if (member) list.at(-1)!.group = { title: '', icon: 'spark' }
  }
  for (const block of list)
    if (block.group) block.group = groupHeader(block.items.map((item) => item.segment))
  return list
})
// StepSection 是依次显示的一段步骤块：fold 表示它是完成后可折叠的过程
interface StepSection {
  key: string
  fold: boolean
  blocks: StepBlock[]
}
// sections 把可折叠轮次的步骤块分为折叠区中的过程、折叠后显示在过程标题之后的过程媒体与回答，其余轮次只有一段
const sections = computed((): StepSection[] => {
  if (turnProcess.value?.foldable !== true)
    return [{ key: 'answer', fold: false, blocks: blocks.value }]
  const process = blocks.value.filter((block) => block.key < processEnd.value)
  const media = folded.value
    ? process.flatMap((block) => block.items).filter((item) => isMedia(item.segment))
    : []
  return [
    { key: 'process', fold: true, blocks: process },
    {
      key: 'media',
      fold: false,
      blocks: media.length ? [{ key: media[0]!.key, items: media }] : [],
    },
    {
      key: 'answer',
      fold: false,
      blocks: blocks.value.filter((block) => block.key >= processEnd.value),
    },
  ]
})
// Bare 原样渲染插槽内容，是不折叠的段的容器
const Bare: FunctionalComponent = (_, { slots }) => slots.default?.()
// openGroups 是已展开的步骤组，以组内第一段的序号标识
const openGroups = ref<number[]>([])
// toggleGroup 展开或收起步骤组
function toggleGroup(key: number): void {
  openGroups.value = openGroups.value.includes(key)
    ? openGroups.value.filter((item) => item !== key)
    : [...openGroups.value, key]
}
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
// roleLabel 是轮次标签：用户轮次为 User，模型轮次与官网一样所选模型为 agent 时为 Agent
const roleLabel = computed(() => {
  if (props.turn.role === 'user') return t('pg.user')
  return isAgent(playground.currentModel.value) ? t('pg.agent') : t('pg.model')
})
// hasThoughtSegment 表示模型轮次单独显示思考段
const hasThoughtSegment = computed(
  () => props.turn.role === 'model' && !agent.value && thoughtText.value !== '',
)
// hasStickyCode 表示回复段含有块级代码标题，供操作栏避让粘性标题而无需全树匹配 :has(.ms-code-block)
const hasStickyCode = computed(() =>
  answerParts.value.some(
    (part) =>
      part.functionCall !== undefined ||
      part.executableCode !== undefined ||
      part.codeExecutionResult !== undefined ||
      (part.text !== undefined && (part.text.includes('```') || part.text.includes('~~~'))),
  ),
)
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
// now 是生成中每 100 ms 更新的时刻
const now = useRunningClock(() => props.streaming)
// elapsed 是生成中从提交起的耗时
const elapsed = computed(() =>
  props.streaming ? Math.round(now.value - session.started) : undefined,
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
// turnProcess 是 Agent 模式轮次完成后过程标题的文字与能否折叠：出错为失败、停止为已停止，二者与等待函数响应时不折叠，否则为完成耗时
const turnProcess = computed(() => {
  if (processEnd.value === 0 || props.streaming) return undefined
  if (props.turn.error !== undefined) return { label: t('pg.processFailed'), foldable: false }
  if (props.turn.finishReason === undefined)
    return { label: t('pg.processStopped'), foldable: false }
  return {
    label: t('pg.processDone').replace('{duration}', runDuration(props.turn.duration ?? 0)),
    foldable: !awaitingResponse.value,
  }
})
// runDuration 与 dsh 过程标题一样把耗时写为整秒的时、分、秒，不足一秒记为一秒
function runDuration(ms: number): string {
  const total = Math.floor(Math.max(1000, ms) / 1000)
  const hours = Math.floor(total / 3600)
  let text = hours > 0 ? `${hours}${t('pg.hourUnit')}` : ''
  if (total >= 60) text += `${Math.floor(total / 60) % 60}${t('pg.minuteUnit')}`
  return `${text}${total % 60}${t('pg.secondUnit')}`
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

// startEdit 进入编辑：文本变为可编辑文本框
async function startEdit(): Promise<void> {
  if (editing.value) {
    finishEdit()
    return
  }
  draft.value = plainText()
  editing.value = true
  await nextTick()
  editor.value!.focus()
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
  const element = editor.value!
  element.style.height = 'auto'
  element.style.height = `${element.scrollHeight}px`
}

// plainText 返回本轮正文的 Markdown 原文
// renderedText 返回模型轮次各文本段渲染后的文字，按显示顺序以空行连接
function renderedText(): string {
  return [...(root.value?.querySelectorAll<HTMLElement>('.answer-text') ?? [])]
    .map((element) => element.innerText)
    .join('\n\n')
}

function plainText(): string {
  return props.turn.role === 'user'
    ? (props.turn.parts.find((part) => part.text)?.text ?? '')
    : answerParts.value
        .filter((part) => part.text !== undefined)
        .map((part) => part.text)
        .join('')
}

// branch 从这一轮创建分支并打开
async function branch(): Promise<void> {
  try {
    const id = await playground.branch(props.index)
    void router.push(`/prompts/${id}`)
  } catch (error) {
    showNotice(errorText(error), 'error')
  }
}

let intersection: IntersectionObserver
const resize = new ResizeObserver(([entry]) => {
  if (visible.value) height.value = entry!.contentRect.height
})

// observe 观察轮次是否在视口附近与它的高度
function observe(): void {
  intersection.observe(root.value!)
  resize.observe(root.value!)
}

// disconnect 停止观察轮次
function disconnect(): void {
  intersection.disconnect()
  resize.disconnect()
}

onMounted(() => {
  intersection = new IntersectionObserver(
    (entries) => (visible.value = entries.at(-1)!.isIntersecting),
    { root: root.value!.closest('.chat-scroller'), rootMargin: '1500px 0px' },
  )
  observe()
})
// preview 是用户轮次附件的预览对话框：点击图片、视频、音频、文本与 PDF 附件打开
const { preview, show: showAttachment, release: releasePreview } = useAttachmentPreview()
onUnmounted(disconnect)
onUnmounted(releasePreview)
onDeactivated(disconnect)
onActivated(observe)
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
      <div v-if="hasThoughtSegment" tabindex="-1" class="chat-turn-container model">
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
        :class="[turn.role, { edit: editing, 'has-code': hasStickyCode }]"
      >
        <div class="actions-container">
          <div class="actions hover-or-edit">
            <button
              v-if="turn.error === undefined"
              v-tooltip.below="editing ? t('pg.stopEditing') : t('pg.edit')"
              type="button"
              ms-button
              class="toggle-edit-button ms-button-borderless ms-button-icon"
              :style="{ visibility: waiting ? 'hidden' : undefined }"
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
                  <DropdownMenuItem
                    class="ms-menu-item"
                    @select="copyToClipboard(renderedText() || plainText())"
                  >
                    <UiIcon name="copy" :size="18" />
                    <span class="ms-menu-item-text">{{ t('pg.copyText') }}</span>
                  </DropdownMenuItem>
                  <DropdownMenuItem class="ms-menu-item" @select="copyToClipboard(plainText())">
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
          <button
            v-if="turnProcess"
            type="button"
            class="process-header"
            :disabled="!turnProcess.foldable"
            :aria-expanded="turnProcess.foldable ? processOpen : undefined"
            @click="processOpen = !processOpen"
          >
            <span class="process-label">{{ turnProcess.label }}</span>
            <DisclosureChevron
              v-if="turnProcess.foldable"
              :open="processOpen"
              :turn="180"
              class="process-chevron"
              ><UiIcon name="chevronDown" :size="14"
            /></DisclosureChevron>
          </button>
          <template v-for="section in sections" :key="section.key">
            <component :is="section.fold ? UiCollapse : Bare" :open="!folded">
              <template v-for="block in section.blocks" :key="block.key">
                <button
                  v-if="block.group"
                  type="button"
                  class="step-group"
                  :aria-expanded="openGroups.includes(block.key)"
                  @click="toggleGroup(block.key)"
                >
                  <span class="group-leading" aria-hidden="true">
                    <UiIcon :name="block.group.icon" :size="14" class="group-icon" />
                    <span class="group-chevron"
                      ><DisclosureChevron :open="openGroups.includes(block.key)" :turn="180"
                        ><UiIcon name="chevronDown" :size="16" /></DisclosureChevron
                    ></span>
                  </span>
                  <span class="group-label">{{ block.group.title }}</span>
                </button>
                <SegmentRun
                  v-slot="{ segment, text, key }"
                  :items="block.items"
                  :wrap="block.group !== undefined"
                  :open="openGroups.includes(block.key)"
                >
                  <div
                    v-if="segment.kind === 'text' && turn.role === 'model' && !editing && text"
                    class="answer-text"
                  >
                    <MarkdownView :text="text" :raw="session.rawMode" />
                  </div>
                  <div v-else-if="segment.kind === 'thought'" class="step">
                    <ThoughtStep
                      :text="segment.text"
                      :running="streaming && key === segments.length - 1"
                      :raw="session.rawMode"
                    />
                  </div>
                  <div
                    v-else-if="
                      segment.kind === 'part' &&
                      !pairedResult(segment.index) &&
                      !(stepImage(segment.index) && !folded)
                    "
                    :class="{ step: isStep(segment) }"
                  >
                    <template
                      v-if="
                        turn.role === 'user' && (segment.part.inlineData || segment.part.fileData)
                      "
                    >
                      <button
                        v-if="segment.part.inlineData?.mimeType.startsWith('image/')"
                        type="button"
                        class="image-chunk"
                        :class="{ 'chunk-gap': segment.index < answerParts.length - 1 }"
                        :aria-label="t('pg.preview')"
                        @click="showAttachment(segment.part)"
                      >
                        <img
                          class="loaded-image"
                          :src="dataURL(segment.part.inlineData)"
                          :alt="attachmentName(segment.part)"
                        />
                      </button>
                      <FileChunk
                        v-else
                        :part="segment.part"
                        :class="{ 'chunk-gap': segment.index < answerParts.length - 1 }"
                        @preview="showAttachment(segment.part)"
                      />
                    </template>
                    <template v-else-if="segment.part.inlineData || segment.part.fileData">
                      <img
                        v-if="segment.part.inlineData?.mimeType.startsWith('image/')"
                        class="media-image"
                        :src="dataURL(segment.part.inlineData)"
                        alt=""
                      />
                      <audio
                        v-else-if="segment.part.inlineData?.mimeType.startsWith('audio/')"
                        controls
                        :src="dataURL(segment.part.inlineData)"
                      ></audio>
                      <VideoResult
                        v-else-if="segment.part.inlineData?.mimeType.startsWith('video/')"
                        :src="dataURL(segment.part.inlineData)"
                      />
                      <a
                        v-else-if="
                          segment.part.fileData && !youtubeVideoID(segment.part.fileData.fileUri)
                        "
                        class="file-chip"
                        :href="segment.part.fileData.fileUri"
                        target="_blank"
                        rel="noreferrer"
                      >
                        <UiIcon
                          :name="
                            driveNames.has(segment.part.fileData.fileUri) ? 'addToDrive' : 'youtube'
                          "
                          :size="18"
                        />{{
                          driveNames.get(segment.part.fileData.fileUri) ??
                          segment.part.fileData.fileUri
                        }}
                      </a>
                      <FileChunk
                        v-else
                        :part="segment.part"
                        :class="{ 'chunk-gap': segment.index < answerParts.length - 1 }"
                      />
                    </template>
                    <FunctionCallChunk
                      v-else-if="segment.part.functionCall"
                      :call="segment.part.functionCall"
                      :response="responseText(segment.part.functionCall)"
                      :awaiting="awaitingResponse"
                      @respond="playground.respond(segment.part.functionCall, $event)"
                    />
                    <CodeExecutionChunk
                      v-else-if="segment.part.executableCode || segment.part.codeExecutionResult"
                      :code="segment.part.executableCode"
                      :result="
                        segment.part.codeExecutionResult ??
                        answerParts[segment.index + 1]?.codeExecutionResult
                      "
                      :images="stepImages(segment.index)"
                      :running="streaming && segment.index === answerParts.length - 1"
                    />
                    <ServerToolChunk
                      v-else-if="segment.part.toolCall"
                      :call="segment.part.toolCall"
                      :response="toolResponse(segment.index)"
                      :sources="segment.index === lastSearch && !streaming ? sources : []"
                      :running="streaming"
                    />
                    <ServerToolChunk
                      v-else-if="segment.part.toolResponse"
                      :call="segment.part.toolResponse"
                      :response="segment.part.toolResponse"
                      :sources="[]"
                      :running="false"
                    />
                  </div>
                </SegmentRun>
              </template>
            </component>
          </template>
          <div v-if="turn.role === 'user' && !editing" class="user-chunk">
            <MarkdownView :text="plainText()" :raw="session.rawMode" />
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
            <span>{{ logText(turn.error) }}</span>
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
    <MediaViewDialog
      v-if="turn.role === 'user'"
      v-model:open="preview.open"
      :title="preview.part ? attachmentName(preview.part) : ''"
      :kind="preview.kind"
      :src="preview.src"
      :text="preview.text"
      @download="preview.part && downloadAttachment(preview.part)"
    />
  </div>
</template>

<style scoped>
.chat-turn {
  display: flex;
  flex-direction: column;
  width: 100%;
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
.chat-turn-container:not(.edit):has(> .actions-container [aria-expanded='true']) {
  border-color: var(--color-v3-outline);
}

@media (hover: hover), (pointer: none) {
  .chat-turn-container:hover:not(.edit) {
    border-color: var(--color-v3-outline);
  }

  .chat-turn-container:hover > .actions-container .hover-or-edit,
  .chat-turn-container:hover > .turn-footer .hover-or-edit {
    opacity: 1;
    pointer-events: auto;
  }

  .chat-turn-container:hover > .turn-footer {
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

.chat-turn-container.has-code > .actions-container {
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

.chat-turn-container.model.has-code > .actions-container .actions {
  transform: translateX(-8px);
}

.chat-turn-container.user.has-code > .actions-container .actions {
  transform: translateX(-20px);
}

.hover-or-edit {
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.3s ease-in-out;
}

.chat-turn-container:focus-within > .actions-container .hover-or-edit,
.chat-turn-container:focus-within > .turn-footer .hover-or-edit,
.chat-turn-container:has(> .actions-container [aria-expanded='true'])
  > .actions-container
  .hover-or-edit,
.chat-turn-container:has(> .actions-container [aria-expanded='true']) > .turn-footer .hover-or-edit,
.chat-turn-container.edit > .actions-container .hover-or-edit,
.chat-turn-container.edit > .turn-footer .hover-or-edit {
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

/* step 为步骤行所在的块：相邻步骤间隔 4 px，与正文间隔 12 px，取 dsh 对话流 */
.step + .step {
  margin-top: 4px;
}

.step + .answer-text,
.answer-text + .step {
  margin-top: 12px;
}

/* process-header 为 Agent 模式轮次完成后的过程标题：高度、下边框、字号与箭头取 dsh TurnProcessNodeView，颜色取 AI Studio 主题 */
.process-header {
  display: flex;
  align-items: center;
  box-sizing: border-box;
  width: 100%;
  min-width: 0;
  height: 33px;
  padding: 0 0 8px;
  border: none;
  border-bottom: 0.5px solid var(--color-v3-outline-var);
  background: none;
  color: var(--color-v3-text-var);
  cursor: pointer;
  font: inherit;
  font-size: 14px;
  text-align: left;
  transition: color 100ms;
}

.process-header:disabled {
  cursor: default;
}

.process-header:hover:not(:disabled) {
  color: var(--color-v3-text);
}

.process-header + * {
  margin-top: 12px;
}

.process-label {
  min-width: 0;
  overflow: hidden;
  font-variant-numeric: tabular-nums;
  line-height: 24px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.process-chevron {
  margin-left: 4px;
}

/* step-group 为展开过程中的步骤组标题：图标与箭头叠放、悬停或展开时显示箭头，字号与间距取 dsh ChatGroupSeat，颜色取 AI Studio 主题 */
.step-group {
  display: flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  padding: 0;
  border: none;
  background: none;
  color: var(--color-v3-text-var);
  cursor: pointer;
  font: inherit;
  font-size: 14px;
  line-height: 24px;
  text-align: left;
  transition: color 100ms;
}

.step-group:hover,
.step-group:focus-visible {
  color: var(--color-v3-text);
}

.group-leading {
  position: relative;
  display: inline-flex;
  flex: none;
  width: 16px;
  height: 16px;
}

.group-icon {
  position: absolute;
  inset: 0;
  margin: auto;
  transition: opacity 100ms;
}

.group-chevron {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: opacity 100ms;
}

.group-chevron,
.step-group:is(:hover, :focus-visible, [aria-expanded='true']) .group-icon {
  opacity: 0;
}

.step-group:is(:hover, :focus-visible, [aria-expanded='true']) .group-chevron {
  opacity: 1;
}

.group-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.answer-text + .step-group,
.step-group + .answer-text,
.ui-collapse + .answer-text {
  margin-top: 12px;
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

.media-image {
  max-width: 100%;
  max-height: min(30vh, 358px);
  object-fit: cover;
  margin: 4px 0 8px;
  border-radius: 8px;
}

/* image-chunk 与官网 ms-image-chunk 一致：带 1 px 边框与 4 px 圆角的图片，点击打开预览 */
.image-chunk {
  display: inline-block;
  max-width: 100%;
  padding: 0;
  overflow: clip;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 4px;
  background: none;
  vertical-align: top;
}

.loaded-image {
  display: block;
  max-width: 100%;
  max-height: min(30vh, 358px);
  cursor: pointer;
  object-fit: cover;
}

/* chunk-gap 为官网相邻两个用户轮次之间的间距：下内边距 24 px、上外边距 8 px、上内边距 8 px 与两条 1 px 边框 */
.chunk-gap {
  margin-bottom: 42px;
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
