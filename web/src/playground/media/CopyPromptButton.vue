<script setup lang="ts">
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { copyToClipboard } from '../clipboard'

const props = defineProps<{ text: () => string; disabled: boolean; disabledMessage?: string }>()
const { t } = useI18n()

// copy 把当前提示词复制到剪贴板
function copy(): void {
  if (!props.disabled) void copyToClipboard(props.text())
}
</script>

<template>
  <button
    v-tooltip.below="disabled && disabledMessage ? disabledMessage : t('pg.copyPromptToClipboard')"
    type="button"
    ms-button
    class="ms-button-borderless ms-button-icon"
    :aria-label="t('pg.copyPromptToClipboard')"
    :aria-disabled="disabled"
    @click="copy"
  >
    <UiIcon name="copy" :size="20" />
  </button>
</template>
