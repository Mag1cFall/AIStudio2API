<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

// ViewerFrame 是服务端推送的画面所属页面，宽高为 CSS 像素
interface ViewerFrame {
  context: string
  url: string
  title: string
  width: number
  height: number
}

// ViewerDialog 是页面打开且等待用户处理的 alert、confirm 或 prompt
interface ViewerDialog {
  kind: string
  message: string
  value: string
}

// ViewerMessage 是登录窗口连接上的服务端消息
interface ViewerMessage extends Partial<ViewerFrame> {
  type: string
  status?: string
  dialog?: ViewerDialog | null
}

const props = defineProps<{ view: string; error: string }>()
const emit = defineEmits<{ active: []; unavailable: []; cancel: []; close: [] }>()
const { t } = useI18n()

const status = ref('')
const frame = ref<ViewerFrame | null>(null)
const dialog = ref<ViewerDialog | null>(null)
const dialogText = ref('')
const reconnecting = ref(false)
const focused = ref(false)
const actualSize = ref(false)
const stage = ref<HTMLDivElement>()
const canvas = ref<HTMLCanvasElement>()
const sink = ref<HTMLTextAreaElement>()
const dialogInput = ref<HTMLInputElement>()
const dialogConfirm = ref<HTMLButtonElement>()
const stageSize = ref({ width: 0, height: 0 })

let socket: WebSocket | null = null
let opened = false
let disposed = false
let pendingFrame: ViewerFrame | null = null
let drawSequence = 0
let reconnectTimer = 0
let resizeObserver: ResizeObserver | null = null
let pendingMove: { x: number; y: number } | null = null
let moveFrame = 0
// pressed 是已发送按下的键，键为 KeyboardEvent.code，值为按下时发送的 key
const pressed = new Map<string, string>()
// touch 是正在进行的单指触摸，移动超过阈值后转为滚动
let touch: {
  id: number
  startX: number
  startY: number
  lastX: number
  lastY: number
  scrolling: boolean
} | null = null

const visible = computed(() => status.value !== '')
const pageURL = computed(() => {
  try {
    return new URL(frame.value?.url ?? '')
  } catch {
    return null
  }
})
const host = computed(() => pageURL.value?.hostname ?? '')
const secure = computed(() => pageURL.value?.protocol === 'https:')
const busy = computed(() => status.value === 'starting' || status.value === 'saving')
const statusText = computed(() => {
  if (status.value === 'starting') return t('accounts.liveStarting')
  if (status.value === 'saving') return t('accounts.liveSaving')
  return t('accounts.liveWaiting')
})
// fitScale 是画面完整放进画布区时的缩放，放得下时为 1
const fitScale = computed(() => {
  if (frame.value === null || stageSize.value.width === 0) return 1
  return Math.min(
    1,
    stageSize.value.width / frame.value.width,
    stageSize.value.height / frame.value.height,
  )
})
const displayScale = computed(() => (actualSize.value ? 1 : fitScale.value))
const canvasStyle = computed(() =>
  frame.value === null
    ? {}
    : {
        width: `${frame.value.width * displayScale.value}px`,
        height: `${frame.value.height * displayScale.value}px`,
        touchAction: actualSize.value ? 'pan-x pan-y' : 'none',
      },
)

// connect 建立登录窗口连接，首次连接失败时报告不可用，之后断开时每秒重连
function connect(): void {
  const current = new WebSocket(api.loginViewURL(props.view))
  current.binaryType = 'blob'
  socket = current
  current.onopen = () => {
    opened = true
    reconnecting.value = false
  }
  current.onmessage = (event: MessageEvent<string | Blob>) => {
    if (typeof event.data === 'string') receive(JSON.parse(event.data) as ViewerMessage)
    else void draw(event.data)
  }
  current.onclose = () => {
    if (socket !== current || disposed) return
    socket = null
    if (!opened) {
      emit('unavailable')
      return
    }
    if (props.error !== '') return
    reconnecting.value = status.value !== ''
    reconnectTimer = window.setTimeout(connect, 1000)
  }
}

// receive 处理登录阶段、页面对话框与画面元数据
function receive(message: ViewerMessage): void {
  if (message.type === 'status' && message.status) {
    if (status.value === '') emit('active')
    status.value = message.status
  } else if (message.type === 'dialog') {
    dialog.value = message.dialog ?? null
    dialogText.value = message.dialog?.value ?? ''
    if (dialog.value !== null)
      void nextTick(() => (dialogInput.value ?? dialogConfirm.value)?.focus())
  } else if (message.type === 'frame') {
    pendingFrame = {
      context: message.context ?? '',
      url: message.url ?? '',
      title: message.title ?? '',
      width: message.width ?? 0,
      height: message.height ?? 0,
    }
  }
}

// draw 解码一帧 JPEG 画到画布上，较早到达但较晚解码完成的帧被丢弃
async function draw(blob: Blob): Promise<void> {
  const meta = pendingFrame
  if (meta === null) return
  const sequence = ++drawSequence
  const bitmap = await createImageBitmap(blob)
  const element = canvas.value
  if (sequence !== drawSequence || element === undefined) {
    bitmap.close()
    return
  }
  if (element.width !== bitmap.width) element.width = bitmap.width
  if (element.height !== bitmap.height) element.height = bitmap.height
  element.getContext('2d')?.drawImage(bitmap, 0, 0)
  bitmap.close()
  const first = frame.value === null
  frame.value = meta
  if (first) {
    await nextTick()
    focusSink()
  }
}

// send 写出一条消息，连接未打开时丢弃
function send(message: Record<string, unknown>): void {
  if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(message))
}

// sendInput 写出一条输入，附带当前画面所属页面
function sendInput(message: Record<string, unknown>): void {
  if (frame.value !== null) send({ ...message, context: frame.value.context })
}

// remotePoint 把指针位置换算为页面 CSS 像素
function remotePoint(event: { clientX: number; clientY: number }): { x: number; y: number } {
  const rect = canvas.value?.getBoundingClientRect()
  const meta = frame.value
  if (rect === undefined || meta === null || rect.width === 0 || rect.height === 0)
    return { x: 0, y: 0 }
  return {
    x: Math.min(
      Math.max(((event.clientX - rect.left) / rect.width) * meta.width, 0),
      meta.width - 1,
    ),
    y: Math.min(
      Math.max(((event.clientY - rect.top) / rect.height) * meta.height, 0),
      meta.height - 1,
    ),
  }
}

function focusSink(): void {
  sink.value?.focus({ preventScroll: true })
}

// flushMove 写出等待中的指针移动，每个动画帧最多一次
function flushMove(): void {
  if (moveFrame !== 0) cancelAnimationFrame(moveFrame)
  moveFrame = 0
  if (pendingMove !== null) sendInput({ type: 'pointer', action: 'move', ...pendingMove })
  pendingMove = null
}

function pointerDown(event: PointerEvent): void {
  if (frame.value === null) return
  if (event.pointerType === 'touch') {
    touch = {
      id: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      lastX: event.clientX,
      lastY: event.clientY,
      scrolling: false,
    }
    return
  }
  event.preventDefault()
  focusSink()
  canvas.value?.setPointerCapture(event.pointerId)
  flushMove()
  sendInput({ type: 'pointer', action: 'down', ...remotePoint(event), button: event.button })
}

function pointerMove(event: PointerEvent): void {
  if (frame.value === null) return
  if (event.pointerType === 'touch') {
    touchMove(event)
    return
  }
  pendingMove = remotePoint(event)
  if (moveFrame === 0) moveFrame = requestAnimationFrame(flushMove)
}

function pointerUp(event: PointerEvent): void {
  if (frame.value === null) return
  if (event.pointerType === 'touch') {
    touchEnd(event)
    return
  }
  flushMove()
  sendInput({ type: 'pointer', action: 'up', ...remotePoint(event), button: event.button })
}

// touchMove 在适应窗口时把单指拖动转为页面滚动
function touchMove(event: PointerEvent): void {
  if (touch === null || touch.id !== event.pointerId || actualSize.value) return
  if (
    !touch.scrolling &&
    Math.hypot(event.clientX - touch.startX, event.clientY - touch.startY) < 8
  )
    return
  touch.scrolling = true
  const scale = displayScale.value
  sendInput({
    type: 'wheel',
    ...remotePoint(event),
    dx: (touch.lastX - event.clientX) / scale,
    dy: (touch.lastY - event.clientY) / scale,
  })
  touch.lastX = event.clientX
  touch.lastY = event.clientY
}

// touchEnd 把没有拖动的触摸作为一次点击，并让手机弹出键盘
function touchEnd(event: PointerEvent): void {
  if (touch === null || touch.id !== event.pointerId) return
  const tapped = !touch.scrolling
  touch = null
  if (!tapped) return
  const point = remotePoint(event)
  focusSink()
  sendInput({ type: 'pointer', action: 'move', ...point })
  sendInput({ type: 'pointer', action: 'down', ...point, button: 0 })
  sendInput({ type: 'pointer', action: 'up', ...point, button: 0 })
}

function wheel(event: WheelEvent): void {
  if (frame.value === null) return
  event.preventDefault()
  const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? frame.value.height : 1
  flushMove()
  sendInput({
    type: 'wheel',
    ...remotePoint(event),
    dx: event.deltaX * unit,
    dy: event.deltaY * unit,
  })
}

// press 写出一次按下并抬起
function press(key: string): void {
  sendInput({ type: 'key', action: 'down', key })
  sendInput({ type: 'key', action: 'up', key })
}

// releaseKeys 抬起已按下且符合条件的键
function releaseKeys(match: (key: string) => boolean = () => true): void {
  for (const [code, key] of pressed) {
    if (!match(key)) continue
    pressed.delete(code)
    sendInput({ type: 'key', action: 'up', key })
  }
}

// keyDown 接管按键，输入法组字与手机键盘的字符改由 input 与 compositionend 发送，粘贴快捷键交给 paste
function keyDown(event: KeyboardEvent): void {
  if (event.isComposing || ['Process', 'Unidentified', 'Dead', 'AltGraph'].includes(event.key))
    return
  if ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === 'v') return
  event.preventDefault()
  if (event.key.length === 1 && event.getModifierState('AltGraph')) {
    releaseKeys((key) => key === 'Control' || key === 'Alt')
  }
  pressed.set(event.code || event.key, event.key)
  sendInput({ type: 'key', action: 'down', key: event.key })
}

function keyUp(event: KeyboardEvent): void {
  const code = event.code || event.key
  const key = pressed.get(code)
  if (key === undefined) return
  event.preventDefault()
  pressed.delete(code)
  sendInput({ type: 'key', action: 'up', key })
}

// paste 抬起触发粘贴的修饰键后把剪贴板文本逐字输入
function paste(event: ClipboardEvent): void {
  event.preventDefault()
  releaseKeys((key) => key === 'Control' || key === 'Meta')
  const text = event.clipboardData?.getData('text/plain') ?? ''
  if (text !== '') sendInput({ type: 'text', text })
}

function compositionEnd(event: CompositionEvent): void {
  if (event.data) sendInput({ type: 'text', text: event.data })
  if (sink.value) sink.value.value = ''
}

// textInput 转发手机键盘直接提交的字符、换行与退格
function textInput(event: Event): void {
  const input = event as InputEvent
  if (input.isComposing) return
  if (input.inputType === 'insertText' && input.data) sendInput({ type: 'text', text: input.data })
  else if (input.inputType === 'insertLineBreak') press('Enter')
  else if (input.inputType === 'deleteContentBackward') press('Backspace')
  if (sink.value) sink.value.value = ''
}

function blur(): void {
  focused.value = false
  releaseKeys()
}

// answer 按用户的选择关闭页面对话框
function answer(accept: boolean): void {
  send({ type: 'dialog', accept, text: dialogText.value })
  dialog.value = null
  focusSink()
}

onMounted(() => {
  resizeObserver = new ResizeObserver(([entry]) => {
    if (entry)
      stageSize.value = { width: entry.contentRect.width, height: entry.contentRect.height }
  })
  if (stage.value) resizeObserver.observe(stage.value)
  connect()
})

onBeforeUnmount(() => {
  disposed = true
  window.clearTimeout(reconnectTimer)
  if (moveFrame !== 0) cancelAnimationFrame(moveFrame)
  resizeObserver?.disconnect()
  socket?.close()
})
</script>

<template>
  <Teleport to="body">
    <div
      v-show="visible"
      class="fixed inset-0 z-[80] flex flex-col bg-canvas"
      role="dialog"
      aria-modal="true"
      :aria-label="t('accounts.browserLogin')"
    >
      <header
        class="flex h-12 shrink-0 items-center gap-2 border-b border-line bg-panel px-2 sm:gap-3 sm:px-4"
      >
        <div
          class="flex h-8 min-w-0 flex-1 items-center gap-2 rounded-full border border-line bg-canvas px-3"
          :title="frame?.title"
        >
          <UiIcon v-if="secure" name="lock" :size="14" class="text-gray-400" />
          <span class="truncate text-sm text-gray-200">{{ host }}</span>
        </div>
        <span class="flex shrink-0 items-center gap-1.5 text-xs text-gray-400" aria-live="polite">
          <UiIcon v-if="busy" name="spinner" :size="14" />
          <span v-else class="h-2 w-2 rounded-full bg-green-500"></span>
          <span class="max-sm:sr-only">{{ statusText }}</span>
        </span>
        <button
          v-if="fitScale < 1"
          v-tooltip="actualSize ? t('accounts.liveFit') : t('accounts.liveActual')"
          type="button"
          class="rounded p-1.5 text-gray-400 transition hover:bg-raised hover:text-white"
          :aria-label="actualSize ? t('accounts.liveFit') : t('accounts.liveActual')"
          @click="actualSize = !actualSize"
        >
          <UiIcon :name="actualSize ? 'zoomFit' : 'zoomActual'" :size="16" />
        </button>
        <button
          type="button"
          class="shrink-0 rounded border border-line bg-raised px-3 py-1.5 text-sm text-gray-200 transition hover:bg-line"
          @click="error === '' ? emit('cancel') : emit('close')"
        >
          {{ error === '' ? t('common.cancel') : t('common.close') }}
        </button>
      </header>
      <div ref="stage" class="relative flex min-h-0 flex-1 overflow-auto">
        <canvas
          v-show="frame"
          ref="canvas"
          class="m-auto block shrink-0 select-none"
          :class="focused ? 'outline-2 outline-blue-500/70' : 'outline-1 outline-line'"
          :style="canvasStyle"
          role="img"
          :aria-label="t('accounts.liveScreen')"
          @pointerdown="pointerDown"
          @pointermove="pointerMove"
          @pointerup="pointerUp"
          @pointercancel="touch = null"
          @mousedown.prevent
          @wheel="wheel"
          @contextmenu.prevent
        ></canvas>
        <textarea
          ref="sink"
          class="pointer-events-none fixed top-0 left-0 h-px w-px resize-none opacity-0"
          style="font-size: 16px"
          aria-hidden="true"
          tabindex="-1"
          autocapitalize="off"
          autocomplete="off"
          spellcheck="false"
          @focus="focused = true"
          @blur="blur"
          @keydown="keyDown"
          @keyup="keyUp"
          @paste="paste"
          @compositionend="compositionEnd"
          @input="textInput"
        ></textarea>
        <div
          v-if="frame === null && error === ''"
          class="absolute inset-0 flex flex-col items-center justify-center gap-3 text-sm text-gray-400"
        >
          <UiIcon name="spinner" :size="22" />
          {{ statusText }}
        </div>
        <div
          v-if="reconnecting && error === ''"
          class="absolute top-3 left-1/2 flex -translate-x-1/2 items-center gap-2 rounded-full border border-amber-500/30 bg-panel px-3 py-1.5 text-xs text-amber-200 shadow-lg"
        >
          <UiIcon name="spinner" :size="13" />
          {{ t('accounts.liveReconnecting') }}
        </div>
        <div
          v-if="dialog && error === ''"
          class="absolute inset-0 flex items-start justify-center bg-black/30 px-4 pt-16"
        >
          <form
            class="w-full max-w-md rounded-lg border border-line bg-panel p-5 shadow-2xl"
            @submit.prevent="answer(true)"
          >
            <p class="mb-2 text-sm font-medium text-white">
              {{ t('accounts.liveDialog').replace('{host}', host) }}
            </p>
            <p
              class="mb-4 max-h-60 overflow-y-auto text-sm whitespace-pre-wrap [overflow-wrap:anywhere] text-gray-300"
            >
              {{ dialog.message }}
            </p>
            <input
              v-if="dialog.kind === 'prompt'"
              ref="dialogInput"
              v-model="dialogText"
              class="mb-4 w-full rounded border border-line bg-canvas px-3 py-2 text-sm text-white focus:border-blue-500 focus:outline-none"
            />
            <div class="flex justify-end gap-2">
              <button
                v-if="dialog.kind !== 'alert'"
                type="button"
                class="rounded border border-line bg-raised px-4 py-1.5 text-sm text-gray-200 transition hover:bg-line"
                @click="answer(false)"
              >
                {{ t('common.cancel') }}
              </button>
              <button
                ref="dialogConfirm"
                type="submit"
                class="rounded bg-blue-600 px-4 py-1.5 text-sm text-white transition hover:bg-blue-500"
              >
                {{ t('accounts.liveOk') }}
              </button>
            </div>
          </form>
        </div>
        <div
          v-if="error !== ''"
          class="absolute inset-0 flex items-center justify-center bg-black/40 px-4"
          role="alert"
        >
          <div class="w-full max-w-md rounded-lg border border-line bg-panel p-5 shadow-2xl">
            <p class="mb-2 flex items-center gap-2 text-sm font-medium text-red-300">
              <UiIcon name="warning" :size="16" />
              {{ t('accounts.liveFailed') }}
            </p>
            <p class="mb-4 text-sm [overflow-wrap:anywhere] whitespace-pre-wrap text-gray-300">
              {{ error }}
            </p>
            <div class="flex justify-end">
              <button
                type="button"
                class="rounded border border-line bg-raised px-4 py-1.5 text-sm text-gray-200 transition hover:bg-line"
                @click="emit('close')"
              >
                {{ t('common.close') }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>
