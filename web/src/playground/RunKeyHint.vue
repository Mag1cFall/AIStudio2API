<script setup lang="ts">
import UiIcon from '@/playground/ui/UiIcon.vue'
import { mac, phone, tablet } from './platform'
import { submitKey } from './shell/preferences'
</script>

<template>
  <template v-if="!phone && !tablet">
    <template v-if="submitKey === 'ctrl-enter'">
      <UiIcon v-if="mac" name="keyboardCommandKey" :size="15" class="command-key-icon" />
      <span v-else class="secondary-key">Ctrl</span>
    </template>
    <UiIcon name="keyboardReturn" :size="15" />
  </template>
</template>

<style scoped>
.secondary-key {
  font-size: 14px;
}
</style>
