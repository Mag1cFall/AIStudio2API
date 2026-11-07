<script setup lang="ts">
import { useI18n } from '@/i18n'

defineProps<{
  label: string
  controlId?: string
  groupId?: string
  description?: string
  effect: 'immediate' | 'service' | 'management'
  changed?: boolean
  env?: string | undefined
}>()

const { t } = useI18n()
</script>

<template>
  <div class="@container border-b border-line py-4 last:border-b-0">
    <div
      class="flex flex-col gap-3 has-[[role=switch]]:flex-row has-[[role=switch]]:justify-between @xl:flex-row @xl:items-start @xl:justify-between @xl:gap-10"
    >
      <div class="min-w-0 flex-1 @xl:max-w-md">
        <div class="flex flex-wrap items-center gap-2">
          <span v-if="groupId" :id="`${groupId}-label`" class="text-sm font-medium text-gray-200">{{
            label
          }}</span>
          <label v-else :for="controlId" class="text-sm font-medium text-gray-200">{{
            label
          }}</label>
          <span
            v-if="changed"
            v-tooltip="t('settings.unsavedField')"
            class="h-1.5 w-1.5 rounded-full bg-amber-400"
            ><span class="sr-only">{{ t('settings.unsavedField') }}</span></span
          >
        </div>
        <p v-if="description" class="mt-1 text-xs leading-5 text-gray-500">{{ description }}</p>
        <p class="mt-1 text-xs text-gray-600">{{ t(`settings.effect.${effect}`) }}</p>
        <p v-if="env" class="mt-1 text-xs text-amber-300">
          {{ t('settings.environmentLocked').replace('{key}', env) }}
        </p>
      </div>
      <fieldset
        :disabled="env !== undefined"
        class="w-full min-w-0 disabled:opacity-60 has-[[role=switch]]:w-auto has-[[role=switch]]:shrink-0 @xl:w-80 @xl:shrink-0"
      >
        <slot />
      </fieldset>
    </div>
  </div>
</template>
