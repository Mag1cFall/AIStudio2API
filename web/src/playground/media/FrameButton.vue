<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import AddImageButton from './AddImageButton.vue'
import { dataURL } from '../protocols'
import type { VideoFrame } from './video'

const props = defineProps<{
  frame: VideoFrame | undefined
  label: string
}>()
const emit = defineEmits<{ add: [image: File]; remove: [] }>()
const { t } = useI18n()
const source = computed(() => (props.frame ? dataURL(props.frame) : ''))
</script>

<template>
  <div class="ms-frame-button">
    <div v-if="frame" class="media-container">
      <div class="preview-container">
        <img class="thumbnail" :src="source" :alt="frame.name" />
      </div>
      <span v-tooltip="frame.name" class="name">{{ frame.name }}</span>
      <button
        v-tooltip.below="t('pg.removeMedia')"
        type="button"
        ms-button
        class="close-button ms-button-borderless ms-button-icon ms-button-small"
        :aria-label="t('pg.removeMedia')"
        @click="emit('remove')"
      >
        <UiIcon name="close" :size="15" />
      </button>
    </div>
    <AddImageButton v-else icon="addPhoto" :label="label" @images="emit('add', $event[0]!)" />
  </div>
</template>

<style scoped>
.ms-frame-button {
  display: inline-flex;
  align-items: center;
}

@keyframes frame-button-enter {
  0% {
    opacity: 0;
    transform: scale(0.9);
  }

  to {
    opacity: 1;
    transform: scale(1);
  }
}

.media-container {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 8px;
  width: fit-content;
  height: 56px;
  padding: 8px 12px;
  overflow: hidden;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background-color: var(--color-v3-surface-container);
  animation: frame-button-enter 0.2s ease-out;
}

.media-container .name {
  max-width: 100px;
  overflow: hidden;
  color: var(--color-v3-text);
  font-size: 12px;
  font-weight: 400;
  line-height: normal;
  text-overflow: ellipsis;
  white-space: nowrap;
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

.thumbnail {
  width: 100%;
  height: 100%;
  border-radius: 4px;
  object-fit: cover;
}

.close-button {
  color: var(--color-v3-text-var);
}
</style>
