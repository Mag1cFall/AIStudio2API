<script setup lang="ts">
import { HoverCardContent, HoverCardPortal, HoverCardRoot, HoverCardTrigger } from 'reka-ui'
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import { formatDuration } from './duration'

const props = defineProps<{
  duration: number
  latency?: number | undefined
  tokens?: number | undefined
}>()
const { t, locale } = useI18n()

const label = computed(() => formatDuration(props.duration / 1000))
// tokensPerSecond 是首字之后每秒输出的 token 数
const tokensPerSecond = computed(() =>
  props.tokens !== undefined && props.latency !== undefined && props.duration > props.latency
    ? props.tokens / ((props.duration - props.latency) / 1000)
    : undefined,
)
// stats 与官网 ms-model-run-stats 一样，有首字时间或每秒 token 数时悬停耗时显示运行统计
const stats = computed(() => props.latency !== undefined || tokensPerSecond.value !== undefined)
const numberFormat = computed(() => new Intl.NumberFormat(locale.value))
</script>

<template>
  <span class="model-run-time-pill">
    <HoverCardRoot v-if="stats" :open-delay="300" :close-delay="150">
      <HoverCardTrigger as-child>
        <span>{{ label }}</span>
      </HoverCardTrigger>
      <HoverCardPortal>
        <HoverCardContent
          side="top"
          align="start"
          :side-offset="4"
          role="dialog"
          :aria-label="t('pg.runStats')"
          class="model-run-stats"
        >
          <div class="panel-header">{{ t('pg.runStats') }}</div>
          <ul class="stats-list">
            <li v-if="latency !== undefined" class="stat-item">
              <span>{{ t('pg.timeToFirstToken') }}</span>
              <span>{{ Math.round(latency / 100) / 10 }}s</span>
            </li>
            <li v-if="tokensPerSecond !== undefined" class="stat-item">
              <span>{{ t('pg.tokensPerSecond') }}</span>
              <span>{{ Math.round(tokensPerSecond * 10) / 10 }}</span>
            </li>
            <li v-if="tokens !== undefined" class="stat-item">
              <span>{{ t('pg.outputTokens') }}</span>
              <span>{{ numberFormat.format(tokens) }}</span>
            </li>
          </ul>
        </HoverCardContent>
      </HoverCardPortal>
    </HoverCardRoot>
    <template v-else>{{ label }}</template>
  </span>
</template>

<style scoped>
.model-run-time-pill {
  display: flex;
  align-items: center;
  align-self: stretch;
  padding: 0 0.5em;
  border-radius: 8px;
  background: transparent;
  cursor: pointer;
  font-variant-numeric: tabular-nums;
}
</style>

<style>
.model-run-stats {
  z-index: 1000;
  width: 300px;
  max-width: calc(100vw - 16px);
  overflow: auto;
  border-radius: 8px;
  background: var(--color-v3-surface-container-high);
  box-shadow:
    0 1px 2px 0 rgba(60, 64, 67, 0.3),
    0 1px 3px 1px rgba(60, 64, 67, 0.15);
  color: var(--color-on-surface);
}

.model-run-stats .panel-header {
  padding: 12px;
  font-size: 16px;
  font-weight: 500;
  line-height: 24px;
}

.model-run-stats .stats-list {
  margin: 0;
  padding: 12px;
  list-style: none;
  font-size: 14px;
  line-height: 21px;
}

.model-run-stats .stat-item {
  display: flex;
  justify-content: space-between;
  gap: 16px;
}
</style>
