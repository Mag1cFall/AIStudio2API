<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { confirmState, settleConfirm } from '@/confirm'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const cancelButton = ref<HTMLButtonElement>()

watch(
  () => confirmState.open,
  async (open) => {
    if (!open) return
    await nextTick()
    cancelButton.value?.focus()
  },
)
</script>

<template>
  <Transition name="dialog">
    <div
      v-if="confirmState.open"
      class="modal-overlay"
      role="alertdialog"
      aria-modal="true"
      @click.self="settleConfirm(false)"
      @keydown.esc="settleConfirm(false)"
    >
      <div
        class="mx-4 w-full max-w-sm rounded-lg border border-[#30363d] bg-[#161b22] p-5 shadow-xl"
      >
        <p class="text-sm text-gray-200">{{ confirmState.message }}</p>
        <div class="mt-5 flex justify-end gap-2">
          <button
            ref="cancelButton"
            class="rounded bg-[#21262d] px-4 py-1.5 text-sm text-gray-300 transition hover:bg-[#30363d]"
            type="button"
            @click="settleConfirm(false)"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            class="rounded bg-red-600 px-4 py-1.5 text-sm text-white transition hover:bg-red-500"
            type="button"
            @click="settleConfirm(true)"
          >
            {{ confirmState.action }}
          </button>
        </div>
      </div>
    </div>
  </Transition>
</template>
