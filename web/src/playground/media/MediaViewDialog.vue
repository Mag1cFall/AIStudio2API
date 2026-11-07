<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import type { MediaKind } from '../attachment'

const open = defineModel<boolean>('open', { required: true })
// src 是媒体地址，text 是文本附件的全文
const props = withDefaults(
  defineProps<{ title: string; src: string; kind?: MediaKind; text?: string }>(),
  { kind: 'video', text: '' },
)
const emit = defineEmits<{ download: [] }>()
const { t } = useI18n()

// width 与官网 ms-view-media-dialog 一致：图片按图片宽度，音频最宽 560 px，文本最宽 800 px，视频与 PDF 占视口 80%
const width = computed(
  () =>
    ({ image: 'fit-content', audio: '560px', text: '800px', video: '80vw', pdf: '80vw' })[
      props.kind
    ],
)
</script>

<template>
  <UiDialog v-model:open="open" :title="title" :width="width">
    <template #header>
      <button
        v-tooltip.below="t('pg.download')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.download')"
        @click="emit('download')"
      >
        <UiIcon name="download" :size="20" />
      </button>
    </template>
    <div v-if="kind === 'image'" class="image-container">
      <img class="main-image" :src="src" :alt="title" />
    </div>
    <div v-else-if="kind === 'audio'" class="audio-container">
      <audio class="main-audio" :src="src" controls></audio>
    </div>
    <pre v-else-if="kind === 'text'" class="main-code" tabindex="0">{{ text }}</pre>
    <iframe v-else-if="kind === 'pdf'" class="main-pdf" :src="src" :title="title"></iframe>
    <div v-else class="video-container">
      <video class="main-video" :src="src" controls playsinline></video>
    </div>
  </UiDialog>
</template>

<style scoped>
.image-container {
  text-align: center;
}

.main-image {
  display: block;
  max-width: calc(80vw - 34px);
  max-height: calc(80dvh - 97px);
  margin: auto;
}

.audio-container {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
}

.main-audio {
  width: 100%;
  max-width: 500px;
}

.main-code {
  max-height: 60vh;
  margin: 0;
  padding: 8px;
  overflow: auto;
  border-radius: 8px;
  background-color: var(--color-v3-surface-container);
  color: var(--color-v3-text);
  font-family: 'Google Sans Code', monospace;
  font-size: 13px;
  line-height: 1.5;
}

.main-pdf {
  display: block;
  width: 100%;
  height: calc(80dvh - 97px);
  border: none;
  border-radius: 8px;
  background: #fff;
}

.video-container {
  display: flex;
  max-width: 100%;
  min-width: min(77vw, 100%);
  max-height: calc(80dvh - 72px);
  padding: 12px 4px;
}

.main-video {
  width: 100%;
  border-radius: 12px;
  object-fit: contain;
}
</style>
