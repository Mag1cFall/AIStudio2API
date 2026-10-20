<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api'
import type { SavedPrompt } from '@/types'
import PlaygroundPage from './PlaygroundPage.vue'
import VideoPage from './media/VideoPage.vue'
import { useVideo, type VideoData } from './media/video'
import { usePlayground } from './session'
import type { PromptData } from './types'

defineOptions({ name: 'PromptRoute' })

const route = useRoute()
const playground = usePlayground()
const video = useVideo()
// kind 是已保存提示词的页面类型，读取完成前为空
const kind = ref<'chat' | 'video' | ''>('')
const prompt = ref<SavedPrompt<PromptData | VideoData>>()
// savedVideo 是当前地址对应的已保存视频会话
const savedVideo = computed(() => {
  const saved = prompt.value
  if (!saved || saved.id !== route.params.id) return undefined
  return { title: saved.title, data: saved.data as VideoData }
})

watch(
  () => route.params.id,
  async (id) => {
    if (typeof id !== 'string' || id === '') return
    if (playground.session.id === id) {
      kind.value = 'chat'
      return
    }
    if (video.state.id === id) {
      kind.value = 'video'
      return
    }
    try {
      const saved = await api.prompt<PromptData | VideoData>(id)
      if (route.params.id !== id) return
      prompt.value = saved
      kind.value = 'kind' in saved.data && saved.data.kind === 'video' ? 'video' : 'chat'
    } catch {
      prompt.value = undefined
      kind.value = 'chat'
    }
  },
  { immediate: true },
)
</script>

<template>
  <PlaygroundPage
    v-if="kind === 'chat'"
    :prompt="prompt?.id === route.params.id ? (prompt as SavedPrompt<PromptData>) : undefined"
  />
  <VideoPage
    v-else-if="kind === 'video'"
    :prompt-id="String(route.params.id)"
    :prompt="savedVideo"
  />
</template>
