<script setup lang="ts">
import { computed } from 'vue'
import { icons, type IconName } from '@/playground/ui/icons'

const props = withDefaults(defineProps<{ name: IconName; size?: number }>(), { size: 20 })
const svg = computed(() => icons[props.name])
</script>

<template>
  <span
    class="ui-icon"
    :class="props.name === 'spinner' ? 'animate-spin' : ''"
    :style="{ width: `${props.size}px`, height: `${props.size}px` }"
    aria-hidden="true"
    v-html="svg"
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
