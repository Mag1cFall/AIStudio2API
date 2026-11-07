<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { attachmentKind, attachmentName } from './attachment'
import { base64Bytes } from './bytes'
import { pdfThumbnail } from './pdf'
import { dataURL } from './protocols'
import { youtubeVideoID } from './sample-media'
import type { Part } from './types'

const props = defineProps<{ part: Part }>()
const emit = defineEmits<{ preview: [] }>()
const { t, tokenLabel } = useI18n()

// name 是附件的文件名或视频标题，没有记录时为链接或 MIME 类型
const name = computed(() => attachmentName(props.part))
// kind 是可在对话框中预览的附件类型，为空时附件不能点开
const kind = computed(() => attachmentKind(props.part))
// youtube 是 YouTube 视频附件的视频 ID，与官网一样在文件条上方嵌入播放器
const youtube = computed(() => youtubeVideoID(props.part.fileData?.fileUri ?? ''))
// tokens 是附件加入时统计的 token 数
const tokens = computed(() =>
  props.part.tokenCount === undefined ? '' : tokenLabel(props.part.tokenCount),
)
// source 是内联音频与视频的播放地址
const source = computed(() => (props.part.inlineData ? dataURL(props.part.inlineData) : ''))

// thumbnail 与官网 Drive 缩略图一致：PDF 显示第一页预览，无法解析的 PDF 与其他文件显示文件图标
const thumbnail = ref('')
// textPage 是文本附件缩略图中的开头文字，与官网 Drive 缩略图一样合并空白排成一页
const textPage = ref('')
onMounted(async () => {
  const inline = props.part.inlineData
  if (inline?.mimeType === 'application/pdf')
    thumbnail.value = await pdfThumbnail(inline.data, 346).catch(() => '')
  else if (inline?.mimeType.startsWith('text/'))
    textPage.value = new TextDecoder()
      .decode(base64Bytes(inline.data.slice(0, 8000)))
      .replace(/\s+/g, ' ')
      .slice(0, 5000)
})
</script>

<template>
  <div v-if="kind === 'audio'" class="file-chunk audio-chunk">
    <div class="audio-widget">
      <audio class="audio-player" :src="source" controls></audio>
      <button
        type="button"
        class="file-meta"
        :aria-label="t('pg.preview')"
        @click="emit('preview')"
      >
        <UiIcon name="audioFile" :size="14" class="audio-file-icon" />
        <span class="name" :title="name">{{ name }}</span>
        <span v-if="tokens" class="token-count">{{ tokens }}</span>
      </button>
    </div>
  </div>
  <div v-else class="file-chunk" :class="{ 'video-chunk': kind === 'video' || youtube }">
    <div v-if="youtube" class="video-container">
      <iframe
        :title="t('pg.youtubeVideo')"
        :src="`https://www.youtube.com/embed/${youtube}`"
        allow="
          accelerometer;
          autoplay;
          clipboard-write;
          encrypted-media;
          gyroscope;
          picture-in-picture;
          web-share;
        "
        referrerpolicy="strict-origin-when-cross-origin"
        allowfullscreen
      ></iframe>
    </div>
    <div v-else-if="kind === 'video'" class="video-container">
      <video :src="source" controls preload="metadata"></video>
    </div>
    <button
      v-else
      type="button"
      class="preview-container"
      :class="{ previewable: kind }"
      :disabled="!kind"
      :aria-label="t('pg.preview')"
      @click="emit('preview')"
    >
      <img v-if="thumbnail" class="thumbnail-img" :src="thumbnail" :alt="t('pg.fileThumbnail')" />
      <span v-else-if="textPage" class="text-page" :aria-label="t('pg.fileThumbnail')">
        <span class="text-page-content">{{ textPage }}</span>
      </span>
      <UiIcon v-else name="document" :size="36" class="thumbnail-icon" />
    </button>
    <component
      :is="kind ? 'button' : 'div'"
      :type="kind ? 'button' : undefined"
      class="file-chunk-container"
      :class="{ previewable: kind }"
      @click="kind && emit('preview')"
    >
      <div>
        <UiIcon
          :name="youtube || kind === 'video' ? 'playCircle' : 'document'"
          :size="18"
          class="file-icon"
        />
        <span class="name" :title="name">{{ name }}</span>
      </div>
      <div>
        <span v-if="tokens" class="token-count">{{ tokens }}</span>
      </div>
    </component>
  </div>
</template>

<style scoped>
/* file-chunk 与官网 ms-file-chunk、ms-video-chunk 共用的外框：最宽 348 px、1 px 边框、4 px 圆角 */
.file-chunk {
  display: flex;
  flex-direction: column;
  max-width: 348px;
  overflow: clip;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 4px;
}

.video-chunk {
  background-color: var(--color-v3-surface-container);
}

.video-container {
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px 4px 0 0;
}

.video-container iframe {
  width: 100%;
  height: 150px;
  border: 0;
}

.video-container video {
  width: 100%;
  max-height: 100%;
}

.preview-container {
  display: flex;
  justify-content: center;
  width: 100%;
  padding: 0;
  border: none;
  background-color: var(--color-v3-surface-container-highest);
  color: inherit;
}

.previewable {
  cursor: pointer;
}

.thumbnail-img {
  width: 100%;
  height: auto;
  object-fit: cover;
  object-position: center top;
  pointer-events: none;
}

/* text-page 以三倍大小排版后缩小，避免浏览器最小字号限制，得到与官网 Drive 缩略图相同的 346 px 白色页面 */
.text-page {
  position: relative;
  width: 100%;
  aspect-ratio: 1;
  overflow: hidden;
  background-color: #fff;
}

.text-page-content {
  position: absolute;
  top: 0;
  left: 0;
  width: 300%;
  height: 300%;
  padding: 6px 9px;
  overflow: hidden;
  color: #222;
  font-family: Arial, sans-serif;
  font-size: 11px;
  line-height: 14px;
  text-align: left;
  transform: scale(calc(1 / 3));
  transform-origin: top left;
}

.thumbnail-icon {
  align-self: center;
}

.file-chunk-container {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  height: 36px;
  padding: 4px 8px;
  border: none;
  background-color: var(--color-v3-surface-container-high);
  color: inherit;
  font: inherit;
  text-align: left;
}

button.file-chunk-container:hover {
  background-color: var(--color-inverse-on-surface);
}

.file-chunk-container > div:first-child {
  display: flex;
  align-items: center;
  min-width: 0;
}

.file-icon {
  flex-shrink: 0;
  margin-right: 10px;
}

.name {
  flex: 1;
  overflow: hidden;
  line-height: 21px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-chunk-container > div:nth-child(2) {
  display: flex;
  align-items: center;
  margin-left: 10px;
  white-space: nowrap;
}

.token-count {
  color: var(--color-on-surface-variant);
}

/* audio-chunk 与官网 ms-audio-chunk 一致：带边框的音频卡片，播放器下方为文件名与 token 数 */
.audio-chunk {
  border: none;
  border-radius: 0;
  overflow: visible;
}

.audio-widget {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container);
  transition:
    border-color 0.2s ease,
    box-shadow 0.2s ease;
}

.audio-widget:hover {
  border-color: var(--color-v3-outline);
  box-shadow: 0 1px 3px rgb(0 0 0 / 4%);
}

.audio-player {
  width: 100%;
  height: 40px;
  border-radius: 8px;
}

.file-meta {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0;
  border: none;
  background: none;
  color: var(--color-v3-text-var);
  cursor: pointer;
  font-size: 12px;
  line-height: 18px;
  text-align: left;
}

.audio-file-icon {
  flex-shrink: 0;
  opacity: 0.5;
}

.file-meta .name {
  color: var(--color-v3-text);
  line-height: 18px;
}

.file-meta .token-count {
  flex-shrink: 0;
  color: inherit;
  opacity: 0.5;
}
</style>
