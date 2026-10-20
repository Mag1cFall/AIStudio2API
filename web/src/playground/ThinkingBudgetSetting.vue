<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import type { ThinkingBudget } from '@/types'
import UiSlider from '@/playground/ui/UiSlider.vue'
import UiSwitch from '@/playground/ui/UiSwitch.vue'

const props = defineProps<{
  config: ThinkingBudget | undefined
  disabled: boolean
  disabledTip: string
}>()
// value 是思考预算：-1 由模型决定，0 不思考，其余为手动设定的 token 数
const value = defineModel<number>({ required: true })
const { t } = useI18n()

// max、min 与 initial 是官网缺省的预算上限 24576、下限 0 与开启手动预算时的初始值 8000
const max = computed(() => props.config?.max ?? 24576)
const min = computed(() => props.config?.min ?? 0)
const initial = computed(() => props.config?.default ?? 8000)
const canDisable = computed(() => props.config?.can_disable ?? true)
const thinking = computed(() => value.value !== 0 || !canDisable.value)
const manual = computed(() => value.value > 0)
// tip 在所选协议不能表达思考预算时附上原因
const tip = (text: string) => (props.disabled ? props.disabledTip : text)
const budgetTip = computed(() =>
  (min.value > 0 ? t('pg.thinkingBudgetMaxTip') : t('pg.thinkingBudgetTip')).replace(
    '{max}',
    String(max.value),
  ),
)

// setBudget 把输入的预算限制在上下限之间的整数，与官网一样设为 0 时关闭思考
function setBudget(input: number): void {
  value.value = Math.min(Math.max(Math.round(input), min.value), max.value)
}
</script>

<template>
  <div class="thinking-budget-setting">
    <h3 class="thinking-group-title">{{ t('pg.thinkingGroup') }}</h3>
    <div
      v-tooltip.left="tip(canDisable ? t('pg.thinkingModeTip') : t('pg.thinkingModeLocked'))"
      class="settings-item"
    >
      <p class="settings-item-title">{{ t('pg.thinkingMode') }}</p>
      <UiSwitch
        :model-value="thinking"
        :disabled="disabled || !canDisable"
        :label="t('pg.thinkingMode')"
        @update:model-value="value = $event ? -1 : 0"
      />
    </div>
    <template v-if="thinking">
      <div v-tooltip.left="tip(t('pg.setThinkingBudgetTip'))" class="settings-item">
        <p class="settings-item-title">{{ t('pg.setThinkingBudget') }}</p>
        <UiSwitch
          :model-value="manual"
          :disabled="disabled"
          :label="t('pg.setThinkingBudget')"
          @update:model-value="value = $event ? initial : -1"
        />
      </div>
      <div v-if="manual" v-tooltip.left="tip(budgetTip)" class="ms-slider">
        <UiSlider
          :model-value="value"
          :min="min"
          :max="max"
          :step="1"
          :disabled="disabled"
          :label="t('pg.setThinkingBudget')"
          @update:model-value="setBudget"
        />
        <input
          ms-input
          class="small slider-number-input"
          type="number"
          :min="min"
          :max="max"
          step="1"
          :disabled="disabled"
          :aria-label="t('pg.setThinkingBudget')"
          :value="value"
          @change="setBudget(Number(($event.target as HTMLInputElement).value))"
        />
      </div>
    </template>
  </div>
</template>

<style scoped>
.thinking-budget-setting {
  margin-block: 12px 8px;
}

.thinking-group-title {
  margin: 0 0 12px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.settings-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
}

.settings-item-title {
  margin-block: 8px;
  font-size: 14px;
  font-weight: 400;
  line-height: 21px;
}
</style>
