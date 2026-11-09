<script setup lang="ts">
import { useI18n } from '@/i18n'
import HelpHint from './HelpHint.vue'

defineProps<{
  label: string
  controlId?: string
  groupId?: string
  description?: string
  effect: 'immediate' | 'service' | 'management'
  changed?: boolean
  env?: string | undefined
  // envNote 是部分控件由环境变量决定时的说明，整行控件仍可编辑
  envNote?: string | undefined
}>()

const { t } = useI18n()
</script>

<template>
  <div
    class="group/row @container border-b border-line py-3.5 last:border-b-0 [&:has([role=switch])_label]:cursor-pointer"
  >
    <div
      class="flex flex-col gap-3 has-[[role=switch]]:flex-row has-[[role=switch]]:items-center has-[[role=switch]]:justify-between @xl:flex-row @xl:items-start @xl:justify-between"
    >
      <div class="min-w-0 flex-1 @xl:max-w-md @xl:pt-2 has-[+[role=switch]]:pt-0">
        <div class="flex flex-wrap items-center gap-2">
          <span v-if="groupId" :id="`${groupId}-label`" class="text-sm font-medium text-gray-200">{{
            label
          }}</span>
          <label v-else :for="controlId" class="text-sm font-medium text-gray-200">{{
            label
          }}</label>
          <slot name="tag" />
          <HelpHint :label="label">
            <p v-if="description">{{ description }}</p>
            <p :class="description ? 'mt-1.5 text-gray-500' : 'text-gray-500'">
              {{ t(`settings.effect.${effect}`) }}
            </p>
          </HelpHint>
        </div>
        <p v-if="changed && effect !== 'immediate'" class="mt-1 text-xs text-gray-500">
          {{ t(`settings.effect.${effect}`) }}
        </p>
        <p v-if="env ?? envNote" class="mt-1 text-xs text-gray-400">
          {{ t('settings.environmentLocked').replace('{key}', (env ?? envNote)!) }}
        </p>
        <slot name="note" />
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
