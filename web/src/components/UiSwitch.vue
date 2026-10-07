<script setup lang="ts">
import { SwitchRoot, SwitchThumb } from 'reka-ui'

const model = defineModel<boolean>({ required: true })
defineProps<{ id: string; disabled?: boolean }>()
</script>

<template>
  <SwitchRoot
    :id="id"
    v-model="model"
    :disabled="disabled"
    class="relative inline-flex h-5 w-9 shrink-0 items-center rounded-full border border-line-strong bg-raised transition-colors disabled:opacity-50 data-[state=checked]:border-blue-500 data-[state=checked]:bg-blue-600"
  >
    <SwitchThumb
      class="block h-3.5 w-3.5 translate-x-0.5 rounded-full bg-gray-400 transition-transform data-[state=checked]:translate-x-[17px] data-[state=checked]:bg-white"
    />
  </SwitchRoot>
</template>
