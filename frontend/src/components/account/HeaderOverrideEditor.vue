<template>
  <div v-if="rows.length > 0" class="space-y-2">
    <div
      v-for="(row, index) in rows"
      :key="getHeaderOverrideRowKey(row)"
      class="flex items-center gap-2"
    >
      <UInput
        v-model="row.name"
        class="flex-1"
        :placeholder="t('admin.accounts.headerOverride.namePlaceholder')"
      />
      <UInput
        v-model="row.value"
        class="flex-1"
        :placeholder="t('admin.accounts.headerOverride.valuePlaceholder')"
      />
      <UButton color="error" variant="ghost" icon="i-lucide-trash-2" square :aria-label="t('common.delete')" @click="removeRow(index)" />
    </div>
  </div>

  <UButton color="neutral" variant="outline" icon="i-lucide-plus" block :label="t('admin.accounts.headerOverride.addRow')" @click="addRow" />

  <div class="flex flex-wrap gap-2">
    <HeaderOverrideJsonTools :rows="rows" @update:rows="emit('update:rows', $event)" />
  </div>

  <p class="text-xs text-gray-500 dark:text-gray-400">
    {{ t('admin.accounts.headerOverride.emptyValueHint') }}
  </p>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { createStableObjectKeyResolver } from '@/utils/stableObjectKey'
import HeaderOverrideJsonTools from './HeaderOverrideJsonTools.vue'
import type { HeaderOverrideRow } from './credentialsBuilder'

const props = defineProps<{
  rows: HeaderOverrideRow[]
}>()

const emit = defineEmits<{
  (e: 'update:rows', rows: HeaderOverrideRow[]): void
}>()

const { t } = useI18n()

const getHeaderOverrideRowKey = createStableObjectKeyResolver<HeaderOverrideRow>(
  'header-override-row'
)

const addRow = () => {
  emit('update:rows', [...props.rows, { name: '', value: '' }])
}

const removeRow = (index: number) => {
  emit('update:rows', props.rows.filter((_, i) => i !== index))
}
</script>
