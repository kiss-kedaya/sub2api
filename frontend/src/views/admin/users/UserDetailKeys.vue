<template>
  <div v-if="user" class="space-y-4">
    <UPageCard :title="t('admin.users.detail.tabs.keys')" variant="naked" orientation="horizontal">
      <UButton color="neutral" variant="outline" icon="i-lucide-settings-2" class="w-fit lg:ms-auto" :label="t('admin.users.detail.manageKeys')" @click="detail.openApiKeys()" />
    </UPageCard>
    <UTable :data="keys" :columns="columns" :loading="loading" class="rounded-lg border border-default">
      <template #name-cell="{ row }">
        <span class="font-medium text-highlighted">{{ row.original.name }}</span>
      </template>
      <template #key-cell="{ row }">
        <code class="font-mono text-xs text-muted">{{ row.original.key.substring(0, 12) }}…{{ row.original.key.slice(-6) }}</code>
      </template>
      <template #group-cell="{ row }">
        <GroupBadge v-if="row.original.group" :name="row.original.group.name" :platform="row.original.group.platform" :rate-multiplier="row.original.group.rate_multiplier" />
        <span v-else class="text-dimmed">-</span>
      </template>
      <template #status-cell="{ row }">
        <UBadge :color="row.original.status === 'active' ? 'success' : 'error'" variant="subtle" :label="row.original.status" />
      </template>
      <template #created_at-cell="{ row }">
        <span class="text-muted">{{ formatDateTime(row.original.created_at) }}</span>
      </template>
    </UTable>
    <Pagination v-if="total > pageSize" :page="page" :total="total" :page-size="pageSize" @update:page="(p: number) => { page = p }" @update:page-size="(s: number) => { pageSize = s; page = 1 }" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import { adminAPI } from '@/api/admin'
import type { ApiKey } from '@/types'
import { formatDateTime } from '@/utils/format'
import GroupBadge from '@/components/common/GroupBadge.vue'
import Pagination from '@/components/common/Pagination.vue'
import { useUserDetail } from './userDetailContext'

const { t } = useI18n()
const detail = useUserDetail()
const user = detail.user

const keys = ref<ApiKey[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

const columns = computed<TableColumn<ApiKey>[]>(() => [
  { accessorKey: 'name', header: t('keys.nameLabel') },
  { accessorKey: 'key', header: t('keys.apiKey') },
  { id: 'group', header: t('keys.group') },
  { accessorKey: 'status', header: t('admin.users.columns.status') },
  { accessorKey: 'created_at', header: t('keys.created') }
])

let seq = 0
async function load() {
  const id = user.value?.id
  if (!id) return
  const current = ++seq
  loading.value = true
  try {
    const res = await adminAPI.users.getUserApiKeys(id, page.value, pageSize.value)
    if (current !== seq) return
    keys.value = res.items || []
    total.value = res.total
  } finally {
    if (current === seq) loading.value = false
  }
}

watch([() => user.value?.id, detail.version, page, pageSize], () => { void load() }, { immediate: true })
</script>
