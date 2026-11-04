<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ text: string; label: string }>()
const { t } = useI18n()
// clipboardAvailable 表示页面处于可使用剪贴板 API 的安全上下文，其余情况下不显示按钮
const clipboardAvailable = window.isSecureContext
const copied = ref(false)
let timer = 0

// copy 复制文字并在 1.2 秒内显示已复制
async function copy(): Promise<void> {
  await navigator.clipboard.writeText(props.text)
  copied.value = true
  window.clearTimeout(timer)
  timer = window.setTimeout(() => (copied.value = false), 1200)
}
</script>

<template>
  <button
    v-if="clipboardAvailable"
    v-tooltip="copied ? t('common.copied') : label"
    type="button"
    class="flex h-7 w-7 shrink-0 items-center justify-center rounded text-gray-500 transition hover:bg-raised hover:text-gray-200"
    :class="{ 'text-green-400 hover:text-green-400': copied }"
    :aria-label="label"
    @click="copy"
  >
    <UiIcon :name="copied ? 'check' : 'copy'" :size="14" />
  </button>
</template>
