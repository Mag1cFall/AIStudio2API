<script setup lang="ts">
import { nextTick, onUnmounted, ref, watch } from 'vue'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'

const emit = defineEmits<{ capture: [photo: Blob] }>()
const { t } = useI18n()
const { showNotice } = useConsole()
const open = ref(false)
const video = ref<HTMLVideoElement>()
let stream: MediaStream | undefined

// start 打开摄像头预览，没有权限时提示
async function start(): Promise<void> {
  try {
    stream = await navigator.mediaDevices.getUserMedia({ video: true })
  } catch {
    showNotice(t('pg.cameraDenied'), 'error')
    return
  }
  open.value = true
  await nextTick()
  if (video.value) video.value.srcObject = stream
}

// capture 拍下当前画面为 JPEG 并关闭预览
async function capture(): Promise<void> {
  const element = video.value
  if (element === undefined) return
  const canvas = document.createElement('canvas')
  canvas.width = element.videoWidth
  canvas.height = element.videoHeight
  canvas.getContext('2d')?.drawImage(element, 0, 0)
  const blob = await new Promise<Blob | null>((resolve) =>
    canvas.toBlob(resolve, 'image/jpeg', 0.92),
  )
  if (blob) emit('capture', blob)
  open.value = false
}

// stopStream 关闭摄像头
function stopStream(): void {
  stream?.getTracks().forEach((track) => track.stop())
  stream = undefined
}

watch(open, (value) => {
  if (!value) stopStream()
})
onUnmounted(stopStream)

defineExpose({ start })
</script>

<template>
  <UiDialog v-model:open="open" :title="t('pg.camera')" width="640px">
    <video ref="video" class="camera-preview" autoplay playsinline muted></video>
    <template #actions>
      <button type="button" ms-button class="ms-button-borderless" @click="open = false">
        {{ t('common.cancel') }}
      </button>
      <button type="button" ms-button class="ms-button-primary" @click="capture">
        <UiIcon name="camera" :size="18" />
        {{ t('pg.capture') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.camera-preview {
  width: 100%;
  border-radius: 8px;
  background-color: #000;
}
</style>
