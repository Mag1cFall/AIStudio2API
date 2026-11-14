<script setup lang="ts">
defineProps<{ label: string; percent: number | null; sizeText: string }>()
</script>

<template>
  <div
    role="progressbar"
    :aria-label="label"
    aria-valuemin="0"
    aria-valuemax="100"
    :aria-valuenow="percent ?? undefined"
    :aria-valuetext="sizeText || undefined"
    class="h-1 overflow-hidden rounded-full bg-blue-500/20"
  >
    <div
      v-if="percent !== null"
      class="h-full rounded-full bg-blue-500 transition-[width] duration-200 ease-out"
      :style="{ width: `${percent}%` }"
    />
    <div v-else class="camoufox-indeterminate h-full w-1/3 rounded-full bg-blue-500" />
  </div>
</template>

<style scoped>
.camoufox-indeterminate {
  animation: camoufox-indeterminate 1.2s ease-in-out infinite;
}
@keyframes camoufox-indeterminate {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(300%);
  }
}
@media (prefers-reduced-motion: reduce) {
  .camoufox-indeterminate {
    animation: none;
  }
}
</style>
