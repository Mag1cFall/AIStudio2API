<script setup lang="ts">
import { useI18n } from '@/i18n'

defineProps<{ text?: string; noAnimation?: boolean }>()
const { t } = useI18n()
</script>

<template>
  <span class="thinking-shimmer" :class="{ 'no-animation': noAnimation }">{{
    text ?? t('pg.thinking')
  }}</span>
</template>

<style scoped>
.thinking-shimmer {
  display: inline-block;
  background: linear-gradient(
    90deg,
    var(--color-v3-chat-separator) 0,
    var(--color-v3-chat-separator) 19%,
    #7bacf8 23%,
    #7bacf8 27%,
    var(--color-v3-chat-separator) 31%,
    var(--color-v3-chat-separator) 37%,
    #5ec47a 41%,
    #5ec47a 45%,
    var(--color-v3-chat-separator) 49%,
    var(--color-v3-chat-separator) 55%,
    #fcd462 59%,
    #fcd462 63%,
    var(--color-v3-chat-separator) 67%,
    var(--color-v3-chat-separator) 71%,
    #e8b8b5 75%,
    #e8b8b5 79%,
    var(--color-v3-chat-separator) 83%,
    var(--color-v3-chat-separator) 100%
  );
  background-size: 500% 100%;
  background-clip: text;
  font-size: 14px;
  font-weight: 400;
  line-height: 21px;
  -webkit-text-fill-color: transparent;
  animation: shimmer-cycle 5s linear infinite;
}

.thinking-shimmer.no-animation {
  background-position: 50% 0;
  animation: none;
}

@keyframes shimmer-cycle {
  0% {
    background-position: 100% 0;
  }

  to {
    background-position: 0 0;
  }
}
</style>
