<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import type { SavedPrompt } from '@/types'
import PlaygroundPage from './PlaygroundPage.vue'
import UiGradientSpinner from './ui/UiGradientSpinner.vue'
import { usePlayground } from './session'
import type { PromptData } from './types'

const route = useRoute()
const { t } = useI18n()
const playground = usePlayground()
// ready 表示已读取过地址中的会话，读取完成前显示读取中
const ready = ref(false)
const prompt = ref<SavedPrompt<PromptData>>()

// load 读取已保存的会话交给对话页，读取失败时交给对话页再读一次并显示原因
async function load(id: string): Promise<void> {
  try {
    const saved = await api.prompt<PromptData>(id)
    if (route.params.id !== id) return
    prompt.value = saved
  } catch {
    if (route.params.id !== id) return
    prompt.value = undefined
  }
  ready.value = true
}

// retry 重新打开读取失败的会话
function retry(): void {
  ready.value = false
  void load(String(route.params.id))
}

watch(
  () => route.params.id,
  (id) => {
    if (typeof id !== 'string' || id === '') return
    if (playground.session.id === id) {
      ready.value = true
      return
    }
    void load(id)
  },
  { immediate: true },
)
</script>

<template>
  <PlaygroundPage
    v-if="ready"
    :prompt="prompt?.id === route.params.id ? prompt : undefined"
    @retry="retry"
  />
  <div v-else class="prompt-loading" role="status" :aria-label="t('common.loading')">
    <UiGradientSpinner />
  </div>
</template>

<style scoped>
.prompt-loading {
  display: flex;
  flex: 1;
  align-items: center;
  justify-content: center;
  animation: prompt-loading-in 0.2s ease-out 0.3s both;
}

@keyframes prompt-loading-in {
  from {
    opacity: 0;
  }
}
</style>
