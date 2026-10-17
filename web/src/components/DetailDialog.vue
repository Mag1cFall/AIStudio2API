<script setup lang="ts">
import { ref } from 'vue'
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import { useI18n } from '@/i18n'
import UiIcon from './UiIcon.vue'

defineProps<{
  open: boolean
  title: string
  description?: string | undefined
  copyText?: string | undefined
}>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const copied = ref(false)
// clipboardAvailable 表示页面处于可使用剪贴板 API 的安全上下文
const clipboardAvailable = window.isSecureContext

// copy 复制完整正文并短暂显示已复制
async function copy(text: string): Promise<void> {
  await navigator.clipboard.writeText(text)
  copied.value = true
  window.setTimeout(() => (copied.value = false), 1200)
}
</script>

<template>
  <DialogRoot :open="open" @update:open="(value: boolean) => !value && emit('close')">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-[70] bg-black/60" />
      <DialogContent
        class="detail-dialog fixed top-1/2 left-1/2 z-[71] flex max-h-[85vh] w-[calc(100vw-2rem)] max-w-3xl -translate-x-1/2 -translate-y-1/2 flex-col rounded-lg border border-line bg-panel shadow-2xl focus:outline-none"
      >
        <header class="flex items-start gap-3 border-b border-line px-5 py-4">
          <div class="min-w-0 flex-1">
            <DialogTitle class="text-base font-semibold [overflow-wrap:anywhere] text-white">
              {{ title }}
            </DialogTitle>
            <DialogDescription
              class="mt-1 text-xs [overflow-wrap:anywhere] text-gray-400"
              :class="{ 'sr-only': !description }"
            >
              {{ description || title }}
            </DialogDescription>
          </div>
          <button
            v-if="copyText && clipboardAvailable"
            type="button"
            class="flex shrink-0 items-center gap-1 rounded px-2 py-1 text-xs text-gray-400 hover:bg-raised hover:text-white"
            @click="copy(copyText)"
          >
            <UiIcon :name="copied ? 'check' : 'copy'" :size="12" />
            {{ copied ? t('common.copied') : t('common.copy') }}
          </button>
          <DialogClose
            class="shrink-0 rounded p-1 text-gray-500 hover:bg-raised hover:text-white"
            :aria-label="t('common.close')"
          >
            <UiIcon name="close" :size="16" />
          </DialogClose>
        </header>
        <div class="detail-dialog-body min-h-0 flex-1 overflow-y-auto px-5 py-4 text-sm">
          <slot />
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
