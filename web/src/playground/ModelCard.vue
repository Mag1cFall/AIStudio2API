<script setup lang="ts">
import { useI18n } from '@/i18n'
import { vTooltip } from '@/playground/ui/tooltip'
import type { Model } from '@/types'
import type { PageKind } from './media/kinds'
import { openModelSelector } from './model-selector'

const props = defineProps<{
  model: Model | undefined
  kind: PageKind
  hasContent: boolean
  sideBySide?: boolean
  // lockedTip 非空时模型不可更换，悬停显示该说明
  lockedTip?: string
}>()
const emit = defineEmits<{ select: [model: Model] }>()
const { t } = useI18n()

// openSelector 为本页面打开共用的模型选择面板，选中本页面的模型时以 select 事件交给页面
function openSelector(): void {
  openModelSelector({
    kind: props.kind,
    selected: props.model?.id ?? '',
    hasContent: () => props.hasContent,
    select: (model) => emit('select', model),
  })
}
</script>

<template>
  <button
    v-tooltip.left="lockedTip ?? ''"
    type="button"
    class="settings-card model-selector-card"
    :class="{ 'side-by-side': sideBySide, locked: Boolean(lockedTip) }"
    :aria-disabled="Boolean(lockedTip)"
    @click="!lockedTip && openSelector()"
  >
    <span class="title" :class="{ 'is-side-by-side': sideBySide }">{{
      model?.name ?? t('pg.selectModel')
    }}</span>
    <template v-if="!sideBySide && model">
      <span class="subtitle">{{ model?.id }}</span>
      <span v-if="model?.details?.summary" class="subtitle">{{ model.details.summary }}</span>
    </template>
  </button>
</template>

<style scoped>
.model-selector-card.locked {
  background: transparent;
  cursor: not-allowed;
}

.model-selector-card.locked :is(.title, .subtitle) {
  color: var(--color-v3-text-disable);
}

.model-selector-card.side-by-side {
  flex: 1;
  min-width: 0;
  padding: 8px 12px;
  border: none;
  border-radius: 12px;
  background: var(--color-v3-surface-container);
  box-shadow: var(--shadow-v3-card);
  color: var(--color-v3-text);
  cursor: pointer;
  text-align: start;
  transition: all 0.2s ease;
}

.model-selector-card.side-by-side:hover {
  box-shadow: var(--shadow-v3-card-hover);
}

.model-selector-card .title.is-side-by-side {
  display: block;
  overflow: hidden;
  margin-bottom: 0;
  font-size: 14px;
  font-weight: 500;
  line-height: 21px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
