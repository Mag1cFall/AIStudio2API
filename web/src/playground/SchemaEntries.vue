<script setup lang="ts">
import { vTooltip } from '@/playground/ui/tooltip'
import { useI18n } from '@/i18n'
import UiIcon from '@/playground/ui/UiIcon.vue'
import UiSelect from '@/playground/ui/UiSelect.vue'
import { newEntry, schemaTypes, type SchemaEntry, type SchemaType } from './schema'

defineOptions({ name: 'SchemaEntries' })

const entries = defineModel<SchemaEntry[]>('entries', { required: true })
const props = defineProps<{ path: string }>()
const emit = defineEmits<{ change: [] }>()
const { t } = useI18n()

// setType 修改属性类型，离开 object 与 enum 时清除子属性与枚举值
function setType(item: SchemaEntry, type: SchemaType): void {
  item.type = type
  if (type !== 'object') delete item.children
  if (type !== 'enum') delete item.values
  emit('change')
}

// remove 删除第 index 个属性
function remove(index: number): void {
  entries.value.splice(index, 1)
  emit('change')
}

// addChild 为 object 属性添加嵌套属性
function addChild(item: SchemaEntry): void {
  item.children = [...(item.children ?? []), newEntry()]
  emit('change')
}

// addValue 为 enum 属性添加枚举值
function addValue(item: SchemaEntry): void {
  item.values = [...(item.values ?? []), '']
  emit('change')
}

// removeValue 删除枚举值
function removeValue(item: SchemaEntry, index: number): void {
  item.values?.splice(index, 1)
  emit('change')
}
</script>

<template>
  <template v-for="(item, index) in entries" :key="index">
    <div class="entry">
      <div class="entry-key">
        <label class="entry-key-label" :for="`property-name-${props.path}-${index}`">{{
          t('pg.property')
        }}</label>
        <input
          :id="`property-name-${props.path}-${index}`"
          v-model="item.key"
          ms-input
          :aria-label="t('pg.propertyName')"
          @input="emit('change')"
        />
      </div>
      <div v-tooltip="t('pg.propertyTypeTip')" class="entry-type">
        <UiSelect
          :model-value="item.type"
          class="w-full"
          :aria-label="t('pg.propertyType')"
          @update:model-value="setType(item, $event as SchemaType)"
        >
          <option v-for="type in schemaTypes" :key="type" :value="type">{{ type }}</option>
        </UiSelect>
      </div>
      <button
        v-tooltip.below="item.isArray ? t('pg.makeSingle') : t('pg.makeArray')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :class="{ 'ms-button-active': item.isArray }"
        :aria-label="t('pg.toggleArray')"
        :aria-pressed="item.isArray === true"
        @click="((item.isArray = !item.isArray), emit('change'))"
      >
        <UiIcon name="dataArray" :size="20" />
      </button>
      <button
        v-tooltip.below="item.isRequired ? t('pg.markOptional') : t('pg.markRequired')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :class="{ 'ms-button-active': item.isRequired }"
        :aria-label="t('pg.toggleRequired')"
        :aria-pressed="item.isRequired"
        @click="((item.isRequired = !item.isRequired), emit('change'))"
      >
        <UiIcon name="asterisk" :size="20" />
      </button>
      <button
        v-tooltip.below="t('pg.deleteProperty')"
        type="button"
        ms-button
        class="ms-button-borderless ms-button-icon"
        :aria-label="t('pg.deleteProperty')"
        @click="remove(index)"
      >
        <UiIcon name="delete" :size="20" />
      </button>
    </div>
    <div v-if="item.type === 'object'" class="entry-children">
      <SchemaEntries
        v-if="item.children"
        v-model:entries="item.children"
        :path="`${props.path}-${index}`"
        @change="emit('change')"
      />
      <button type="button" ms-button class="ms-button-borderless" @click="addChild(item)">
        {{ t('pg.addNestedProperty') }}
      </button>
    </div>
    <div v-else-if="item.type === 'enum'" class="entry-children">
      <div v-for="(_, valueIndex) in item.values ?? []" :key="valueIndex" class="entry-enum">
        <div class="enum-value">
          <input
            v-model="item.values![valueIndex]"
            v-tooltip="t('pg.enumValue')"
            ms-input
            :aria-label="t('pg.enumValue')"
            @input="emit('change')"
          />
        </div>
        <div class="button-container">
          <button
            type="button"
            ms-button
            class="ms-button-borderless ms-button-icon"
            :aria-label="t('common.delete')"
            @click="removeValue(item, valueIndex)"
          >
            <UiIcon name="delete" :size="20" />
          </button>
        </div>
      </div>
      <button type="button" ms-button class="ms-button-borderless" @click="addValue(item)">
        {{ t('pg.addEnumValue') }}
      </button>
    </div>
  </template>
</template>

<style scoped>
.entry {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 4px 12px;
  margin-bottom: 8px;
}

.entry-key,
.entry-type,
.enum-value {
  flex: 1;
  min-width: 0;
}

.entry-key-label {
  display: block;
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
}

.entry-children {
  padding-left: 20px;
}

.entry-enum {
  display: flex;
  align-items: flex-end;
  gap: 12px;
  margin-bottom: 8px;
}

.entry-enum .button-container {
  flex: 1;
  min-width: 0;
}
</style>
