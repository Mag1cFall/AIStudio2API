<script setup lang="ts">
import { computed, onActivated, ref } from 'vue'
import { api } from '@/api'
import { useI18n } from '@/i18n'
import { markdown } from '@/markdown'
import type { UpdateStatus } from '@/types'
import UiIcon from '@/components/UiIcon.vue'

const props = defineProps<{ status: UpdateStatus | null; activeRequests: number }>()
const emit = defineEmits<{ status: [UpdateStatus]; restarting: [] }>()
const { t, locale, errorText } = useI18n()
const checking = ref(false)
const confirming = ref(false)
const phase = ref<'idle' | 'installing' | 'restarting'>('idle')
const error = ref('')

const latest = computed(() => props.status?.latest ?? '')
const notes = computed(() => markdown.render(props.status?.notes ?? ''))
const publishedAt = computed(() =>
  props.status?.published_at
    ? new Date(props.status.published_at).toLocaleString(locale.value)
    : '',
)
const installable = computed(
  () => props.status?.mode === 'binary' && props.status.available && !props.status.updating,
)
const summary = computed(() => {
  if (phase.value === 'installing') return t('version.installing')
  if (phase.value === 'restarting') return t('version.restarting')
  if (props.status?.available) return t('version.available').replace('{version}', latest.value)
  if (latest.value) return t('version.upToDate')
  return ''
})
const confirmText = computed(() =>
  t('version.confirmText')
    .replace('{version}', latest.value)
    .replace('{count}', String(props.activeRequests)),
)

// check 读取 GitHub 最新发行版本，refresh 为 true 时跳过服务端缓存
async function check(refresh: boolean): Promise<void> {
  checking.value = true
  error.value = ''
  try {
    emit('status', await api.updateStatus(refresh))
  } catch (reason) {
    error.value = errorText(reason)
  } finally {
    checking.value = false
  }
}

// install 下载并替换为最新版本，成功后通知上层等待重启
async function install(): Promise<void> {
  confirming.value = false
  phase.value = 'installing'
  error.value = ''
  try {
    emit('status', await api.applyUpdate())
  } catch (reason) {
    phase.value = 'idle'
    error.value = errorText(reason)
    return
  }
  phase.value = 'restarting'
  emit('restarting')
}

onActivated(() => void check(false))
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col overflow-auto md:overflow-hidden">
    <header class="flex flex-wrap items-center gap-3 border-b border-line px-4 py-4 md:px-8">
      <div class="min-w-0 flex-1">
        <h2 class="text-xl font-semibold text-white">{{ t('section.version.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500">{{ t('section.version.description') }}</p>
      </div>
    </header>
    <div class="mx-auto flex min-h-0 w-full max-w-4xl flex-1 flex-col p-4 md:p-8">
      <article class="shrink-0 rounded-lg border border-line bg-panel p-4 md:p-6">
        <dl class="grid gap-4 sm:grid-cols-2">
          <div>
            <dt class="text-xs text-gray-500">{{ t('version.current') }}</dt>
            <dd class="mt-1 font-mono text-lg break-all text-white">
              {{ status?.current ?? '—' }}
            </dd>
          </div>
          <div v-if="status?.mode !== 'managed'">
            <dt class="text-xs text-gray-500">{{ t('version.latest') }}</dt>
            <dd class="mt-1 font-mono text-lg text-white">{{ latest || '—' }}</dd>
            <dd v-if="publishedAt" class="mt-0.5 text-xs text-gray-500">{{ publishedAt }}</dd>
          </div>
        </dl>

        <p
          v-if="summary"
          class="mt-4 text-sm"
          :class="status?.available || phase !== 'idle' ? 'text-blue-300' : 'text-gray-400'"
          role="status"
        >
          {{ summary }}
        </p>

        <div class="mt-4 flex flex-wrap items-center gap-2">
          <button
            v-if="status?.mode !== 'managed'"
            type="button"
            class="flex items-center gap-2 rounded border border-line bg-raised px-4 py-2 text-sm font-medium text-gray-200 transition hover:bg-line disabled:opacity-50"
            :disabled="checking || phase !== 'idle'"
            @click="check(true)"
          >
            <UiIcon :name="checking ? 'spinner' : 'refresh'" :size="15" />
            {{ t('version.check') }}
          </button>
          <button
            v-if="installable && !confirming"
            type="button"
            class="flex items-center gap-2 rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-500 disabled:opacity-50"
            :disabled="phase !== 'idle'"
            @click="confirming = true"
          >
            <UiIcon :name="phase === 'idle' ? 'download' : 'spinner'" :size="15" />
            {{ t('version.install').replace('{version}', latest) }}
          </button>
          <a
            v-if="status?.url"
            :href="status.url"
            target="_blank"
            rel="noreferrer"
            class="px-2 py-2 text-sm text-blue-400 hover:underline"
          >
            {{ t('version.release') }}
          </a>
        </div>

        <div
          v-if="confirming"
          role="alert"
          class="mt-4 rounded-md border border-amber-500/40 bg-amber-500/10 p-4 text-sm text-amber-200"
        >
          <p>{{ confirmText }}</p>
          <div class="mt-3 flex flex-wrap gap-2">
            <button
              type="button"
              class="rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-500"
              @click="install"
            >
              {{ t('version.confirm') }}
            </button>
            <button
              type="button"
              class="rounded border border-line bg-raised px-4 py-2 text-sm font-medium text-gray-200 transition hover:bg-line"
              @click="confirming = false"
            >
              {{ t('common.cancel') }}
            </button>
          </div>
        </div>

        <div v-if="status?.mode === 'container'" class="mt-4 text-sm text-gray-400">
          <p>{{ t('version.container') }}</p>
          <code
            class="mt-2 block overflow-x-auto rounded border border-line bg-canvas px-3 py-2 font-mono text-xs text-gray-200"
            >docker compose pull &amp;&amp; docker compose up -d</code
          >
        </div>
        <p v-else-if="status?.mode === 'development'" class="mt-4 text-sm text-gray-400">
          {{ t('version.development') }}
        </p>
        <p v-else-if="status?.mode === 'managed'" class="mt-4 text-sm text-gray-400">
          {{ t('version.managed').replaceAll('{name}', status.managed_by ?? '') }}
        </p>
        <p v-else-if="status?.mode === 'download'" class="mt-4 text-sm text-gray-400">
          {{ t('version.download') }}
        </p>

        <p
          v-if="error || status?.error"
          role="alert"
          class="mt-4 rounded border border-red-500/40 bg-red-500/10 p-3 text-sm break-words text-red-300"
        >
          {{ error || status?.error }}
        </p>
      </article>

      <article
        v-if="status?.notes"
        class="mt-6 flex min-h-0 flex-1 flex-col rounded-lg border border-line bg-panel p-4 md:p-6"
      >
        <h3 class="font-bold text-gray-300">
          {{ t('version.notes').replace('{version}', latest) }}
        </h3>
        <div
          class="markdown mt-3 min-h-0 flex-1 text-sm leading-6 [overflow-wrap:anywhere] text-gray-300 md:overflow-y-auto"
          v-html="notes"
        />
      </article>
    </div>
  </section>
</template>
