<script setup lang="ts">
import { computed, defineAsyncComponent } from 'vue'
import { useRoute } from 'vue-router'
import { useConsole } from '@/console'
import { pageKindOf } from './kinds'

const PlaygroundPage = defineAsyncComponent(() => import('../PlaygroundPage.vue'))
const ImagenPage = defineAsyncComponent(() => import('./ImagenPage.vue'))
const route = useRoute()
const store = useConsole()

// imagen 与官网图片页一致：地址中的模型为 Imagen 模型，或没有指定模型且目录中有 Imagen 模型时显示 Imagen 页，其余为 Nano Banana 对话
const imagen = computed(() => {
  const models = store.models.value
  const requested = typeof route.query.model === 'string' ? route.query.model : ''
  const model = requested
    ? models.find((item) => item.id === requested)
    : models.find((item) => pageKindOf(item) === 'image')
  return model !== undefined && pageKindOf(model) === 'image'
})
</script>

<template>
  <ImagenPage v-if="imagen" />
  <PlaygroundPage v-else />
</template>
