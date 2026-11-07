<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
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
import AgentDisclaimer from './AgentDisclaimer.vue'
import {
  attachmentKind,
  attachmentName,
  attachmentText,
  downloadAttachment,
  useAttachmentPreview,
} from './attachment'
import ApiKeyButton from './ApiKeyButton.vue'
import CameraDialog from './CameraDialog.vue'
import DriveDialog from './DriveDialog.vue'
import SampleMediaDialog, { type SampleSelection } from './SampleMediaDialog.vue'
import TokenCapDialog from './TokenCapDialog.vue'
import { Capability, hasCode, isAgent, isOmniModel } from './catalog'
import { driveNames, uploadDriveFile, type DriveFile } from './drive'
import { fileMIME } from './file-types'
import { readImage } from './media/api'
import MediaViewDialog from './media/MediaViewDialog.vue'
import VideoSettingsDialog from './media/VideoSettingsDialog.vue'
import { useCompare, usePlayground } from './session'
import { useAudioRecording } from './recording'
import { useMediaQuery } from './media-query'
import { dataURL, protocolSupports } from './protocols'
import { failureText } from './request'
import RunKeyHint from './RunKeyHint.vue'
import { appends, submits, useRunTooltip } from './run-keys'
import { useTools } from './tools'
import type { Part } from './types'

const { t, tokenLabel, locale } = useI18n()
const { showNotice } = useConsole()
const { session, currentModel, countAttachment } = usePlayground()
const { panes, comparing, running, submitAll, appendAll, stopAll } = useCompare()
const { tools, setTool } = useTools()

const textarea = ref<HTMLTextAreaElement>()
const fileInput = ref<HTMLInputElement>()
const dragging = ref(false)
const youtubeOpen = ref(false)
const driveOpen = ref(false)
const sampleOpen = ref(false)
// preparing 为正在读取的附件与正在下载的示例图片数，完成前与官网一样不能运行
const preparing = ref(0)

// addDriveFile 把从 Drive 选中的已上传文件作为文件引用加入附件，与本地附件一样记录文件名
function addDriveFile(file: DriveFile): void {
  const name = file.displayName || file.name
  driveNames.set(file.uri, name)
  attach({ fileData: { mimeType: file.mimeType, fileUri: file.uri }, fileName: name })
}

// attach 与官网一致：附件加入后单独统计它的 token 数
function attach(part: Part): void {
  session.attachments.push(part)
  void countAttachment(session.attachments.at(-1)!)
}

// addSample 与官网一致：示例图片下载后作为图片附件，示例视频作为 YouTube 视频附件
async function addSample(selection: SampleSelection): Promise<void> {
  if ('video' in selection) {
    attach({
      fileData: {
        mimeType: 'video/*',
        fileUri: `https://www.youtube.com/watch?v=${selection.video.id}`,
      },
      fileName: selection.video.name,
    })
    return
  }
  preparing.value++
  try {
    const response = await fetch(selection.image)
    if (!response.ok) throw new Error(`${response.status} ${selection.image}`)
    attach({ inlineData: await readImage(await response.blob()) })
  } catch (error) {
    showNotice(failureText(error, t('pg.sampleImageFailed'), t('pg.sampleImageFailed')), 'error')
  } finally {
    preparing.value--
  }
}

const youtubeURL = ref('')
const camera = ref<InstanceType<typeof CameraDialog>>()
const recordingMode = ref<'attach' | 'transcribe'>('transcribe')
const {
  recording: audioRecording,
  opening,
  transcribing,
  start: startAudio,
  stop: stopRecording,
  transcribe,
} = useAudioRecording(async (audio) => {
  window.clearInterval(timer)
  if (recordingMode.value === 'attach') attach(await readFile(audio, audio.type.split(';')[0]))
  else {
    const text = await transcribe(audio)
    if (text !== '') session.draft = session.draft === '' ? text : `${session.draft} ${text}`
  }
})
// recording 表示正在录音：attach 为插入音频附件，transcribe 为语音转文字
const recording = computed(() => (audioRecording.value ? recordingMode.value : ''))
const elapsed = ref('')
let timer = 0

// enabledTools 是已开启并显示为标签的工具
const enabledTools = computed(() => tools.value.filter((tool) => tool.enabled))
// filesystem 表示带远程执行环境的 agent 始终启用文件系统工具，标签不能移除
const filesystem = computed(() => hasCode(currentModel.value, Capability.RemoteEnvironment))
// toolRow 是已启用工具的标签行，scrollable 记录左右两侧是否还有被遮住的标签
const toolRow = ref<HTMLElement>()
const scrollable = ref({ start: false, end: false })
// measureTools 按滚动位置更新左右滚动按钮
function measureTools(): void {
  const row = toolRow.value
  if (!row) return
  scrollable.value = {
    start: row.scrollLeft > 0,
    end: row.scrollLeft + row.clientWidth < row.scrollWidth - 1,
  }
}
// scrollTools 向左或向右滚动一屏标签
function scrollTools(direction: number): void {
  toolRow.value!.scrollBy({ left: direction * toolRow.value!.clientWidth, behavior: 'smooth' })
}
const toolObserver = new ResizeObserver(measureTools)
watch(toolRow, (row, previous) => {
  if (previous) toolObserver.unobserve(previous)
  if (row) toolObserver.observe(row)
})
watch([enabledTools, filesystem], () => void nextTick(measureTools))
onUnmounted(() => toolObserver.disconnect())
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

// placeholder 与官网一致，agent 的占位文字为看看 agent 能做什么
const placeholder = computed(() =>
  t(isAgent(currentModel.value) ? 'pg.agentPlaceholder' : 'pg.placeholder'),
)
// disclaimer 表示与官网一样为带远程执行环境的 agent 显示运行提示与 token 上限按钮
const disclaimer = computed(
  () => !comparing.value && hasCode(currentModel.value, Capability.RemoteEnvironment),
)
// inputs 是模型可接受的附件类型，目录能力码 3、4、5 分别表示图片、视频与音频
const inputs = computed(() => ({
  image: hasCode(currentModel.value, 3),
  video: hasCode(currentModel.value, 4),
  audio: hasCode(currentModel.value, 5),
}))
// insertLabel 是附件按钮的说明，列出模型可接受的附件类型
const insertLabel = computed(() => {
  const types = [
    inputs.value.image && t('pg.mediaImages'),
    inputs.value.video && t('pg.mediaVideos'),
    inputs.value.audio && t('pg.mediaAudio'),
    t('pg.mediaFiles'),
  ].filter((item): item is string => Boolean(item))
  const list = new Intl.ListFormat(locale.value, { style: 'long', type: 'disjunction' })
  return t('pg.insertMedia').replace('{types}', list.format(types))
})
const tokenCapOpen = ref(false)

// placeholderHeight 是占位文字在当前宽度下的高度，输入框宽度、网页字体或界面语言变化后重新测量
let placeholderHeight: number | undefined

// resize 与官网 cdkTextareaAutosize 一致：输入框高度取内容与占位文字中较高者，超过 210 px 后在框内滚动
function resize(): void {
  const element = textarea.value!
  if (element.clientHeight >= 210 && element.scrollHeight > element.clientHeight) return
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

// measuredWidth 是上次测量占位文字时输入框的宽度，页面未显示时为 0
let measuredWidth = 0
// measureWidth 在输入框宽度变化后重新测量，未显示时宽度为 0 不测量
function measureWidth(width: number): void {
  if (width === 0 || width === measuredWidth) return
  measuredWidth = width
  remeasure()
}
const widthObserver = new ResizeObserver(([entry]) => measureWidth(entry!.contentRect.width))
// onFontsLoaded 在网页字体载入后按新字形重新测量，页面未显示时留到再次显示
function onFontsLoaded(): void {
  measuredWidth = 0
  measureWidth(textarea.value!.clientWidth)
}
watch(
  () => session.draft,
  () => void nextTick(resize),
)
watch(placeholder, remeasure, { flush: 'post' })
onMounted(() => {
  widthObserver.observe(textarea.value!)
  document.fonts.addEventListener('loadingdone', onFontsLoaded)
})

// focus 聚焦输入框
function focus(): void {
  textarea.value!.focus()
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
    textarea.value!.blur()
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
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

// addFiles 把文件加入待发送附件，读取期间显示占位，文件附件随后统计 token 数；与官网一样 Gemini API 协议下的视频先上传 Drive，生成时引用该文件
async function addFiles(files: Iterable<File>): Promise<void> {
  const list = [...files]
  preparing.value += list.length
  for (const file of list) {
    try {
      const part = await readFile(file, fileMIME(file))
      part.fileName = file.name
      if (videoOptions.value && attachmentKind(part) === 'video') {
        const uploaded = await uploadDriveFile(file, file.name)
        part.fileData = { mimeType: uploaded.mimeType, fileUri: uploaded.uri }
        part.fileExpires = Date.parse(uploaded.expirationTime)
      }
      attach(part)
    } catch (error) {
      showNotice(failureText(error, t('pg.uploadFailed'), t('pg.uploadFailed')), 'error')
    } finally {
      preparing.value--
    }
  }
}

// onFileChange 读取文件选择框中的文件
async function onFileChange(): Promise<void> {
  await addFiles(fileInput.value!.files!)
  fileInput.value!.value = ''
}

// onDrop 读取拖放到输入框的文件
async function onDrop(event: DragEvent): Promise<void> {
  dragging.value = false
  await addFiles(event.dataTransfer!.files)
}

// largePaste 是官网 msPasteLargeTextAsFile 的长文字数：粘贴的纯文本超过它时作为文本附件加入
const largePaste = 25000

// pasteName 与官网一致以粘贴时刻的 en-US 日期命名长文附件，如 Paste October 28, 2026 - 10:27PM
function pasteName(date = new Date()): string {
  const month = date.toLocaleString('en-US', { month: 'long' })
  const day = String(date.getDate()).padStart(2, '0')
  const minute = String(date.getMinutes()).padStart(2, '0')
  const meridiem = date.getHours() < 12 ? 'AM' : 'PM'
  return `Paste ${month} ${day}, ${date.getFullYear()} - ${date.getHours() % 12 || 12}:${minute}${meridiem}`
}

// onPaste 读取粘贴的文件；与官网一致，超过 largePaste 字的纯文本作为文本附件加入，不写入输入框
async function onPaste(event: ClipboardEvent): Promise<void> {
  const { files } = event.clipboardData!
  if (files.length > 0) {
    event.preventDefault()
    await addFiles(files)
    return
  }
  const text = event.clipboardData!.getData('text/plain')
  if (text.length > largePaste) {
    event.preventDefault()
    await addFiles([new File([text], pasteName(), { type: 'text/plain' })])
  }
}

// textPart 表示片段是可以转回输入框文字的文本附件
function textPart(part: Part): boolean {
  return part.inlineData?.mimeType.startsWith('text/') ?? false
}

// inlineAttachment 与官网 Inline media 一致：文本附件的内容接到输入框文字之后，两段之间空一行，并移除该附件
async function inlineAttachment(index: number): Promise<void> {
  const part = session.attachments[index]!
  const text = await attachmentText(part)
  const at = session.attachments.indexOf(part)
  if (at === -1) return
  session.attachments.splice(at, 1)
  session.draft = [session.draft, text].filter(Boolean).join('\n\n')
}

// preview 是附件预览对话框：与官网一样点击图片与文本附件的缩略图打开，视频与 PDF 同样可以打开
const { preview, show: showAttachment, release: releasePreview } = useAttachmentPreview()

// videoSettings 是视频附件的 Video settings 对话框：与官网一样 Gemini API 协议下的视频附件可设置截取、帧率与 Agentic 模式
const videoSettings = ref<{ open: boolean; part?: Part }>({ open: false })
const videoOptions = computed(() => protocolSupports(session.settings.protocol, 'videoSettings'))
const agenticVideo = computed(() => hasCode(currentModel.value, 69))

// saveVideoSettings 以保存的视频设置替换附件原有设置，与官网一样按新设置重新统计 token 数
function saveVideoSettings(
  settings: Pick<Part, 'videoMetadata' | 'mediaProcessing' | 'fileName'>,
): void {
  const part = videoSettings.value.part!
  delete part.videoMetadata
  Object.assign(part, settings)
  void countAttachment(part)
}

// playing 是正在播放的音频附件，与官网 ms-prompt-audio 一样点击播放图标播放、再次点击暂停
const playing = ref<Part>()
const player = new Audio()
player.addEventListener('pause', () => (playing.value = undefined))
player.addEventListener('ended', () => (playing.value = undefined))

// toggleAudio 播放或暂停音频附件
function toggleAudio(part: Part): void {
  if (playing.value === part) {
    player.pause()
    return
  }
  player.src = dataURL(part.inlineData!)
  void player.play()
  playing.value = part
}

// removeAttachment 移除附件，正在播放的音频随之停止
function removeAttachment(index: number): void {
  if (playing.value === session.attachments[index]) player.pause()
  session.attachments.splice(index, 1)
}

// tick 更新录音计时
function tick(started: number): void {
  const seconds = Math.floor((Date.now() - started) / 1000)
  elapsed.value = `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}

// startRecording 开始录音：attach 结束后作为音频附件，transcribe 结束后转写为文字
async function startRecording(mode: 'attach' | 'transcribe'): Promise<void> {
  if (opening.value) return
  if (recording.value !== '') {
    stopRecording()
    return
  }
  if (!(await startAudio())) return
  recordingMode.value = mode
  const started = Date.now()
  tick(started)
  timer = window.setInterval(() => tick(started), 500)
}

// addPhoto 把相机拍下的照片加入附件
async function addPhoto(photo: Blob): Promise<void> {
  attach(await readFile(photo))
}

// addYouTube 把 YouTube 链接作为视频附件
function addYouTube(): void {
  const url = youtubeURL.value.trim()
  if (!/^https?:\/\/(www\.|m\.)?(youtube\.com|youtu\.be)\//.test(url)) {
    showNotice(t('pg.youtubeInvalid'), 'error')
    return
  }
  attach({ fileData: { mimeType: 'video/*', fileUri: url } })
  youtubeURL.value = ''
  youtubeOpen.value = false
}

onUnmounted(() => {
  player.pause()
  releasePreview()
  window.clearInterval(timer)
  widthObserver.disconnect()
  document.fonts.removeEventListener('loadingdone', onFontsLoaded)
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
      <div class="textarea-row">
        <div class="text-wrapper">
          <textarea
            ref="textarea"
            v-model="session.draft"
            rows="1"
            class="textarea"
            :placeholder="placeholder"
            :aria-label="t('pg.enterPrompt')"
            @keydown="onKeydown"
            @paste="onPaste"
          ></textarea>
        </div>
        <AgentDisclaimer
          v-if="disclaimer"
          :running="session.running"
          :token-cap="session.settings.maxTotalTokens"
          @token-cap="tokenCapOpen = true"
        />
      </div>
      <div v-if="session.attachments.length || preparing" class="multi-media-row">
        <div v-for="(part, index) in session.attachments" :key="index" class="attachment">
          <div class="preview-container">
            <img
              v-if="attachmentKind(part) === 'image'"
              :src="dataURL(part.inlineData!)"
              :alt="attachmentName(part)"
              class="attachment-image"
              @click="showAttachment(part)"
            />
            <video
              v-else-if="attachmentKind(part) === 'video'"
              :src="dataURL(part.inlineData!)"
              :aria-label="t('pg.preview')"
              preload="metadata"
              muted
              @click="showAttachment(part)"
            ></video>
            <button
              v-else-if="attachmentKind(part) === 'audio'"
              type="button"
              class="thumbnail-icon interactive"
              :aria-label="playing === part ? t('pg.pause') : t('pg.play')"
              @click="toggleAudio(part)"
            >
              <UiIcon :name="playing === part ? 'pauseCircle' : 'playCircle'" :size="24" />
            </button>
            <button
              v-else-if="attachmentKind(part) !== ''"
              type="button"
              class="thumbnail-icon interactive"
              :aria-label="t('pg.preview')"
              @click="showAttachment(part)"
            >
              <UiIcon name="document" :size="24" />
            </button>
            <UiIcon
              v-else
              :name="
                part.fileData
                  ? driveNames.has(part.fileData.fileUri)
                    ? 'addToDrive'
                    : 'youtube'
                  : 'document'
              "
              :size="24"
              class="thumbnail-icon"
            />
          </div>
          <div class="content-container">
            <span v-tooltip.below="attachmentName(part)" class="attachment-name">{{
              attachmentName(part)
            }}</span>
            <span v-if="part.tokenCount !== undefined" class="attachment-tokens">{{
              tokenLabel(part.tokenCount)
            }}</span>
          </div>
          <div class="actions-container">
            <template v-if="videoOptions && attachmentKind(part) === 'video' && part.inlineData">
              <button
                v-tooltip.below="t('pg.editVideoOptions')"
                type="button"
                ms-button
                class="attachment-video-settings action-button ms-button-borderless ms-button-icon"
                :aria-label="t('pg.editVideoOptions')"
                @click="videoSettings = { open: true, part }"
              >
                <UiIcon name="settings" :size="20" />
              </button>
              <div class="divider"></div>
            </template>
            <button
              v-if="textPart(part)"
              v-tooltip.below="t('pg.inlineAttachment')"
              type="button"
              ms-button
              class="attachment-inline action-button ms-button-borderless ms-button-icon"
              :aria-label="t('pg.inlineAttachment')"
              @click="inlineAttachment(index)"
            >
              <UiIcon name="inlineText" :size="20" />
            </button>
            <button
              v-tooltip.below="t('pg.removeAttachment')"
              type="button"
              ms-button
              class="attachment-remove action-button ms-button-borderless ms-button-icon"
              :aria-label="t('pg.removeAttachment')"
              @click="removeAttachment(index)"
            >
              <UiIcon name="close" :size="20" />
            </button>
          </div>
        </div>
        <div v-for="index in preparing" :key="`preparing-${index}`" class="attachment">
          <div class="preview-container">
            <UiIcon name="spinner" :size="24" class="thumbnail-icon spin" />
          </div>
          <span class="attachment-preparing">{{ t('common.loading') }}…</span>
        </div>
      </div>
      <div class="buttons-row">
        <div class="button-row-left">
          <DropdownMenuRoot :modal="false">
            <DropdownMenuTrigger
              v-tooltip.below="insertLabel"
              ms-button
              class="add-media ms-button-filter-chip"
              :aria-label="insertLabel"
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
                <DropdownMenuItem
                  v-if="inputs.audio"
                  class="ms-menu-item"
                  @select="startRecording('attach')"
                >
                  <UiIcon name="mic" :size="18" />
                  <span class="ms-menu-item-text">{{ t('pg.recordAudio') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem
                  v-if="inputs.image || inputs.video"
                  class="ms-menu-item"
                  @select="camera?.start()"
                >
                  <UiIcon name="camera" :size="18" />
                  <span class="ms-menu-item-text">{{ t('pg.camera') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem
                  v-if="inputs.video && !isOmniModel(currentModel)"
                  class="ms-menu-item"
                  @select="youtubeOpen = true"
                >
                  <UiIcon name="youtube" :size="18" />
                  <span class="ms-menu-item-text">{{ t('pg.youtubeVideo') }}</span>
                </DropdownMenuItem>
                <DropdownMenuItem
                  v-if="inputs.image || inputs.video"
                  class="ms-menu-item"
                  @select="sampleOpen = true"
                >
                  <UiIcon name="modeImage" :size="18" />
                  <span class="ms-menu-item-text">{{ t('pg.sampleMedia') }}</span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenuPortal>
          </DropdownMenuRoot>
          <ApiKeyButton />
          <input ref="fileInput" type="file" class="file-input" multiple @change="onFileChange" />
          <button
            v-if="!tools.length && !comparing"
            type="button"
            ms-button
            class="tools-button ms-button-primary"
            :class="{ 'ms-button-icon': handset }"
            :aria-label="t('pg.openTools')"
            aria-disabled="true"
          >
            <UiIcon name="tools" :size="18" />
            <template v-if="!handset">{{ t('pg.tools') }}</template>
          </button>
          <PopoverRoot v-else-if="!comparing">
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
          <div
            v-if="(enabledTools.length || filesystem) && !handset && !comparing"
            class="enabled-tool-scroll"
          >
            <button
              v-if="scrollable.start"
              type="button"
              ms-button
              class="chevron-btn left ms-button-borderless ms-button-icon"
              :aria-label="t('pg.scrollLeft')"
              @click="scrollTools(-1)"
            >
              <UiIcon name="chevronLeft" :size="20" />
            </button>
            <div ref="toolRow" class="enabled-tool-container" @scroll="measureTools">
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
              <div v-if="filesystem" class="enabled-tool">
                <span class="tool-chip">
                  <UiIcon name="folder" :size="16" class="tool-icon" />
                  <span class="tool-name">{{ t('pg.filesystemTools') }}</span>
                </span>
              </div>
            </div>
            <button
              v-if="scrollable.end"
              type="button"
              ms-button
              class="chevron-btn right ms-button-borderless ms-button-icon"
              :aria-label="t('pg.scrollRight')"
              @click="scrollTools(1)"
            >
              <UiIcon name="chevronRight" :size="20" />
            </button>
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
            :aria-disabled="transcribing || opening || recording === 'attach'"
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
            @click="stopRecording"
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
            <span class="run-state stop-state" :class="{ inactive: !running }">
              <UiIcon v-if="running" name="refresh" :size="18" class="spin" />
              <span class="run-button-label">{{ t('pg.stop') }}</span>
            </span>
            <span class="run-state" :class="{ inactive: running }">
              <span class="run-button-label">{{ t('pg.run') }}</span>
              <RunKeyHint />
            </span>
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
    <MediaViewDialog
      v-model:open="preview.open"
      :title="preview.part ? attachmentName(preview.part) : ''"
      :kind="preview.kind"
      :src="preview.src"
      :text="preview.text"
      @download="preview.part && downloadAttachment(preview.part)"
    />
    <VideoSettingsDialog
      v-if="videoSettings.part"
      v-model:open="videoSettings.open"
      :part="videoSettings.part"
      :src="dataURL(videoSettings.part.inlineData!)"
      :name="attachmentName(videoSettings.part)"
      :agentic="agenticVideo"
      @save="saveVideoSettings"
    />
    <CameraDialog ref="camera" @capture="addPhoto" />
    <DriveDialog v-model:open="driveOpen" @select="addDriveFile" />
    <SampleMediaDialog v-model:open="sampleOpen" @select="addSample" />
    <TokenCapDialog
      v-if="disclaimer"
      v-model:open="tokenCapOpen"
      v-model:cap="session.settings.maxTotalTokens"
    />
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

/* attachment 与官网 ms-prompt-media 一致：56 px 高的附件块，32 px 预览、文件名与 token 数、操作按钮 */
.attachment {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 8px;
  width: fit-content;
  min-width: 180px;
  max-width: 280px;
  height: 56px;
  padding: 8px 12px;
  overflow: hidden;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container);
  cursor: default;
}

.preview-container {
  position: relative;
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  overflow: hidden;
  border-radius: 4px;
  background-color: var(--color-v3-surface-container-highest);
}

.preview-container :is(img, video) {
  width: 100%;
  height: 100%;
  border-radius: 4px;
  cursor: pointer;
  object-fit: cover;
}

.thumbnail-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: none;
  background: none;
  color: inherit;
  pointer-events: none;
}

.thumbnail-icon.interactive {
  cursor: pointer;
  pointer-events: auto;
}

.content-container {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  justify-content: center;
  min-width: 0;
}

.attachment-name {
  overflow: hidden;
  color: var(--color-v3-text);
  font-size: 12px;
  line-height: normal;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.attachment-tokens,
.attachment-preparing {
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 16px;
}

.actions-container {
  display: flex;
  flex-shrink: 0;
  align-items: center;
}

.divider {
  width: 1px;
  height: 24px;
  background-color: var(--color-v3-outline-var);
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
  scrollbar-gutter: stable;
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

.enabled-tool-scroll {
  display: flex;
  align-items: center;
  min-width: 0;
}

.enabled-tool-container {
  display: flex;
  gap: 4px;
  min-width: 0;
  padding-inline: 0;
  overflow-x: auto;
  scrollbar-width: none;
}

.chevron-btn {
  flex-shrink: 0;
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
  position: relative;
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 8px;
  height: 36px;
  margin-left: 8px;
}

.recording-time {
  position: absolute;
  right: calc(100% + 8px);
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.run-button .run-button-label {
  min-width: 28px;
}

.run-button {
  position: relative;
}

.run-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
}

.stop-state {
  position: absolute;
  inset: 0;
}

.run-state.inactive {
  visibility: hidden;
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
