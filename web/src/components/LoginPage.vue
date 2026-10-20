<script setup lang="ts">
import { ref } from 'vue'
import { api, type AdminSession } from '@/api'
import { useI18n } from '@/i18n'
import ThemeToggle from './ThemeToggle.vue'

const emit = defineEmits<{ authenticated: [session: AdminSession] }>()
const { t, errorText } = useI18n()
const username = ref('')
const password = ref('')
const remember = ref(false)
const busy = ref(false)
const error = ref('')

// login 提交管理员凭据并清理密码输入
async function login(): Promise<void> {
  busy.value = true
  error.value = ''
  try {
    emit('authenticated', await api.login(username.value, password.value, remember.value))
  } catch (cause) {
    error.value = errorText(cause)
  } finally {
    password.value = ''
    busy.value = false
  }
}
</script>

<template>
  <main class="flex h-full items-center justify-center overflow-auto bg-canvas p-6">
    <form
      class="w-full max-w-sm space-y-5 rounded-lg border border-line bg-panel p-7"
      @submit.prevent="login"
    >
      <div class="flex items-center justify-between gap-2">
        <h1 class="text-xl font-semibold text-white">AIStudio2API</h1>
        <ThemeToggle />
      </div>
      <label class="block">
        <span class="mb-2 block text-sm text-gray-300">{{ t('auth.username') }}</span>
        <input
          v-model.trim="username"
          name="username"
          autocomplete="username"
          autofocus
          required
          class="w-full rounded border border-line bg-canvas px-3 py-2 text-white focus:border-blue-500 focus:outline-none"
        />
      </label>
      <label class="block">
        <span class="mb-2 block text-sm text-gray-300">{{ t('auth.password') }}</span>
        <input
          v-model="password"
          name="password"
          type="password"
          autocomplete="current-password"
          required
          class="w-full rounded border border-line bg-canvas px-3 py-2 text-white focus:border-blue-500 focus:outline-none"
        />
      </label>
      <label class="flex w-fit cursor-pointer items-center gap-2 text-sm text-gray-300">
        <input v-model="remember" type="checkbox" name="remember" class="size-4 accent-blue-600" />
        {{ t('auth.remember') }}
      </label>
      <p v-if="error" role="alert" class="text-sm text-red-300">{{ error }}</p>
      <button
        :disabled="busy"
        type="submit"
        class="w-full rounded bg-blue-600 px-3 py-2 font-medium text-white transition hover:bg-blue-500 disabled:opacity-50"
      >
        {{ busy ? t('common.loading') : t('auth.login') }}
      </button>
    </form>
  </main>
</template>
