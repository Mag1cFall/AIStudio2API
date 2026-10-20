<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, nextTick, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import type { IconName } from '@/playground/ui/icons'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSwitch from '@/playground/ui/UiSwitch.vue'
import UiDialog from '@/playground/ui/UiDialog.vue'
import CodeDialog from '../CodeDialog.vue'
import MarkdownView from '../MarkdownView.vue'
import ThoughtChunk from '../ThoughtChunk.vue'
import { hasCode } from '../catalog'
import LiveSettings from './LiveSettings.vue'
import MediaLayout from './MediaLayout.vue'
import MediaPromptBox from './MediaPromptBox.vue'
import ModelEmptyState, { type Example, type Starter } from './ModelEmptyState.vue'
import { pageKindOf } from './kinds'
import { useMediaQuery } from '../media-query'
import { liveCode, liveCodeOptions, type LiveCodeLanguage } from './live-code'
import {
  inputParagraphs,
  outputParagraphs,
  useLive,
  type LivePart,
  type LiveSource,
  type LiveTurn,
} from './live'

const route = useRoute()
const { t } = useI18n()
const store = useConsole()
const live = useLive()
const { state, currentModel, variant } = live
const codeOpen = ref(false)
const scroller = ref<HTMLElement>()
const preview = ref<HTMLVideoElement>()
const panels = ref<HTMLElement>()
const toolsOpen = ref(false)

// zeroConfig 是官网 gemini-live 空态：对话类 Gemini Live 模型显示示例，翻译与转录模型显示 Try the Live API
const zeroConfig = {
  description:
    'Low-latency bidirectional voice, camera, and screen-sharing conversations with tool use.',
  starters: [
    {
      title: 'Practice a mock system-design interview',
      prompt:
        'Act as a staff engineer conducting a 15-minute system design interview. Ask me to design a distributed rate limiter and probe my tradeoffs.',
    },
    {
      title: 'Brainstorm architecture while I whiteboard',
      prompt:
        'Watch my screen as I sketch a service architecture diagram and suggest failure modes, caching layers, and schema improvements in real time.',
    },
    {
      title: 'Practice ordering in conversational Japanese',
      prompt:
        'Play a friendly izakaya chef in Tokyo. Speak natural polite Japanese, ask one question at a time, and gently recaps any grammar slips.',
    },
  ] satisfies Starter[],
  examples: [
    {
      title: 'Co-Drawing Canvas',
      prompt:
        'Collaborate with me on a shared sketch canvas in real time—watch what I draw and take turns adding details to the scene.',
      preview: 'https://www.gstatic.com/aistudio/starter-apps/thumbnails/codrawing.png',
    },
    {
      title: 'Screen-Share Tutor',
      prompt:
        'Look at my screen with me. Walk me through what this interface shows and answer my questions step by step.',
      preview: 'https://www.gstatic.com/aistudio/zero-state/live_screen_share_tutor.webp',
    },
    {
      title: 'Live Hardware Debug',
      prompt:
        'Look at this breadboard and microcontroller wiring through my camera and help me verify the pin connections and resistor values.',
      preview: 'https://www.gstatic.com/aistudio/zero-state/live_hardware_debug.webp',
    },
  ] satisfies Example[],
}

// liveModels 是目录中的 Live 模型，顺序沿用目录
const liveModels = computed(() =>
  store.models.value.filter((model) => pageKindOf(model) === 'live'),
)
// conversation 表示当前模型是对话类 Live 模型
const conversation = computed(() => variant.value === 'conversation')
// zero 表示会话未开始且页面没有内容
const zero = computed(
  () =>
    state.status === 'idle' &&
    !state.interim &&
    (conversation.value
      ? state.turns.length === 0
      : state.input.length === 0 && state.output.length === 0),
)
// geminiConfig 表示使用官网 gemini-live 空态：Gemini 对话类 Live 模型
const geminiConfig = computed(
  () => conversation.value && (currentModel.value?.id.startsWith('gemini-') ?? false),
)
// connected 表示会话连接中或进行中
const connected = computed(() => state.status === 'connecting' || state.status === 'live')
// can 返回模型能否使用一种能力：66 麦克风、67 摄像头、68 屏幕、78 转写中间结果
const can = (code: number) => hasCode(currentModel.value, code)
// inputs 与 outputs 是翻译与转录面板的段落
const inputs = computed(inputParagraphs)
const outputs = computed(outputParagraphs)
// chips 是 Try the Live API 下方的入口按钮，按模型能力列出
const chips = computed(() => {
  const items: { source: LiveSource; icon: IconName; label: string }[] = []
  if (can(66)) items.push({ source: 'mic', icon: 'mic', label: t('pg.talk') })
  if (conversation.value && can(67))
    items.push({ source: 'camera', icon: 'webcam', label: t('pg.webcam') })
  if (can(68))
    items.push({
      source: conversation.value ? 'screen' : 'tab',
      icon: 'screenShare',
      label: conversation.value ? t('pg.shareScreen') : t('pg.shareAudioFromTab'),
    })
  return items
})
// streamButtons 是输入框右侧的实时输入按钮：对话模型为麦克风、摄像头与屏幕，翻译与转录模型为麦克风与标签页音频
const streamButtons = computed(() => {
  const items: { source: LiveSource; icon: IconName; label: string }[] = []
  if (can(66)) items.push({ source: 'mic', icon: 'mic', label: t('pg.microphone') })
  if (conversation.value && can(67))
    items.push({ source: 'camera', icon: 'webcam', label: t('pg.camera') })
  if (can(68))
    items.push({
      source: conversation.value ? 'screen' : 'tab',
      icon: 'screenShare',
      label: t('pg.screen'),
    })
  return items
})
// tools 是 Tools 菜单中的 Live 工具
const tools = computed(() => [
  {
    id: 'functionCalling' as const,
    label: t('pg.functionCalling'),
    icon: 'functionCalling' as IconName,
  },
  { id: 'googleSearch' as const, label: t('pg.googleSearch'), icon: 'search' as IconName },
])
// enabledTools 是已开启并显示为标签的工具
const enabledTools = computed(() => tools.value.filter((tool) => state.settings[tool.id]))
// handset 与官网一致，768 px 及以下 Tools 只显示图标，已启用的工具列在提示中
const handset = useMediaQuery('(max-width: 768px)')
// statusText 是会话状态行的文字
const statusText = computed(() => {
  if (state.status === 'connecting') return t('pg.streamConnecting')
  if (state.status === 'live') return t('pg.streamLive')
  return state.error || t('pg.streamStart')
})
// canRun 表示输入框有文字且会话不在连接中
const canRun = computed(() => state.draft.trim() !== '' && state.status !== 'connecting')

// sourceTip 返回实时输入按钮的提示
function sourceTip(source: LiveSource): string {
  const active = state.sources.includes(source)
  if (source === 'mic')
    return state.sources.includes('tab') ? t('pg.stopTabForMic') : t('pg.microphone')
  if (source === 'camera') return active ? t('pg.stopCamera') : t('pg.startCamera')
  if (source === 'tab') return active ? t('pg.stopTabAudio') : t('pg.shareAudioFromTab')
  return active ? t('pg.stopScreen') : t('pg.startScreen')
}

// submit 发送输入框中的文本
function submit(): void {
  if (live.requireDisclaimer()) return
  const text = state.draft
  state.draft = ''
  void live.sendText(text)
}

// setTool 开关一个工具，会话进行中保持不变
function setTool(id: 'functionCalling' | 'googleSearch', enabled: boolean): void {
  if (!connected.value) state.settings[id] = enabled
}

// separated 表示该轮前显示分隔线：第一轮之后的用户轮次
function separated(turn: LiveTurn, index: number): boolean {
  return turn.role === 'user' && index > 0
}

// streamingPart 表示该片段是正在生成的最后一个片段
function streamingPart(turn: LiveTurn, index: number): boolean {
  return state.status === 'live' && turn === state.turns.at(-1) && index === turn.parts.length - 1
}

// copyResponse 复制函数调用的响应
async function copyResponse(part: Extract<LivePart, { type: 'call' }>): Promise<void> {
  try {
    await navigator.clipboard.writeText(part.response)
    store.showNotice(t('pg.copied'), 'success')
  } catch (error) {
    store.showNotice(error instanceof Error ? error.message : String(error), 'error')
  }
}

// responseTip 是发送响应按钮的提示
function responseTip(part: Extract<LivePart, { type: 'call' }>): string {
  if (part.cancelled) return t('pg.callCancelled')
  if (part.response.trim() === '') return t('pg.responseInvalid')
  return t('pg.sendResponse')
}

// code 生成 Live 示例代码
function code(language: LiveCodeLanguage): string {
  const { session_token: _token, ...setup } = live.setupFrame()
  return liveCode(language, { baseURL: window.location.origin, setup, prompt: state.draft })
}

// videoBox 是视频浮窗相对默认位置的偏移与拖动缩放后的宽度，width 为 0 时使用默认宽度
const videoBox = reactive({ x: 0, y: 0, width: 0 })
// VideoDrag 是一次拖动或缩放：handle 为 move 或四角之一
interface VideoDrag {
  handle: 'move' | 'tl' | 'tr' | 'bl' | 'br'
  pointerX: number
  pointerY: number
  x: number
  y: number
  width: number
  ratio: number
}
let videoDrag: VideoDrag | undefined

// startVideoDrag 开始拖动或缩放视频浮窗
function startVideoDrag(event: PointerEvent, handle: VideoDrag['handle']): void {
  const target = event.currentTarget as HTMLElement
  const container = target.closest<HTMLElement>('.video-container')
  if (container === null) return
  target.setPointerCapture(event.pointerId)
  videoDrag = {
    handle,
    pointerX: event.clientX,
    pointerY: event.clientY,
    x: videoBox.x,
    y: videoBox.y,
    width: container.offsetWidth,
    ratio: container.offsetHeight / container.offsetWidth,
  }
}

// moveVideoDrag 按指针位移移动浮窗，四角缩放时保持视频比例
function moveVideoDrag(event: PointerEvent): void {
  if (videoDrag === undefined) return
  const dx = event.clientX - videoDrag.pointerX
  const dy = event.clientY - videoDrag.pointerY
  if (videoDrag.handle === 'move') {
    videoBox.x = videoDrag.x + dx
    videoBox.y = videoDrag.y + dy
    return
  }
  const right = videoDrag.handle.endsWith('r')
  const width = Math.max(160, videoDrag.width + (right ? dx : -dx))
  const grown = width - videoDrag.width
  videoBox.width = width
  videoBox.x = videoDrag.x + (right ? grown : 0)
  videoBox.y = videoDrag.y + (videoDrag.handle.startsWith('b') ? grown * videoDrag.ratio : 0)
}

// endVideoDrag 结束拖动或缩放
function endVideoDrag(): void {
  videoDrag = undefined
}

watch(
  [() => route.query.model, liveModels],
  () => {
    const requested = typeof route.query.model === 'string' ? route.query.model : ''
    const target = liveModels.value.find((model) => model.id === requested) ?? liveModels.value[0]
    if (target && target.id !== state.model && (requested !== '' || state.model === ''))
      live.selectModel(target.id)
  },
  { immediate: true },
)
watch(
  () => state.preview,
  (stream) => {
    if (stream === undefined) Object.assign(videoBox, { x: 0, y: 0, width: 0 })
    void nextTick(() => {
      if (preview.value) preview.value.srcObject = stream ?? null
    })
  },
)
watch(
  () => [state.turns.length, state.turns.at(-1)?.parts.length, state.interim],
  () => {
    const element = scroller.value
    if (element === undefined) return
    const atBottom = element.scrollHeight - element.scrollTop - element.clientHeight < 80
    if (atBottom) void nextTick(() => element.scrollTo({ top: element.scrollHeight }))
  },
)
watch([inputs, outputs, () => state.interim], () =>
  nextTick(() => {
    for (const element of panels.value?.querySelectorAll<HTMLElement>('.transcript-content') ?? [])
      element.scrollTop = element.scrollHeight
  }),
)

onUnmounted(() => live.disconnect())
</script>

<template>
  <MediaLayout>
    <template #actions>
      <button
        v-if="!zero"
        v-tooltip.below="t('pg.newSession')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.newSession')"
        @click="live.newSession()"
      >
        <UiIcon name="add" :size="20" />
      </button>
    </template>

    <div class="bidi-chat">
      <div v-if="zero" class="title-cards-and-footer zero-state">
        <div v-if="geminiConfig" class="empty-state">
          <ModelEmptyState
            kind="live"
            :title="t('pg.liveTalkTitle').replace('{model}', currentModel?.name ?? 'Gemini Live')"
            :description="zeroConfig.description"
            :starters="zeroConfig.starters"
            :examples="zeroConfig.examples"
            @starter="live.sendText($event.prompt)"
            @example="live.sendText($event.prompt)"
          />
        </div>
        <template v-else>
          <div class="title-container">
            <h1 class="hero-text">{{ t('pg.tryLiveAPI') }}</h1>
          </div>
          <div class="chips-container">
            <button
              v-for="chip in chips"
              :key="chip.source"
              type="button"
              ms-button
              class="ms-button-large ms-button-primary"
              :aria-label="chip.label"
              @click="live.toggleSource(chip.source)"
            >
              <UiIcon :name="chip.icon" :size="20" />
              {{ chip.label }}
            </button>
          </div>
        </template>
      </div>

      <div v-else-if="conversation" ref="scroller" class="autoscroll">
        <div class="chat-turns-wrapper">
          <div v-for="(turn, turnIndex) in state.turns" :key="turn.id" class="bidi-turn">
            <div v-if="separated(turn, turnIndex)" class="turn-separator"></div>
            <div class="author-label">
              {{ turn.role === 'user' ? t('pg.user') : t('pg.model') }}
            </div>
            <div class="turn" :class="turn.role">
              <div class="content">
                <template v-for="(part, index) in turn.parts" :key="index">
                  <div v-if="part.type === 'text'" class="text-part">
                    <MarkdownView :text="part.text" />
                  </div>
                  <ThoughtChunk
                    v-else-if="part.type === 'thought'"
                    class="thought-part"
                    :text="part.text"
                    :streaming="streamingPart(turn, index)"
                  />
                  <div v-else-if="part.type === 'audio'" class="audio-part">
                    <audio v-if="part.url" :src="part.url" controls></audio>
                    <div v-else class="streaming-output">
                      <div class="sound-wave"></div>
                    </div>
                    <div v-if="part.transcript" class="text-part">
                      <MarkdownView :text="part.transcript" />
                    </div>
                  </div>
                  <div v-else class="call-part">
                    <div class="tool-block">
                      <div class="tool-title">
                        <UiIcon name="functionCalling" :size="18" />{{ part.name }}
                      </div>
                      <pre>{{ JSON.stringify(part.args, null, 2) }}</pre>
                      <div
                        v-for="(text, position) in part.responses"
                        :key="position"
                        class="response"
                      >
                        {{ text }}
                      </div>
                      <form class="call-form" @submit.prevent="live.respond(part)">
                        <textarea
                          v-model="part.response"
                          class="call-input"
                          rows="3"
                          :disabled="part.sent || part.cancelled || part.generating"
                          :placeholder="
                            part.cancelled
                              ? t('pg.callCancelled')
                              : part.generating
                                ? t('pg.generatingResponse')
                                : t('pg.responsePlaceholder')
                          "
                          :aria-label="t('pg.responsePlaceholder')"
                          @keydown.enter.ctrl.prevent="
                            part.response.trim() && !part.sent && live.respond(part)
                          "
                        ></textarea>
                        <div
                          v-if="part.behavior === 'NON_BLOCKING'"
                          class="will-continue-container"
                        >
                          <label class="will-continue">
                            <UiSwitch
                              v-model="part.willContinue"
                              :disabled="part.sent || part.cancelled"
                              :aria-label="t('pg.willContinue')"
                            />
                            {{ t('pg.willContinue') }}
                          </label>
                        </div>
                        <footer class="call-footer">
                          <div class="footer-start">
                            <button
                              v-tooltip="t('pg.copyToClipboard')"
                              type="button"
                              ms-button
                              class="ms-button-borderless ms-button-icon"
                              :aria-label="t('pg.copyToClipboard')"
                              @click="copyResponse(part)"
                            >
                              <UiIcon name="copy" :size="18" />
                            </button>
                            <span v-if="part.error" class="call-error">{{ part.error }}</span>
                          </div>
                          <button
                            v-tooltip.right="responseTip(part)"
                            type="submit"
                            ms-button
                            class="ms-button-borderless ms-button-icon"
                            :aria-label="t('pg.sendResponse')"
                            :disabled="part.sent || part.cancelled || part.response.trim() === ''"
                          >
                            <UiIcon name="send" :size="18" />
                          </button>
                        </footer>
                      </form>
                    </div>
                  </div>
                </template>
              </div>
            </div>
          </div>
          <div v-if="state.interim" class="bidi-turn">
            <div v-if="state.turns.length" class="turn-separator"></div>
            <div class="author-label">{{ t('pg.user') }}</div>
            <div class="turn user">
              <div class="content">
                <p class="interim">{{ state.interim }}</p>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-else class="translation-mode-container">
        <div
          ref="panels"
          class="transcript-panels"
          :class="{ 'stt-mode': variant === 'transcription' }"
        >
          <div class="transcript-panel">
            <div class="panel-title">{{ t('pg.inputTranscript') }}</div>
            <div class="transcript-content">
              <p v-for="(paragraph, index) in inputs" :key="index" class="transcript-paragraph">
                <span v-if="paragraph.language" class="language-badge">{{
                  paragraph.language
                }}</span>
                {{ paragraph.text }}
              </p>
              <p v-if="state.interim && can(78)" class="transcript-paragraph interim-transcript">
                {{ state.interim }}
              </p>
            </div>
          </div>
          <div v-if="variant === 'translation'" class="transcript-panel">
            <div class="panel-title">{{ t('pg.outputTranscript') }}</div>
            <div class="transcript-content">
              <p v-for="(paragraph, index) in outputs" :key="index" class="transcript-paragraph">
                <span v-if="paragraph.language" class="language-badge">{{
                  paragraph.language
                }}</span>
                {{ paragraph.text }}
              </p>
            </div>
          </div>
        </div>
      </div>

      <div class="footer" :class="{ 'zero-state': zero, 'translation-mode': !conversation }">
        <div v-if="!zero" class="stream-status" role="status">
          <div class="stream-status-divider left"><hr /></div>
          <div class="stream-status-content">
            <span v-tooltip.overflow="statusText" class="stream-status-text">{{ statusText }}</span>
            <button
              v-if="connected"
              v-tooltip="t('pg.disconnect')"
              type="button"
              ms-button
              class="ms-button-borderless ms-button-icon"
              :aria-label="t('pg.disconnect')"
              @click="live.disconnect()"
            >
              <UiIcon name="close" :size="20" />
            </button>
          </div>
          <div class="stream-status-divider right"><hr /></div>
        </div>

        <div class="bidi-input" :class="{ 'translation-mode': !conversation }">
          <MediaPromptBox
            v-if="conversation"
            v-model="state.draft"
            :placeholder="t('pg.livePlaceholder')"
            :label="t('pg.enterPrompt')"
            :running="false"
            :can-run="canRun"
            @run="submit"
          >
            <template #left>
              <PopoverRoot :open="toolsOpen" @update:open="toolsOpen = $event && !connected">
                <PopoverTrigger
                  v-tooltip.below="
                    connected
                      ? t('pg.liveToolsLocked')
                      : handset
                        ? enabledTools.map((tool) => tool.label).join(', ')
                        : ''
                  "
                  ms-button
                  class="ms-button-primary tools-button"
                  :class="{
                    'ms-button-icon': handset,
                    'ms-button-active': handset && enabledTools.length,
                  }"
                  :aria-label="t('pg.openTools')"
                  :aria-disabled="connected"
                >
                  <UiIcon name="tools" :size="18" />
                  <template v-if="!handset">{{ t('pg.tools') }}</template>
                </PopoverTrigger>
                <PopoverPortal>
                  <PopoverContent
                    side="top"
                    align="start"
                    :side-offset="8"
                    class="live-tools-dialog"
                  >
                    <div v-for="tool in tools" :key="tool.id" class="tool-item">
                      <label class="tool-label" :for="`live-tool-${tool.id}`">
                        <UiIcon :name="tool.icon" :size="18" class="tool-icon" />
                        {{ tool.label }}
                      </label>
                      <UiSwitch
                        :id="`live-tool-${tool.id}`"
                        :model-value="state.settings[tool.id]"
                        @update:model-value="setTool(tool.id, $event)"
                      />
                    </div>
                  </PopoverContent>
                </PopoverPortal>
              </PopoverRoot>
              <div v-if="enabledTools.length && !handset" class="enabled-tool-container">
                <div v-for="tool in enabledTools" :key="tool.id" class="enabled-tool">
                  <span class="tool-chip">
                    <UiIcon :name="tool.icon" :size="16" class="tool-icon" />
                    <span class="tool-name">{{ tool.label }}</span>
                  </span>
                  <button
                    v-tooltip.below="
                      connected
                        ? t('pg.liveToolsLocked')
                        : t('pg.removeTool').replace('{tool}', tool.label)
                    "
                    type="button"
                    ms-button
                    class="ms-button-borderless ms-button-icon ms-button-small"
                    :aria-label="t('pg.removeTool').replace('{tool}', tool.label)"
                    :aria-disabled="connected"
                    @click="setTool(tool.id, false)"
                  >
                    <UiIcon name="close" :size="16" />
                  </button>
                </div>
              </div>
            </template>
            <template #right>
              <div class="streaming-buttons-container">
                <template v-for="button in streamButtons" :key="button.source">
                  <div v-if="button.source === 'mic'" class="audio-button-wrapper">
                    <button
                      v-tooltip.below="sourceTip('mic')"
                      type="button"
                      ms-button
                      class="ms-button-icon ms-button-primary"
                      :class="{ 'ms-button-active': state.sources.includes('mic') }"
                      :aria-label="button.label"
                      :aria-pressed="state.sources.includes('mic')"
                      @click="live.toggleSource('mic')"
                    >
                      <UiIcon :name="button.icon" :size="20" />
                    </button>
                  </div>
                  <button
                    v-else
                    v-tooltip.below="sourceTip(button.source)"
                    type="button"
                    ms-button
                    class="ms-button-icon ms-button-primary"
                    :class="{ 'ms-button-active': state.sources.includes(button.source) }"
                    :aria-label="button.label"
                    :aria-pressed="state.sources.includes(button.source)"
                    @click="live.toggleSource(button.source)"
                  >
                    <UiIcon :name="button.icon" :size="20" />
                  </button>
                </template>
              </div>
            </template>
          </MediaPromptBox>

          <div v-else class="buttons-container">
            <div class="streaming-buttons-container">
              <template v-for="button in streamButtons" :key="button.source">
                <div v-if="button.source === 'mic'" class="audio-button-wrapper">
                  <button
                    v-tooltip="sourceTip('mic')"
                    type="button"
                    ms-button
                    class="ms-button-icon ms-button-primary"
                    :class="{ 'ms-button-active': state.sources.includes('mic') }"
                    :aria-label="button.label"
                    :aria-pressed="state.sources.includes('mic')"
                    @click="live.toggleSource('mic')"
                  >
                    <UiIcon :name="button.icon" :size="20" />
                  </button>
                </div>
                <button
                  v-else
                  v-tooltip="sourceTip(button.source)"
                  type="button"
                  ms-button
                  class="ms-button-icon ms-button-primary"
                  :class="{ 'ms-button-active': state.sources.includes(button.source) }"
                  :aria-label="button.label"
                  :aria-pressed="state.sources.includes(button.source)"
                  @click="live.toggleSource(button.source)"
                >
                  <UiIcon :name="button.icon" :size="20" />
                </button>
              </template>
            </div>
          </div>

          <div
            v-if="state.preview"
            class="video-container"
            :style="{
              transform: `translate(${videoBox.x}px, ${videoBox.y}px)`,
              width: videoBox.width ? `${videoBox.width}px` : undefined,
            }"
          >
            <video
              ref="preview"
              class="video-move"
              autoplay
              muted
              playsinline
              @pointerdown.prevent="startVideoDrag($event, 'move')"
              @pointermove="moveVideoDrag"
              @pointerup="endVideoDrag"
              @pointercancel="endVideoDrag"
            ></video>
            <div
              v-for="handle in ['tl', 'tr', 'bl', 'br'] as const"
              :key="handle"
              class="resize-handle"
              :class="handle"
              @pointerdown.prevent="startVideoDrag($event, handle)"
              @pointermove="moveVideoDrag"
              @pointerup="endVideoDrag"
              @pointercancel="endVideoDrag"
            ></div>
          </div>
        </div>
      </div>
    </div>

    <template #settings="{ open, narrow, close }">
      <LiveSettings :open="open" :narrow="narrow" @close="close" @code="codeOpen = true" />
    </template>
  </MediaLayout>
  <CodeDialog v-model:open="codeOpen" :options="liveCodeOptions" :code="code" />
  <UiDialog v-model:open="state.disclaimer" :title="t('pg.liveDisclaimerTitle')" :closable="false">
    <p class="disclaimer-text">
      <template
        v-for="(part, position) in t('pg.liveDisclaimerBody').split('{terms}')"
        :key="position"
      >
        <a
          v-if="position > 0"
          href="https://ai.google.dev/gemini-api/terms"
          target="_blank"
          rel="noopener"
          >{{ t('pg.liveDisclaimerTerms') }}</a
        >{{ part }}
      </template>
    </p>
    <p class="disclaimer-text">{{ t('pg.liveDisclaimerPrivacy') }}</p>
    <template #actions>
      <button
        type="button"
        ms-button
        class="ms-button-borderless"
        @click="state.disclaimer = false"
      >
        {{ t('common.cancel') }}
      </button>
      <button type="button" ms-button class="ms-button-primary" @click="live.acknowledgeDisclaimer">
        {{ t('pg.acknowledge') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.disclaimer-text + .disclaimer-text {
  margin-top: 16px;
}

.bidi-chat {
  display: flex;
  flex: 1;
  flex-direction: column;
  justify-content: space-between;
  min-height: 0;
  background-color: var(--color-v3-surface);
}

.title-cards-and-footer {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  min-height: 0;
  padding-inline: 20px;
  overflow: hidden auto;
}

.title-cards-and-footer.zero-state {
  justify-content: start;
}

.empty-state {
  display: flex;
  justify-content: center;
  width: 100%;
  margin: auto 0;
}

.title-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding-top: calc(50vh - 200px);
  text-align: center;
  user-select: none;
}

.hero-text {
  margin: 0;
  color: var(--color-v3-text);
  font-family: var(--font-heading);
  font-size: 36px;
  font-weight: 300;
  line-height: 1.2em;
}

.chips-container {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  max-width: 1000px;
  margin-top: 8px;
  user-select: none;
}

.autoscroll {
  flex: 1;
  box-sizing: border-box;
  width: 100%;
  max-width: 1000px;
  min-height: 0;
  margin: 0 auto;
  padding: 20px 20px 0;
  overflow-y: auto;
}

.chat-turns-wrapper {
  margin-bottom: 72px;
}

.bidi-turn {
  container-type: inline-size;
}

.bidi-turn:not(:first-child) {
  margin-top: 24px;
}

.turn-separator {
  border-top: 1px solid var(--color-v3-outline);
}

.author-label {
  margin-top: 20px;
  margin-bottom: 8px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.turn {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.content {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
  width: 100%;
  min-width: 0;
}

.text-part {
  display: flex;
  flex-direction: column;
  width: 100%;
}

.model .text-part {
  padding: 4px 0;
}

.thought-part {
  width: 100%;
}

.interim {
  margin: 0;
  color: var(--color-v3-text-var);
  font-size: 14px;
  font-style: italic;
  line-height: 21px;
  animation: interim-flicker 1.2s ease-in-out infinite;
}

.audio-part {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.audio-part audio {
  max-width: max(256px, 33cqw);
}

.streaming-output {
  display: flex;
  gap: 8px;
}

.sound-wave {
  width: 62px;
  height: 20px;
  margin: 10px 0;
  background: var(--color-v3-text);
  mask: url('https://www.gstatic.com/aistudio/sound-wave.gif') center / contain no-repeat;
}

.call-part {
  display: flex;
  min-width: 50cqw;
  max-width: 100cqw;
}

.tool-block {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container);
}

.tool-title {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 36px;
  padding: 0 12px;
  border-bottom: 1px solid var(--color-v3-outline-var);
  font-size: 14px;
  font-weight: 500;
}

.tool-block pre {
  margin: 0;
  padding: 12px 16px 12px 12px;
  overflow-x: auto;
  font-family: var(--font-mono);
  font-size: 13px;
  line-height: 22px;
}

.call-form {
  display: flex;
  flex-direction: column;
}

.call-input {
  box-sizing: border-box;
  width: 100%;
  padding: 16px;
  border: 1px solid transparent;
  border-top-color: var(--color-v3-outline-var);
  outline: none;
  background: var(--color-v3-surface-container);
  color: var(--color-v3-text);
  font-family: var(--font-mono);
  font-size: 14px;
  line-height: 21px;
  resize: none;
  white-space: pre-wrap;
}

.call-input:disabled {
  opacity: 0.38;
}

.response {
  padding: 16px;
  border: 1px solid transparent;
  border-bottom: 4px solid var(--color-surface-bright);
  background: var(--color-v3-surface-container);
  font-family: var(--font-mono);
  font-size: 14px;
  line-height: 21px;
  white-space: pre-wrap;
  opacity: 0.38;
}

.will-continue-container {
  padding: 12px;
  background: var(--color-surface-bright);
  font-size: 14px;
  line-height: 21px;
}

.will-continue {
  display: flex;
  align-items: center;
  gap: 12px;
  width: fit-content;
  cursor: pointer;
}

.call-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 4px;
  background: var(--color-v3-surface-container-high);
  font-size: 14px;
  line-height: 21px;
}

.footer-start {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.call-error {
  color: var(--color-v3-accent-3);
  font-size: 12px;
  line-height: 18px;
}

.translation-mode-container {
  display: flex;
  flex: 1;
  flex-direction: column;
  width: 100%;
  max-width: 1000px;
  min-height: 0;
  margin: 0 auto 56px;
  overflow-y: hidden;
}

.transcript-panels {
  display: grid;
  flex: 1;
  grid-template-columns: 1fr 1fr;
  min-height: 0;
  overflow: hidden;
}

.transcript-panels > .transcript-panel:first-child {
  border-right: 1px solid var(--color-v3-outline);
}

.transcript-panels.stt-mode {
  grid-template-columns: 1fr;
}

.transcript-panels.stt-mode > .transcript-panel:first-child {
  border-right: none;
}

.transcript-panel {
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 20px 20px 0;
  overflow-y: hidden;
}

.panel-title {
  margin-bottom: 12px;
  color: var(--color-v3-text-var);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
}

.transcript-content {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  font-size: 14px;
  line-height: 21px;
}

.transcript-paragraph {
  margin: 0;
  padding: 0;
}

.transcript-paragraph:not(:first-child) {
  margin-top: 12px;
}

.interim-transcript {
  color: var(--color-v3-text-var);
  font-style: italic;
  animation: interim-flicker 1.2s ease-in-out infinite;
}

.language-badge {
  display: inline-block;
  margin-right: 4px;
  padding: 1px 4px;
  border-radius: 4px;
  background-color: color-mix(in srgb, var(--color-v3-outline-accent) 10%, transparent);
  color: var(--color-v3-outline-accent);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.03em;
  line-height: 18px;
  text-transform: uppercase;
  vertical-align: baseline;
}

.footer {
  position: relative;
  display: flex;
  align-items: flex-end;
  gap: 8px;
  width: 100%;
}

.footer.translation-mode {
  box-sizing: border-box;
  padding-inline: 20px;
  padding-bottom: 16px;
}

.footer.zero-state {
  align-self: center;
  max-width: 1000px;
}

.stream-status {
  position: absolute;
  top: 0;
  left: 50%;
  z-index: 4;
  display: flex;
  align-items: center;
  justify-content: center;
  box-sizing: border-box;
  width: 100%;
  max-width: 1000px;
  min-height: 56px;
  padding: 8px 16px;
  text-align: center;
  transform: translate(-50%, -100%);
  user-select: none;
  white-space: nowrap;
}

.stream-status-divider {
  flex-grow: 1;
  padding: 4px;
  border-radius: 8px;
  background: var(--color-v3-surface);
}

.stream-status-divider.left {
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
}

.stream-status-divider.right {
  border-top-left-radius: 0;
  border-bottom-left-radius: 0;
}

.stream-status-divider hr {
  margin: 0;
  border: 0;
  border-top: 1px solid var(--color-v3-outline-var);
}

.stream-status-content {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 16px;
  min-width: 0;
  padding: 0 12px;
  border-radius: 8px;
  background: var(--color-v3-surface);
}

.stream-status-text {
  overflow: hidden;
  color: var(--color-v3-text);
  font-size: 14px;
  line-height: 21px;
  text-overflow: ellipsis;
}

.bidi-input {
  position: relative;
  display: flex;
  flex: 1;
  min-width: 0;
}

.bidi-input.translation-mode {
  justify-content: center;
}

.buttons-container {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 28px;
}

.streaming-buttons-container {
  display: flex;
  align-items: flex-end;
  gap: 12px;
}

.audio-button-wrapper {
  position: relative;
  display: flex;
  align-items: center;
  box-sizing: border-box;
  height: 32px;
  border: 1px solid var(--color-v3-outline);
  border-radius: 12px;
  background-color: var(--color-v3-button-container);
}

.audio-button-wrapper > button {
  height: 100%;
  border: none;
}

.tools-button {
  flex-shrink: 0;
}

.enabled-tool-container {
  display: flex;
  gap: 4px;
  min-width: 0;
  overflow-x: auto;
  scrollbar-width: none;
}

.enabled-tool {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  max-height: 32px;
  padding: 2px 8px;
  border-radius: 12px;
  background-color: var(--color-v3-button-container-accent);
  cursor: default;
}

.tool-chip {
  display: flex;
  align-items: center;
}

.tool-icon {
  margin-right: 4px;
}

.tool-name {
  color: var(--color-v3-text-on-button);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  white-space: nowrap;
}

.enabled-tool [ms-button] {
  padding: 0;
}

.live-tools-dialog {
  position: relative;
  z-index: 1000;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 360px;
  max-width: 100vw;
  padding: 16px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container);
  box-shadow: var(--shadow-v3-dropdown);
}

.tool-item {
  display: flex;
  align-items: center;
  gap: 20px;
  min-height: 32px;
}

.tool-label {
  display: flex;
  flex-grow: 1;
  align-items: center;
  min-width: 0;
  color: var(--color-v3-text);
  cursor: pointer;
  font-size: 14px;
  line-height: 21px;
}

.video-container {
  position: absolute;
  right: 8px;
  bottom: calc(100% + 8px);
  z-index: 5;
  display: flex;
  width: min(340px, 33vw);
  overflow: hidden;
  border-radius: 8px;
  background: var(--color-v3-surface);
  box-shadow: var(--shadow-v3-lg);
  touch-action: none;
}

.video-move {
  display: flex;
  width: 100%;
  height: auto;
  cursor: move;
}

.resize-handle {
  position: absolute;
  width: 20px;
  height: 20px;
}

.resize-handle.tl {
  top: 0;
  left: 0;
  cursor: nwse-resize;
}

.resize-handle.tr {
  top: 0;
  right: 0;
  cursor: nesw-resize;
}

.resize-handle.bl {
  bottom: 0;
  left: 0;
  cursor: nesw-resize;
}

.resize-handle.br {
  right: 0;
  bottom: 0;
  cursor: nwse-resize;
}

@keyframes interim-flicker {
  0%,
  to {
    opacity: 0.45;
  }

  50% {
    opacity: 0.85;
  }
}

@media (max-width: 768px) {
  .autoscroll {
    padding: 20px 8px 0;
  }

  .transcript-panels {
    grid-template: 1fr 1fr / 1fr;
  }

  .transcript-panels > .transcript-panel:first-child {
    padding-bottom: 20px;
    border-right: none;
    border-bottom: 1px solid var(--color-v3-outline);
  }

  .transcript-panels.stt-mode {
    grid-template: 1fr / 1fr;
  }

  .transcript-panels.stt-mode > .transcript-panel:first-child {
    padding-bottom: 0;
    border-bottom: none;
  }

  .transcript-panel {
    padding: 20px 8px 0;
  }

  .video-container {
    bottom: calc(100% + 68px);
  }
}

@media (max-width: 600px) {
  .title-cards-and-footer {
    padding: 0 20px 8px;
  }

  .title-cards-and-footer.zero-state {
    justify-content: space-between;
  }

  .title-container {
    padding-top: 0;
  }

  .hero-text {
    padding-top: calc(50vh - 140px);
    font-size: 32px;
  }

  .chips-container {
    flex-direction: column;
    flex-wrap: nowrap;
    gap: 16px;
    max-width: 200px;
  }

  .footer.zero-state .streaming-buttons-container {
    display: none;
  }
}
</style>
