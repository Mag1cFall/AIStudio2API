<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, ApiError } from '@/api'
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

// ViewerMessage 是远程操作连接上的服务端消息
type ViewerMessage =
  | { type: 'status'; status: string }
  | { type: 'dialog'; dialog: ViewerDialog | null }
  | { type: 'closed'; error?: string }
  | (ViewerFrame & { type: 'frame' })

const props = defineProps<{ accountId: string; accountLabel: string }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()

const frame = ref<ViewerFrame | null>(null)
const dialog = ref<ViewerDialog | null>(null)
const dialogText = ref('')
const focused = ref(false)
const actualSize = ref(false)
const connecting = ref(true)
const fatal = ref('')
const address = ref('')
const stage = ref<HTMLDivElement>()
const canvas = ref<HTMLCanvasElement>()
const sink = ref<HTMLTextAreaElement>()
const dialogInput = ref<HTMLInputElement>()
const dialogConfirm = ref<HTMLButtonElement>()
const stageSize = ref({ width: 0, height: 0 })

let socket: WebSocket | null = null
let disposed = false
let pendingFrame: ViewerFrame | null = null
let drawSequence = 0
const resizeObserver = new ResizeObserver(([entry]) => {
  stageSize.value = { width: entry!.contentRect.width, height: entry!.contentRect.height }
})
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

const pageURL = computed(() => (frame.value === null ? null : new URL(frame.value.url)))
const host = computed(() => pageURL.value?.hostname ?? '')
const secure = computed(() => pageURL.value?.protocol === 'https:')

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

// connect 建立远程操作连接，浏览器连接断开时不重连
function connect(): void {
  const current = new WebSocket(api.accountLiveViewURL(props.accountId))
  current.binaryType = 'blob'
  socket = current
  current.onopen = () => {
    connecting.value = false
  }
  current.onmessage = (event: MessageEvent<string | Blob>) => {
    if (typeof event.data === 'string') receive(JSON.parse(event.data) as ViewerMessage)
    else void draw(event.data)
  }
  current.onclose = () => {
    if (socket !== current || disposed) return
    socket = null
    if (fatal.value === '') fatal.value = t('accounts.liveDisconnected')
    connecting.value = false
  }
  current.onerror = () => {
    if (fatal.value === '') fatal.value = t('accounts.liveDisconnected')
  }
}

// receive 处理对话框、画面元数据与浏览器断连
function receive(message: ViewerMessage): void {
  if (message.type === 'dialog') {
    dialog.value = message.dialog
    dialogText.value = message.dialog?.value ?? ''
    if (dialog.value !== null)
      void nextTick(() => (dialogInput.value ?? dialogConfirm.value)?.focus())
  } else if (message.type === 'closed') {
    fatal.value = message.error || t('accounts.liveDisconnected')
  } else if (message.type === 'frame') {
    pendingFrame = message
    address.value = message.url
  }
}

// draw 解码一帧 JPEG 画到画布上，较早到达但较晚解码完成的帧被丢弃
async function draw(blob: Blob): Promise<void> {
  const meta = pendingFrame!
  const sequence = ++drawSequence
  const bitmap = await createImageBitmap(blob)
  if (sequence !== drawSequence) {
    bitmap.close()
    return
  }
  const element = canvas.value!
  if (element.width !== bitmap.width) element.width = bitmap.width
  if (element.height !== bitmap.height) element.height = bitmap.height
  element.getContext('2d')!.drawImage(bitmap, 0, 0)
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

// control 写出一条地址栏或标签页控制命令
function control(message: Record<string, unknown>): void {
  if (frame.value !== null) send({ ...message, context: frame.value.context })
}

// submitAddress 导航到地址栏输入的网址，缺协议时补 https://
function submitAddress(): void {
  let target = address.value.trim()
  if (target === '') {
    if (frame.value !== null) address.value = frame.value.url
    return
  }
  if (!/^[a-z]+:\/\//i.test(target)) target = `https://${target}`
  control({ type: 'navigate', url: target })
}

function remotePoint(event: { clientX: number; clientY: number }): { x: number; y: number } {
  const rect = canvas.value!.getBoundingClientRect()
  const meta = frame.value!
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
  sink.value!.focus({ preventScroll: true })
}

// flushMove 写出等待中的指针移动，每个动画帧最多一次
function flushMove(): void {
  cancelAnimationFrame(moveFrame)
  moveFrame = 0
  if (pendingMove !== null) sendInput({ type: 'pointer', action: 'move', ...pendingMove })
  pendingMove = null
}

function pointerDown(event: PointerEvent): void {
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
  canvas.value!.setPointerCapture(event.pointerId)
  flushMove()
  sendInput({ type: 'pointer', action: 'down', ...remotePoint(event), button: event.button })
}

function pointerMove(event: PointerEvent): void {
  if (event.pointerType === 'touch') {
    touchMove(event)
    return
  }
  pendingMove = remotePoint(event)
  if (moveFrame === 0) moveFrame = requestAnimationFrame(flushMove)
}

function pointerUp(event: PointerEvent): void {
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
  event.preventDefault()
  const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? frame.value!.height : 1
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
  const text = event.clipboardData!.getData('text/plain')
  if (text !== '') sendInput({ type: 'text', text })
}

function compositionEnd(event: CompositionEvent): void {
  if (event.data) sendInput({ type: 'text', text: event.data })
  sink.value!.value = ''
}

// textInput 转发手机键盘直接提交的字符、换行与退格
function textInput(event: Event): void {
  const input = event as InputEvent
  if (input.isComposing) return
  if (input.inputType === 'insertText' && input.data) sendInput({ type: 'text', text: input.data })
  else if (input.inputType === 'insertLineBreak') press('Enter')
  else if (input.inputType === 'deleteContentBackward') press('Backspace')
  sink.value!.value = ''
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

const navButtonClass =
  'rounded p-1.5 text-gray-400 transition hover:bg-raised hover:text-white disabled:opacity-40 disabled:hover:bg-transparent'

onMounted(async () => {
  resizeObserver.observe(stage.value!)
  // 浏览器 WebSocket 握手失败时读不到响应体，先经 REST 预检拿到不可用的具体原因
  try {
    const status = await api.liveViewStatus(props.accountId)
    if (!status.available) {
      fatal.value = status.reason || t('accounts.liveUnavailable')
      connecting.value = false
      return
    }
  } catch (error) {
    fatal.value = error instanceof ApiError ? error.message : t('accounts.liveUnavailable')
    connecting.value = false
    return
  }
  connect()
})

onBeforeUnmount(() => {
  disposed = true
  cancelAnimationFrame(moveFrame)
  resizeObserver.disconnect()
  socket?.close()
})
</script>

<template>
  <Teleport to="body">
    <div
      class="fixed inset-0 z-[80] flex flex-col bg-canvas"
      role="dialog"
      aria-modal="true"
      :aria-label="t('accounts.liveViewTitle')"
    >
      <header
        class="flex h-12 shrink-0 items-center gap-1.5 border-b border-line bg-panel px-2 sm:gap-2 sm:px-3"
      >
        <button
          v-tooltip="t('accounts.liveBack')"
          type="button"
          class="shrink-0"
          :class="navButtonClass"
          :aria-label="t('accounts.liveBack')"
          :disabled="fatal !== ''"
          @click="control({ type: 'history', delta: -1 })"
        >
          <UiIcon name="undo" :size="16" />
        </button>
        <button
          v-tooltip="t('accounts.liveForward')"
          type="button"
          class="shrink-0"
          :class="navButtonClass"
          :aria-label="t('accounts.liveForward')"
          :disabled="fatal !== ''"
          @click="control({ type: 'history', delta: 1 })"
        >
          <UiIcon name="forward" :size="16" />
        </button>
        <button
          v-tooltip="t('accounts.liveReload')"
          type="button"
          class="shrink-0"
          :class="navButtonClass"
          :aria-label="t('accounts.liveReload')"
          :disabled="fatal !== ''"
          @click="control({ type: 'reload' })"
        >
          <UiIcon name="refresh" :size="15" />
        </button>
        <form
          class="flex h-8 min-w-0 flex-1 items-center gap-2 rounded-full border border-line bg-canvas px-3"
          @submit.prevent="submitAddress"
        >
          <UiIcon v-if="secure" name="lock" :size="13" class="shrink-0 text-gray-400" />
          <input
            v-model="address"
            type="text"
            enterkeyhint="go"
            spellcheck="false"
            autocomplete="off"
            autocapitalize="off"
            class="min-w-0 flex-1 bg-transparent text-sm text-gray-200 outline-none placeholder:text-gray-600"
            :placeholder="t('accounts.liveAddressPlaceholder')"
            :aria-label="t('accounts.liveAddress')"
            :disabled="fatal !== ''"
          />
        </form>
        <button
          v-tooltip="t('accounts.liveNewTab')"
          type="button"
          class="shrink-0"
          :class="navButtonClass"
          :aria-label="t('accounts.liveNewTab')"
          :disabled="fatal !== ''"
          @click="control({ type: 'newTab' })"
        >
          <UiIcon name="add" :size="16" />
        </button>
        <button
          v-tooltip="t('accounts.liveCloseTab')"
          type="button"
          class="shrink-0"
          :class="navButtonClass"
          :aria-label="t('accounts.liveCloseTab')"
          :disabled="fatal !== '' || frame === null"
          @click="control({ type: 'closeTab' })"
        >
          <UiIcon name="close" :size="15" />
        </button>
        <button
          v-if="fitScale < 1"
          v-tooltip="actualSize ? t('accounts.liveFit') : t('accounts.liveActual')"
          type="button"
          class="shrink-0"
          :class="navButtonClass"
          :aria-label="actualSize ? t('accounts.liveFit') : t('accounts.liveActual')"
          @click="actualSize = !actualSize"
        >
          <UiIcon :name="actualSize ? 'zoomFit' : 'zoomActual'" :size="16" />
        </button>
        <span
          class="hidden max-w-32 shrink-0 truncate text-xs text-gray-500 sm:block"
          :title="accountLabel"
        >
          {{ accountLabel }}
        </span>
        <button
          type="button"
          class="shrink-0 rounded border border-line bg-raised px-3 py-1.5 text-sm text-gray-200 transition hover:bg-line"
          @click="emit('close')"
        >
          {{ t('common.close') }}
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
          v-if="frame === null && fatal === ''"
          class="absolute inset-0 flex flex-col items-center justify-center gap-3 text-sm text-gray-400"
        >
          <UiIcon name="spinner" :size="22" class="animate-spin" />
          {{ connecting ? t('accounts.liveConnecting') : t('accounts.liveWaiting') }}
        </div>
        <div
          v-if="dialog && fatal === ''"
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
          v-if="fatal !== ''"
          class="absolute inset-0 flex items-center justify-center bg-black/40 p-4"
          role="alert"
        >
          <div
            class="flex max-h-full w-full max-w-md flex-col rounded-lg border border-line bg-panel p-5 shadow-2xl"
          >
            <p class="mb-2 flex shrink-0 items-center gap-2 text-sm font-medium text-red-300">
              <UiIcon name="warning" :size="16" />
              {{ t('accounts.liveClosed') }}
            </p>
            <p
              class="mb-4 min-h-0 overflow-y-auto text-sm [overflow-wrap:anywhere] whitespace-pre-wrap text-gray-300"
            >
              {{ fatal }}
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
