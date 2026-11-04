<script setup lang="ts">
import DisclosureChevron from '@/components/DisclosureChevron.vue'
import UiCollapse from '@/components/UiCollapse.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import type { IconName } from '@/playground/ui/icons'

const expanded = defineModel<boolean>('expanded', { default: false })
defineProps<{
  icon: IconName
  title: string
  summary?: string
  state?: 'running' | 'ok' | 'error'
  badge?: string | undefined
}>()

// onKey 以回车或空格展开与收起
function onKey(event: KeyboardEvent): void {
  if (event.key !== 'Enter' && event.key !== ' ') return
  event.preventDefault()
  expanded.value = !expanded.value
}
</script>

<template>
  <div class="tool-step" :data-open="expanded || undefined" :data-state="state">
    <div
      class="step-row"
      role="button"
      tabindex="0"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
      @keydown="onKey"
    >
      <span class="leading">
        <UiIcon :name="icon" :size="14" class="step-icon" />
        <span class="chevron"
          ><DisclosureChevron :open="expanded" :turn="180"
            ><UiIcon name="chevronDown" :size="14" /></DisclosureChevron
        ></span>
      </span>
      <span class="title shimmer" :title="title"
        >{{ title
        }}<span v-if="state === 'running'" class="sweep" aria-hidden="true"
          ><span class="highlight">{{ title }}</span></span
        ></span
      >
      <template v-if="summary">
        <span class="sep" aria-hidden="true" />
        <span class="summary shimmer"
          >{{ summary
          }}<span v-if="state === 'running'" class="sweep" aria-hidden="true"
            ><span class="highlight">{{ summary }}</span></span
          ></span
        >
      </template>
      <span v-if="badge" class="step-badge"
        ><span class="badge-dot" aria-hidden="true" />{{ badge }}</span
      >
    </div>
    <UiCollapse :open="expanded"
      ><div class="step-body"><slot /></div
    ></UiCollapse>
  </div>
</template>

<style scoped>
/* 行高、图标与分隔点取 dsh ToolRow 与 DisclosureRow，颜色取 AI Studio 主题 */
.tool-step {
  display: flex;
  flex-direction: column;
}

.step-row {
  display: flex;
  align-items: center;
  height: 24px;
  min-width: 0;
  color: var(--color-v3-text-var);
  cursor: pointer;
  font-size: 13px;
  line-height: 24px;
  outline: none;
  transition: color 100ms ease;
}

.step-row:hover,
.step-row:focus-visible,
.tool-step[data-open] .step-row {
  color: var(--color-v3-text);
}

.leading {
  position: relative;
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  margin-right: 6px;
}

.chevron {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0;
  transition: opacity 100ms ease;
}

.step-row:hover .step-icon,
.tool-step[data-open] .step-icon {
  opacity: 0;
}

.step-row:hover .chevron,
.tool-step[data-open] .chevron {
  opacity: 1;
}

.title {
  flex: 0 1 auto;
}

.sep {
  flex: none;
  width: 2px;
  height: 2px;
  margin: 0 8px;
  border-radius: 1px;
  background: currentcolor;
  opacity: 0.6;
}

.summary {
  flex: 1 1 auto;
}

.tool-step:has(.step-badge) .summary {
  flex-grow: 0;
}

.tool-step[data-state='error'] .summary {
  color: var(--color-error);
}

/* step-badge 为失败与超时的状态徽标：中性胶囊加错误色圆点，取 dsh ExitCodeBadge，颜色与圆角取 AI Studio 主题 */
.step-badge {
  display: inline-flex;
  flex: none;
  gap: 5px;
  align-items: center;
  height: 20px;
  margin-left: 8px;
  padding: 0 8px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 10px;
  background: var(--color-v3-surface-container);
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
  white-space: nowrap;
}

.badge-dot {
  flex: none;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: var(--color-error);
}

.shimmer {
  position: relative;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 运行中以遮罩扫过文字副本：遮罩与副本反向平移，字形保持对齐 */
.sweep {
  position: absolute;
  inset: 0;
  overflow: hidden;
  color: color-mix(in srgb, var(--color-v3-text) 45%, transparent);
  mask-image: linear-gradient(105deg, transparent 0%, black 40% 60%, transparent 100%);
  pointer-events: none;
  transform: translateX(-100%);
  animation: step-sweep 1.5s steps(48, end) 0.3s infinite;
}

.highlight {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  transform: translateX(100%);
  animation: step-highlight 1.5s steps(48, end) 0.3s infinite;
}

@keyframes step-sweep {
  0% {
    transform: translateX(-100%);
  }

  66.6667%,
  100% {
    transform: translateX(100%);
  }
}

@keyframes step-highlight {
  0% {
    transform: translateX(100%);
  }

  66.6667%,
  100% {
    transform: translateX(-100%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .sweep {
    display: none;
  }
}

.step-body {
  display: flex;
  flex-direction: column;
}
</style>
