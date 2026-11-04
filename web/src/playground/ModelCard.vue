<script setup lang="ts">
import { computed } from 'vue'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import type { Model } from '@/types'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { hasCode } from './catalog'
import type { PageKind } from './media/kinds'
import { agentModelID, openAgentModel, openModelSelector, type ModelChoice } from './model-selector'

const props = defineProps<{
  model: Model | undefined
  kind: PageKind
  hasContent: boolean
  sideBySide?: boolean
  // lockedTip 非空时模型不可更换，悬停显示该说明
  lockedTip?: string
  // agentModel 是对话为 agent 选择的模型，空字符串为 agent 默认，只有对话页传入
  agentModel?: string
}>()
const emit = defineEmits<{ select: [model: Model]; agentModel: [id: string] }>()
const { t } = useI18n()
const { models } = useConsole()

// choice 是本页面交给模型选择面板的处理：选中本页面的模型时以 select 事件交给页面，对话页另以 agentModel 事件接收 agent 所用的模型
function choice(): ModelChoice {
  return {
    kind: props.kind,
    selected: props.model?.id ?? '',
    hasContent: () => props.hasContent,
    select: (model) => emit('select', model),
    ...(props.agentModel !== undefined && {
      agentModel: () => props.agentModel ?? '',
      setAgentModel: (id: string) => emit('agentModel', id),
    }),
  }
}

// agentName 是可选模型的 agent 所用模型的名称：与官网一样只有带能力码 65 的 agent 显示，卡片随之显示所选模型标记
const agentName = computed(() => {
  const model = props.model
  if (props.sideBySide || !model || !hasCode(model, 65) || model.agent_model === undefined)
    return undefined
  const id = agentModelID(model, choice())
  return models.value.find((item) => item.id === id)?.name ?? id
})
</script>

<template>
  <div
    v-if="agentName"
    v-tooltip.left="lockedTip ?? ''"
    class="settings-card model-selector-card"
    :class="{ locked: Boolean(lockedTip) }"
  >
    <button
      type="button"
      class="model-selector-trigger"
      :aria-label="t('pg.selectPrimaryModel')"
      :aria-disabled="Boolean(lockedTip)"
      @click="!lockedTip && openModelSelector(choice())"
    >
      <span class="title">{{ model!.name }}</span>
      <span class="subtitle">{{ model!.details?.summary || model!.description }}</span>
    </button>
    <div class="agent-badge-row">
      <span class="agent-badge-label">{{ t('pg.selectedModel') }}</span>
      <button
        type="button"
        class="agent-badge"
        :aria-label="t('pg.selectAgentModel')"
        @click="openAgentModel(model!, choice())"
      >
        <span>{{ agentName }}</span
        ><UiIcon name="chevronRight" :size="16" />
      </button>
    </div>
  </div>
  <button
    v-else
    v-tooltip.left="lockedTip ?? ''"
    type="button"
    class="settings-card model-selector-card"
    :class="{ 'side-by-side': sideBySide, locked: Boolean(lockedTip) }"
    :aria-disabled="Boolean(lockedTip)"
    @click="!lockedTip && openModelSelector(choice())"
  >
    <span class="title" :class="{ 'is-side-by-side': sideBySide }">{{
      model?.name ?? t('pg.selectModel')
    }}</span>
    <template v-if="!sideBySide && model">
      <span class="subtitle">{{ model.id }}</span>
      <span v-if="model.details?.summary" class="subtitle">{{ model.details.summary }}</span>
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

/* model-selector-trigger 为 agent 卡片中打开模型选择面板的名称与说明，agent-badge-row 为其下的所选模型：分隔线、标签与标记取官网 ms-model-selector */
.model-selector-trigger {
  display: block;
  width: 100%;
  padding: 0;
  border: none;
  background: transparent;
  color: inherit;
  cursor: pointer;
  text-align: start;
}

.model-selector-card.locked .model-selector-trigger {
  cursor: not-allowed;
}

.model-selector-trigger .subtitle {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 4;
  line-clamp: 4;
}

.agent-badge-row {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  margin-top: 8px;
  padding-top: 4px;
  border-top: 1px solid var(--color-v3-outline);
  animation: agent-badge-in 0.2s ease-out;
}

.agent-badge-label {
  display: block;
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.agent-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px 6px 1px 5px;
  border: 1px solid var(--color-v3-outline);
  border-radius: 8px;
  background-color: var(--color-v3-surface-container-high);
  color: var(--color-v3-text);
  cursor: pointer;
  font-size: 12px;
  font-weight: 400;
  line-height: 16px;
  transition: background-color 0.2s ease;
}

.agent-badge::before {
  flex-shrink: 0;
  width: 6px;
  aspect-ratio: 1 / 1;
  border-radius: 50%;
  background-color: var(--color-v3-accent-4);
  content: '';
}

.agent-badge:hover {
  background-color: var(--color-v3-surface-container-highest);
}

@keyframes agent-badge-in {
  from {
    opacity: 0;
  }
}
</style>
