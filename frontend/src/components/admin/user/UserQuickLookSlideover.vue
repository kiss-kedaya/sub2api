<template>
  <!-- Quick look from the users list (row click); the full detail lives at /admin/users/:id -->
  <USlideover
    :open="open"
    :title="t('admin.users.detail.quickLook')"
    :description="user?.email"
    @update:open="(value: boolean) => emit('update:open', value)"
  >
    <template #body>
      <div v-if="user" class="space-y-6">
        <div class="flex items-center gap-3">
          <UAvatar :alt="user.email" :text="user.email.charAt(0).toUpperCase()" size="lg" />
          <div class="min-w-0 flex-1">
            <p class="truncate font-semibold text-highlighted">{{ user.email }}</p>
            <p class="text-sm text-muted">#{{ user.id }}<template v-if="user.username"> · {{ user.username }}</template></p>
          </div>
          <div class="flex shrink-0 gap-1.5">
            <UBadge :color="user.role === 'admin' ? 'primary' : 'neutral'" variant="subtle" :label="t('admin.users.roles.' + user.role)" />
            <UBadge :color="user.status === 'active' ? 'success' : 'error'" variant="subtle" :label="user.status === 'active' ? t('common.active') : t('admin.users.disabled')" />
          </div>
        </div>

        <dl class="divide-y divide-default border-y border-default text-sm">
          <div v-for="row in rows" :key="row.label" class="flex items-center justify-between gap-4 py-2.5">
            <dt class="text-muted">{{ row.label }}</dt>
            <dd class="truncate text-right text-default" :class="row.strong ? 'font-semibold tabular-nums text-highlighted' : ''">{{ row.value }}</dd>
          </div>
        </dl>

        <div class="grid grid-cols-2 gap-2">
          <UButton color="neutral" variant="outline" icon="i-lucide-circle-plus" :label="t('admin.users.deposit')" @click="emit('deposit')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-circle-minus" :label="t('admin.users.withdraw')" @click="emit('withdraw')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-key-round" :label="t('admin.users.apiKeys')" @click="emit('keys')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-users" :label="t('admin.users.groups')" @click="emit('groups')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-history" :label="t('admin.users.balanceHistory')" @click="emit('history')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-pencil" :label="t('common.edit')" @click="emit('edit')" />
        </div>
      </div>
    </template>
    <template #footer>
      <UButton
        v-if="user"
        block
        trailing-icon="i-lucide-arrow-right"
        :to="`/admin/users/${user.id}/profile`"
        :label="t('admin.users.detail.openDetail')"
      />
    </template>
  </USlideover>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminUser } from '@/types'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{
  open: boolean
  user: AdminUser | null
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  edit: []
  deposit: []
  withdraw: []
  keys: []
  groups: []
  history: []
}>()

const { t } = useI18n()

const rows = computed(() => {
  const u = props.user
  if (!u) return []
  const when = (value?: string | null) => (value ? formatDateTime(value) : '-')
  return [
    { label: t('admin.users.currentBalance'), value: `$${(u.balance ?? 0).toFixed(2)}`, strong: true },
    { label: t('admin.users.columns.concurrency'), value: `${u.current_concurrency ?? 0} / ${u.concurrency || '∞'}`, strong: false },
    { label: t('admin.users.columns.lastActive'), value: when(u.last_active_at), strong: false },
    { label: t('admin.users.columns.lastUsed'), value: when(u.last_used_at), strong: false },
    { label: t('admin.users.columns.created'), value: when(u.created_at), strong: false },
    { label: t('admin.users.notes'), value: u.notes || '-', strong: false }
  ]
})
</script>
