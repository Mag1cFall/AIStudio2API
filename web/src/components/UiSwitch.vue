<script setup lang="ts">
const model = defineModel<boolean>({ required: true })
defineProps<{ id: string }>()
</script>

<template>
  <button
    :id="id"
    type="button"
    role="switch"
    :aria-checked="model"
    :data-state="model ? 'checked' : 'unchecked'"
    class="group relative flex h-[18px] w-8 shrink-0 items-center rounded-full bg-line-strong transition-colors duration-150 hover:bg-gray-600 disabled:cursor-default disabled:opacity-50 data-[state=checked]:bg-blue-600 data-[state=checked]:hover:bg-blue-500 md:ms-auto"
    @click="model = !model"
  >
    <span
      class="block size-3.5 translate-x-0.5 rounded-full bg-[#fff] shadow-[0_1px_2px_rgb(0_0_0/0.35)] transition-transform duration-150 ease-[cubic-bezier(0.2,0.8,0.2,1)] group-data-[state=checked]:translate-x-4"
    ></span>
  </button>
</template>
