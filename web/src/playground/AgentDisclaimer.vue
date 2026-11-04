<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'

const props = defineProps<{
  // running 表示 agent 正在运行，开始运行时横幅收起
  running: boolean
  // tokenCap 是已设置的 token 上限，有值时横幅收起，按钮为修改上限
  tokenCap: number | null
}>()
const emit = defineEmits<{ tokenCap: [] }>()
const { t } = useI18n()

// expanded 是横幅是否展开，与官网一样运行中或设置上限后收起，收起后由灯泡按钮重新展开
const expanded = ref(!props.running && props.tokenCap === null)
watch(
  () => props.running || props.tokenCap !== null,
  (collapse) => {
    if (collapse) expanded.value = false
  },
)
const collapseButton = ref<HTMLButtonElement>()
const expandButton = ref<HTMLButtonElement>()

// toggle 展开或收起横幅，焦点移到另一状态的按钮
function toggle(): void {
  expanded.value = !expanded.value
  void nextTick(() => (expanded.value ? collapseButton : expandButton).value?.focus())
}
</script>

<template>
  <div class="disclaimer-expanded" :class="{ collapsed: !expanded }">
    <UiIcon name="lightbulb" :size="18" class="lightbulb-icon" />
    <div class="disclaimer-text">
      <span>{{ t('pg.agentDisclaimer') }}</span>
      <div class="token-cap-container">
        <a
          href="https://aistudio.google.com/docs/aistudio-agents#safety-and-cost-management"
          target="_blank"
          rel="noopener noreferrer"
          >{{ t('pg.learnMore') }}</a
        >
        <button
          type="button"
          ms-button
          class="ms-button-primary ms-button-small"
          @click="emit('tokenCap')"
        >
          {{ tokenCap === null ? t('pg.setTokenCap') : t('pg.editTokenCap') }}
        </button>
      </div>
    </div>
    <button
      ref="collapseButton"
      type="button"
      ms-button
      class="collapse-button ms-button-borderless ms-button-icon ms-button-small"
      :aria-label="t('pg.collapseDisclaimer')"
      :tabindex="expanded ? 0 : -1"
      @click="toggle"
    >
      <UiIcon name="collapseContent" :size="18" />
    </button>
  </div>
  <button
    ref="expandButton"
    v-tooltip.below="t('pg.showDisclaimer')"
    type="button"
    ms-button
    class="collapsed-button ms-button-borderless ms-button-icon"
    :class="{ 'hidden-button': expanded }"
    :aria-label="t('pg.showDisclaimer')"
    :tabindex="expanded ? -1 : 0"
    @click="toggle"
  >
    <UiIcon name="lightbulb" :size="20" />
  </button>
</template>

<style scoped>
/* disclaimer-expanded 为输入框上方的 agent 提示：带 token 上限按钮时铺满输入框宽度、压在输入框后方 6px，收起时下滑隐去，样式与动效取官网 ms-light-bulb-disclaimer */
.disclaimer-expanded {
  position: absolute;
  z-index: -1;
  right: 0;
  bottom: calc(100% - 6px);
  left: 0;
  display: flex;
  align-items: flex-start;
  gap: 4px;
  padding: 12px 12px 18px;
  border: 1px solid var(--color-v3-outline);
  border-bottom: none;
  border-radius: 12px 12px 0 0;
  background: var(--color-v3-surface-container);
  opacity: 1;
  transform: translateY(0);
  transition:
    opacity 0.2s cubic-bezier(0.23, 1, 0.32, 1),
    transform 0.2s cubic-bezier(0.23, 1, 0.32, 1);
}

.disclaimer-expanded.collapsed {
  opacity: 0;
  pointer-events: none;
  transform: translateY(100%);
}

.lightbulb-icon {
  flex-shrink: 0;
  color: var(--color-v3-accent-1);
}

.disclaimer-text {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.disclaimer-text a {
  color: var(--color-v3-text-link);
  text-decoration: none;
}

.disclaimer-text a:hover {
  text-decoration: underline;
}

.token-cap-container {
  display: flex;
  align-items: center;
  gap: 12px;
}

.collapse-button {
  flex-shrink: 0;
  margin-top: -6px;
  color: var(--color-v3-text-var);
}

.collapsed-button {
  flex-shrink: 0;
  align-self: flex-start;
  margin-top: -5.5px;
  color: var(--color-v3-accent-1);
  opacity: 1;
  transform: scale(1);
  transition:
    opacity 0.2s cubic-bezier(0.23, 1, 0.32, 1),
    transform 0.25s cubic-bezier(0.175, 0.885, 0.32, 1.25);
}

.collapsed-button.hidden-button {
  opacity: 0;
  pointer-events: none;
  transform: scale(0.85);
}

@media (prefers-reduced-motion: reduce) {
  .disclaimer-expanded,
  .collapsed-button {
    transition: opacity 0.2s ease;
  }

  .disclaimer-expanded.collapsed,
  .collapsed-button.hidden-button {
    transform: none;
  }
}
</style>
