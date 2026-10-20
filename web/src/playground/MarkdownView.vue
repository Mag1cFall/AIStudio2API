<script setup lang="ts">
import { computed, ref } from 'vue'
import MarkdownBlock from './MarkdownBlock'
import { IncrementalMarkdown } from './markdown'

const props = defineProps<{ text: string; raw?: boolean }>()
const root = ref<HTMLElement>()
const engine = new IncrementalMarkdown()
// blocks 是正文的顶层块，流式增长时只有末尾块重新解析与渲染
const blocks = computed(() => (props.raw ? [] : engine.render(props.text)))

defineExpose({ root })
</script>

<template>
  <pre v-if="raw" class="raw-text">{{ text }}</pre>
  <div v-else ref="root" class="cmark-node">
    <MarkdownBlock v-for="block in blocks" :key="block.key" :nodes="block.nodes" />
  </div>
</template>

<style scoped>
.raw-text {
  margin: 0;
  font-family: inherit;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.cmark-node {
  display: flex;
  flex-direction: column;
  min-width: 100%;
  overflow-wrap: anywhere;
}

.cmark-node :deep(:is(p, li, td, th, blockquote, dt)) {
  font-size: 14px;
  line-height: 21px;
}

.cmark-node :deep(p) {
  margin: 0 0 18px;
}

.cmark-node :deep(:is(h1, h2, h3, h4, h5, h6)) {
  margin: 0;
  font-weight: 700;
}

.cmark-node :deep(:is(h1, h2, h3, h4, h5, h6):has(+ :is(h1, h2, h3, h4, h5, h6))) {
  margin-bottom: 18px;
}

.cmark-node :deep(h1) {
  font-size: 2em;
  line-height: 1.15;
}

.cmark-node :deep(h2) {
  font-size: 1.5em;
}

.cmark-node :deep(h3) {
  font-size: 1.17em;
}

.cmark-node :deep(h4) {
  font-size: 1em;
}

.cmark-node :deep(h5) {
  font-size: 0.83em;
}

.cmark-node :deep(h6) {
  font-size: 0.67em;
}

.cmark-node :deep(:is(ol, ul)) {
  margin: 0 0 18px;
  padding-left: 24px;
}

.cmark-node :deep(ul) {
  list-style: disc;
}

.cmark-node :deep(ol) {
  list-style: decimal;
}

.cmark-node :deep(:is(ul, ol) ul) {
  list-style: circle;
}

.cmark-node :deep(:is(ul, ol) :is(ul, ol) ul) {
  list-style: square;
}

.cmark-node :deep(li p) {
  margin-bottom: 7px;
}

.cmark-node :deep(li > p:last-child) {
  margin-bottom: 0;
}

.cmark-node > :deep(:last-child),
.cmark-node > :deep(:last-child > :last-child) {
  margin-bottom: 0;
}

.cmark-node :deep(:not(pre) > code) {
  display: inline-block;
  padding: 0 2px;
  border-radius: 4px;
  background: light-dark(var(--color-v3-surface-container-high), var(--color-v3-button-container));
  color: var(--color-v3-text-on-button);
  font-family: 'DM Mono', monospace;
  font-size: 1em;
}

.cmark-node :deep(a) {
  color: var(--color-v3-text-link);
}

.cmark-node :deep(blockquote) {
  margin: 16px 0;
  padding: 12px 16px;
  border-radius: 8px;
  background-color: var(--color-markdown-blockquote-bg);
}

.cmark-node :deep(blockquote p:last-child) {
  margin-bottom: 0;
}

.cmark-node :deep(hr) {
  width: 100%;
  min-width: 33cqw;
  margin: 16px 0;
  border: none;
  border-top: 1px solid var(--color-v3-outline);
}

.cmark-node :deep(img) {
  display: block;
  max-width: 100%;
  height: auto;
  margin: 8px 0;
  border-radius: 4px;
}

.cmark-node :deep(.table-container) {
  max-width: 100%;
  margin-bottom: 16px;
  overflow: auto hidden;
}

.cmark-node :deep(table) {
  border-collapse: collapse;
  border-radius: 8px;
}

.cmark-node :deep(:is(td, th)) {
  padding: 6px 12px;
  overflow-wrap: normal;
  text-align: left;
}

.cmark-node :deep(th) {
  font-weight: 700;
}

.cmark-node :deep(.katex-display) {
  margin: 8px 0;
  overflow: auto hidden;
}

.cmark-node :deep(:is(p, li, td, th):has(.katex)) {
  line-height: 2;
}
</style>
