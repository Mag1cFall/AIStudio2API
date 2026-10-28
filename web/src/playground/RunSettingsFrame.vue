<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import '@/playground/styles/run-settings.css'

const props = withDefaults(
  defineProps<{
    open: boolean
    narrow: boolean
    resettable?: boolean
    codeButton?: boolean
    title?: string | undefined
  }>(),
  { resettable: true, codeButton: true, title: '' },
)
const emit = defineEmits<{ close: []; code: []; reset: [] }>()
const { t, locale } = useI18n()
const heading = ref<HTMLElement | null>(null)
// compact 表示标题放不下时获取代码按钮只显示图标
const compact = ref(false)

// fit 先显示获取代码按钮的文字，标题因此被截断时改为只显示图标
async function fit(): Promise<void> {
  compact.value = false
  await nextTick()
  const element = heading.value
  compact.value = element !== null && element.scrollWidth > element.clientWidth
}

onMounted(() => void document.fonts.ready.then(fit))
watch([locale, () => props.title], () => void fit())
</script>

<template>
  <aside class="ms-run-settings" :class="{ expanded: open, narrow }" :inert="!open">
    <div class="overlay-header">
      <h2 ref="heading" class="header-label">{{ title || t('pg.runSettings') }}</h2>
      <div class="right">
        <button
          v-if="codeButton"
          v-tooltip.below="t('pg.getCodeTip')"
          type="button"
          ms-button
          class="ms-button-borderless ms-button-small"
          :class="{ 'ms-button-icon': compact }"
          :aria-label="t('pg.getCode')"
          @click="emit('code')"
        >
          <UiIcon name="code" :size="18" />
          <template v-if="!compact">{{ t('pg.getCode') }}</template>
        </button>
        <button
          v-if="resettable"
          v-tooltip.below="t('pg.resetSettings')"
          type="button"
          ms-button
          class="ms-button-borderless ms-button-icon ms-button-small"
          :aria-label="t('pg.resetSettings')"
          @click="emit('reset')"
        >
          <UiIcon name="resetSettings" :size="18" />
        </button>
        <button
          type="button"
          ms-button
          class="ms-button-borderless ms-button-icon ms-button-small"
          :aria-label="t('pg.closeSettings')"
          @click="emit('close')"
        >
          <UiIcon name="close" :size="18" />
        </button>
      </div>
    </div>
    <div class="scrollable-area">
      <slot />
    </div>
  </aside>
</template>

<style scoped>
.ms-run-settings {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  width: 300px;
  height: 100%;
  overflow: hidden;
  border-left: 1px solid var(--color-v3-outline-var);
  color: var(--color-run-settings-text);
  user-select: none;
  transition:
    width 200ms ease-in,
    opacity 200ms ease-in,
    border-left-width 200ms ease-in;
}

.ms-run-settings:not(.narrow) > * {
  min-width: 299px;
}

.ms-run-settings:not(.expanded):not(.narrow) {
  width: 0;
  border-left-width: 0;
  opacity: 0;
}

.ms-run-settings.narrow {
  position: fixed;
  z-index: 5;
  top: 0;
  right: 0;
  bottom: 0;
  backdrop-filter: blur(3px);
  background: var(--color-v3-surface-container);
  opacity: 0;
  pointer-events: none;
  transform: translate(100%);
  transition:
    transform 0.2s cubic-bezier(0.4, 0, 0.2, 0),
    opacity 0.2s cubic-bezier(0.4, 0, 0.2, 0);
}

.ms-run-settings.narrow.expanded {
  opacity: 1;
  pointer-events: auto;
  transform: translate(0);
}

.overlay-header {
  display: flex;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
  padding: 16px 16px 0;
}

.header-label {
  min-width: 0;
  margin: 0;
  overflow: hidden;
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.right {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  margin-left: auto;
}

.scrollable-area {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  overflow: hidden auto;
  padding: 16px;
}
</style>
