<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '@/i18n'
import CodeDialog from './CodeDialog.vue'
import { codeOptions, generateCode, type CodeLanguage } from './code'
import { buildRequest } from './request'
import { usePlayground } from './session'
import type { Turn } from './types'

const open = defineModel<boolean>('open', { required: true })
const { session, currentModel } = usePlayground()
const { locale } = useI18n()
// docs 是本服务 README 中的 API 使用说明，中文界面指向中文 README
const docs = computed(() =>
  locale.value.startsWith('zh')
    ? 'https://github.com/Mag1cFall/AIStudio2API/blob/main/README.md#api-使用'
    : 'https://github.com/Mag1cFall/AIStudio2API/blob/main/README_en.md#api-usage',
)

// turns 是示例代码中的对话：已有轮次加上输入框中尚未发送的内容，即按下运行时页面发出的请求；输入框为空且最后不是用户轮次时与官网一样以 INSERT_INPUT_HERE 作为新的用户轮次
const turns = computed<Turn[]>(() => {
  const draft = session.draft.trim()
  if (draft === '' && session.attachments.length === 0) {
    if (session.turns.at(-1)?.role === 'user') return session.turns
    return [
      ...session.turns,
      { id: 'draft', role: 'user', parts: [{ text: 'INSERT_INPUT_HERE' }], created: 0 },
    ]
  }
  return [
    ...session.turns,
    {
      id: 'draft',
      role: 'user',
      parts: [...session.attachments, ...(draft ? [{ text: session.draft }] : [])],
      created: 0,
    },
  ]
})

// code 按语言生成当前对话的示例代码，请求与页面运行时以所选协议发送的请求相同
function code(language: CodeLanguage): string {
  return generateCode(language, {
    model: currentModel.value,
    modelID: currentModel.value?.id ?? session.settings.model,
    baseURL: window.location.origin,
    request: buildRequest(session.settings, currentModel.value, turns.value),
    protocol: session.settings.protocol,
    stream: session.settings.stream,
    images: {
      count: session.settings.imageCount,
      size: session.settings.openAISize,
      quality: session.settings.imageQuality,
    },
  })
}
</script>

<template>
  <CodeDialog v-model:open="open" :options="codeOptions" :code="code" :docs="docs" />
</template>
