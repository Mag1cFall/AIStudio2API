<script setup lang="ts">
import { computed } from 'vue'
import { useConsole } from '@/console'
import UiSlidingPanel from '@/playground/ui/UiSlidingPanel.vue'
import ModelRow from './ModelRow.vue'
import { hasCode } from './catalog'
import {
  agentModelID,
  agentModelOpen as open,
  agentModelTarget as agent,
  modelChoice as choice,
  modelSelectorOpen,
} from './model-selector'

const { models } = useConsole()

// candidates 与官网一致是带能力码 83 且不是别名的模型
const candidates = computed(() =>
  models.value.filter((model) => hasCode(model, 83) && !model.details?.alias),
)
// selected 是 agent 当前所用模型
const selected = computed(() => (agent.value ? agentModelID(agent.value, choice.value) : ''))
// title 与官网一样是 agent 当前所用模型的名称
const title = computed(
  () => models.value.find((model) => model.id === selected.value)?.name ?? selected.value,
)

// back 返回模型选择面板
function back(): void {
  open.value = false
  modelSelectorOpen.value = true
}

// pick 把模型设为页面中 agent 所用的模型并关闭面板
function pick(id: string): void {
  choice.value?.setAgentModel?.(id)
  open.value = false
}
</script>

<template>
  <UiSlidingPanel v-model:open="open" :title="title" back @back="back">
    <div class="agent-model-options">
      <template v-for="(model, index) in candidates" :key="model.id">
        <div v-if="index > 0" class="row-divider"></div>
        <ModelRow
          :model="model"
          :category="10"
          :selected="model.id === selected"
          @choose="pick(model.id)"
        />
      </template>
    </div>
  </UiSlidingPanel>
</template>

<style scoped>
.agent-model-options {
  display: flex;
  flex-direction: column;
  padding: 1px;
}

.row-divider {
  height: 1px;
  margin: 8px 0;
  background-color: var(--color-v3-outline-var);
}
</style>
