<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, onUnmounted, ref, watch } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { useRoute, useRouter } from 'vue-router'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import AddImageButton from './AddImageButton.vue'
import FrameButton from './FrameButton.vue'
import CodeDialog from '../CodeDialog.vue'
import MediaLayout from './MediaLayout.vue'
import MediaPromptBox from './MediaPromptBox.vue'
import ModelEmptyState, { type Example, type Starter } from './ModelEmptyState.vue'
import VideoSettings from './VideoSettings.vue'
import VideoResult from './VideoResult.vue'
import { readImage } from './api'
import { mediaCodeOptions, videoCode, type MediaCodeLanguage } from './code'
import { maxReferences, useVideo, type VideoData, type VideoFrame } from './video'
import { useMediaQuery } from '../media-query'

const props = defineProps<{
  promptId?: string
  prompt?: { title: string; data: VideoData } | undefined
}>()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useConsole()
const video = useVideo()
const { state, currentModel } = video
const codeOpen = ref(false)
const editing = ref(false)
const titleDraft = ref('')
const confirmDelete = ref(false)
// handset 与官网一致，768 px 及以下 Tools 只显示图标
const handset = useMediaQuery('(max-width: 768px)')

// zero 是官网视频页空态的示例
const zero = {
  starters: [
    {
      title: 'Direct a match-cut journey ("The Chocolate Echo")',
      prompt:
        'Starting from this Mayan temple cacao altar frame on ARRI Alexa with vintage anamorphic lenses: a priestess tastes cacao and closes her eyes in bliss, then match-cut on the eye-close to a candle-lit 17th-century Paris salon and a 2099 neon Tokyo apartment.',
      thumb: 'https://www.gstatic.com/aistudio/zero-state/veo-chocolate-frame.webp',
    },
    {
      title: 'Animate a macro botanical starting frame',
      prompt:
        'Starting from this clover leaf frame, rack focus to the suspended dewdrop on the leaf tip as a soft morning breeze sways the stem and golden sunlight flares through the water bead.',
      thumb: 'https://www.gstatic.com/aistudio/zero-state/veo_3_1_clover_dewdrop.webp',
    },
    {
      title: 'Capture a 120fps tabletop espresso pour',
      prompt:
        'Starting from this bottomless portafilter pour frame, animate a 120fps Phantom Flex macro shot as rich golden-brown espresso ribbons swirl into thick tiger-stripe crema with rising steam.',
      thumb: 'https://www.gstatic.com/aistudio/zero-state/veo-espresso-frame.webp',
    },
  ] satisfies Starter[],
  examples: [
    {
      title: 'Snow leopard in winter forest',
      prompt:
        'Cinematic 85mm telephoto tracking shot of a snow leopard with thick frost-dusted fur walking softly through a quiet winter cedar forest during gentle snowfall, shallow depth of field.',
      preview: 'https://www.gstatic.com/aistudio/zero-state/veo_3_1_snow_leopard.webp',
      video: 'https://www.gstatic.com/aistudio/zero-state/veo_3_1_snow_leopard.mp4',
    },
    {
      title: 'Macro raindrop on clover leaf',
      prompt:
        'A cinematic macro video in ultra-slow motion of a vibrant green clover leaf after a rainstorm. A single dewdrop at the tip reflects the inverted forest canopy before detaching and landing on a glistening spiderweb below.',
      preview: 'https://www.gstatic.com/aistudio/zero-state/veo_3_1_clover_dewdrop.webp',
      video: 'https://www.gstatic.com/aistudio/zero-state/clover_leaf.mp4',
    },
  ] satisfies Example[],
}

// hasImages 表示输入中有图片，官网此时提示 Veo 不支持含儿童的图片
const hasImages = computed(
  () => state.start !== undefined || state.last !== undefined || state.references.length > 0,
)
const canRun = computed(
  () => currentModel.value !== undefined && state.draft.trim() !== '' && !video.openAIFrames.value,
)

// now 在生成期间每 100 ms 刷新，用于显示已用时间
const now = ref(performance.now())
let ticker = 0
watch(
  () => state.running,
  (running) => {
    window.clearInterval(ticker)
    if (running) ticker = window.setInterval(() => (now.value = performance.now()), 100)
  },
  { immediate: true },
)
onUnmounted(() => window.clearInterval(ticker))

// elapsed 返回模型轮次的耗时文字
function elapsed(index: number, duration: number | undefined): string {
  const running = state.running && index === state.turns.length - 1
  const milliseconds = running ? now.value - state.started : (duration ?? 0)
  return `${(milliseconds / 1000).toFixed(1)}s`
}

// toFrame 把图片文件读为帧
async function toFrame(file: Blob, name: string): Promise<VideoFrame> {
  return { ...(await readImage(file)), name }
}

// setStart 设置起始帧
async function setStart(file: File): Promise<void> {
  state.start = await toFrame(file, file.name)
}

// setLastFrame 设置尾帧
async function setLastFrame(file: File): Promise<void> {
  video.setLast(await toFrame(file, file.name))
}

// addReferences 加入参考图，最多 3 张
async function addReferences(files: File[]): Promise<void> {
  for (const file of files.slice(0, maxReferences - state.references.length))
    state.references.push(await toFrame(file, file.name))
}

// useStarter 填入示例提示词，带起始帧的示例下载帧图片后直接运行
async function useStarter(item: Starter): Promise<void> {
  state.draft = item.prompt
  if (item.thumb) {
    const response = await fetch(item.thumb)
    state.start = await toFrame(await response.blob(), item.thumb.split('/').at(-1) ?? 'frame')
  }
  void video.run()
}

// useExample 填入示例提示词并运行
function useExample(item: Example): void {
  state.draft = item.prompt
  void video.run()
}

// code 按语言生成当前输入与设置的视频示例代码
function code(language: MediaCodeLanguage): string {
  return videoCode(language, {
    baseURL: window.location.origin,
    prompt: state.draft,
    settings: state.settings,
    start: state.start,
    last: state.last,
    references: state.references,
    aspectRatios: video.aspectRatios.value,
    durations: video.durations.value.map((item) => item.value),
    resolutions: video.resolutions.value.map((item) => item.value),
  })
}

// newChat 打开新的视频会话
function newChat(): void {
  video.newSession()
  void router.push('/prompts/new_video')
}

// openRename 打开标题编辑框
function openRename(): void {
  titleDraft.value = state.title
  editing.value = true
}

// saveTitle 保存编辑后的标题
function saveTitle(): void {
  editing.value = false
  void video.rename(titleDraft.value)
}

// removePrompt 删除当前视频会话并回到新会话
async function removePrompt(): Promise<void> {
  confirmDelete.value = false
  await video.remove()
  void router.push('/prompts/new_video')
}

watch(
  () => [props.promptId, props.prompt] as const,
  ([id, prompt]) => {
    if (id && prompt) video.load(id, prompt.title, prompt.data)
    else if (!id && state.id !== null) video.newSession()
  },
  { immediate: true },
)
watch(
  () => store.models.value,
  () => video.ensureModel(typeof route.query.model === 'string' ? route.query.model : undefined),
  { immediate: true },
)
watch(
  () => state.id,
  (id) => {
    if (id !== null && route.params.id !== id) void router.replace(`/prompts/${id}`)
  },
)
</script>

<template>
  <MediaLayout :title="state.title">
    <template v-if="state.id !== null" #title>
      <button
        v-tooltip.below="t('pg.editTitle')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon ms-button-small"
        :aria-label="t('pg.editTitle')"
        @click="openRename"
      >
        <UiIcon name="edit" :size="18" />
      </button>
    </template>
    <template #actions>
      <button
        v-tooltip.below="state.turns.length === 0 ? t('pg.alreadyNewChat') : t('pg.newChat')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.newChat')"
        :aria-disabled="state.turns.length === 0 || state.running"
        @click="state.turns.length > 0 && !state.running && newChat()"
      >
        <UiIcon name="add" :size="20" />
      </button>
      <DropdownMenuRoot :modal="false">
        <DropdownMenuTrigger
          v-tooltip.below="t('pg.moreActions')"
          ms-button
          class="ms-button-borderless ms-button-icon"
          :aria-label="t('pg.moreActions')"
        >
          <UiIcon name="more" :size="20" />
        </DropdownMenuTrigger>
        <DropdownMenuPortal>
          <DropdownMenuContent align="end" :side-offset="4" class="ms-menu-panel">
            <DropdownMenuItem
              class="ms-menu-item"
              :disabled="state.id === null || state.running"
              @select="confirmDelete = true"
            >
              <UiIcon name="delete" :size="18" />
              <span class="ms-menu-item-text">{{ t('common.delete') }}</span>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenuPortal>
      </DropdownMenuRoot>
    </template>

    <div v-if="state.loadError === 'not_found'" class="load-error">
      <h2>{{ t('pg.notFoundTitle') }}</h2>
      <p>{{ t('pg.notFoundBody') }}</p>
    </div>
    <div v-else-if="state.turns.length === 0" class="zero-state-container">
      <ModelEmptyState
        kind="video"
        :title="t('pg.videoEmptyTitle').replace('{model}', currentModel?.name ?? 'Veo')"
        :description="t('pg.videoEmptyDescription')"
        :starters="zero.starters"
        :examples="zero.examples"
        @starter="useStarter"
        @example="useExample"
      />
    </div>
    <div v-else class="video-panel">
      <div class="scroller">
        <div class="turns-container">
          <template v-for="(turn, index) in state.turns" :key="turn.id">
            <template v-if="turn.role === 'user'">
              <div class="turn-role">{{ t('pg.user') }}</div>
              <div class="turn-prompt">{{ turn.prompt }}</div>
              <div class="image-container">
                <img
                  v-for="(frame, frameIndex) in [
                    turn.start,
                    turn.last,
                    ...(turn.references ?? []),
                  ].filter(Boolean)"
                  :key="frameIndex"
                  class="loaded-image"
                  :src="`data:${frame!.mimeType};base64,${frame!.data}`"
                  :alt="frame!.name"
                />
              </div>
            </template>
            <template v-else>
              <span class="turn-role">{{ turn.model }} • {{ elapsed(index, turn.duration) }}</span>
              <p v-if="turn.error" class="turn-error">{{ turn.error }}</p>
              <div v-else class="gallery-container">
                <VideoResult
                  v-for="(path, videoIndex) in turn.videos ?? ['']"
                  :key="videoIndex"
                  :path="path"
                  :aspect-ratio="turn.aspectRatio ?? '16:9'"
                />
              </div>
              <div v-if="index < state.turns.length - 1" class="turn-separator"></div>
            </template>
          </template>
        </div>
      </div>
    </div>

    <div class="prompt-input-container">
      <MediaPromptBox
        v-model="state.draft"
        :placeholder="t('pg.describeVideo')"
        :label="t('pg.videoPromptLabel')"
        :running="state.running"
        :can-run="canRun"
        :run-tooltip="video.openAIFrames.value ? t('pg.openAIVideoStartOnly') : ''"
        @run="video.run()"
        @stop="video.stop()"
        @files="addReferences"
      >
        <template #top>
          <FrameButton
            :frame="state.start"
            :label="t('pg.startFrame')"
            @add="setStart"
            @remove="state.start = undefined"
          />
          <span class="augmentation-separator"><UiIcon name="swapHoriz" :size="24" /></span>
          <FrameButton
            :frame="state.last"
            :label="t('pg.lastFrame')"
            @add="setLastFrame"
            @remove="video.setLast(undefined)"
          />
        </template>
        <template v-if="state.references.length" #media>
          <div v-for="(frame, index) in state.references" :key="index" class="reference">
            <img
              class="reference-image"
              :src="`data:${frame.mimeType};base64,${frame.data}`"
              :alt="frame.name"
            />
            <span class="reference-name">{{ frame.name }}</span>
            <button
              type="button"
              ms-button
              class="ms-button-borderless ms-button-icon ms-button-small"
              :aria-label="t('pg.removeMedia')"
              @click="state.references.splice(index, 1)"
            >
              <UiIcon name="close" :size="16" />
            </button>
          </div>
        </template>
        <template #left>
          <AddImageButton
            icon="addCircle"
            chip
            multiple
            :label="t('pg.addReferenceImages')"
            :disabled="state.references.length >= maxReferences"
            @images="addReferences"
          />
          <button
            type="button"
            ms-button
            class="ms-button-primary"
            :class="{ 'ms-button-icon': handset }"
            :aria-label="t('pg.openTools')"
            aria-disabled="true"
          >
            <UiIcon name="tools" :size="18" />
            <template v-if="!handset">{{ t('pg.tools') }}</template>
          </button>
        </template>
      </MediaPromptBox>
      <p v-if="hasImages" class="input-warning">{{ t('pg.veoChildren') }}</p>
    </div>

    <template #settings="{ open, narrow, close }">
      <VideoSettings :open="open" :narrow="narrow" @close="close" @code="codeOpen = true" />
    </template>
  </MediaLayout>
  <CodeDialog v-model:open="codeOpen" :options="mediaCodeOptions" :code="code" />
  <UiDialog v-model:open="editing" :title="t('pg.editTitle')" width="480px">
    <label class="field-label" for="video-title">{{ t('pg.title') }}</label>
    <input
      id="video-title"
      v-model="titleDraft"
      ms-input
      class="title-input"
      maxlength="120"
      @keydown.enter.prevent="saveTitle"
    />
    <template #actions>
      <button type="button" ms-button class="ms-button-borderless" @click="editing = false">
        {{ t('common.cancel') }}
      </button>
      <button type="button" ms-button class="ms-button-primary" @click="saveTitle">
        {{ t('common.save') }}
      </button>
    </template>
  </UiDialog>
  <UiDialog v-model:open="confirmDelete" :title="t('pg.deleteTitle')" width="420px">
    <p class="confirm-text">{{ t('pg.deleteBody') }}</p>
    <template #actions>
      <button type="button" ms-button class="ms-button-borderless" @click="confirmDelete = false">
        {{ t('common.cancel') }}
      </button>
      <button type="button" ms-button class="ms-button-primary" @click="removePrompt">
        {{ t('common.delete') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.field-label {
  display: block;
  margin-bottom: 8px;
  font-weight: 500;
}

.title-input {
  width: 100%;
}

.confirm-text {
  margin: 0;
  color: var(--color-v3-text-var);
}

.zero-state-container {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  align-items: center;
  justify-content: center;
  min-height: 0;
  padding: 20px;
  overflow-y: auto;
}

@media screen and (max-width: 600px) {
  .zero-state-container {
    justify-content: flex-start;
    padding: 16px;
  }
}

.video-panel {
  position: relative;
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  min-height: 0;
  margin-left: 20px;
  overflow: hidden;
}

.scroller {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

@media screen and (max-width: 768px) {
  .scroller {
    padding: 20px 8px 0;
  }
}

.turns-container {
  display: flex;
  flex-direction: column;
  flex-grow: 1;
  align-items: flex-start;
  max-width: 1000px;
  margin: 0 auto;
  overflow-x: hidden;
}

.turn-prompt {
  margin: 8px 12px;
  color: var(--color-on-surface);
  font-size: 14px;
  font-weight: 400;
  line-height: 21px;
  white-space: pre-wrap;
}

.turn-role {
  margin: 8px 12px;
  color: var(--color-on-surface-variant);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.turn-error {
  margin: 8px 12px;
  color: var(--color-error);
  font-size: 14px;
  line-height: 21px;
}

.gallery-container {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 16px;
  width: 100%;
  max-width: 60%;
  margin: 8px 12px;
}

.turn-separator {
  width: 100%;
  margin-top: 12px;
  margin-right: 140px;
  margin-left: 8px;
  border-top: 1px solid var(--turn-separator-color, var(--color-v3-outline));
}

.image-container {
  position: relative;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  box-sizing: border-box;
  width: 100%;
  max-width: 60%;
  margin: 8px 12px;
  padding-right: 16px;
}

.image-container .loaded-image {
  width: 100%;
  max-width: 520px;
  border-radius: 8px;
  object-fit: cover;
}

.prompt-input-container {
  position: relative;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  align-items: center;
  margin-bottom: 6px;
  padding-inline: 20px;
  padding-bottom: 8px;
}

@media screen and (max-width: 600px) {
  .prompt-input-container {
    margin-bottom: 8px;
  }
}

.augmentation-separator {
  display: flex;
  align-items: center;
  color: var(--color-v3-text-var);
}

.reference {
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

.reference-image {
  width: 56px;
  height: 56px;
  border-radius: 4px;
  object-fit: cover;
}

.reference-name {
  overflow: hidden;
  font-size: 12px;
  line-height: 18px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.input-warning {
  display: block;
  width: 100%;
  max-width: 1000px;
  margin: 8px auto;
  padding: 0 20px;
  color: var(--color-on-surface-variant);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.load-error {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 24px;
  text-align: center;
}
</style>
