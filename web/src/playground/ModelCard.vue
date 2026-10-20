<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from '@/i18n'
import { vTooltip } from '@/playground/ui/tooltip'
import type { Model } from '@/types'
import ModelSelector from './ModelSelector.vue'
import type { PageKind } from './media/kinds'

defineProps<{
  model: Model | undefined
  kind: PageKind
  hasContent: boolean
  sideBySide?: boolean
  // lockedTip 非空时模型不可更换，悬停显示该说明
  lockedTip?: string
}>()
const emit = defineEmits<{ select: [model: Model] }>()
const { t } = useI18n()
const open = ref(false)
</script>

<template>
  <button
    v-tooltip.left="lockedTip ?? ''"
    type="button"
    class="settings-card model-selector-card"
    :class="{ 'side-by-side': sideBySide, locked: Boolean(lockedTip) }"
    :aria-disabled="Boolean(lockedTip)"
    @click="!lockedTip && (open = true)"
  >
    <span class="title" :class="{ 'is-side-by-side': sideBySide }">{{
      model?.name ?? t('pg.selectModel')
    }}</span>
    <template v-if="!sideBySide">
      <span class="subtitle">{{ model?.id }}</span>
      <span v-if="model?.details?.summary" class="subtitle">{{ model.details.summary }}</span>
    </template>
  </button>
  <ModelSelector
    v-model:open="open"
    :kind="kind"
    :has-content="hasContent"
    :selected="model?.id ?? ''"
    @select="emit('select', $event)"
  />
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
