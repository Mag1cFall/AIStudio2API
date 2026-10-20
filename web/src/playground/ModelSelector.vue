<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useConsole } from '@/console'
import { useI18n, type TranslationKey } from '@/i18n'
import type { Model } from '@/types'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSlidingPanel from '@/playground/ui/UiSlidingPanel.vue'
import {
  categoryLabels,
  categoryOrder,
  hasCategory,
  modelBadges,
  modelIcon,
  priceLines,
  type ModelCategory,
} from './catalog'
import { pageKindOf, pageRoutes, unsavedKinds, type PageKind } from './media/kinds'

const open = defineModel<boolean>('open', { required: true })
const props = withDefaults(
  defineProps<{ kind?: PageKind; hasContent?: boolean; selected?: string }>(),
  { kind: 'chat', hasContent: false, selected: '' },
)
const emit = defineEmits<{ select: [model: Model] }>()
const { t, locale } = useI18n()
const router = useRouter()
const { models, showNotice } = useConsole()
// pending 是等待确认切换页面的模型
const pending = ref<Model>()

// starredKey 是本机收藏模型的存储键
const starredKey = 'playground_starred_models'

// loadStarred 读取本机收藏的模型
function loadStarred(): string[] {
  try {
    return JSON.parse(localStorage.getItem(starredKey) ?? '[]') as string[]
  } catch {
    return []
  }
}

const starred = ref(new Set(loadStarred()))
const search = ref('')
const category = ref<ModelCategory>(10)

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

const dateFormat = computed(
  () => new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', day: 'numeric' }),
)
const monthFormat = computed(
  () => new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short' }),
)
const numberFormat = computed(() => new Intl.NumberFormat(locale.value))

// badgeLabels 是模型标记的文案
const badgeLabels: Record<ReturnType<typeof modelBadges>[number], TranslationKey> = {
  Confidential: 'pg.badgeConfidential',
  New: 'pg.badgeNew',
  Paid: 'pg.badgePaid',
}

// categoryLabel 返回分类按钮文字
function categoryLabel(item: ModelCategory): string {
  if (item === 'All') return t('pg.categoryAll')
  if (item === 'Starred') return t('pg.categoryStarred')
  const label = categoryLabels[item]
  return label === undefined ? String(item) : t(label)
}

// notifySelected 显示官网的模型选中提示
function notifySelected(model: Model): void {
  showNotice(t('pg.selectedModel').replace('{name}', model.name), 'success')
}

// switchPage 打开模型所属页面的新会话
function switchPage(model: Model): void {
  pending.value = undefined
  notifySelected(model)
  void router.push({ path: pageRoutes[pageKindOf(model)], query: { model: model.id } })
}

// choose 选择模型：属于当前页面时在当前会话使用，属于其他页面时打开该页面的新会话，当前会话有内容时先确认
function choose(model: Model): void {
  open.value = false
  if (pageKindOf(model) === props.kind) {
    emit('select', model)
    notifySelected(model)
    return
  }
  if (props.hasContent) pending.value = model
  else switchPage(model)
}

// toggleStar 收藏或取消收藏模型
function toggleStar(model: Model): void {
  const next = new Set(starred.value)
  if (next.has(model.id)) next.delete(model.id)
  else next.add(model.id)
  starred.value = next
  try {
    localStorage.setItem(starredKey, JSON.stringify([...next]))
  } catch {
    // 本机存储不可用时收藏只在本次打开期间有效
  }
}

// copyID 复制模型 ID
async function copyID(model: Model): Promise<void> {
  await navigator.clipboard.writeText(model.id)
  showNotice(t('pg.copied'), 'success')
}
</script>

<template>
  <UiSlidingPanel v-model:open="open" :title="t('pg.modelSelection')">
    <div class="model-carousel">
      <div class="input-wrapper">
        <UiIcon name="search" :size="20" class="prefix-icon" />
        <input
          v-model="search"
          ms-input
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
          v-for="item in categories"
          :key="item"
          type="button"
          ms-button
          class="ms-button-filter-chip"
          :class="{ 'ms-button-active ms-button-active-border': category === item && !search }"
          :aria-selected="category === item"
          @click="category = item"
        >
          {{ categoryLabel(item) }}
        </button>
      </div>
      <div class="model-options-container">
        <p v-if="visibleModels.length === 0" class="empty">{{ t('pg.noModels') }}</p>
        <template v-for="(model, index) in visibleModels" :key="model.id">
          <div v-if="index > 0" class="row-divider"></div>
          <div class="model-row" :class="{ selected: model.id === selected }">
            <button type="button" class="content-button" @click="choose(model)">
              <div class="row-header">
                <span class="model-icon"><UiIcon :name="modelIcon(model)" :size="20" /></span>
                <div class="row-header-text">
                  <div class="model-title">
                    <span class="model-title-text">{{ model.name }}</span>
                    <span
                      v-for="badge in modelBadges(model, category)"
                      :key="badge"
                      class="badge"
                      :class="badge.toLowerCase()"
                      >{{ t(badgeLabels[badge]) }}</span
                    >
                  </div>
                  <span class="model-subtitle">{{ model.id }}</span>
                </div>
              </div>
              <ul class="model-details">
                <li v-if="model.details?.warning">
                  <UiIcon name="warning" :size="15" />
                  <span>{{ model.details.warning }}</span>
                </li>
                <li v-if="model.details?.alias">
                  <UiIcon name="spark" :size="15" />
                  <span>{{ model.details.alias }}</span>
                </li>
                <li v-if="model.details?.summary || model.description">
                  <UiIcon name="info" :size="15" />
                  <span>{{ model.details?.summary || model.description }}</span>
                </li>
                <li
                  v-for="(line, lineIndex) in priceLines(model, t)"
                  :key="`price-${lineIndex}`"
                  v-tooltip.left="line.tooltip ?? ''"
                >
                  <UiIcon name="price" :size="15" />
                  <span
                    >{{ line.text }}
                    <a
                      v-if="line.link"
                      :href="line.link"
                      target="_blank"
                      rel="noopener"
                      @click.stop
                      >{{ t('pg.learnMore') }}</a
                    ></span
                  >
                </li>
                <li v-if="model.input_token_limit && model.output_token_limit">
                  <UiIcon name="tokenLimits" :size="15" />
                  <span>{{
                    t('pg.tokenLimits')
                      .replace('{input}', numberFormat.format(model.input_token_limit))
                      .replace('{output}', numberFormat.format(model.output_token_limit))
                  }}</span>
                </li>
                <li v-if="model.capabilities?.output_continuation">
                  <UiIcon name="tokens" :size="15" />
                  <span>{{ t('pg.outputContinuation') }}</span>
                </li>
                <li v-if="model.details?.model_card_url || model.details?.knowledge_cutoff">
                  <UiIcon name="cutoff" :size="15" />
                  <span>
                    {{ t('pg.knowledgeCutoff') }}
                    <a
                      v-if="model.details?.model_card_url"
                      :href="model.details.model_card_url"
                      target="_blank"
                      rel="noopener"
                      @click.stop
                      >{{ t('pg.seeModelCard') }}</a
                    >
                    <template v-else-if="model.details?.knowledge_cutoff">{{
                      monthFormat.format(model.details.knowledge_cutoff * 1000)
                    }}</template>
                  </span>
                </li>
                <li v-if="model.details?.release_date">
                  <UiIcon name="release" :size="15" />
                  <span
                    >{{ t('pg.releaseDate') }}
                    {{ dateFormat.format(model.details.release_date * 1000) }}</span
                  >
                </li>
                <li v-if="model.channels?.length">
                  <UiIcon name="channel" :size="15" />
                  <span>{{ t('pg.channels') }} {{ model.channels.join(', ') }}</span>
                </li>
              </ul>
            </button>
            <div class="row-actions">
              <button
                v-tooltip.below="starred.has(model.id) ? t('pg.unstar') : t('pg.star')"
                type="button"
                ms-button
                class="ms-button-borderless ms-button-icon ms-button-small"
                :class="{ filled: starred.has(model.id) }"
                :aria-label="starred.has(model.id) ? t('pg.unstar') : t('pg.star')"
                @click="toggleStar(model)"
              >
                <UiIcon name="star" :size="18" />
              </button>
              <span class="vertical-divider"></span>
              <button
                v-tooltip.below="t('pg.copyToClipboard')"
                type="button"
                ms-button
                class="ms-button-borderless ms-button-icon ms-button-small"
                :aria-label="t('pg.copyToClipboard')"
                @click="copyID(model)"
              >
                <UiIcon name="copy" :size="18" />
              </button>
              <template v-if="model.details?.docs_url">
                <span class="vertical-divider"></span>
                <a
                  v-tooltip.below="t('pg.developerDocs')"
                  ms-button
                  class="ms-button-borderless ms-button-icon ms-button-small"
                  :href="model.details.docs_url"
                  target="_blank"
                  rel="noreferrer"
                  :aria-label="t('pg.developerDocs')"
                >
                  <UiIcon name="docs" :size="18" />
                </a>
              </template>
            </div>
          </div>
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
      <template v-if="unsavedKinds.includes(props.kind)">{{ t('pg.switchModelLost') }}</template>
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
  flex-direction: column;
  gap: 20px;
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

.model-options-container {
  display: flex;
  flex-direction: column;
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

.model-row {
  display: grid;
  grid-template-columns: 1fr auto;
  border-radius: 8px;
}

.model-row.selected,
.model-row:hover {
  background-color: var(--color-v3-surface-container-high);
}

.content-button {
  grid-column: 1 / 3;
  grid-row: 1;
  padding: 12px;
  border: 0;
  background: none;
  color: var(--color-v3-text);
  cursor: pointer;
  font-weight: 500;
  text-align: left;
}

.row-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding-right: 96px;
}

.model-icon {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: 8px;
  background-color: var(--color-v3-surface-container);
  color: var(--color-v3-text-link);
}

.row-header-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.model-title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  font-size: 14px;
  line-height: 20px;
}

.model-title-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-subtitle {
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px 6px 1px 5px;
  border: 1px solid var(--color-v3-outline);
  border-radius: 8px;
  background-color: var(--color-v3-surface-container-high);
  color: var(--color-v3-text);
  font-size: 12px;
  font-weight: 400;
  line-height: 16px;
}

.badge::before {
  width: 6px;
  aspect-ratio: 1 / 1;
  border-radius: 50%;
  content: '';
}

.badge.new::before {
  background-color: var(--color-v3-accent-4);
}

.badge.paid::before {
  background-color: var(--color-v3-text-link);
}

.badge.confidential::before {
  background-color: var(--color-v3-accent-1);
}

.model-details {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 12px 0 0;
  padding: 0;
  list-style: none;
}

.model-details li {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-weight: 400;
  line-height: 16px;
}

.model-details li > :first-child {
  flex-shrink: 0;
  margin-top: 1px;
}

.model-details a {
  color: var(--color-v3-text-link);
  font-weight: 500;
}

.row-actions {
  z-index: 1;
  display: flex;
  grid-column: 2;
  grid-row: 1;
  align-items: center;
  height: min-content;
  margin-top: 20px;
  margin-right: 12px;
}

.vertical-divider {
  width: 1px;
  height: 28px;
  margin: 0 2px;
  background-color: var(--color-v3-outline-var);
}

.filled {
  color: var(--color-v3-text-link);
}
</style>
