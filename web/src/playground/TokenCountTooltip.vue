<script setup lang="ts">
import { HoverCardContent, HoverCardPortal, HoverCardRoot, HoverCardTrigger } from 'reka-ui'
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import { Capability, hasCode, isAgent, priceLines } from './catalog'
import { usePlayground } from './session'

const { t, locale, tokenLabel } = useI18n()
const { session, currentModel, usage } = usePlayground()

const numberFormat = computed(() => new Intl.NumberFormat(locale.value))
const total = computed(() => usage.value.total)
const input = computed(() => usage.value.input)
const output = computed(() => usage.value.output)
const cached = computed(() => usage.value.cached)

// exceeded 表示当前总 token 数已达到或超过模型的上下文上限
const exceeded = computed(() => {
  const limit = currentModel.value?.input_token_limit
  return limit !== undefined && limit > 0 && total.value >= limit
})

// usageText 是标题行右侧的当前总 token 与模型输入上限
const usageText = computed(() => {
  const count = total.value
  const limit = currentModel.value?.input_token_limit
  return limit
    ? `${numberFormat.value.format(count)} / ${numberFormat.value.format(limit)}`
    : numberFormat.value.format(count)
})

// parseRate 从价格整句说明中提取每百万 token 的美元单价
function parseRate(text: string, label: 'Input' | 'Output'): number | undefined {
  const match = text.match(new RegExp(`${label}:\\s*\\$([0-9]+(?:\\.[0-9]+)?)`, 'i'))
  return match ? Number(match[1]) : undefined
}

// partCost 按每百万 token 单价或按次单价计算一项费用
function partCost(rate = 0, perRequest = false, tokens = 0): number {
  if (rate <= 0 || tokens <= 0) return 0
  return perRequest ? rate : (tokens / 1_000_000) * rate
}

// estimatedCost 按当前模型价格分档估算本次对话的美元费用，命中缓存的输入按输入单价的 10% 计，没有价格信息时为 null
const estimatedCost = computed<number | null>(() => {
  const model = currentModel.value
  const pricing = model?.details?.pricing
  if (!model || !pricing || pricing.length === 0) return null
  const inTokens = input.value
  const hitTokens = Math.min(inTokens, cached.value)
  const billedInput = Math.max(0, inTokens - hitTokens) + hitTokens * 0.1
  const outTokens = output.value
  const first = pricing[0]!
  if (first.text && first.input === undefined && first.output === undefined) {
    const inRate = parseRate(first.text, 'Input')
    const outRate = parseRate(first.text, 'Output')
    if (inRate === undefined && outRate === undefined) return null
    return partCost(inRate, false, billedInput) + partCost(outRate, false, outTokens)
  }
  const usesThinking = hasCode(model, Capability.ThinkingBudget)
    ? session.settings.thinkingBudget !== 0
    : hasCode(model, Capability.ThinkingLevel) || hasCode(model, Capability.Thinking)
  const byThinking = pricing.some((row) => row.thinking)
    ? pricing.filter((row) => Boolean(row.thinking) === usesThinking)
    : pricing
  const pool = byThinking.length > 0 ? byThinking : pricing
  const tier =
    pool.find((row) => row.up_to !== undefined && row.up_to > 0 && inTokens <= row.up_to) ??
    pool.at(-1)!
  if (tier.input === undefined && tier.output === undefined) return null
  const inCost = partCost(tier.input, tier.input_per_request, billedInput)
  const outCost = partCost(tier.output, tier.output_per_request, outTokens)
  return inCost + outCost
})

// formatCost 把估算美元费用格式化为可读金额
function formatCost(value: number): string {
  if (value === 0) return '$0.00'
  if (value < 0.0001) return '< $0.0001'
  return `$${new Intl.NumberFormat(locale.value, {
    minimumFractionDigits: 2,
    maximumFractionDigits: value < 0.01 ? 4 : 2,
  }).format(value)}`
}

// lines 是底栏显示的价格分档说明
const lines = computed(() => {
  const model = currentModel.value
  if (!model) return []
  if (isAgent(model)) return [{ text: t('pg.agentPricing') }]
  return priceLines(model, t)
})

// pricingURL 是底栏价格页面的链接地址
const pricingURL = computed(() => {
  const model = currentModel.value
  if (!model) return ''
  if (model.details?.pricing_url) return model.details.pricing_url
  if (isAgent(model)) return 'https://aistudio.google.com/docs/pricing#pricing-for-agents'
  return lines.value.length > 0 ? 'https://aistudio.google.com/docs/pricing' : ''
})
</script>

<template>
  <HoverCardRoot v-if="total > 0" :open-delay="200" :close-delay="150">
    <HoverCardTrigger as-child>
      <span class="token-count" :class="{ exceeded }" tabindex="0">{{ tokenLabel(total) }}</span>
    </HoverCardTrigger>
    <HoverCardPortal>
      <HoverCardContent
        side="bottom"
        align="start"
        :side-offset="8"
        :collision-padding="12"
        role="dialog"
        :aria-label="t('pg.tokenCountTooltip')"
        class="token-count-tooltip"
      >
        <div class="tooltip-row">
          <span class="token-count-tooltip-section-header">{{ t('pg.tokenUsage') }}</span>
          <span class="tooltip-text" :class="{ exceeded }">{{ usageText }}</span>
        </div>
        <div class="tooltip-row">
          <span>{{ t('pg.inputTokens') }}</span>
          <span>{{ numberFormat.format(input) }}</span>
        </div>
        <div v-if="cached > 0" class="tooltip-row">
          <span>{{ t('pg.cachedTokens') }}</span>
          <span>{{ numberFormat.format(cached) }}</span>
        </div>
        <div class="tooltip-row">
          <span>{{ t('pg.outputTokens') }}</span>
          <span>{{ numberFormat.format(output) }}</span>
        </div>
        <div v-if="estimatedCost !== null" class="tooltip-row">
          <span>{{ t('pg.estimatedCost') }}</span>
          <span>{{ formatCost(estimatedCost) }}</span>
        </div>
        <template v-if="lines.length > 0 || pricingURL">
          <div role="separator" aria-orientation="horizontal" class="tooltip-divider"></div>
          <div class="token-count-tooltip-footer">
            <p v-for="(line, index) in lines" :key="index">{{ line.text }}</p>
            <p v-if="pricingURL">
              {{ t('pg.seePricingDetails') }}
              <a :href="pricingURL" target="_blank" rel="noopener">{{ t('pg.pricingPage') }}</a>
            </p>
          </div>
        </template>
      </HoverCardContent>
    </HoverCardPortal>
  </HoverCardRoot>
</template>

<style scoped>
.token-count {
  padding: 0 8px;
  cursor: default;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  line-height: 18px;
  outline: none;
  white-space: nowrap;
}

.token-count.exceeded {
  color: var(--color-v3-accent-3);
}
</style>

<style>
.token-count-tooltip {
  z-index: 1000;
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 220px;
  max-width: min(320px, calc(100vw - 16px));
  min-height: 64px;
  padding: 12px 16px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container-high);
  box-shadow: var(--shadow-v3-dropdown);
  color: var(--color-v3-text);
  font-size: 12px;
  line-height: 18px;
}

.token-count-tooltip .tooltip-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 16px;
  font-variant-numeric: tabular-nums;
}

.token-count-tooltip .token-count-tooltip-section-header {
  font-weight: 500;
}

.token-count-tooltip .tooltip-text.exceeded {
  color: var(--color-v3-accent-3);
}

.token-count-tooltip .tooltip-divider {
  margin: 6px 0;
  border-top: 1px solid var(--color-v3-outline-var);
}

.token-count-tooltip .token-count-tooltip-footer {
  display: flex;
  flex-direction: column;
  gap: 4px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 16px;
}

.token-count-tooltip .token-count-tooltip-footer p {
  margin: 0;
}

.token-count-tooltip .token-count-tooltip-footer a {
  color: var(--color-v3-text-link);
  text-decoration: underline;
}
</style>
