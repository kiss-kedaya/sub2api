<template>
  <div v-if="user" class="space-y-4">
    <UPageCard :title="t('admin.users.detail.tabs.subscriptions')" variant="naked" />
    <p v-if="!loading && subscriptions.length === 0" class="py-10 text-center text-sm text-muted">{{ t('admin.users.detail.noSubscriptions') }}</p>
    <UTable v-else :data="subscriptions" :columns="columns" :loading="loading" class="rounded-lg border border-default">
      <template #group-cell="{ row }">
        <GroupBadge
          v-if="row.original.group"
          :name="row.original.group.name"
          :platform="row.original.group.platform"
          :subscription-type="row.original.group.subscription_type"
          :rate-multiplier="row.original.group.rate_multiplier"
        />
        <span v-else class="text-dimmed">#{{ row.original.group_id }}</span>
      </template>
      <template #status-cell="{ row }">
        <UBadge :color="statusColor(row.original.status)" variant="subtle" :label="t('admin.subscriptions.status.' + row.original.status)" />
      </template>
      <template #usage-cell="{ row }">
        <span class="text-xs tabular-nums text-muted">
          ${{ row.original.daily_usage_usd.toFixed(2) }} / ${{ row.original.weekly_usage_usd.toFixed(2) }} / ${{ row.original.monthly_usage_usd.toFixed(2) }}
        </span>
      </template>
      <template #expires_at-cell="{ row }">
        <span class="text-muted">{{ row.original.expires_at ? formatDateTime(row.original.expires_at) : '-' }}</span>
      </template>
    </UTable>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import { adminAPI } from '@/api/admin'
import type { UserSubscription } from '@/types'
import { formatDateTime } from '@/utils/format'
import GroupBadge from '@/components/common/GroupBadge.vue'
import { useUserDetail } from './userDetailContext'

const { t } = useI18n()
const detail = useUserDetail()
const user = detail.user

const subscriptions = ref<UserSubscription[]>([])
const loading = ref(false)

const columns = computed<TableColumn<UserSubscription>[]>(() => [
  { id: 'group', header: t('admin.subscriptions.columns.group') },
  { accessorKey: 'status', header: t('admin.subscriptions.columns.status') },
  { id: 'usage', header: t('admin.subscriptions.columns.usage') },
  { accessorKey: 'expires_at', header: t('admin.subscriptions.columns.expires') }
])

const statusColor = (status: UserSubscription['status']): 'success' | 'error' | 'warning' | 'neutral' => {
  if (status === 'active') return 'success'
  if (status === 'suspended') return 'warning'
  if (status === 'revoked') return 'error'
  return 'neutral'
}

let seq = 0
watch([() => user.value?.id, detail.version], async () => {
  const id = user.value?.id
  if (!id) return
  const current = ++seq
  loading.value = true
  try {
    const res = await adminAPI.subscriptions.listByUser(id, 1, 100)
    if (current === seq) subscriptions.value = res.items || []
  } catch {
    if (current === seq) subscriptions.value = []
  } finally {
    if (current === seq) loading.value = false
  }
}, { immediate: true })
</script>
