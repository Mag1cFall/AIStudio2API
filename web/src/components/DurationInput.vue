<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from '@/i18n'
import UiSelect from './UiSelect.vue'

const props = defineProps<{ id: string; inputClass: string }>()
const model = defineModel<string>({ required: true })

const { t } = useI18n()
const units: Record<string, number> = {
  h: 3600,
  m: 60,
  s: 1,
  ms: 1e-3,
  us: 1e-6,
  µs: 1e-6,
  ns: 1e-9,
}

// seconds 把 Go 时长（如 2m0s、1h30m、500ms）换算为秒
function seconds(value: string): number {
  return [...value.matchAll(/(\d+(?:\.\d+)?)(ms|us|µs|ns|h|m|s)/g)].reduce(
    (total, [, amount, unit]) => total + Number(amount) * units[unit!]!,
    0,
  )
}

// unit 是编辑时使用的单位，初始为整分钟时用分钟，否则用秒
const unit = ref<'m' | 's'>(seconds(model.value) > 0 && seconds(model.value) % 60 === 0 ? 'm' : 's')

// write 以数值与单位写回 Go 时长
function write(value: number): void {
  model.value = `${value}${unit.value}`
}

// changeUnit 切换单位并按新单位写回同一时长
function changeUnit(next: 'm' | 's'): void {
  const total = seconds(model.value)
  unit.value = next
  write(next === 'm' ? total / 60 : total)
}
</script>

<template>
  <div class="flex gap-2">
    <div class="min-w-0 flex-1">
      <input
        :id="props.id"
        type="number"
        min="1"
        step="1"
        :value="seconds(model) / units[unit]!"
        :class="inputClass"
        @change="write(Number(($event.target as HTMLInputElement).value))"
      />
    </div>
    <div class="w-28 shrink-0">
      <UiSelect
        :model-value="unit"
        :class="inputClass"
        :aria-label="t('settings.timeUnit')"
        @update:model-value="changeUnit($event as 'm' | 's')"
      >
        <option value="s">{{ t('settings.unitSeconds') }}</option>
        <option value="m">{{ t('settings.unitMinutes') }}</option>
      </UiSelect>
    </div>
  </div>
</template>
