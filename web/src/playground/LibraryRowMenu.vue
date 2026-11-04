<script setup lang="ts">
import { DropdownMenuItem } from 'reka-ui'
import AnchoredMenu from '@/components/AnchoredMenu.vue'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'

// LibraryRowMenu 是历史页各行共用的更多选项菜单
const open = defineModel<boolean>('open', { required: true })
defineProps<{ anchor: HTMLElement | undefined }>()
const emit = defineEmits<{ rename: []; copy: []; remove: [] }>()
const { t } = useI18n()
</script>

<template>
  <AnchoredMenu
    v-model:open="open"
    :anchor="anchor"
    align="end"
    :side-offset="4"
    class="ms-menu-panel"
  >
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
  </AnchoredMenu>
</template>
