<template>
  <BaseDialog
    :show="show"
    :title="t('admin.channelMonitor.form.selectKeyTitle')"
    width="wide"
    @close="$emit('close')"
  >
    <div class="flex flex-col gap-3">
      <p class="text-xs text-muted">
        {{ t('admin.channelMonitor.form.selectKeyHint') }}
      </p>

      <UInput v-model="search" icon="i-lucide-search" class="w-full" :placeholder="t('keys.searchPlaceholder')" :aria-label="t('keys.searchPlaceholder')" />

      <UTable
        :data="filteredKeys"
        :columns="keyColumns"
        :loading="loading"
        :empty="loading ? t('common.loading') : t('admin.channelMonitor.form.noActiveKey')"
        sticky
        class="max-h-96"
        :ui="{ ...tableUi, tr: 'cursor-pointer hover:bg-elevated/50' }"
        @select="pickRow"
      >
        <template #name-cell="{ row }"><span class="font-medium text-highlighted">{{ row.original.name }}</span></template>
        <template #key-cell="{ row }"><span class="font-mono text-xs text-muted">{{ maskApiKey(row.original.key) }}</span></template>
        <template #group-cell="{ row }">
          <GroupBadge
            v-if="row.original.group"
            :name="row.original.group.name"
            :platform="row.original.group.platform"
            :subscription-type="row.original.group.subscription_type"
            :rate-multiplier="row.original.group.rate_multiplier"
            :user-rate-multiplier="userGroupRates[row.original.group.id]"
          />
          <span v-else class="text-xs text-dimmed">—</span>
        </template>
      </UTable>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <UButton color="neutral" variant="outline" :label="t('common.cancel')" @click="$emit('close')" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ApiKey } from '@/types'
import type { Provider } from '@/api/admin/channelMonitor'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import { maskApiKey } from '@/utils/maskApiKey'
import type { TableColumn, TableRow } from '@nuxt/ui'
import { compactTableUi } from '@/views/admin/ops/utils/templateUi'

const props = withDefaults(defineProps<{
  show: boolean
  loading: boolean
  keys: ApiKey[]
  provider: Provider
  userGroupRates?: Record<number, number>
}>(), {
  userGroupRates: () => ({}),
})

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'pick', key: ApiKey): void
}>()

const { t } = useI18n()
const tableUi = compactTableUi
const keyColumns = computed<TableColumn<ApiKey>[]>(() => [
  { id: 'name', header: t('common.name') },
  { id: 'key', header: t('keys.apiKey') },
  { id: 'group', header: t('keys.group') },
])
function pickRow(_e: Event, row: TableRow<ApiKey>) {
  emit('pick', row.original)
}

const search = ref('')

watch(() => props.show, (shown) => {
  if (!shown) search.value = ''
})

const filteredKeys = computed<ApiKey[]>(() => {
  const q = search.value.trim().toLowerCase()
  return props.keys.filter((k) => {
    if (k.group?.platform !== props.provider) return false
    if (!q) return true
    return (
      k.name.toLowerCase().includes(q) ||
      k.key.toLowerCase().includes(q) ||
      (k.group?.name || '').toLowerCase().includes(q)
    )
  })
})
</script>
