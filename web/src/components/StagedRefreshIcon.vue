<script setup lang="ts">
withDefaults(defineProps<{ size?: number }>(), { size: 15 })
</script>

<!-- StagedRefreshIcon 是刷新全部进行中的分段刷新图形：停顿、两段箭弧收短并旋转半圈、箭弧伸回、箭头弹出，循环播放 -->
<template>
  <svg
    class="staged-refresh shrink-0"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.75"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path class="arc" pathLength="1" d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" />
    <path class="head" d="M21 3v5h-5" />
    <path class="arc" pathLength="1" d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" />
    <path class="head" d="M8 16H3v5" />
  </svg>
</template>

<style scoped>
.staged-refresh {
  animation: staged-turn 1.6s cubic-bezier(0.65, 0, 0.35, 1) infinite;
}

.arc {
  stroke-dasharray: 1 1;
  animation: staged-arc 1.6s ease-in-out infinite;
}

.head {
  transform-box: fill-box;
  transform-origin: center;
  animation: staged-head 1.6s ease-out infinite;
}

@keyframes staged-turn {
  0%,
  20% {
    transform: rotate(0deg);
  }
  62%,
  100% {
    transform: rotate(180deg);
  }
}

@keyframes staged-arc {
  0%,
  20% {
    stroke-dashoffset: 0;
  }
  40% {
    stroke-dashoffset: 0.62;
  }
  62%,
  100% {
    stroke-dashoffset: 0;
  }
}

@keyframes staged-head {
  0%,
  16% {
    opacity: 1;
    transform: scale(1);
  }
  26%,
  60% {
    opacity: 0;
    transform: scale(0.4);
  }
  72% {
    opacity: 1;
    transform: scale(1.18);
  }
  82%,
  100% {
    opacity: 1;
    transform: scale(1);
  }
}

@media (prefers-reduced-motion: reduce) {
  .staged-refresh,
  .arc,
  .head {
    animation: none;
  }
}
</style>
