<script setup lang="ts">
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'

const emit = defineEmits<{ rename: []; copy: []; remove: [] }>()
const { t } = useI18n()
</script>

<template>
  <DropdownMenuRoot :modal="false">
    <DropdownMenuTrigger
      ms-button
      class="ms-button-borderless ms-button-icon"
      :aria-label="t('pg.moreOptions')"
    >
      <UiIcon name="more" :size="20" />
    </DropdownMenuTrigger>
    <DropdownMenuPortal>
      <DropdownMenuContent align="end" :side-offset="4" class="ms-menu-panel">
        <DropdownMenuItem class="ms-menu-item" @select="emit('rename')">
          <UiIcon name="edit" :size="18" />
          <span class="ms-menu-item-text">{{ t('pg.rename') }}</span>
        </DropdownMenuItem>
        <DropdownMenuItem class="ms-menu-item" disabled>
          <UiIcon name="share" :size="18" />
          <span class="ms-menu-item-text">{{ t('pg.share') }}</span>
        </DropdownMenuItem>
        <DropdownMenuItem class="ms-menu-item" @select="emit('copy')">
          <UiIcon name="copy" :size="18" />
          <span class="ms-menu-item-text">{{ t('pg.makeCopy') }}</span>
        </DropdownMenuItem>
        <DropdownMenuItem class="ms-menu-item" @select="emit('remove')">
          <UiIcon name="delete" :size="18" />
          <span class="ms-menu-item-text">{{ t('common.delete') }}</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>
