<script setup lang="ts">
import { useId } from 'vue'

const gradient = `spinner-gradient-${useId()}`
</script>

<template>
  <span class="ui-gradient-spinner" aria-hidden="true">
    <svg viewBox="0 0 20 20" fill="none" class="gradient-spinner" overflow="visible">
      <defs>
        <linearGradient
          :id="gradient"
          x1="10"
          y1="1"
          x2="10"
          y2="19"
          gradientUnits="userSpaceOnUse"
        >
          <stop offset="0" stop-color="#EA4335" />
          <stop offset="0.3333" stop-color="#EA4335" />
          <stop offset="0.3333" stop-color="#34A853" />
          <stop offset="0.6666" stop-color="#34A853" />
          <stop offset="0.6666" stop-color="#4285F4" />
          <stop offset="1" stop-color="#4285F4" />
        </linearGradient>
      </defs>
      <circle
        cx="10"
        cy="10"
        r="9"
        fill="none"
        stroke-linecap="round"
        :stroke="`url(#${gradient})`"
      />
    </svg>
  </span>
</template>

<style scoped>
@keyframes rotate {
  to {
    transform: rotate(1turn);
  }
}

@keyframes dash {
  0% {
    stroke-dasharray: 1, 56;
    stroke-dashoffset: 0;
  }

  50% {
    stroke-dasharray: 28, 29;
    stroke-dashoffset: -15;
  }

  to {
    stroke-dasharray: 1, 56;
    stroke-dashoffset: -56;
  }
}

.ui-gradient-spinner {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
}

.gradient-spinner {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  margin: auto;
  transform-origin: center;
  animation: rotate 2s linear infinite;
}

.gradient-spinner circle {
  stroke-width: 1;
  animation: dash 2s cubic-bezier(0.4, 0, 0.2, 1) infinite;
}
</style>
