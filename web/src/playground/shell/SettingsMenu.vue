<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { ref } from 'vue'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from 'reka-ui'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UserSettingsDialog from './UserSettingsDialog.vue'

defineProps<{ adminUsername: string }>()
const emit = defineEmits<{ logout: [] }>()
const { t } = useI18n()
const userSettingsOpen = ref(false)
</script>

<template>
  <DropdownMenuRoot :modal="false">
    <DropdownMenuTrigger
      v-tooltip="t('nav.settings')"
      ms-button
      class="trigger-button ms-button-borderless ms-button-icon"
      :aria-label="t('nav.settings')"
    >
      <UiIcon name="settings" :size="20" />
    </DropdownMenuTrigger>
    <DropdownMenuPortal>
      <DropdownMenuContent
        side="top"
        align="start"
        :side-offset="6"
        class="ms-menu-panel settings-menu"
      >
        <DropdownMenuItem class="ms-menu-item" @select="userSettingsOpen = true">
          <UiIcon name="settings" :size="18" />
          <span class="ms-menu-item-text">{{ t('pg.userSettings') }}</span>
        </DropdownMenuItem>
        <template v-if="adminUsername">
          <DropdownMenuSeparator class="ms-menu-divider" />
          <DropdownMenuItem class="ms-menu-item" @select="emit('logout')">
            <UiIcon name="logout" :size="18" />
            <span class="ms-menu-item-text">{{ t('auth.logout') }}</span>
          </DropdownMenuItem>
        </template>
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
  <UserSettingsDialog v-model:open="userSettingsOpen" />
</template>

<style>
.settings-menu {
  min-width: 281px;
}
</style>
