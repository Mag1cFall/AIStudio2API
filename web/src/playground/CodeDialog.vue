<script setup lang="ts" generic="Language extends string">
import { computed, ref } from 'vue'
import hljs from 'highlight.js/lib/common'
import { useI18n } from '@/i18n'
import UiDialog from '@/playground/ui/UiDialog.vue'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSelect from '@/playground/ui/UiSelect.vue'
import { copyToClipboard } from './clipboard'
import type { CodeOption } from './code'

const props = defineProps<{
  options: CodeOption<Language>[]
  code: (language: Language) => string
  docs?: string
}>()
const open = defineModel<boolean>('open', { required: true })
const { t } = useI18n()
const language = ref<string>(props.options[0]!.value)
const option = computed(() => props.options.find((item) => item.value === language.value)!)
const text = computed(() => props.code(option.value.value))
const highlighted = computed(
  () =>
    hljs.highlight(text.value, {
      language: option.value.highlight,
      ignoreIllegals: true,
    }).value,
)

// download 下载示例代码
function download(): void {
  const url = URL.createObjectURL(new Blob([text.value], { type: 'text/plain' }))
  const link = document.createElement('a')
  link.href = url
  link.download = option.value.file
  link.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <UiDialog v-model:open="open" :title="t('pg.getCode')" width="840px">
    <div class="language-select">
      <UiSelect v-model="language" class="w-full" :aria-label="t('pg.language')">
        <option v-for="item in options" :key="item.value" :value="item.value">
          {{ item.label }}
        </option>
      </UiSelect>
    </div>
    <div class="code-container">
      <div class="code-header">
        <div class="header-content">
          <UiIcon name="code" :size="20" class="title-icon" />
          <span>{{ t('pg.code') }}</span>
        </div>
        <button
          v-tooltip="t('pg.download')"
          type="button"
          ms-button
          class="ms-button-borderless ms-button-icon"
          :aria-label="t('pg.download')"
          @click="download"
        >
          <UiIcon name="download" :size="20" />
        </button>
        <a
          v-if="docs"
          v-tooltip="t('pg.apiDocs')"
          ms-button
          class="ms-button-borderless ms-button-icon"
          :href="docs"
          target="_blank"
          rel="noopener noreferrer"
          :aria-label="t('pg.apiDocs')"
        >
          <UiIcon name="developerGuide" :size="20" />
        </a>
      </div>
      <pre class="code-body"><code class="hljs" v-html="highlighted"></code></pre>
    </div>
    <template #actions>
      <button type="button" ms-button class="ms-button-borderless" @click="open = false">
        {{ t('common.close') }}
      </button>
      <button type="button" ms-button class="ms-button-primary" @click="copyToClipboard(text)">
        {{ t('pg.copy') }}
      </button>
    </template>
  </UiDialog>
</template>

<style scoped>
.language-select {
  width: 100%;
  margin-bottom: 24px;
}

.code-container {
  border: 1px solid var(--color-v3-outline-var);
  border-radius: 12px;
  background: var(--color-v3-surface-container);
  box-shadow: var(--shadow-v3-xs);
  overflow: hidden;
}

.code-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 36px;
  padding-inline: 12px 6px;
  font-weight: 500;
}

.header-content {
  display: flex;
  flex: 1;
  align-items: center;
}

.title-icon {
  margin-right: 4px;
  color: var(--color-v3-text-var);
}

.code-body {
  max-height: 400px;
  margin: 0;
  padding: 0 16px 12px 12px;
  overflow: auto;
  line-height: 22px;
}

.code-body code {
  font-family: monospace;
  font-size: 13px;
}
</style>
