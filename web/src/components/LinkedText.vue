<script setup lang="ts">
import { computed } from 'vue'

// LinkedText 显示含 https 地址的文字，地址为在新窗口打开的链接，compact 时链接文字只取主机与路径
const props = defineProps<{ text: string; compact?: boolean }>()

// pieces 把文字按其中的 https 地址切分，地址片段标记为链接
const pieces = computed(() =>
  props.text
    .split(/(https:\/\/[^\s，。；）)]+)/)
    .filter(Boolean)
    .map((piece) => ({ text: piece, url: piece.startsWith('https://') })),
)

// label 返回链接显示的文字
function label(url: string): string {
  if (!props.compact) return url
  const parsed = new URL(url)
  return parsed.host + parsed.pathname
}
</script>

<template>
  <template v-for="(piece, index) in pieces" :key="index"
    ><a
      v-if="piece.url"
      :href="piece.text"
      target="_blank"
      rel="noreferrer"
      class="underline underline-offset-2 hover:text-red-200"
      >{{ label(piece.text) }}</a
    ><template v-else>{{ piece.text }}</template></template
  >
</template>
