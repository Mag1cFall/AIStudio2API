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
import ApiKeyButton from '../ApiKeyButton.vue'
import CodeDialog from '../CodeDialog.vue'
import SavePromptDialog from '../SavePromptDialog.vue'
import MediaLayout from './MediaLayout.vue'
import MediaPromptBox from './MediaPromptBox.vue'
import PaidUsageZeroState, { type Suggestion } from './PaidUsageZeroState.vue'
import VideoSettings from './VideoSettings.vue'
import VideoResult from './VideoResult.vue'
import { readImage } from './api'
import { mediaCodeOptions, videoCode, type MediaCodeLanguage } from './code'
import { maxReferences, useVideo, type VideoData, type VideoFrame } from './video'
import { useMediaQuery } from '../media-query'

const props = defineProps<{
  promptId?: string
  prompt?: { title: string; description: string; data: VideoData } | undefined
}>()
const route = useRoute()
const router = useRouter()
const { t, logText } = useI18n()
const store = useConsole()
const video = useVideo()
const { state, currentModel } = video
const codeOpen = ref(false)
const editing = ref(false)
const confirmDelete = ref(false)
// handset 与官网一致，768 px 及以下 Tools 只显示图标
const handset = useMediaQuery('(max-width: 768px)')

// zeroState 是官网空态示例图片所在目录
const zeroState = 'https://www.gstatic.com/aistudio/zero-state/'

// suggestions 是当前界面语言的官网视频页空态三张示例卡片，reference 为作为起始图发送的图片
const suggestions = computed<Suggestion[]>(() => [
  {
    label: t('zero.video.example1'),
    caption: t('zero.video.example1'),
    prompt: '',
    image: `${zeroState}perfume_animated_v2_opt_288x162.gif`,
    reference: `${zeroState}perfume_v2.png`,
  },
  {
    label: t('zero.video.example2'),
    caption: t('zero.video.example2'),
    prompt: t('zero.video.example2Prompt'),
    image: `${zeroState}snow_leopard_animated_opt_288x162.gif`,
    reference: `${zeroState}snow_leopard.png`,
  },
  {
    label: t('zero.video.example3'),
    caption: t('zero.video.example3'),
    prompt: t('zero.video.example3Prompt'),
    image: `${zeroState}stir_fry_vegetables_animated_opt_288x162.gif`,
  },
])

// hasImages 表示输入中有图片，官网此时提示 Veo 不支持含儿童的图片
const hasImages = computed(
  () => state.start !== undefined || state.last !== undefined || state.references.length > 0,
)
// referenceInputs 表示模型接受参考图，参考图最多 3 张且与首尾帧互斥
const referenceInputs = computed(() => video.videoInputs.value.references)
// frameInputs 表示显示帧按钮：模型接受尾帧，或同时接受起始图与参考图，且没有参考图
const frameInputs = computed(
  () =>
    (video.videoInputs.value.last || (video.videoInputs.value.start && referenceInputs.value)) &&
    state.references.length === 0,
)
// imageDisabled 表示不能再加图片：没有模型或模型不接受图片，或参考图已满或已有帧
const imageDisabled = computed(
  () =>
    currentModel.value === undefined ||
    (!referenceInputs.value && !video.videoInputs.value.start) ||
    (referenceInputs.value &&
      (state.references.length >= maxReferences ||
        state.start !== undefined ||
        state.last !== undefined)),
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

// addImages 按模型能力加入图片：接受参考图时加为参考图，否则第一张作为起始图
async function addImages(files: File[]): Promise<void> {
  if (imageDisabled.value) return
  if (!referenceInputs.value) {
    if (files[0]) await setStart(files[0])
    return
  }
  for (const file of files.slice(0, maxReferences - state.references.length))
    state.references.push(await toFrame(file, file.name))
}

// suggestionPick 是最近一次点击示例的序号
let suggestionPick = 0

// useSuggestion 与官网一样以示例的提示词与图片直接生成，图片作为起始图；没有模型时提示生成失败，示例图片读取完成前又点了其他示例时放弃这次点击
async function useSuggestion(item: Suggestion): Promise<void> {
  const pick = ++suggestionPick
  if (currentModel.value === undefined) {
    store.showNotice(t('pg.videoFailed').replace('{error}', t('pg.selectModel')), 'error')
    return
  }
  const start = item.reference
    ? await toFrame(
        await (await fetch(item.reference)).blob(),
        item.reference.split('/').at(-1) ?? 'image',
      )
    : undefined
  if (pick !== suggestionPick) return
  state.draft = item.prompt
  state.references = []
  video.setLast(undefined)
  state.start = start
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

// removePrompt 删除当前视频会话并回到新会话
async function removePrompt(): Promise<void> {
  confirmDelete.value = false
  await video.remove()
  void router.push('/prompts/new_video')
}

watch(
  () => [props.promptId, props.prompt] as const,
  ([id, prompt]) => {
    if (id && prompt) video.load(id, prompt)
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
        @click="editing = true"
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
            <DropdownMenuItem v-if="handset" class="ms-menu-item" disabled>
              <UiIcon name="share" :size="18" />
              <span class="ms-menu-item-text">{{ t('pg.share') }}</span>
            </DropdownMenuItem>
            <DropdownMenuItem class="ms-menu-item" disabled>
              <UiIcon name="incognito" :size="18" />
              <span class="ms-menu-item-text">{{ t('pg.temporaryChat') }}</span>
            </DropdownMenuItem>
            <DropdownMenuItem
              class="ms-menu-item"
              :disabled="state.running"
              @select="video.save(true)"
            >
              <UiIcon name="save" :size="18" />
              <span class="ms-menu-item-text">{{ t('pg.savePrompt') }}</span>
            </DropdownMenuItem>
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

    <PaidUsageZeroState
      v-if="state.turns.length === 0"
      :title="t('pg.videoEmptyTitle').replace('{model}', currentModel?.name ?? 'Veo')"
      :subtitle="t('pg.videoEmptyDescription')"
      layout="single-row"
      icon="modelVideo"
      :suggestions="suggestions"
      @select="useSuggestion"
    />
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
              <p v-if="turn.stopped" class="turn-error">{{ t('pg.generationStopped') }}</p>
              <p v-else-if="turn.error" class="turn-error">{{ logText(turn.error) }}</p>
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
        :can-run="video.canRun.value"
        :run-tooltip="video.openAIFrames.value ? t('pg.openAIVideoStartOnly') : ''"
        @run="video.run()"
        @stop="video.stop()"
        @files="addImages"
      >
        <template v-if="frameInputs" #top>
          <FrameButton
            :frame="state.start"
            :label="t('pg.startFrame')"
            @add="setStart"
            @remove="state.start = undefined"
          />
          <template v-if="video.videoInputs.value.last">
            <span class="augmentation-separator"><UiIcon name="swapHoriz" :size="24" /></span>
            <FrameButton
              :frame="state.last"
              :label="t('pg.lastFrame')"
              @add="setLastFrame"
              @remove="video.setLast(undefined)"
            />
          </template>
        </template>
        <template v-if="!frameInputs && state.start" #media>
          <div class="reference">
            <img
              class="reference-image"
              :src="`data:${state.start.mimeType};base64,${state.start.data}`"
              :alt="state.start.name"
            />
            <span class="reference-name">{{ state.start.name }}</span>
            <button
              type="button"
              ms-button
              class="ms-button-borderless ms-button-icon ms-button-small"
              :aria-label="t('pg.removeMedia')"
              @click="state.start = undefined"
            >
              <UiIcon name="close" :size="16" />
            </button>
          </div>
        </template>
        <template v-else-if="state.references.length" #media>
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
            :multiple="referenceInputs"
            :label="referenceInputs ? t('pg.addReferenceImages') : t('pg.addImageToPrompt')"
            :tooltip="currentModel === undefined ? t('pg.imagesCannotBeAdded') : undefined"
            :disabled="imageDisabled"
            @images="addImages"
          />
          <ApiKeyButton />
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
  <SavePromptDialog
    v-model:open="editing"
    :title="state.title"
    :description="state.description"
    @save="(title, description) => video.rename(title, description)"
  />
  <UiDialog
    v-model:open="confirmDelete"
    :title="t('pg.deleteTitle')"
    width="500px"
    :closable="false"
  >
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
.confirm-text {
  margin: 0;
  line-height: 21px;
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
</style>
