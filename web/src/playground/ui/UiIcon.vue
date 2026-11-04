<script setup lang="ts">
import { ref, watchPostEffect } from 'vue'
import { iconNode, type IconName } from '@/playground/ui/icons'

const props = withDefaults(defineProps<{ name: IconName; size?: number }>(), { size: 20 })
const host = ref<HTMLSpanElement>()
watchPostEffect(() => host.value?.replaceChildren(iconNode(props.name)))
</script>

<template>
  <span
    ref="host"
    class="ui-icon"
    :class="props.name === 'spinner' ? 'animate-spin' : ''"
    :style="{ width: `${props.size}px`, height: `${props.size}px` }"
    aria-hidden="true"
  ></span>
</template>

<style scoped>
.ui-icon {
  display: inline-flex;
  flex-shrink: 0;
}

.ui-icon :deep(svg) {
  width: 100%;
  height: 100%;
  fill: currentColor;
}
</style>
