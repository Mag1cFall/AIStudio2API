<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiSwitch from '@/playground/ui/UiSwitch.vue'
import type { Part } from '../types'

const open = defineModel<boolean>('open', { required: true })
// part 是正在设置的视频附件，src 是它的播放地址，name 是它的文件名，agentic 表示所选模型支持 Agentic video understanding
const props = defineProps<{ part: Part; src: string; name: string; agentic: boolean }>()
const emit = defineEmits<{
  save: [settings: Pick<Part, 'videoMetadata' | 'mediaProcessing' | 'fileName'>]
}>()
const { t } = useI18n()
const id = useId()

const agenticOn = ref(false)
const start = ref('')
const end = ref('')
const fps = ref('')
const displayName = ref('')
// duration 是视频时长（整秒），元数据载入前为 undefined
const duration = ref<number>()
const player = ref<HTMLVideoElement>()

// seconds 与官网一样把 1h1m10s、70s 这类时长文字换算为秒，格式不对时为 undefined
function seconds(text: string): number | undefined {
  const match = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?$/.exec(text.trim())
  if (!match || (!match[1] && !match[2] && !match[3])) return undefined
  return Number(match[1] ?? 0) * 3600 + Number(match[2] ?? 0) * 60 + Number(match[3] ?? 0)
}

watch(
  open,
  (value) => {
    if (!value) return
    agenticOn.value = props.part.mediaProcessing === 'AGENTIC'
    start.value = props.part.videoMetadata?.startOffset ?? ''
    end.value = props.part.videoMetadata?.endOffset ?? ''
    fps.value = props.part.videoMetadata?.fps?.toString() ?? ''
    displayName.value = ''
  },
  { immediate: true },
)

const startSeconds = computed(() => seconds(start.value))
const endSeconds = computed(() => seconds(end.value))
const fpsValue = computed(() => (fps.value === '' ? undefined : Number(fps.value)))

// formatErrors 是时间格式错误，与官网一样只提示不阻止保存，格式错误的取值不发送
const formatErrors = computed(() => {
  const list: string[] = []
  if (start.value.trim() !== '' && startSeconds.value === undefined)
    list.push(t('pg.videoStartFormat'))
  if (end.value.trim() !== '' && endSeconds.value === undefined) list.push(t('pg.videoEndFormat'))
  return list
})

// rangeErrors 是起止先后、时长范围与帧率错误，存在时不能保存
const rangeErrors = computed(() => {
  const list: string[] = []
  const from = startSeconds.value
  const to = endSeconds.value
  const total = duration.value
  if (from !== undefined && to !== undefined && from >= to) list.push(t('pg.videoEndAfterStart'))
  if (to !== undefined && total !== undefined && (to < 1 || to > total))
    list.push(t('pg.videoEndRange').replace('{seconds}', String(total)))
  if (from !== undefined && total !== undefined && from > total)
    list.push(t('pg.videoStartRange').replace('{seconds}', String(total)))
  const rate = fpsValue.value
  if (rate !== undefined && !(rate > 0 && rate <= 24)) list.push(t('pg.videoFpsRange'))
  return list
})

// valid 表示可以保存：Agentic 模式要求模型支持，静态模式要求没有范围错误
const valid = computed(() => (agenticOn.value ? props.agentic : rangeErrors.value.length === 0))

// clampPlayback 让预览只在截取的起止时间之间播放
function clampPlayback(): void {
  const video = player.value
  if (!video) return
  const from = startSeconds.value ?? 0
  const to = endSeconds.value
  if (video.currentTime < from || (to !== undefined && video.currentTime >= to))
    video.currentTime = from
}

// stopAtEnd 在播放到截取终点时暂停
function stopAtEnd(): void {
  const video = player.value
  if (video && endSeconds.value !== undefined && video.currentTime >= endSeconds.value)
    video.pause()
}

watch(startSeconds, (from) => {
  if (player.value && from !== undefined) player.value.currentTime = from
})

// save 与官网一样把设置写回附件：Agentic 模式只记录处理方式与填写的名称，静态模式记录截取、帧率与 STATIC 处理方式
function save(): void {
  if (!valid.value) return
  const name = displayName.value.trim()
  const metadata = {
    ...(startSeconds.value !== undefined && { startOffset: `${startSeconds.value}s` }),
    ...(endSeconds.value !== undefined && { endOffset: `${endSeconds.value}s` }),
    ...(fpsValue.value !== undefined && { fps: fpsValue.value }),
  }
  emit(
    'save',
    agenticOn.value
      ? { mediaProcessing: 'AGENTIC', ...(name !== '' && { fileName: name }) }
      : {
          mediaProcessing: 'STATIC',
          ...(Object.keys(metadata).length > 0 && { videoMetadata: metadata }),
        },
  )
  open.value = false
}
</script>

<template>
  <UiDialog v-model:open="open" :title="t('pg.videoSettings')" width="500px">
    <video
      ref="player"
      class="video-preview"
      :src="src"
      controls
      @loadedmetadata="duration = Math.round(($event.target as HTMLVideoElement).duration)"
      @timeupdate="stopAtEnd"
      @play="clampPlayback"
    ></video>
    <div class="video-options-container">
      <div
        v-if="agentic || agenticOn"
        v-tooltip.above="t('pg.agenticVideoTip')"
        class="agentic-toggle-row full-row"
      >
        <span class="toggle-label">{{ t('pg.agenticVideo') }}</span>
        <UiSwitch v-model="agenticOn" :label="t('pg.agenticVideo')" />
      </div>
      <template v-if="agenticOn">
        <div class="file-name-display full-row">
          <span class="file-name-label">{{ t('pg.videoFile') }}</span>
          <span class="file-name-value">{{ name }}</span>
        </div>
        <div class="full-row">
          <label :for="`${id}-name`">{{ t('pg.videoName') }}</label>
          <input
            :id="`${id}-name`"
            v-model="displayName"
            ms-input
            type="text"
            :placeholder="t('pg.videoNamePlaceholder')"
          />
        </div>
      </template>
      <template v-else>
        <div>
          <label :for="`${id}-start`">{{ t('pg.videoStart') }}</label>
          <input :id="`${id}-start`" v-model="start" ms-input type="text" />
        </div>
        <div>
          <label :for="`${id}-end`">{{ t('pg.videoEnd') }}</label>
          <input :id="`${id}-end`" v-model="end" ms-input type="text" />
        </div>
        <div class="full-row">
          <label :for="`${id}-fps`">{{ t('pg.videoFps') }}</label>
          <input
            :id="`${id}-fps`"
            v-model="fps"
            ms-input
            type="number"
            max="24"
            :placeholder="t('pg.videoFpsPlaceholder')"
          />
        </div>
      </template>
    </div>
    <p v-if="agenticOn && !agentic" class="dialog-error">
      {{ t('pg.agenticVideoUnsupported') }}
    </p>
    <template v-if="!agenticOn">
      <p v-for="error in [...formatErrors, ...rangeErrors]" :key="error" class="dialog-error">
        {{ error }}
      </p>
    </template>
    <template #actions>
      <button type="button" ms-button class="ms-button-borderless" @click="open = false">
        {{ t('common.cancel') }}
      </button>
      <button
        type="button"
        ms-button
        class="ms-button-primary"
        :aria-disabled="!valid"
        @click="save"
      >
        {{ t('common.save') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
/* 尺寸与间距取官网 ms-video-options-dialog */
.video-preview {
  display: block;
  width: 100%;
  height: min(30vh, 220px);
  margin-bottom: 20px;
  border: none;
}

.video-options-container {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
  width: 100%;
}

.full-row {
  grid-column: 1 / -1;
}

.agentic-toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 4px;
  padding: 4px 0;
}

.toggle-label {
  font-weight: 500;
}

.file-name-display {
  display: flex;
  flex-direction: column;
  padding: 4px 0;
}

.file-name-label {
  margin-bottom: 4px;
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;
}

.file-name-value {
  font-size: 14px;
  line-height: 21px;
  word-break: break-all;
}

.dialog-error {
  margin-bottom: 0;
  color: var(--color-error);
}
</style>
