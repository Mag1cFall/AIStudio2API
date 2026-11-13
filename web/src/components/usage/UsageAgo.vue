<script setup lang="ts">
import { inject } from 'vue'
import { usageClock, useUsageFormat } from './format'

// UsageAgo 显示距今的相对时间，随用量页时钟每秒重绘
defineProps<{ at: string }>()
const now = inject(usageClock)!
const { ago } = useUsageFormat()
</script>

<template>{{ ago(at, now) }}</template>
