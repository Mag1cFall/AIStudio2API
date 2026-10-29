<script setup lang="ts">
import { SwitchRoot, SwitchThumb } from 'reka-ui'

const model = defineModel<boolean>({ required: true })
defineProps<{ id: string }>()
</script>

<template>
  <SwitchRoot
    :id="id"
    v-model="model"
    class="relative flex h-[18px] w-8 shrink-0 items-center rounded-full bg-line-strong transition-colors duration-150 hover:bg-gray-600 data-[state=checked]:bg-blue-600 data-[state=checked]:hover:bg-blue-500 md:ms-auto md:mt-px"
  >
    <SwitchThumb
      class="block size-3.5 translate-x-0.5 rounded-full bg-[#fff] shadow-[0_1px_2px_rgb(0_0_0/0.35)] transition-transform duration-150 ease-[cubic-bezier(0.2,0.8,0.2,1)] data-[state=checked]:translate-x-4"
    />
  </SwitchRoot>
</template>
