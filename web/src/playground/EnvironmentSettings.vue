<script setup lang="ts">
import { computed, ref } from 'vue'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import DisclosureChevron from '@/components/DisclosureChevron.vue'
import UiCollapse from '@/components/UiCollapse.vue'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import EnvironmentNetworkDialog from './EnvironmentNetworkDialog.vue'
import EnvironmentSourcesDialog from './EnvironmentSourcesDialog.vue'
import { usePlayground } from './session'
import type { AgentEnvironment } from './types'

const environment = defineModel<AgentEnvironment>({ required: true })
const { t } = useI18n()
const { showNotice } = useConsole()
const { session } = usePlayground()

const open = ref(true)
const sourcesOpen = ref(false)
const networkOpen = ref(false)
// switchOpen 表示正在确认从已有环境切回新环境
const switchOpen = ref(false)
// types 与官网一样是 New 与 Existing 两种环境
const types = [
  { value: 'new', label: 'pg.environmentNew' },
  { value: 'existing', label: 'pg.environmentExisting' },
] as const
// sourcesLabel 与官网一样没有源时为 Add sources，否则为源的个数
const sourcesLabel = computed(() => {
  const count = environment.value.sources.length
  if (count === 0) return t('pg.addSources')
  return count === 1 ? t('pg.oneSource') : t('pg.sourceCount').replace('{count}', String(count))
})
// rulesLabel 与官网一样没有规则时为 Add rules，否则为规则的条数
const rulesLabel = computed(() => {
  const count = environment.value.rules.length
  if (count === 0) return t('pg.addRules')
  return count === 1 ? t('pg.oneRule') : t('pg.ruleCount').replace('{count}', String(count))
})
// hasId 表示已有环境填了环境 ID
const hasId = computed(() => environment.value.id.trim() !== '')

// selectType 切换环境类型，与官网一样已有环境带 ID 且对话已有轮次时切回 New 先确认
function selectType(type: AgentEnvironment['type']): void {
  if (
    type === 'new' &&
    environment.value.type === 'existing' &&
    hasId.value &&
    session.turns.length > 0
  ) {
    switchOpen.value = true
    return
  }
  environment.value.type = type
}

// confirmSwitch 确认切回新环境
function confirmSwitch(): void {
  environment.value.type = 'new'
  switchOpen.value = false
}

// download 与官网在没有所选 API key 时相同，提示找不到 API key
function download(): void {
  if (hasId.value) showNotice(t('pg.apiKeyNotFound'), 'error')
}
</script>

<template>
  <div class="field-group">
    <button
      type="button"
      class="settings-group-header"
      :aria-expanded="open"
      :aria-label="t('pg.toggleEnvironment')"
      @click="open = !open"
    >
      <span class="group-title">{{ t('pg.environment') }}</span>
      <DisclosureChevron :open="open" :turn="180" class="expand-icon"
        ><UiIcon name="chevronDown" :size="20"
      /></DisclosureChevron>
    </button>
    <UiCollapse :open="open">
      <p class="environment-description">
        {{ t('pg.environmentDescription') }}
        <a
          href="https://aistudio.google.com/docs/aistudio-agents#environment-configuration"
          target="_blank"
          rel="noopener"
          >{{ t('pg.learnMore') }}</a
        >
      </p>
      <div v-tooltip.left="t('pg.environmentTypeTip')" class="environment-row">
        <h3 class="row-label">{{ t('pg.environmentType') }}</h3>
        <div class="button-toggle" role="radiogroup" :aria-label="t('pg.environmentMode')">
          <button
            v-for="option in types"
            :key="option.value"
            type="button"
            role="radio"
            class="toggle-option"
            :class="{ selected: environment.type === option.value }"
            :aria-checked="environment.type === option.value"
            @click="selectType(option.value)"
          >
            {{ t(option.label) }}
          </button>
        </div>
      </div>
      <template v-if="environment.type === 'existing'">
        <div v-tooltip.left="t('pg.environmentIdTip')" class="environment-row id-row">
          <h3 class="row-label">{{ t('pg.environmentId') }}</h3>
          <input
            v-model="environment.id"
            ms-input
            class="small id-input"
            :placeholder="t('pg.environmentIdPlaceholder')"
            :aria-label="t('pg.environmentIdLabel')"
          />
        </div>
        <div class="download-row">
          <button
            v-tooltip.left="hasId ? '' : t('pg.downloadNeedsId')"
            type="button"
            ms-button
            class="ms-button-secondary row-button"
            :aria-disabled="!hasId"
            @click="download"
          >
            {{ t('pg.download') }}
          </button>
        </div>
      </template>
      <template v-else>
        <div v-tooltip.left="t('pg.sourcesTip')" class="environment-row">
          <h3 class="row-label">{{ t('pg.environmentSources') }}</h3>
          <button
            type="button"
            ms-button
            class="ms-button-secondary row-button"
            @click="sourcesOpen = true"
          >
            {{ sourcesLabel }}
          </button>
        </div>
        <div v-tooltip.left="t('pg.networkTip')" class="environment-row">
          <h3 class="row-label">{{ t('pg.environmentNetwork') }}</h3>
          <button
            type="button"
            ms-button
            class="ms-button-secondary row-button"
            @click="networkOpen = true"
          >
            {{ rulesLabel }}
          </button>
        </div>
      </template>
    </UiCollapse>
    <EnvironmentSourcesDialog v-model:open="sourcesOpen" v-model:sources="environment.sources" />
    <EnvironmentNetworkDialog v-model:open="networkOpen" v-model:rules="environment.rules" />
    <UiDialog v-model:open="switchOpen" :title="t('pg.switchEnvironmentTitle')" width="420px">
      <p class="confirm-text">{{ t('pg.switchEnvironmentBody') }}</p>
      <template #actions>
        <button type="button" ms-button class="ms-button-borderless" @click="switchOpen = false">
          {{ t('pg.stayExistingEnvironment') }}
        </button>
        <button type="button" ms-button class="ms-button-primary" @click="confirmSwitch">
          {{ t('pg.confirm') }}
        </button>
      </template>
    </UiDialog>
  </div>
</template>

<style scoped>
.environment-description {
  margin: 4px 0 16px;
  color: var(--color-v3-text);
  font-size: 12px;
  line-height: 18px;
}

.environment-description a {
  color: var(--color-v3-text-link);
}

.environment-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-height: 36px;
  margin-bottom: 8px;
}

.row-label {
  margin: 0;
  color: var(--color-v3-text);
  font-size: 14px;
  font-weight: 400;
  line-height: 21px;
}

.button-toggle {
  display: flex;
  padding: 3px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
}

.toggle-option {
  height: 30px;
  padding: 0 12px;
  border: 0;
  border-radius: 9px;
  background: none;
  color: var(--color-v3-text-var);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
}

.toggle-option.selected {
  background: var(--color-v3-surface-container-high);
  color: var(--color-v3-text);
}

.id-input {
  flex: 1;
  min-width: 0;
}

.download-row {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;
}

.row-button {
  font-size: 12px;
}

.confirm-text {
  margin: 0;
  color: var(--color-v3-text-var);
}
</style>
