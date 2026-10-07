<script setup lang="ts">
import { useI18n } from '@/i18n'

defineProps<{
  label: string
  controlId?: string
  groupId?: string
  description?: string
  effect: 'immediate' | 'service' | 'management'
  changed?: boolean
}>()

const { t } = useI18n()
</script>

<template>
  <div
    class="flex flex-col gap-3 border-b border-line py-4 last:border-b-0 md:flex-row md:items-start md:justify-between md:gap-10"
  >
    <div class="min-w-0 md:max-w-md">
      <div class="flex flex-wrap items-center gap-2">
        <span v-if="groupId" :id="`${groupId}-label`" class="text-sm font-medium text-gray-200">{{
          label
        }}</span>
        <label v-else :for="controlId" class="text-sm font-medium text-gray-200">{{ label }}</label>
        <span
          v-if="changed"
          v-tooltip="t('settings.unsavedField')"
          class="h-1.5 w-1.5 rounded-full bg-amber-400"
          ><span class="sr-only">{{ t('settings.unsavedField') }}</span></span
        >
      </div>
      <p v-if="description" class="mt-1 text-xs leading-5 text-gray-500">{{ description }}</p>
      <p class="mt-1 text-[11px] text-gray-600">{{ t(`settings.effect.${effect}`) }}</p>
    </div>
    <div class="w-full md:w-80 md:shrink-0">
      <slot />
    </div>
  </div>
</template>
