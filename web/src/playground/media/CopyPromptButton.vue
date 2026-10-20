<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'

const props = defineProps<{ text: () => string; disabled: boolean; disabledMessage?: string }>()
const { t } = useI18n()
const { showNotice } = useConsole()

// copy 把当前提示词复制到剪贴板
async function copy(): Promise<void> {
  if (props.disabled) return
  await navigator.clipboard.writeText(props.text())
  showNotice(t('pg.copied'), 'success')
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
