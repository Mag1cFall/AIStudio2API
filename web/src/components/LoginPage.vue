<script setup lang="ts">
import { ref } from 'vue'
import { api, type AdminSession } from '@/api'
import { useI18n } from '@/i18n'
import { useTheme } from '@/theme'
import UiIcon from './UiIcon.vue'

const emit = defineEmits<{ authenticated: [session: AdminSession] }>()
const { t, errorText } = useI18n()
const { theme, toggleTheme } = useTheme()
const username = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')

// login 提交管理员凭据并清理密码输入
async function login(): Promise<void> {
  busy.value = true
  error.value = ''
  try {
    emit('authenticated', await api.login(username.value, password.value))
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
        <h1 class="text-xl font-semibold text-white">AI Studio Proxy</h1>
        <button
          v-tooltip="t(theme === 'dark' ? 'app.lightMode' : 'app.darkMode')"
          type="button"
          :aria-label="t(theme === 'dark' ? 'app.lightMode' : 'app.darkMode')"
          class="flex h-6 w-6 items-center justify-center rounded border border-gray-700 text-gray-500 transition hover:text-white"
          @click="toggleTheme"
        >
          <UiIcon :name="theme === 'dark' ? 'sun' : 'moon'" :size="13" />
        </button>
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
