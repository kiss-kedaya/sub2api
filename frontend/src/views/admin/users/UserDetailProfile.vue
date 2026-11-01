<template>
  <div v-if="user">
    <UPageCard :title="t('admin.users.detail.tabs.profile')" variant="naked" orientation="horizontal" class="mb-4">
      <div class="flex gap-2 lg:ms-auto">
        <UButton color="neutral" variant="outline" icon="i-lucide-users" :label="t('admin.users.groups')" @click="detail.openAllowedGroups()" />
        <UButton color="neutral" icon="i-lucide-pencil" :label="t('common.edit')" @click="detail.openEdit()" />
      </div>
    </UPageCard>
    <UPageCard variant="subtle">
      <template v-for="(row, index) in rows" :key="row.label">
        <USeparator v-if="index > 0" />
        <div class="flex items-start justify-between gap-4 max-sm:flex-col">
          <span class="text-sm font-medium text-highlighted">{{ row.label }}</span>
          <span class="text-sm text-default sm:text-right">{{ row.value }}</span>
        </div>
      </template>
    </UPageCard>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDateTime } from '@/utils/format'
import { useUserDetail } from './userDetailContext'

const { t } = useI18n()
const detail = useUserDetail()
const user = detail.user

const rows = computed(() => {
  const u = user.value
  if (!u) return []
  const when = (value?: string | null) => (value ? formatDateTime(value) : '-')
  return [
    { label: t('admin.users.columns.id'), value: String(u.id) },
    { label: t('admin.users.email'), value: u.email },
    { label: t('admin.users.username'), value: u.username || '-' },
    { label: t('admin.users.columns.role'), value: t('admin.users.roles.' + u.role) },
    { label: t('admin.users.columns.status'), value: u.status === 'active' ? t('common.active') : t('admin.users.disabled') },
    { label: t('admin.users.columns.concurrency'), value: `${u.current_concurrency ?? 0} / ${u.concurrency || '∞'}` },
    { label: t('admin.users.form.rpmLimit'), value: u.rpm_limit ? String(u.rpm_limit) : '∞' },
    { label: t('admin.users.notes'), value: u.notes || '-' },
    { label: t('admin.users.columns.created'), value: when(u.created_at) },
    { label: t('admin.users.columns.lastActive'), value: when(u.last_active_at) },
    { label: t('admin.users.columns.lastUsed'), value: when(u.last_used_at) }
  ]
})
</script>
