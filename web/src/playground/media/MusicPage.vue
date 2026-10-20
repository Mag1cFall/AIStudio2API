<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import { useMediaQuery } from '../media-query'
import type { IconName } from '@/playground/ui/icons'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiDialog from '@/playground/ui/UiDialog.vue'
import CodeDialog from '../CodeDialog.vue'
import RunSettingsFrame from '../RunSettingsFrame.vue'
import ModelCard from '../ModelCard.vue'
import AddImageButton from './AddImageButton.vue'
import AudioFooter from './AudioFooter.vue'
import CopyPromptButton from './CopyPromptButton.vue'
import MediaLayout from './MediaLayout.vue'
import ModelEmptyState, { type Example, type Starter } from './ModelEmptyState.vue'
import MusicComposer from './MusicComposer.vue'
import RealtimeMusic from './RealtimeMusic.vue'
import RealtimeMusicSettings from './RealtimeMusicSettings.vue'
import SttButton from './SttButton.vue'
import { readImage } from './api'
import {
  musicCode,
  musicCodeOptions,
  realtimeMusicCode,
  type MusicCodeLanguage,
} from './music-code'
import { useRealtimeMusic } from './realtime-music'
import { useMusic, type MusicView } from './music'

const route = useRoute()
const { t } = useI18n()
const store = useConsole()
const music = useMusic()
const { state, currentModel } = music
const codeOpen = ref(false)
const confirmNew = ref(false)
const dragging = ref(false)
// maxImages 是音乐提示可附带的图片上限
const maxImages = 10

// MusicStarter 是带示例音频的示例提示词
interface MusicStarter extends Starter {
  audio: string
}

// MusicExample 是带示例音频的示例卡片
interface MusicExample extends Example {
  audio: string
}

// zero 是官网音乐页空态的示例
const zero = {
  starters: [
    {
      title: 'Compose a cinematic orchestral hybrid score',
      prompt:
        'Born for This Moment: [0:00-0:20] Lone close-mic cello and distant heartbeat timpani in D minor. [0:20-0:50] Spiccato string ostinatos and low French horn warning calls build momentum. [0:50-1:30] Thunderous taiko drums, brass fanfare, and soaring strings erupt in a triumphant major key.',
      audio: 'https://www.gstatic.com/aistudio/lyria/zero-state/born-for-this-moment.wav',
    },
    {
      title: 'Produce a 90s indie alt-rock anthem with vocals',
      prompt:
        'Smiling Through the Static: Bright jangly 90s alternative rock at 124 BPM with dual chorus-infused electric guitars, mahogany acoustic strumming, tambourine backbeat, fuzzy walking bass, and a soulful female alto belting an anthemic chorus.',
      audio: 'https://www.gstatic.com/aistudio/lyria/zero-state/smiling-through-the-static.wav',
    },
    {
      title: 'Build a peak-time big-room EDM festival drop',
      prompt:
        'Again and Again: Driving 128 BPM club track anchored by a punchy four-on-the-floor kick, resonant low-pass filter sweep on a metallic synth pluck, side-chained sub-bass, and an euphoric supersaw chorus.',
      audio: 'https://www.gstatic.com/aistudio/lyria/zero-state/again-and-again.wav',
    },
  ] satisfies MusicStarter[],
  examples: [
    {
      title: 'Make a wake-up track',
      prompt:
        'Create a gentle wake-up track with warm analog synth pads, plucked harp harmonics, and a sunlit melody that gradually brightens over 30 seconds at 108 BPM. No vocals.',
      preview: 'https://www.gstatic.com/aistudio/zero-state/lyria_3_orchestra_cover.webp',
      audio: 'https://www.gstatic.com/aistudio/lyria/zero-state/lyria-song-4.mp3',
    },
    {
      title: 'Score a rhythm game',
      prompt:
        'Create an energetic rhythm-game soundtrack with crisp punchy drums, side-chained FM bass, and a driving 124 BPM arcade synth lead. No vocals.',
      preview: 'https://www.gstatic.com/aistudio/zero-state/lyria_3_synthwave_cover.webp',
      audio: 'https://www.gstatic.com/aistudio/lyria/zero-state/again-and-again.wav',
    },
  ] satisfies MusicExample[],
}

// TipSegment 是提示卡片中的一段文字，quote 为引号中的示例
interface TipSegment {
  text: string
  quote?: boolean
}

// tips 是官网音乐编辑器的两组提示，每次打开编辑器各随机取一条
const tips: { title: string; icon: IconName; items: TipSegment[][] }[] = [
  {
    title: 'Pro tip: Structure',
    icon: 'modeSpeech',
    items: [
      [
        { text: 'Use ' },
        { text: '[mm:ss – mm:ss]', quote: true },
        { text: ' to define segments like Intro, Build, or Chorus.' },
      ],
      [
        { text: 'Outline flow with arrows: ' },
        { text: '[Intro] -> [Verse] -> [Chorus] -> [Bridge] -> [Outro]', quote: true },
        { text: '.' },
      ],
      [
        {
          text: 'Build tension in the pre-chorus, then drop to silence before an explosive chorus.',
        },
      ],
      [
        {
          text: 'Start with a quiet piano intro, build into a loud verse, then explode into the chorus.',
        },
      ],
      [
        {
          text: 'Add a gradual crescendo, layering one instrument at a time until a wall of sound.',
        },
      ],
    ],
  },
  {
    title: 'Pro tip: Details',
    icon: 'modeMusic',
    items: [
      [
        { text: 'Specify ' },
        { text: 'A major', quote: true },
        { text: ' or ' },
        { text: '120 BPM', quote: true },
        { text: ' for technical precision.' },
      ],
      [
        { text: 'Lead your prompt with a genre — try ' },
        { text: 'early 90s hip-hop', quote: true },
        { text: ' or ' },
        { text: '80s synth-pop', quote: true },
        { text: '.' },
      ],
      [
        { text: 'Mix genres for unique sounds, like ' },
        { text: 'a fusion of metal and rap', quote: true },
        { text: '.' },
      ],
      [
        { text: 'Describe specific instruments: ' },
        { text: 'warm analog synth pads under a dry acoustic guitar', quote: true },
        { text: '.' },
      ],
      [
        { text: 'Use ' },
        { text: '3/4 waltz time', quote: true },
        { text: ' or ' },
        { text: '6/8 blues shuffle', quote: true },
        { text: ' for a specific rhythmic feel.' },
      ],
      [
        { text: 'Add vocal details: ' },
        { text: 'female soprano with a clear, crystalline timbre', quote: true },
        { text: '.' },
      ],
      [
        { text: 'Describe the lyric theme: ' },
        { text: 'lyrics about lost love and the pain of heartbreak', quote: true },
        { text: '.' },
      ],
    ],
  },
]
const tipIndex = ref(tips.map((tip) => Math.floor(Math.random() * tip.items.length)))
const viewOptions: { value: MusicView; icon: IconName; label: string }[] = [
  { value: 'text', icon: 'editNote', label: 'pg.musicText' },
  { value: 'composer', icon: 'layout', label: 'pg.musicComposer' },
]
// handset 与官网一致，768 px 及以下视图切换只显示图标
const handset = useMediaQuery('(max-width: 768px)')
const toggle = ref<HTMLElement>()
const indicator = ref('')
// placeIndicator 把滑块移到当前视图按钮下方
function placeIndicator(): void {
  const button = toggle.value?.querySelector<HTMLElement>('[aria-checked="true"]')
  indicator.value = button
    ? `transform: translate(${button.offsetLeft}px, ${button.offsetTop}px); width: ${button.offsetWidth}px; height: ${button.offsetHeight}px`
    : ''
}
const toggleResize = new ResizeObserver(placeIndicator)
watch(toggle, (element, previous) => {
  if (previous) toggleResize.unobserve(previous)
  if (element) toggleResize.observe(element)
})
// realtime 表示所选模型只支持实时音乐会话
const realtime = computed(() => currentModel.value?.methods.includes('bidiGenerateMusic') ?? false)
const realtimeMusic = useRealtimeMusic()
const canRun = computed(() => currentModel.value !== undefined && state.prompt.trim() !== '')
const fileName = computed(() => {
  const name = state.title.trim() || state.audio?.name || 'music'
  return /\.(wav|mp3)$/.test(name)
    ? name
    : `${name}.${state.audio?.name.split('.').at(-1) ?? 'mp3'}`
})

// openEditor 打开编辑器并重新抽取提示
function openEditor(prompt = '', audio?: string): void {
  tipIndex.value = tips.map((tip) => Math.floor(Math.random() * tip.items.length))
  music.edit(
    prompt,
    audio ? { url: audio, name: audio.split('/').at(-1) ?? 'music.wav', sample: true } : undefined,
  )
}

// requestNewSession 与官网一致：已有提示词、图片或音频时先确认再清空
function requestNewSession(): void {
  if (!state.editing || realtime.value) return
  if (state.prompt !== '' || state.images.length > 0 || state.audio !== undefined)
    confirmNew.value = true
  else music.newSession()
}

// confirmNewSession 确认后清空会话
function confirmNewSession(): void {
  confirmNew.value = false
  music.newSession()
}

// addImages 加入参考图片
async function addImages(files: File[]): Promise<void> {
  for (const file of files.slice(0, maxImages - state.images.length))
    state.images.push({ ...(await readImage(file)), name: file.name })
}

// onDrop 接收拖入的图片
function onDrop(event: DragEvent): void {
  dragging.value = false
  const files = [...(event.dataTransfer?.files ?? [])].filter((file) =>
    file.type.startsWith('image/'),
  )
  void addImages(files)
}

// appendText 把语音转写结果追加到提示词
function appendText(text: string): void {
  state.prompt = state.prompt ? `${state.prompt.trimEnd()} ${text}` : text
}

// onKeydown 处理 Ctrl+Enter 生成
function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey) && !event.isComposing) {
    event.preventDefault()
    void music.run()
  }
}

// code 按语言生成当前提示词的音乐示例代码
function code(language: MusicCodeLanguage): string {
  if (realtime.value)
    return realtimeMusicCode(language, {
      baseURL: window.location.origin,
      model: state.model,
      prompts: realtimeMusic.state.prompts,
      config: realtimeMusic.state.config,
    })
  return musicCode(language, {
    baseURL: window.location.origin,
    model: state.model,
    prompt: state.prompt,
    images: state.images,
  })
}

watch(
  () => store.models.value,
  () => music.ensureModel(typeof route.query.model === 'string' ? route.query.model : undefined),
  { immediate: true },
)
watch(() => state.view, placeIndicator, { flush: 'post' })
onUnmounted(() => {
  music.stop()
  toggleResize.disconnect()
})
</script>

<template>
  <MediaLayout>
    <template #actions>
      <div
        v-if="state.editing && !realtime"
        ref="toggle"
        class="toggle-group"
        :class="{ 'labels-hidden': handset }"
        role="radiogroup"
        :aria-label="t('pg.viewMode')"
      >
        <div
          aria-hidden="true"
          class="slide-indicator"
          :class="{ ready: indicator !== '' }"
          :style="indicator"
        ></div>
        <button
          v-for="option in viewOptions"
          :key="option.value"
          v-tooltip.below="handset ? t(option.label as 'pg.musicText') : undefined"
          type="button"
          ms-button
          role="radio"
          class="toggle-button ms-button-borderless"
          :class="{ 'ms-button-active': state.view === option.value }"
          :aria-checked="state.view === option.value"
          :tabindex="state.view === option.value ? 0 : -1"
          @click="music.setView(option.value)"
        >
          <UiIcon :name="option.icon" :size="18" />
          <span class="toggle-label">{{ t(option.label as 'pg.musicText') }}</span>
        </button>
      </div>
      <CopyPromptButton
        :text="() => state.prompt"
        :disabled="realtime || state.prompt === ''"
        :disabled-message="t('pg.enterPromptToCopy')"
      />
      <button
        v-tooltip.below="state.editing ? t('pg.newSession') : t('pg.alreadyNewChat')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.newSession')"
        :aria-disabled="!state.editing || realtime"
        @click="requestNewSession"
      >
        <UiIcon name="add" :size="20" />
      </button>
    </template>

    <RealtimeMusic v-if="realtime" :key="state.model" :model="state.model" />
    <div v-else-if="!state.editing" class="usage-zero-state-container">
      <ModelEmptyState
        kind="music"
        clickable-title
        :title="t('pg.musicEmptyTitle').replace('{model}', currentModel?.name ?? 'Lyria')"
        :description="t('pg.musicEmptyDescription')"
        :starters="zero.starters"
        :examples="zero.examples"
        @starter="openEditor($event.prompt, ($event as MusicStarter).audio)"
        @example="openEditor($event.prompt, ($event as MusicExample).audio)"
        @title="openEditor()"
      />
    </div>
    <div v-else-if="state.view === 'composer'" class="composer-area">
      <MusicComposer />
    </div>
    <div
      v-else
      class="prompt-input-area"
      :class="{ dragging }"
      @dragover.prevent="dragging = true"
      @dragleave.self="dragging = false"
      @drop.prevent="onDrop"
    >
      <div class="dragging-overlay">
        <UiIcon name="upload" :size="24" />
        {{ t('pg.dropFiles') }}
      </div>
      <div class="prompt-input-container">
        <textarea
          v-model="state.prompt"
          class="textarea"
          :placeholder="t('pg.describeMusic')"
          :aria-label="t('pg.promptInput')"
          @keydown="onKeydown"
        ></textarea>
        <div class="prompt-media-container">
          <div class="prompt-media-list">
            <div v-for="(image, index) in state.images" :key="index" class="media-item">
              <img :src="`data:${image.mimeType};base64,${image.data}`" :alt="image.name" />
              <button
                type="button"
                ms-button
                class="ms-button-borderless ms-button-icon ms-button-small remove-media"
                :aria-label="t('pg.removeMedia')"
                @click="state.images.splice(index, 1)"
              >
                <UiIcon name="close" :size="14" />
              </button>
            </div>
          </div>
          <div class="prompt-input-buttons-container">
            <AddImageButton
              icon="addCircle"
              multiple
              :label="t('pg.addImages')"
              :disabled="state.images.length >= maxImages"
              @images="addImages"
            />
            <SttButton @text="appendText" />
          </div>
        </div>
      </div>
      <div class="pro-tips-container">
        <div v-for="(tip, index) in tips" :key="tip.title" class="pro-tip-card">
          <div class="pro-tip-header">
            <UiIcon :name="tip.icon" :size="16" />
            <span class="pro-tip-title">{{ tip.title }}</span>
          </div>
          <div class="pro-tip-content">
            <template
              v-for="(segment, segmentIndex) in tip.items[tipIndex[index]!]"
              :key="segmentIndex"
            >
              {{ segment.quote ? `"${segment.text}"` : segment.text }}
            </template>
          </div>
        </div>
      </div>
    </div>

    <p v-if="state.error" role="alert" class="music-error">{{ state.error }}</p>
    <AudioFooter
      v-if="state.editing && !realtime"
      :audio="state.audio?.url ?? ''"
      :running="state.running"
      :can-run="canRun"
      :run-label="t('pg.generate')"
      :file-name="fileName"
      @run="music.run()"
      @stop="music.stop()"
    >
      <template #title>
        <input
          v-if="state.audio && !state.audio.sample"
          v-model="state.title"
          ms-input
          type="text"
          class="song-title"
          :aria-label="t('pg.songTitle')"
          :placeholder="t('pg.untitledMusic')"
        />
      </template>
    </AudioFooter>

    <template #settings="{ open, narrow, close }">
      <RunSettingsFrame
        :open="open"
        :narrow="narrow"
        :resettable="realtime"
        @close="close"
        @code="codeOpen = true"
        @reset="realtimeMusic.resetConfig()"
      >
        <div class="field-group">
          <ModelCard
            :model="currentModel"
            kind="music"
            :has-content="state.editing"
            @select="state.model = $event.id"
          />
        </div>
        <RealtimeMusicSettings v-if="realtime" />
      </RunSettingsFrame>
    </template>
  </MediaLayout>
  <CodeDialog v-model:open="codeOpen" :options="musicCodeOptions" :code="code" />
  <UiDialog v-model:open="confirmNew" :title="t('pg.newSessionTitle')" width="420px">
    <p class="confirm-text">{{ t('pg.newSessionBody') }}</p>
    <template #actions>
      <button type="button" ms-button class="ms-button-borderless" @click="confirmNew = false">
        {{ t('common.cancel') }}
      </button>
      <button type="button" ms-button class="ms-button-primary" @click="confirmNewSession">
        {{ t('pg.newSession') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.toggle-group {
  position: relative;
  display: flex;
  align-items: center;
  margin-right: 4px;
  padding: 2px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 9999px;
  background: var(--color-v3-surface-container);
}

.slide-indicator {
  position: absolute;
  z-index: 0;
  top: 0;
  left: 0;
  visibility: hidden;
  border-radius: 9999px;
  background: var(--color-v3-surface-container-highest);
  pointer-events: none;
  transition:
    transform 0.2s cubic-bezier(0.4, 0, 0.2, 1),
    width 0.2s cubic-bezier(0.4, 0, 0.2, 1),
    height 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

.slide-indicator.ready {
  visibility: visible;
}

[ms-button].toggle-button {
  position: relative;
  z-index: 1;
  gap: 4px;
  border: none;
  background: transparent;
  color: var(--color-v3-text-var);
  transition:
    color 0.2s ease,
    box-shadow 0.2s ease;
}

.toggle-group.labels-hidden .toggle-label {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
}

.toggle-label {
  font-size: 12px;
  font-weight: 400;
  line-height: 16px;
}

[ms-button].toggle-button.ms-button-active,
[ms-button].toggle-button:hover:not(.ms-button-active) {
  background: transparent;
  color: var(--color-v3-text);
}

.usage-zero-state-container {
  display: flex;
  flex-grow: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  width: 100%;
  min-height: 0;
  padding-bottom: 150px;
  overflow-y: auto;
  animation: fade-in 0.3s ease-in-out;
}

@media screen and (max-width: 600px) {
  .usage-zero-state-container {
    justify-content: flex-start;
    padding-bottom: 0;
  }
}

.composer-area {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  align-self: center;
  width: 100%;
  max-width: 1000px;
  margin: 12px 40px;
  padding: 12px;
  overflow-y: auto;
  animation: fade-in 0.2s ease;
  container-type: inline-size;
}

@keyframes fade-in {
  from {
    opacity: 0;
  }

  to {
    opacity: 1;
  }
}

.prompt-input-area {
  position: relative;
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  align-self: center;
  justify-content: start;
  gap: 12px;
  box-sizing: border-box;
  width: 100%;
  max-width: 800px;
  margin: 40px;
  padding: 12px;
  overflow-y: auto;
  animation: fade-in 0.2s ease;
}

@media screen and (max-width: 600px) {
  .prompt-input-area,
  .composer-area {
    margin: 0;
  }
}

.dragging-overlay {
  position: absolute;
  z-index: 10;
  inset: 0;
  display: flex;
  visibility: hidden;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 4px dashed var(--color-v3-text-link);
  border-radius: 12px;
  backdrop-filter: blur(5px);
  background-color: var(--color-v3-overlay-background);
  font-size: 14px;
  font-weight: 500;
  pointer-events: none;
}

.prompt-input-area.dragging .dragging-overlay {
  visibility: visible;
}

.prompt-input-container {
  position: relative;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  min-height: 200px;
  border: 1px solid var(--color-v3-outline);
  border-radius: 8px;
  transition: border-color 0.2s ease;
}

.prompt-input-container:focus-within {
  border-color: var(--color-v3-outline-accent);
}

.textarea {
  min-height: min(30%, 10lh);
  max-height: 360px;
  margin: 24px;
  padding: 2px;
  border: none;
  outline: none;
  background: transparent;
  color: var(--color-v3-text);
  font: inherit;
  font-size: 14px;
  line-height: 21px;
  resize: none;
  field-sizing: content;
}

@media screen and (max-width: 480px) {
  .textarea {
    margin: 12px;
  }
}

.prompt-media-container {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 16px;
  overflow-x: auto;
  border-top: 1px solid var(--color-v3-outline);
  border-bottom-right-radius: 8px;
  border-bottom-left-radius: 8px;
  background-color: var(--color-v3-surface-container-high);
}

.prompt-media-list {
  display: flex;
  flex-grow: 1;
  gap: 8px;
  overflow-x: auto;
}

.media-item {
  position: relative;
  flex-shrink: 0;
}

.media-item img {
  width: 48px;
  height: 48px;
  border-radius: 8px;
  object-fit: cover;
}

.remove-media {
  position: absolute;
  top: -6px;
  right: -6px;
  width: 20px;
  height: 20px;
  background: var(--color-v3-surface);
}

.prompt-input-buttons-container {
  display: flex;
  align-items: center;
  gap: 8px;
}

.pro-tips-container {
  display: flex;
  gap: 12px;
}

@media screen and (max-width: 480px) {
  .pro-tips-container {
    flex-direction: column;
  }
}

.pro-tip-card {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 4px;
  padding: 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  background-color: var(--color-v3-surface-container-high);
}

.pro-tip-header {
  display: flex;
  align-items: center;
  gap: 4px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  letter-spacing: 0.5px;
  line-height: 18px;
}

.pro-tip-content {
  color: var(--color-v3-text-var);
  font-size: 14px;
  line-height: 21px;
}

.song-title {
  flex-basis: 100%;
  padding-inline-start: 6px;
  border: none;
  outline: none;
  background: transparent;
  color: var(--color-v3-text-var);
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: text;
}

.song-title:hover,
.song-title:focus-visible {
  color: var(--color-v3-text);
}

.confirm-text {
  margin: 0;
  color: var(--color-v3-text-var);
}

.music-error {
  max-width: 800px;
  margin: 8px auto;
  padding: 8px 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  color: var(--color-error);
  font-size: 14px;
  line-height: 21px;
}
</style>
