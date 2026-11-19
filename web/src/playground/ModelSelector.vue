<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useConsole } from '@/console'
import { useI18n } from '@/i18n'
import type { Model } from '@/types'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSlidingPanel from '@/playground/ui/UiSlidingPanel.vue'
import ModelRow from './ModelRow.vue'
import PeriodicTable from './PeriodicTable.vue'
import { categoryLabels, categoryOrder, hasCategory, hasCode, type ModelCategory } from './catalog'
import { pageKindOf, pageRoutes, unsavedKinds } from './media/kinds'
import { releasedElements } from './periodic'
import {
  agentModelID,
  modelChoice,
  modelSelectorOpen,
  openAgentModel,
  starredModels as starred,
} from './model-selector'

const open = modelSelectorOpen
const choice = modelChoice
const { t } = useI18n()
const router = useRouter()
const { models } = useConsole()
// pending 是等待确认切换页面的模型
const pending = ref<Model>()

const search = ref('')
const category = ref<ModelCategory>(10)
// periodic 表示面板显示 Gemini 4 元素周期表，每次打开面板时回到模型列表
const periodic = ref(false)
// hasPeriodic 表示实时目录中有 Gemini 4 的元素代号模型
const hasPeriodic = computed(() => releasedElements(models.value).size > 0)
// showPeriodic 表示面板正在显示周期表
const showPeriodic = computed(() => periodic.value && hasPeriodic.value)
watch(open, (value) => {
  if (value) periodic.value = false
})

// pickCategory 切换到模型列表的一个分类
function pickCategory(item: ModelCategory): void {
  category.value = item
  periodic.value = false
}

// categories 是至少有一个模型的分类，Starred 在有收藏时显示
const categories = computed(() =>
  categoryOrder.filter((item) => {
    if (item === 'All') return true
    if (item === 'Starred') return starred.value.size > 0
    return models.value.some((model) => hasCategory(model, item))
  }),
)
// visibleModels 是当前分类与搜索下的模型
const visibleModels = computed(() => {
  const query = search.value.trim().toLowerCase()
  return models.value.filter((model) => {
    if (query !== '') return `${model.name} ${model.id}`.toLowerCase().includes(query)
    if (category.value === 'All') return true
    if (category.value === 'Starred') return starred.value.has(model.id)
    return hasCategory(model, category.value)
  })
})

// rowBatch 是打开面板或切换分类、搜索后每个任务渲染的模型行数，其余行在后续任务中补齐
const rowBatch = 12
// shownRows 是已渲染的模型行数
const shownRows = ref(rowBatch)
// shownModels 是已渲染的模型行
const shownModels = computed(() => visibleModels.value.slice(0, shownRows.value))
let growing = 0

// grow 多渲染一批模型行，没有渲染完时在下一个任务继续
function grow(): void {
  shownRows.value += rowBatch
  if (shownRows.value < visibleModels.value.length) growing = window.setTimeout(grow)
}
watch([open, visibleModels], () => {
  window.clearTimeout(growing)
  shownRows.value = rowBatch
  if (open.value && rowBatch < visibleModels.value.length) growing = window.setTimeout(grow)
})

// agentModelName 返回可选模型的 agent 所用模型的名称：与官网一样只有带能力码 65 的 agent 显示所选模型，页面未选择时为 agent 默认模型
function agentModelName(model: Model): string | undefined {
  if (!hasCode(model, 65) || model.agent_model === undefined) return undefined
  const id = agentModelID(model, choice.value)
  return models.value.find((item) => item.id === id)?.name ?? id
}

// categoryLabel 返回分类按钮文字
function categoryLabel(item: ModelCategory): string {
  if (item === 'All') return t('pg.categoryAll')
  if (item === 'Starred') return t('pg.categoryStarred')
  return t(categoryLabels[item]!)
}

// switchPage 打开模型所属页面的新会话
function switchPage(model: Model): void {
  pending.value = undefined
  void router.push({ path: pageRoutes[pageKindOf(model)], query: { model: model.id } })
}

// choose 选择模型：属于当前页面时在当前会话使用，属于其他页面时打开该页面的新会话，当前会话有内容时先确认
function choose(model: Model): void {
  const current = choice.value!
  open.value = false
  if (pageKindOf(model) === current.kind) {
    current.select(model)
    return
  }
  if (current.hasContent()) pending.value = model
  else switchPage(model)
}
</script>

<template>
  <UiSlidingPanel
    v-model:open="open"
    :title="t('pg.modelSelection')"
    :width="showPeriodic ? '900px' : '550px'"
  >
    <div class="model-carousel">
      <div class="input-wrapper">
        <UiIcon name="search" :size="20" class="prefix-icon" />
        <input
          v-model="search"
          ms-input
          autofocus
          class="has-icon"
          type="text"
          :placeholder="t('pg.searchModels')"
          :aria-label="t('pg.searchModels')"
        />
        <button
          v-if="search"
          type="button"
          ms-button
          class="clear-button ms-button-borderless ms-button-icon"
          :aria-label="t('pg.clearSearch')"
          @click="search = ''"
        >
          <UiIcon name="close" :size="20" />
        </button>
      </div>
      <div class="model-categories-container">
        <button
          v-if="hasPeriodic"
          v-tooltip.below="t('pg.periodicLabel')"
          type="button"
          ms-button
          class="ms-button-filter-chip periodic-tab"
          :class="{ 'ms-button-active ms-button-active-border': showPeriodic }"
          :aria-selected="showPeriodic"
          :aria-label="t('pg.periodicLabel')"
          @click="periodic = true"
        >
          <span class="periodic-glyph" aria-hidden="true">4</span>
        </button>
        <button
          v-for="item in categories"
          :key="item"
          type="button"
          ms-button
          class="ms-button-filter-chip"
          :class="{
            'ms-button-active ms-button-active-border':
              category === item && !search && !showPeriodic,
          }"
          :aria-selected="category === item && !showPeriodic"
          @click="pickCategory(item)"
        >
          {{ categoryLabel(item) }}
        </button>
      </div>
      <div class="model-options-container">
        <PeriodicTable
          v-if="showPeriodic"
          :models="models"
          :selected="choice!.selected"
          :query="search"
          @choose="choose"
        />
        <template v-else>
          <p v-if="visibleModels.length === 0" class="empty">{{ t('pg.noModels') }}</p>
          <template v-for="(model, index) in shownModels" :key="model.id">
            <div v-if="index > 0" class="row-divider"></div>
            <ModelRow
              :model="model"
              :category="category"
              :selected="model.id === choice!.selected"
              :agent-model="agentModelName(model)"
              :agent-locked="choice!.setAgentModel === undefined"
              @choose="choose(model)"
              @agent="openAgentModel(model, choice!)"
            />
          </template>
        </template>
      </div>
    </div>
  </UiSlidingPanel>
  <UiDialog
    :open="pending !== undefined"
    :title="t('pg.switchModelTitle')"
    @update:open="!$event && (pending = undefined)"
  >
    <p class="switch-text">
      {{ t('pg.switchModelNewChat') }}
      <template v-if="unsavedKinds.includes(choice!.kind)">{{ t('pg.switchModelLost') }}</template>
      {{ t('pg.switchModelContinue') }}
    </p>
    <template #actions>
      <button type="button" ms-button class="ms-button-borderless" @click="pending = undefined">
        {{ t('common.cancel') }}
      </button>
      <button type="button" ms-button class="ms-button-primary" @click="switchPage(pending!)">
        {{ t('pg.continue') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.switch-text {
  margin: 0;
}

.model-carousel {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 20px;
  min-height: 0;
  padding: 1px;
}

.input-wrapper {
  position: relative;
  display: flex;
  align-items: center;
}

.prefix-icon {
  position: absolute;
  left: 12px;
  color: var(--color-v3-text-var);
  pointer-events: none;
}

.has-icon {
  padding: 1px 36px 1px 40px;
}

.clear-button {
  position: absolute;
  right: 0;
}

.model-categories-container {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.ms-button-active-border {
  border-color: var(--color-v3-text-link);
  background-image: none;
  padding-left: 12px;
}

.periodic-tab[ms-button] {
  min-width: 36px;
  padding: 0 10px;
}

.periodic-glyph {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 4px;
  background-color: var(--color-v3-text);
  color: var(--color-v3-surface-container);
  font-family: var(--font-heading);
  font-size: 13px;
  font-weight: 700;
  line-height: 1;
}

.model-options-container {
  display: flex;
  flex: 1;
  flex-direction: column;
  overflow: auto;
  scrollbar-gutter: stable;
}

.empty {
  margin: 0;
  padding: 24px 0;
  color: var(--color-v3-text-var);
  text-align: center;
}

.row-divider {
  height: 1px;
  margin: 8px 0;
  background-color: var(--color-v3-outline-var);
}
</style>
