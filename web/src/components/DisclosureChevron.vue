<script setup lang="ts">
// turn 是展开时箭头转过的角度：向右的箭头转 90 度指向下方，向下的箭头转 180 度指向上方
withDefaults(defineProps<{ open: boolean; turn?: 90 | 180 }>(), { turn: 90 })
</script>

<template>
  <span class="disclosure-chevron" :class="{ open }" :style="{ '--turn': `${turn}deg` }"
    ><slot
  /></span>
</template>

<style scoped>
/* disclosure-chevron 与展开收起的内容区同时长、同缓动旋转 */
.disclosure-chevron {
  display: inline-flex;
  flex-shrink: 0;
  transition: transform 225ms cubic-bezier(0.4, 0, 0.2, 1);
}
.disclosure-chevron.open {
  transform: rotate(var(--turn));
}
@media (prefers-reduced-motion: reduce) {
  .disclosure-chevron {
    transition: none;
  }
}
</style>
