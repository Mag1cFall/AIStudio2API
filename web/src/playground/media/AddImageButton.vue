<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { ref } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { useI18n } from '@/i18n'
import type { IconName } from '@/playground/ui/icons'
import UiIcon from '@/playground/ui/UiIcon.vue'
import CameraDialog from '../CameraDialog.vue'

withDefaults(
  defineProps<{
    icon: IconName
    label: string
    chip?: boolean
    disabled?: boolean
    multiple?: boolean
  }>(),
  { chip: false, disabled: false, multiple: false },
)
const emit = defineEmits<{ images: [images: File[]] }>()
const { t } = useI18n()
const input = ref<HTMLInputElement>()
const camera = ref<InstanceType<typeof CameraDialog>>()

// onChange 读取选择的图片文件
function onChange(): void {
  const files = [...(input.value?.files ?? [])].filter((file) => file.type.startsWith('image/'))
  if (input.value) input.value.value = ''
  if (files.length > 0) emit('images', files)
}

// onCapture 把相机照片作为图片文件
function onCapture(photo: Blob): void {
  emit('images', [
    new File([photo], `Camera ${new Date().toLocaleString()}.jpg`, { type: 'image/jpeg' }),
  ])
}
</script>

<template>
  <DropdownMenuRoot>
    <DropdownMenuTrigger as-child :disabled="disabled">
      <button
        v-tooltip.below="label"
        type="button"
        ms-button
        :class="
          chip ? 'ms-button-filter-chip add-chip' : 'ms-button-primary ms-button-icon add-icon'
        "
        :aria-label="label"
        :aria-disabled="disabled"
      >
        <UiIcon :name="icon" :size="20" />
      </button>
    </DropdownMenuTrigger>
    <DropdownMenuPortal>
      <DropdownMenuContent side="top" align="start" :side-offset="4" class="ms-menu-panel">
        <DropdownMenuItem class="ms-menu-item" @select="input?.click()">
          <UiIcon name="upload" :size="18" />
          <span class="ms-menu-item-text">{{ t('pg.uploadImage') }}</span>
        </DropdownMenuItem>
        <DropdownMenuItem class="ms-menu-item" @select="camera?.start()">
          <UiIcon name="camera" :size="18" />
          <span class="ms-menu-item-text">{{ t('pg.camera') }}</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
  <input
    ref="input"
    type="file"
    class="file-input"
    accept="image/*"
    :multiple="multiple"
    @change="onChange"
  />
  <CameraDialog ref="camera" @capture="onCapture" />
</template>

<style scoped>
.file-input {
  display: none;
}

.add-chip {
  width: 32px;
  padding: 8px 6px;
  border-color: var(--color-v3-outline);
  background: transparent;
}

.add-icon {
  width: 32px;
  height: 32px;
}
</style>
