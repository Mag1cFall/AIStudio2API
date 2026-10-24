<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { pdfThumbnail } from './pdf'
import { youtubeVideoID } from './sample-media'
import type { Part } from './types'

const props = defineProps<{ part: Part }>()
const { tokenLabel } = useI18n()

// name 是附件的文件名或视频标题，没有记录时为链接或 MIME 类型
const name = computed(
  () =>
    props.part.fileName ?? props.part.fileData?.fileUri ?? props.part.inlineData?.mimeType ?? '',
)
// youtube 是 YouTube 视频附件的视频 ID，与官网一样在文件条上方嵌入播放器
const youtube = computed(() => youtubeVideoID(props.part.fileData?.fileUri ?? ''))
// tokens 是附件加入时统计的 token 数
const tokens = computed(() =>
  props.part.tokenCount === undefined ? '' : tokenLabel(props.part.tokenCount),
)

// thumbnail 与官网一致：PDF 显示第一页预览，其他文件与无法解析的 PDF 只显示文件条
const thumbnail = ref('')
onMounted(async () => {
  const inline = props.part.inlineData
  if (inline?.mimeType !== 'application/pdf') return
  thumbnail.value = await pdfThumbnail(inline.data, 346).catch(() => '')
})
</script>

<template>
  <div class="file-chunk">
    <div v-if="youtube" class="video-container">
      <iframe
        title="YouTube video player"
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
    <div v-else-if="thumbnail" class="preview-container">
      <img class="thumbnail-img" :src="thumbnail" alt="file thumbnail" />
    </div>
    <div class="file-chunk-container">
      <div>
        <UiIcon :name="youtube ? 'playCircle' : 'document'" :size="18" class="file-icon" />
        <span class="name" :title="name">{{ name }}</span>
      </div>
      <div>
        <span v-if="tokens" class="token-count">{{ tokens }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.file-chunk {
  display: flex;
  flex-direction: column;
  max-width: 348px;
  overflow: clip;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 4px;
}

.video-container {
  display: flex;
  background-color: var(--color-v3-surface-container);
}

.video-container iframe {
  width: 100%;
  height: 150px;
  border: 0;
}

.preview-container {
  display: flex;
  justify-content: center;
  width: 100%;
  background-color: var(--color-v3-surface-container-highest);
}

.thumbnail-img {
  width: 100%;
  height: auto;
  object-fit: cover;
  object-position: center top;
  pointer-events: none;
}

.file-chunk-container {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  height: 36px;
  padding: 4px 8px;
  background-color: var(--color-v3-surface-container-high);
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
</style>
