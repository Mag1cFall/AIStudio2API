<script setup lang="ts">
defineProps<{ items: { name: string; color: string }[]; hidden: string[] }>()
const emit = defineEmits<{ toggle: [name: string]; focus: [name: string | null] }>()
</script>

<template>
  <ul class="mb-2 flex flex-wrap gap-x-3 gap-y-1 text-xs">
    <li v-for="item in items" :key="item.name" class="max-w-full min-w-0">
      <button
        type="button"
        class="flex max-w-full items-center gap-1.5 rounded py-0.5 transition-colors"
        :class="hidden.includes(item.name) ? 'text-gray-600' : 'text-gray-400 hover:text-gray-200'"
        :aria-pressed="!hidden.includes(item.name)"
        @click="emit('toggle', item.name)"
        @pointerenter="emit('focus', item.name)"
        @pointerleave="emit('focus', null)"
      >
        <span
          class="h-2.5 w-2.5 shrink-0 rounded-sm"
          :style="{ backgroundColor: hidden.includes(item.name) ? '#4b5563' : item.color }"
        ></span>
        <span v-tooltip.overflow="item.name" class="truncate">{{ item.name }}</span>
      </button>
    </li>
  </ul>
</template>
