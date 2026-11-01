<template>
  <div v-if="user" class="space-y-6">
    <UPageCard :title="t('admin.users.detail.usageTitle')" variant="naked" orientation="horizontal">
      <UButton color="neutral" variant="outline" trailing-icon="i-lucide-arrow-right" class="w-fit lg:ms-auto" to="/admin/usage" :label="t('admin.users.detail.viewAllUsage')" />
    </UPageCard>
    <UPageGrid class="gap-4 sm:grid-cols-3">
      <UPageCard v-for="stat in stats" :key="stat.title" :title="stat.title" :icon="stat.icon" variant="subtle" :ui="statUi">
        <p class="text-2xl font-semibold tabular-nums text-highlighted">{{ stat.value }}</p>
      </UPageCard>
    </UPageGrid>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useUserDetail } from './userDetailContext'

const { t } = useI18n()
const detail = useUserDetail()
const user = detail.user

const usage = ref<{ total_requests: number; total_cost: number; total_tokens: number } | null>(null)

const statUi = {
  container: 'gap-y-1.5',
  wrapper: 'items-start',
  leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
  title: 'font-normal text-muted text-xs uppercase'
}

const stats = computed(() => {
  const u = usage.value
  return [
    { title: t('admin.users.detail.requests'), icon: 'i-lucide-activity', value: u ? u.total_requests.toLocaleString() : '-' },
    { title: t('admin.users.detail.cost'), icon: 'i-lucide-circle-dollar-sign', value: u ? `$${u.total_cost.toFixed(2)}` : '-' },
    { title: t('admin.users.detail.tokens'), icon: 'i-lucide-hash', value: u ? u.total_tokens.toLocaleString() : '-' }
  ]
})

let seq = 0
watch([() => user.value?.id, detail.version], async () => {
  const id = user.value?.id
  if (!id) return
  const current = ++seq
  try {
    const res = await adminAPI.users.getUserUsageStats(id)
    if (current === seq) usage.value = res
  } catch {
    if (current === seq) usage.value = null
  }
}, { immediate: true })
</script>
