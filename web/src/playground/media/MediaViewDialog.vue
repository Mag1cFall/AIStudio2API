<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'

const open = defineModel<boolean>('open', { required: true })
defineProps<{ title: string; src: string }>()
const emit = defineEmits<{ download: [] }>()
const { t } = useI18n()
</script>

<template>
  <UiDialog v-model:open="open" :title="title" width="80vw">
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
    <div class="video-container">
      <video class="main-video" :src="src" controls playsinline></video>
    </div>
  </UiDialog>
</template>

<style scoped>
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
