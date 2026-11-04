<script setup lang="ts">
import '@/hljs.css'
import { shallowRef, watch } from 'vue'
import { useI18n } from '@/i18n'
import { highlightCode } from '@/playground/highlight'
import CopyButton from './CopyButton.vue'

// copy 是复制按钮写入剪贴板的文字，与显示的代码不同时传入
const props = defineProps<{ code: string; language: string; label?: string; copy?: string }>()
const { t } = useI18n()

// highlighted 是共用 Worker 返回的高亮结果与对应原文，结果返回前显示原文
const highlighted = shallowRef<{ code: string; html: string }>()
watch(
  () => [props.code, props.language] as const,
  async ([code, language]) => {
    const html = await highlightCode(code, language)
    if (code === props.code) highlighted.value = { code, html }
  },
  { immediate: true },
)
</script>

<template>
  <figure class="min-w-0 overflow-hidden rounded-md border border-line bg-canvas">
    <figcaption
      class="flex min-h-9 items-center gap-2 border-b border-line py-1 pr-1.5 pl-3 text-xs text-gray-500"
    >
      <span v-tooltip.overflow="label" class="min-w-0 flex-1 truncate font-mono">{{ label }}</span>
      <CopyButton
        :text="copy ?? code"
        :label="label ? t('access.copyValue').replace('{name}', label) : t('common.copy')"
      />
    </figcaption>
    <pre
      class="overflow-x-auto p-3 font-mono text-[13px] leading-5 text-gray-200"
    ><code v-if="highlighted?.code === code" class="hljs" v-html="highlighted.html"></code><code v-else class="hljs">{{ code }}</code></pre>
  </figure>
</template>
