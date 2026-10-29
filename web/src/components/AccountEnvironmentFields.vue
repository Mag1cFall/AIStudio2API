<script setup lang="ts">
import { useI18n } from '@/i18n'
import type { AccountLoginInput } from '@/types'

// environment 是账户的代理、语言与时区
const environment = defineModel<AccountLoginInput>({ required: true })
const { t } = useI18n()
</script>

<template>
  <label class="block">
    <span class="mb-1 block text-sm font-medium text-gray-400">{{ t('accounts.proxy') }}</span>
    <input
      v-model.trim="environment.proxy"
      class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
      placeholder="socks5://127.0.0.1:1080"
    />
  </label>
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
    <label class="block">
      <span class="mb-1 block text-sm font-medium text-gray-400">{{ t('accounts.locale') }}</span>
      <input
        v-model.trim="environment.locale"
        class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
        required
      />
    </label>
    <label class="block">
      <span class="mb-1 block text-sm font-medium text-gray-400">{{ t('accounts.timezone') }}</span>
      <input
        v-model.trim="environment.timezone"
        class="w-full rounded border border-line bg-canvas px-3 py-2 text-white transition focus:border-blue-500 focus:outline-none"
        required
      />
    </label>
  </div>
</template>
