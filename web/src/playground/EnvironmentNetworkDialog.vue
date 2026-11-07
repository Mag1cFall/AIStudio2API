<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import DisclosureChevron from '@/components/DisclosureChevron.vue'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import type { EgressRule } from './types'

const open = defineModel<boolean>('open', { required: true })
const rules = defineModel<EgressRule[]>('rules', { required: true })
const { t } = useI18n()

// draft 是对话框中尚未保存的规则
const draft = ref<EgressRule[]>([])
// warningOpen 表示展开出站白名单的风险说明，与官网一样默认展开
const warningOpen = ref(true)
watch(open, (value) => {
  if (!value) return
  draft.value = rules.value.map((rule) => ({
    domain: rule.domain,
    headers: rule.headers.map((header) => ({ ...header })),
  }))
})

// save 保存规则并关闭对话框
function save(): void {
  rules.value = draft.value
  open.value = false
}
</script>

<template>
  <UiDialog v-model:open="open" :title="t('pg.networkTitle')" width="560px">
    <p class="dialog-description">{{ t('pg.networkDescription') }}</p>
    <button
      type="button"
      ms-button
      class="ms-button-secondary add-rule-button"
      @click="draft.push({ domain: '', headers: [] })"
    >
      <UiIcon name="add" :size="18" /> {{ t('pg.addToAllowlist') }}
    </button>
    <div v-for="(rule, index) in draft" :key="index" class="rule-card">
      <div class="rule-domain-row">
        <input
          v-model="rule.domain"
          ms-input
          :aria-label="t('pg.domain')"
          :placeholder="t('pg.domainPlaceholder')"
        />
        <button
          v-tooltip.left="t('pg.deleteRule')"
          type="button"
          ms-button
          class="ms-button-borderless ms-button-icon"
          :aria-label="t('pg.deleteRule')"
          @click="draft.splice(index, 1)"
        >
          <UiIcon name="delete" :size="20" />
        </button>
      </div>
      <div v-if="rule.headers.length > 0" class="rule-http-headers-section">
        <template v-for="(header, row) in rule.headers" :key="row">
          <input
            v-model="header.name"
            ms-input
            class="small"
            :aria-label="t('pg.headerName')"
            :placeholder="t('pg.headerName')"
          />
          <input
            v-model="header.value"
            ms-input
            class="small"
            :aria-label="t('pg.headerValue')"
            :placeholder="t('pg.headerValue')"
          />
          <button
            v-tooltip.left="t('pg.deleteHeader')"
            type="button"
            ms-button
            class="ms-button-borderless ms-button-icon"
            :aria-label="t('pg.deleteHeader')"
            @click="rule.headers.splice(row, 1)"
          >
            <UiIcon name="delete" :size="20" />
          </button>
        </template>
      </div>
      <button
        type="button"
        ms-button
        class="ms-button-secondary ms-button-small add-header-button"
        @click="rule.headers.push({ name: '', value: '' })"
      >
        {{ t('pg.addHttpHeader') }}
      </button>
    </div>
    <button
      type="button"
      class="warning-toggle"
      :aria-label="t('pg.toggleDisclaimer')"
      :aria-expanded="warningOpen"
      @click="warningOpen = !warningOpen"
    >
      <DisclosureChevron :open="warningOpen"
        ><UiIcon name="chevronRight" :size="18"
      /></DisclosureChevron>
      {{ warningOpen ? t('pg.seeLess') : t('pg.seeDisclaimer') }}
    </button>
    <div v-if="warningOpen" class="warning-callout">
      <UiIcon name="warning" :size="20" />
      <p>{{ t('pg.allowlistWarning') }}</p>
    </div>
    <template #actions>
      <span class="rule-count">{{
        draft.length === 1
          ? t('pg.oneRule')
          : t('pg.ruleCount').replace('{count}', String(draft.length))
      }}</span>
      <button type="button" ms-button class="ms-button-borderless" @click="open = false">
        {{ t('common.cancel') }}
      </button>
      <button type="button" ms-button class="ms-button-primary" @click="save">
        {{ t('common.save') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.dialog-description {
  margin: 0 0 24px;
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 18px;
}

.add-rule-button {
  margin-bottom: 8px;
}

.rule-card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 16px;
  margin-bottom: 8px;
  padding: 16px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
}

.rule-domain-row {
  display: flex;
  align-items: center;
  gap: 16px;
  align-self: stretch;
}

.rule-domain-row input {
  flex: 1;
}

.rule-http-headers-section {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  align-items: center;
  gap: 16px;
  align-self: stretch;
}

.warning-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 8px 0;
  padding: 8px 16px;
  border: 0;
  background: none;
  color: var(--color-v3-text);
  cursor: pointer;
  font: inherit;
  font-size: 14px;
}

.warning-callout {
  display: flex;
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 8px;
  color: var(--color-v3-text);
}

.warning-callout :deep(svg) {
  flex: none;
  color: var(--color-v3-warning, #fbbc04);
}

.warning-callout p {
  margin: 0;
  font-size: 14px;
  line-height: 21px;
}

.rule-count {
  margin-right: auto;
  color: var(--color-v3-text-var);
  font-size: 14px;
}
</style>
