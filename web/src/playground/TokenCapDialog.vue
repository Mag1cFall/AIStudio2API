<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'

const open = defineModel<boolean>('open', { required: true })
// cap 是 Antigravity 本次会话的 token 上限，null 为不设上限
const cap = defineModel<number | null>('cap', { required: true })
const { t } = useI18n()

// presets 是官网的常用上限
const presets = [
  { label: '500K', value: 500_000 },
  { label: '750K', value: 750_000 },
  { label: '1000K', value: 1_000_000 },
]
// draft 是对话框中尚未保存的上限
const draft = ref<number | null>(null)
watch(open, (value) => {
  if (value) draft.value = cap.value
})
// invalid 与官网一致，上限为空或不是正数时不能保存
const invalid = computed(() => draft.value === null || draft.value <= 0)

// save 保存上限并关闭对话框
function save(): void {
  if (invalid.value) return
  cap.value = Math.round(draft.value!)
  open.value = false
}

// remove 去掉上限并关闭对话框
function remove(): void {
  cap.value = null
  open.value = false
}

// input 把输入框内容换为上限，清空时为 null
function input(event: Event): void {
  const value = (event.target as HTMLInputElement).value
  draft.value = value === '' ? null : Number(value)
}
</script>

<template>
  <UiDialog v-model:open="open" :title="t('pg.tokenCapTitle')" width="480px">
    <p class="dialog-description">
      {{ t('pg.tokenCapDescription') }}
      <a
        href="https://aistudio.google.com/docs/antigravity-agent#budget-controls"
        target="_blank"
        rel="noopener"
        >{{ t('pg.learnMore') }}</a
      >
    </p>
    <div class="input-container">
      <label class="input-label" for="token-cap-input">{{ t('pg.tokenCapLabel') }}</label>
      <input
        id="token-cap-input"
        ms-input
        type="number"
        min="1"
        step="1"
        autofocus
        :value="draft ?? ''"
        @input="input"
        @keydown.enter.prevent="save"
      />
    </div>
    <div class="preset-chips-row">
      <button
        v-for="preset in presets"
        :key="preset.value"
        type="button"
        ms-button
        class="ms-button-secondary preset-chip"
        :class="{ selected: draft === preset.value }"
        @click="draft = preset.value"
      >
        {{ preset.label }}
      </button>
    </div>
    <template #actions>
      <button
        v-if="cap !== null"
        type="button"
        ms-button
        class="ms-button-borderless remove-cap-button"
        @click="remove"
      >
        {{ t('pg.remove') }}
      </button>
      <button type="button" ms-button class="ms-button-borderless" @click="open = false">
        {{ t('common.cancel') }}
      </button>
      <button
        type="button"
        ms-button
        class="ms-button-primary"
        :aria-disabled="invalid"
        @click="save"
      >
        {{ t('common.save') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
/* dialog-description、input-container、preset-chips-row 与 remove-cap-button 取官网 ms-token-cap-dialog */
.dialog-description {
  margin: 0 0 12px;
  color: var(--color-v3-text-var);
  font-size: 14px;
  line-height: 21px;
}

.dialog-description a {
  color: var(--color-v3-text-link);
}

.input-container {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
  max-width: 144px;
}

.input-label {
  color: var(--color-v3-text-var);
  font-size: 12px;
  line-height: 16px;
}

.preset-chips-row {
  display: flex;
  gap: 8px;
  margin-top: 12px;
}

.preset-chip.selected {
  background-color: var(--color-v3-surface-container-high);
}

.remove-cap-button {
  margin-right: auto;
}
</style>
