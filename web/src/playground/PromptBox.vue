<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
  PopoverContent,
  PopoverPortal,
  PopoverRoot,
  PopoverTrigger,
} from 'reka-ui'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSwitch from '@/playground/ui/UiSwitch.vue'
import CameraDialog from './CameraDialog.vue'
import DriveDialog from './DriveDialog.vue'
import SampleMediaDialog, { type SampleSelection } from './SampleMediaDialog.vue'
import { driveNames, type DriveFile } from './drive'
import { readImage } from './media/api'
import { useCompare, usePlayground } from './session'
import { audioInput } from './shell/preferences'
import { useMediaQuery } from './media-query'
import RunKeyHint from './RunKeyHint.vue'
import { appends, submits, useRunTooltip } from './run-keys'
import { useTools } from './tools'
import type { Part } from './types'

const { t } = useI18n()
const { showNotice, config } = useConsole()
const { session } = usePlayground()
const { panes, comparing, running, submitAll, appendAll, stopAll } = useCompare()
const { tools, setTool } = useTools()

const textarea = ref<HTMLTextAreaElement>()
const fileInput = ref<HTMLInputElement>()
const dragging = ref(false)
const youtubeOpen = ref(false)
const driveOpen = ref(false)
const sampleOpen = ref(false)
// preparing 为正在下载的示例图片数，下载完成前与官网一样不能运行
const preparing = ref(0)
const router = useRouter()

// addDriveFile 把从 Drive 选中的已上传文件作为文件引用加入附件
function addDriveFile(file: DriveFile): void {
  driveNames.set(file.uri, file.displayName || file.name)
  session.attachments.push({ fileData: { mimeType: file.mimeType, fileUri: file.uri } })
}

// addSample 与官网一致：示例图片下载后作为图片附件，示例视频作为 YouTube 视频附件
async function addSample(selection: SampleSelection): Promise<void> {
  if ('video' in selection) {
    session.attachments.push({
      fileData: {
        mimeType: 'video/*',
        fileUri: `https://www.youtube.com/watch?v=${selection.video.id}`,
      },
    })
    return
  }
  preparing.value++
  try {
    const response = await fetch(selection.image)
    if (!response.ok) throw new Error(`${response.status} ${selection.image}`)
    session.attachments.push({ inlineData: await readImage(await response.blob()) })
  } catch (error) {
    showNotice(error instanceof Error ? error.message : String(error), 'error')
  } finally {
    preparing.value--
  }
}

// openApiKey 打开管理端设置页中的 API 密钥设置
async function openApiKey(): Promise<void> {
  await router.push({ path: '/', query: { tab: 'settings' } })
  await nextTick()
  document.getElementById('setting-api-key')?.scrollIntoView({ block: 'center' })
}
const youtubeURL = ref('')
const camera = ref<InstanceType<typeof CameraDialog>>()
// recording 表示正在录音：attach 为插入音频附件，transcribe 为语音转文字
const recording = ref<'' | 'attach' | 'transcribe'>('')
const transcribing = ref(false)
const elapsed = ref('')
let recorder: MediaRecorder | undefined
let timer = 0

// enabledTools 是已开启并显示为标签的工具
const enabledTools = computed(() => tools.value.filter((tool) => tool.enabled))
// handset 与官网一致，768 px 及以下 Tools 只显示图标，已启用的工具列在提示中
const handset = useMediaQuery('(max-width: 768px)')
const toolsTooltip = computed(() =>
  handset.value ? enabledTools.value.map((tool) => t(tool.label)).join(', ') : '',
)
// canRun 表示有内容可发送，或有窗格的最后一轮是等待回复的用户轮次
const canRun = computed(
  () =>
    preparing.value === 0 &&
    (session.draft.trim() !== '' ||
      session.attachments.length > 0 ||
      panes.some((pane) => pane.session.turns.at(-1)?.role === 'user')),
)

// placeholderHeight 是占位文字在当前宽度下的高度，窗口尺寸或界面语言变化后重新测量
let placeholderHeight: number | undefined

// resize 与官网 cdkTextareaAutosize 一致：输入框高度取内容与占位文字中较高者，超过 210 px 后在框内滚动
function resize(): void {
  const element = textarea.value
  if (element === undefined) return
  element.style.height = 'auto'
  if (placeholderHeight === undefined) {
    const { value, selectionStart, selectionEnd } = element
    element.value = element.placeholder
    placeholderHeight = element.scrollHeight
    element.value = value
    element.setSelectionRange(selectionStart, selectionEnd)
  }
  element.style.height = `${Math.min(Math.max(element.scrollHeight, placeholderHeight, 21), 210)}px`
}

// remeasure 清除占位文字高度后重新定高
function remeasure(): void {
  placeholderHeight = undefined
  resize()
}

let resizeTimer = 0
// onWindowResize 与官网一样在窗口尺寸变化 16 ms 后重新测量，此时布局已按新尺寸更新
function onWindowResize(): void {
  if (resizeTimer !== 0) return
  resizeTimer = window.setTimeout(() => {
    resizeTimer = 0
    remeasure()
  }, 16)
}
watch(
  () => session.draft,
  () => void nextTick(resize),
)
watch(() => t('pg.placeholder'), remeasure, { flush: 'post' })
onMounted(() => {
  resize()
  window.addEventListener('resize', onWindowResize)
})

// focus 聚焦输入框
function focus(): void {
  textarea.value?.focus()
}

// submit 发送输入或停止生成，对比模式下同时发往每个窗格
function submit(): void {
  if (running.value) {
    stopAll()
    return
  }
  if (canRun.value) void submitAll()
}

// runTooltip 是运行按钮的悬停提示
const runTooltip = useRunTooltip(() => running.value)

// onKeydown 与官网 ms-prompt-box 一致：Escape 失焦，生成中或不能运行时快捷键不起作用，否则按提交键运行或追加
function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    textarea.value?.blur()
    return
  }
  if (running.value || !canRun.value) return
  if (submits(event)) {
    event.preventDefault()
    void submitAll()
  } else if (appends(event)) {
    event.preventDefault()
    appendAll()
  }
}

// readFile 读取文件为内联数据片段
function readFile(file: Blob, mimeType = file.type || 'application/octet-stream'): Promise<Part> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = String(reader.result)
      resolve({ inlineData: { mimeType, data: result.slice(result.indexOf(',') + 1) } })
    }
    reader.onerror = () => reject(reader.error ?? new Error('read failed'))
    reader.readAsDataURL(file)
  })
}

// addFiles 把文件加入待发送附件
async function addFiles(files: Iterable<File>): Promise<void> {
  for (const file of files) session.attachments.push(await readFile(file))
}

// onFileChange 读取文件选择框中的文件
async function onFileChange(): Promise<void> {
  await addFiles(fileInput.value?.files ?? [])
  if (fileInput.value) fileInput.value.value = ''
}

// onDrop 读取拖放到输入框的文件
async function onDrop(event: DragEvent): Promise<void> {
  dragging.value = false
  await addFiles(event.dataTransfer?.files ?? [])
}

// onPaste 读取粘贴的文件
async function onPaste(event: ClipboardEvent): Promise<void> {
  const files = event.clipboardData?.files
  if (files && files.length > 0) {
    event.preventDefault()
    await addFiles(files)
  }
}

// attachmentURL 返回图片附件的预览地址
function attachmentURL(part: Part): string {
  return `data:${part.inlineData?.mimeType};base64,${part.inlineData?.data}`
}

// tick 更新录音计时
function tick(started: number): void {
  const seconds = Math.floor((Date.now() - started) / 1000)
  elapsed.value = `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}

// startRecording 开始录音：attach 结束后作为音频附件，transcribe 结束后转写为文字
async function startRecording(mode: 'attach' | 'transcribe'): Promise<void> {
  if (recording.value !== '') {
    recorder?.stop()
    return
  }
  let stream: MediaStream
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: audioInput() })
  } catch {
    showNotice(t('pg.microphoneDenied'), 'error')
    return
  }
  const chunks: Blob[] = []
  recorder = new MediaRecorder(stream)
  recorder.ondataavailable = (event) => chunks.push(event.data)
  recorder.onstop = async () => {
    stream.getTracks().forEach((track) => track.stop())
    window.clearInterval(timer)
    const kind = recording.value
    recording.value = ''
    const audio = new Blob(chunks, { type: recorder?.mimeType || 'audio/webm' })
    if (kind === 'attach') session.attachments.push(await readFile(audio, audio.type.split(';')[0]))
    else await transcribe(audio)
  }
  recording.value = mode
  const started = Date.now()
  tick(started)
  timer = window.setInterval(() => tick(started), 500)
  recorder.start()
}

// transcribe 用本服务的转写接口把录音转为文字并追加到输入框
async function transcribe(audio: Blob): Promise<void> {
  transcribing.value = true
  try {
    const form = new FormData()
    form.append('file', audio, 'speech.webm')
    const headers = new Headers()
    const key = (config.value?.proxy_api_key ?? '').split(',')[0]?.trim() ?? ''
    if (key !== '') headers.set('Authorization', `Bearer ${key}`)
    const response = await fetch('/v1/audio/transcriptions', {
      method: 'POST',
      headers,
      body: form,
    })
    const value = (await response.json()) as { text?: string; error?: { message?: string } }
    if (!response.ok) throw new Error(value.error?.message ?? response.statusText)
    const text = value.text?.trim() ?? ''
    if (text !== '') session.draft = session.draft === '' ? text : `${session.draft} ${text}`
  } catch (error) {
    showNotice(error instanceof Error ? error.message : String(error), 'error')
  } finally {
    transcribing.value = false
  }
}

// addPhoto 把相机拍下的照片加入附件
async function addPhoto(photo: Blob): Promise<void> {
  session.attachments.push(await readFile(photo))
}

// addYouTube 把 YouTube 链接作为视频附件
function addYouTube(): void {
  const url = youtubeURL.value.trim()
  if (!/^https?:\/\/(www\.|m\.)?(youtube\.com|youtu\.be)\//.test(url)) {
    showNotice(t('pg.youtubeInvalid'), 'error')
    return
  }
  session.attachments.push({ fileData: { mimeType: 'video/*', fileUri: url } })
  youtubeURL.value = ''
  youtubeOpen.value = false
}

onUnmounted(() => {
  if (recorder?.state === 'recording') recorder.stop()
  window.clearInterval(timer)
  window.removeEventListener('resize', onWindowResize)
  window.clearTimeout(resizeTimer)
})

defineExpose({ focus })
</script>

<template>
  <div class="ms-prompt-box" @dragenter.prevent="dragging = true">
    <div
      class="dragging-overlay"
      :class="{ dragging }"
      @dragover.prevent
      @dragleave="dragging = false"
      @drop.prevent="onDrop"
    >
      <UiIcon name="addCircle" :size="24" />
      {{ t('pg.dropFiles') }}
    </div>
    <div class="prompt-box-container">
      <div v-if="session.attachments.length || preparing" class="multi-media-row">
        <div v-for="(part, index) in session.attachments" :key="index" class="attachment">
          <img
            v-if="part.inlineData?.mimeType.startsWith('image/')"
            :src="attachmentURL(part)"
            alt=""
            class="attachment-image"
          />
          <span v-else class="attachment-file">
            <UiIcon
              :name="
                part.fileData
                  ? driveNames.has(part.fileData.fileUri)
                    ? 'addToDrive'
                    : 'youtube'
                  : part.inlineData?.mimeType.startsWith('audio/')
                    ? 'mic'
                    : 'upload'
              "
              :size="18"
            />
            <span class="attachment-name">{{
              part.fileData
                ? (driveNames.get(part.fileData.fileUri) ?? part.fileData.fileUri)
                : part.inlineData?.mimeType
            }}</span>
          </span>
          <button
            type="button"
            ms-button
            class="attachment-remove ms-button-borderless ms-button-icon ms-button-small"
            :aria-label="t('pg.removeAttachment')"
            @click="session.attachments.splice(index, 1)"
          >
            <UiIcon name="close" :size="16" />
          </button>
        </div>
        <div v-for="index in preparing" :key="`preparing-${index}`" class="attachment">
          <span class="attachment-file attachment-preparing">{{ t('common.loading') }}…</span>
        </div>
      </div>
      <div class="textarea-row">
        <div class="text-wrapper">
          <textarea
            ref="textarea"
            v-model="session.draft"
            rows="1"
            class="textarea"
            :placeholder="t('pg.placeholder')"
            :aria-label="t('pg.enterPrompt')"
            @keydown="onKeydown"
            @paste="onPaste"
          ></textarea>
        </div>
      </div>
      <div class="buttons-row">
        <div class="button-row-left">
          <DropdownMenuRoot :modal="false">
            <DropdownMenuTrigger
              v-tooltip.below="t('pg.insertMedia')"
              ms-button
              class="add-media ms-button-filter-chip"
              :aria-label="t('pg.insertMedia')"
            >
              <UiIcon name="addCircle" :size="18" />
            </DropdownMenuTrigger>
            <DropdownMenuPortal>
              <DropdownMenuContent side="top" align="start" :side-offset="4" class="ms-menu-panel">
                <DropdownMenuItem class="ms-menu-item" @select="driveOpen = true">
                  <UiIcon name="addToDrive" :size="18" />
                  <span class="ms-menu-item-text">{{ t('pg.drive') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem class="ms-menu-item" @select="fileInput?.click()">
                  <UiIcon name="upload" :size="18" />
                  <span class="ms-menu-item-text">{{ t('pg.uploadFiles') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem class="ms-menu-item" @select="startRecording('attach')">
                  <UiIcon name="mic" :size="18" />
                  <span class="ms-menu-item-text">{{ t('pg.recordAudio') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem class="ms-menu-item" @select="camera?.start()">
                  <UiIcon name="camera" :size="18" />
                  <span class="ms-menu-item-text">{{ t('pg.camera') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem class="ms-menu-item" @select="youtubeOpen = true">
                  <UiIcon name="youtube" :size="18" />
                  <span class="ms-menu-item-text">{{ t('pg.youtubeVideo') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem class="ms-menu-item" @select="sampleOpen = true">
                  <UiIcon name="modeImage" :size="18" />
                  <span class="ms-menu-item-text">{{ t('pg.sampleMedia') }}</span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenuPortal>
          </DropdownMenuRoot>
          <button
            v-tooltip.below="t('pg.apiKey')"
            type="button"
            ms-button
            class="ms-button-filter-chip"
            :aria-label="t('pg.apiKey')"
            @click="openApiKey"
          >
            <UiIcon name="keyOff" :size="18" />
          </button>
          <input ref="fileInput" type="file" class="file-input" multiple @change="onFileChange" />
          <PopoverRoot v-if="tools.length && !comparing">
            <PopoverTrigger
              v-tooltip.below="toolsTooltip"
              ms-button
              class="tools-button ms-button-primary"
              :class="{
                'ms-button-icon': handset,
                'ms-button-active': handset && enabledTools.length,
              }"
              :aria-label="t('pg.openTools')"
            >
              <UiIcon name="tools" :size="18" />
              <template v-if="!handset">{{ t('pg.tools') }}</template>
            </PopoverTrigger>
            <PopoverPortal>
              <PopoverContent side="top" align="start" :side-offset="8" class="tools-dialog">
                <div v-for="tool in tools" :key="tool.id" class="tool-item">
                  <label class="tool-label" :for="`prompt-tool-${tool.id}`">
                    <UiIcon :name="tool.icon" :size="18" class="tool-icon" />
                    {{ t(tool.label) }}
                  </label>
                  <UiSwitch
                    :id="`prompt-tool-${tool.id}`"
                    :model-value="tool.enabled"
                    :disabled="tool.disabled"
                    @update:model-value="setTool(tool.id, $event)"
                  />
                </div>
              </PopoverContent>
            </PopoverPortal>
          </PopoverRoot>
          <div v-if="enabledTools.length && !handset && !comparing" class="enabled-tool-container">
            <div v-for="tool in enabledTools" :key="tool.id" class="enabled-tool">
              <span class="tool-chip">
                <UiIcon :name="tool.icon" :size="16" class="tool-icon" />
                <span class="tool-name">{{ t(tool.label) }}</span>
              </span>
              <button
                v-tooltip.below="t('pg.removeTool').replace('{tool}', t(tool.label))"
                type="button"
                ms-button
                class="ms-button-borderless ms-button-icon ms-button-small"
                :aria-label="t('pg.removeTool').replace('{tool}', t(tool.label))"
                @click="setTool(tool.id, false)"
              >
                <UiIcon name="close" :size="16" />
              </button>
            </div>
          </div>
        </div>
        <div class="button-wrapper">
          <span v-if="recording" class="recording-time">{{ elapsed }}</span>
          <button
            v-tooltip.below="
              recording === 'transcribe' ? t('pg.stopRecording') : t('pg.speechToText')
            "
            type="button"
            ms-button
            class="stt-button ms-button-filter-chip"
            :class="{ recording: recording !== '' }"
            :aria-label="t('pg.speechToText')"
            :aria-disabled="transcribing || recording === 'attach'"
            @click="!transcribing && recording !== 'attach' && startRecording('transcribe')"
          >
            <UiIcon
              :name="transcribing ? 'spinner' : recording === 'transcribe' ? 'stop' : 'mic'"
              :size="18"
              :class="{ spin: transcribing }"
            />
          </button>
          <button
            v-if="recording === 'attach'"
            type="button"
            ms-button
            class="ms-button-primary"
            @click="recorder?.stop()"
          >
            <UiIcon name="stop" :size="18" />
            {{ t('pg.stopRecording') }}
          </button>
          <button
            v-tooltip="runTooltip"
            type="button"
            ms-button
            class="run-button ms-button-primary"
            :aria-label="running ? t('pg.stop') : t('pg.run')"
            :aria-disabled="!running && !canRun"
            @click="submit"
          >
            <template v-if="running">
              <UiIcon name="refresh" :size="18" class="spin" />
              <span class="run-button-label">{{ t('pg.stop') }}</span>
            </template>
            <template v-else>
              <span class="run-button-label">{{ t('pg.run') }}</span>
              <RunKeyHint />
            </template>
          </button>
        </div>
      </div>
    </div>
    <UiDialog v-model:open="youtubeOpen" :title="t('pg.youtubeVideo')" width="480px">
      <label for="youtube-url">{{ t('pg.youtubeURL') }}</label>
      <input
        id="youtube-url"
        v-model="youtubeURL"
        ms-input
        placeholder="https://www.youtube.com/watch?v="
        @keydown.enter.prevent="addYouTube"
      />
      <template #actions>
        <button type="button" ms-button class="ms-button-borderless" @click="youtubeOpen = false">
          {{ t('common.cancel') }}
        </button>
        <button type="button" ms-button class="ms-button-primary" @click="addYouTube">
          {{ t('pg.add') }}
        </button>
      </template>
    </UiDialog>
    <CameraDialog ref="camera" @capture="addPhoto" />
    <DriveDialog v-model:open="driveOpen" @select="addDriveFile" />
    <SampleMediaDialog v-model:open="sampleOpen" @select="addSample" />
  </div>
</template>

<style scoped>
.ms-prompt-box {
  position: relative;
  z-index: 1;
  display: block;
  width: 100%;
  max-width: 1000px;
  margin: 0 auto;
}

.dragging-overlay {
  position: absolute;
  z-index: 10;
  right: 0;
  bottom: 0;
  left: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 174px;
  margin: 0 12px 12px;
  border: 4px dashed var(--color-v3-text-link);
  border-radius: 12px;
  backdrop-filter: blur(5px);
  background-color: var(--color-v3-overlay-background);
  box-shadow: var(--shadow-v3-lg);
  font-weight: 500;
  line-height: 21px;
  pointer-events: none;
  visibility: hidden;
}

.dragging-overlay.dragging {
  pointer-events: auto;
  visibility: visible;
  animation: fade-in-scale-up 0.2s ease-in-out forwards;
}

.prompt-box-container {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 0 12px 12px;
  padding: 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container-high);
  box-shadow: var(--shadow-v3-md);
  transition: box-shadow 0.2s ease-in-out;
}

.multi-media-row {
  display: flex;
  flex-wrap: nowrap;
  gap: 8px;
  margin: 0 -2px;
  padding: 2px;
  overflow-x: auto;
}

.attachment {
  position: relative;
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 4px;
  max-width: 240px;
  padding: 4px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  background-color: var(--color-v3-surface-container);
}

.attachment-image {
  width: 56px;
  height: 56px;
  border-radius: 4px;
  object-fit: cover;
}

.attachment-file {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  padding-left: 4px;
  font-size: 12px;
  line-height: 18px;
}

.attachment-preparing {
  height: 56px;
  padding-inline: 12px;
  color: var(--color-v3-text-var);
}

.attachment-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.textarea-row {
  display: flex;
  align-items: flex-start;
}

.text-wrapper {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 36px;
}

.textarea {
  flex: 1 1 0;
  width: 100%;
  min-width: 0;
  min-height: 21px;
  max-height: 210px;
  padding: 0;
  border: none;
  background-color: transparent;
  color: var(--color-v3-text);
  font-size: 14px;
  line-height: 21px;
  outline: none;
  overflow-wrap: anywhere;
  resize: none;
  white-space: pre-wrap;
}

.textarea::placeholder {
  color: var(--color-v3-text-var);
}

.buttons-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.button-row-left {
  display: flex;
  flex-grow: 1;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.add-media,
.stt-button {
  width: 32px;
  padding: 8px 6px;
  border-color: var(--color-v3-outline);
  background: transparent;
}

.stt-button.recording {
  border-color: var(--color-v3-text-link);
  color: var(--color-v3-text-link);
}

.file-input {
  display: none;
}

.tools-button {
  flex-shrink: 0;
}

.enabled-tool-container {
  display: flex;
  gap: 4px;
  min-width: 0;
  padding-inline: 0;
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

.enabled-tool [ms-button]:hover {
  background-color: var(--color-v3-button-container-accent-hover);
}

.button-wrapper {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 8px;
  height: 36px;
  margin-left: 8px;
}

.recording-time {
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.run-button .run-button-label {
  min-width: 28px;
}

.spin {
  animation: spin 0.66s linear infinite;
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

@keyframes fade-in-scale-up {
  from {
    opacity: 0;
    transform: scale(0.9);
  }
}

@keyframes spin {
  to {
    transform: rotate(1turn);
  }
}
</style>

<style>
.tools-dialog {
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
</style>
