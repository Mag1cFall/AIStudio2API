<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { onUnmounted, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import MediaViewDialog from './MediaViewDialog.vue'
import { downloadMedia } from './api'

const props = defineProps<{ path: string; aspectRatio: string }>()
const { t } = useI18n()
const element = ref<HTMLVideoElement>()
const source = ref('')
const error = ref('')
const playing = ref(false)
const largeView = ref(false)

watch(
  () => props.path,
  async (path) => {
    if (source.value) URL.revokeObjectURL(source.value)
    source.value = ''
    error.value = ''
    if (path === '') return
    try {
      source.value = URL.createObjectURL(await downloadMedia(path))
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : String(reason)
    }
  },
  { immediate: true },
)
onUnmounted(() => {
  if (source.value) URL.revokeObjectURL(source.value)
})

// toggle 播放或暂停
function toggle(): void {
  const video = element.value
  if (video === undefined) return
  if (video.paused) void video.play()
  else video.pause()
}

// openLargeView 暂停预览并在对话框中打开大图查看
function openLargeView(): void {
  element.value?.pause()
  largeView.value = true
}

// save 下载视频文件
function save(): void {
  const link = document.createElement('a')
  link.href = source.value
  link.download = `video-${props.path.split('/').at(-2) ?? 'veo'}.mp4`
  link.click()
}
</script>

<template>
  <div
    class="gallery-video"
    :class="aspectRatio === '9:16' ? 'nine-by-sixteen' : 'sixteen-by-nine'"
  >
    <p v-if="error" class="video-error">{{ error }}</p>
    <div v-else class="video-container" :class="{ pending: !source }">
      <div v-if="!source" class="video-placeholder"></div>
      <template v-else>
        <video
          ref="element"
          class="video"
          :src="source"
          loop
          playsinline
          @play="playing = true"
          @pause="playing = false"
          @click="toggle"
        ></video>
        <div class="bottom-controls-container">
          <button
            v-tooltip="playing ? t('pg.pauseVideo') : t('pg.playVideo')"
            type="button"
            ms-button
            class="ms-button-borderless ms-button-icon"
            :aria-label="playing ? t('pg.pauseVideo') : t('pg.playVideo')"
            @click="toggle"
          >
            <UiIcon :name="playing ? 'pauseCircle' : 'playCircle'" :size="20" />
          </button>
          <span class="spacer"></span>
          <button
            v-tooltip="t('pg.largeView')"
            type="button"
            ms-button
            class="ms-button-borderless ms-button-icon ms-button-small"
            :aria-label="t('pg.largeView')"
            @click="openLargeView"
          >
            <UiIcon name="fullscreen" :size="20" />
          </button>
          <button
            v-tooltip="t('pg.download')"
            type="button"
            ms-button
            class="ms-button-borderless ms-button-icon ms-button-small"
            :aria-label="t('pg.downloadVideo')"
            @click="save"
          >
            <UiIcon name="download" :size="20" />
          </button>
        </div>
      </template>
    </div>
    <MediaViewDialog
      v-if="source"
      v-model:open="largeView"
      :title="t('pg.generatedVideo')"
      :src="source"
      @download="save"
    />
  </div>
</template>

<style scoped>
.gallery-video {
  width: 100%;
}

.sixteen-by-nine {
  max-width: 520px;
}

.nine-by-sixteen {
  max-width: 300px;
}

.sixteen-by-nine :is(.video, .video-placeholder) {
  aspect-ratio: 16 / 9;
}

.nine-by-sixteen :is(.video, .video-placeholder) {
  aspect-ratio: 9 / 16;
}

.video-container {
  position: relative;
  display: flex;
  overflow: clip;
  border-radius: 8px;
}

.video-container.pending {
  background-color: var(--color-v3-button-container);
}

.video,
.video-placeholder {
  width: 100%;
}

.video {
  transition: scale 0.2s ease-in-out;
}

.gallery-video:hover .video {
  scale: 1.05;
}

.video-placeholder {
  animation: moving-gradient 1.7s linear infinite forwards;
  background: linear-gradient(
    290deg,
    var(--color-loading-background) 40%,
    var(--color-loading-background-contrast) 50%,
    var(--color-loading-background) 60%
  );
  background-size: 800px;
}

@keyframes moving-gradient {
  0% {
    background-position: -400px 0;
  }

  to {
    background-position: 400px 0;
  }
}

.bottom-controls-container {
  position: absolute;
  right: 0;
  bottom: 0;
  left: 0;
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 8px;
  background: linear-gradient(180deg, transparent, rgb(0 0 0 / 20%) 30%, rgb(0 0 0 / 33%));
}

.bottom-controls-container .spacer {
  flex: 1;
}

.bottom-controls-container button {
  background: transparent;
  color: #fff;
}

.video-error {
  margin: 0;
  color: var(--color-error);
  font-size: 14px;
  line-height: 21px;
}
</style>
