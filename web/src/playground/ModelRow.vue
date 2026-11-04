<script lang="ts">
import { computed } from 'vue'
import { useI18n, type TranslationKey } from '@/i18n'

const { locale } = useI18n()
// dateFormat、monthFormat 与 numberFormat 是各模型行共用的发布日期、知识截止月份与 token 数格式
const dateFormat = computed(
  () => new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', day: 'numeric' }),
)
const monthFormat = computed(
  () => new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short' }),
)
const numberFormat = computed(() => new Intl.NumberFormat(locale.value))

// badgeLabels 是模型标记的文案
const badgeLabels: Record<'Confidential' | 'New' | 'Paid', TranslationKey> = {
  Confidential: 'pg.badgeConfidential',
  New: 'pg.badgeNew',
  Paid: 'pg.badgePaid',
}
</script>

<script setup lang="ts">
import type { Model } from '@/types'
import UiIcon from '@/playground/ui/UiIcon.vue'
import { hasCategory, modelBadges, modelIcon, priceLines, type ModelCategory } from './catalog'
import { copyToClipboard } from './clipboard'
import { starredModels, toggleStar } from './model-selector'

const props = defineProps<{
  model: Model
  category: ModelCategory
  selected: boolean
  // agentModel 是 agent 所用模型的名称，有值时显示所选模型标记与按所配置模型计价的说明
  agentModel?: string | undefined
  // agentLocked 表示所选模型标记不能打开选择面板
  agentLocked?: boolean
}>()
const emit = defineEmits<{ choose: []; agent: [] }>()
const { t } = useI18n()
const starred = computed(() => starredModels.value.has(props.model.id))

// openAgent 打开 agent 所用模型的选择面板，不选中本行模型
function openAgent(event: Event): void {
  event.stopPropagation()
  if (!props.agentLocked) emit('agent')
}
</script>

<template>
  <div class="model-row" :class="{ selected }">
    <button type="button" class="content-button" @click="emit('choose')">
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
          v-for="(line, lineIndex) in agentModel ? [] : priceLines(model, t)"
          :key="`price-${lineIndex}`"
          v-tooltip.left="line.tooltip ?? ''"
        >
          <UiIcon name="price" :size="15" />
          <span
            >{{ line.text }}
            <a v-if="line.link" :href="line.link" target="_blank" rel="noopener" @click.stop>{{
              t('pg.learnMore')
            }}</a></span
          >
        </li>
        <li v-if="model.input_token_limit && model.output_token_limit && !hasCategory(model, 19)">
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
      <div v-if="agentModel" class="agent-model">
        <span class="agent-model-label">{{ t('pg.selectedModel') }}</span>
        <span
          role="button"
          :tabindex="agentLocked ? -1 : 0"
          class="badge new agent-model-chip"
          :class="{ disabled: agentLocked }"
          :aria-label="t('pg.selectAgentModel')"
          :aria-disabled="agentLocked"
          @click="openAgent"
          @keydown.enter.space.prevent="openAgent"
          ><span>{{ agentModel }}</span
          ><UiIcon name="chevronRight" :size="16"
        /></span>
        <span class="agent-pricing"
          ><UiIcon name="price" :size="15" /><span
            >{{ t('pg.agentPricing') }}
            <a
              href="https://aistudio.google.com/docs/pricing#pricing-for-agents"
              target="_blank"
              rel="noopener"
              @click.stop
              >{{ t('pg.learnMore') }}</a
            ></span
          ></span
        >
      </div>
    </button>
    <div class="row-actions">
      <button
        v-tooltip.below="starred ? t('pg.unstar') : t('pg.star')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon ms-button-small"
        :class="{ filled: starred }"
        :aria-label="starred ? t('pg.unstar') : t('pg.star')"
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
        @click="copyToClipboard(model.id)"
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

<style scoped>
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

.model-details a,
.agent-pricing a {
  color: var(--color-v3-text-link);
  font-weight: 500;
}

/* agent-model 为可选模型的 agent 卡片中的所选模型与计价说明：分隔线、标签、标记与计价行取官网 Agents 分类卡片 */
.agent-model {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--color-v3-outline-var);
}

.agent-model-label,
.agent-pricing {
  color: var(--color-v3-text-var);
  font-size: 12px;
  font-weight: 400;
  line-height: 16px;
}

/* agent-model-chip 为所选模型标记：悬停加深底色，不能打开选择面板时为不可用外观，取官网 agent-badge-row 的 badge */
.agent-model-chip {
  gap: 4px;
  padding-right: 2px;
  cursor: pointer;
  transition: background-color 0.2s ease;
}

.agent-model-chip:hover:not(.disabled) {
  background-color: var(--color-v3-surface-container-highest);
}

.agent-model-chip.disabled {
  background-color: var(--color-v3-surface-container);
  color: var(--color-v3-text-var);
  cursor: not-allowed;
}

.agent-pricing {
  display: flex;
  align-items: flex-start;
  gap: 6px;
}

.agent-pricing > :first-child {
  flex-shrink: 0;
  margin-top: 1px;
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
