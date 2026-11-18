<template>
  <AppLayout>
    <div class="ops-center min-w-0 space-y-6">
      <AdminPageHeader :title="t('opsCenter.title')" :description="t('opsCenter.description')">
        <template #actions>
          <UButton v-if="navigation.length" to="/admin/account-ops" color="neutral" variant="outline" :label="t('accountOps.title')" />
          <UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :loading="loading" :label="t('common.refresh')" @click="refresh" />
        </template>
      </AdminPageHeader>
      <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" role="alert" />
      <UAlert v-if="observer" color="neutral" variant="subtle" :title="t('opsCenter.observer')" />

      <section class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4" :aria-label="t('opsCenter.areas.overview')" :aria-busy="loading">
        <UPageCard variant="subtle" :title="t('opsCenter.balance')" :ui="statUi">
          <div class="space-y-0.5 text-2xl font-semibold tabular-nums text-highlighted">
            <p v-for="total in balanceTotals.slice(0, 2)" :key="total.unit">{{ formatMoney(total.total, total.unit) }}</p>
            <p v-if="!balanceTotals.length">—</p>
          </div>
          <p v-if="balanceTotals.length > 2" class="text-xs text-muted">{{ t('opsCenter.currencies', { count: balanceTotals.length - 2 }) }}</p>
          <p class="text-xs text-muted">{{ t('opsCenter.balanceSnapshots', { count: accountsWithBalance }) }}</p>
        </UPageCard>
        <UPageCard variant="subtle" :title="t('opsCenter.spend')" :ui="statUi">
          <p class="text-2xl font-semibold tabular-nums text-highlighted">${{ formatCost(stats?.today_actual_cost) }}</p>
          <p class="text-xs text-muted">{{ t('opsCenter.requests') }} {{ stats?.today_requests ?? 0 }}</p>
        </UPageCard>
        <UPageCard variant="subtle" :title="t('opsCenter.health')" :ui="statUi">
          <p class="text-2xl font-semibold tabular-nums text-highlighted">{{ stats?.normal_accounts ?? 0 }} <span class="text-sm font-normal text-muted">/ {{ stats?.total_accounts ?? 0 }}</span></p>
          <p class="text-xs text-muted">{{ t('opsCenter.errors') }} {{ stats?.error_accounts ?? 0 }} · {{ t('opsCenter.rateLimited') }} {{ stats?.ratelimit_accounts ?? 0 }} · {{ t('opsCenter.overloaded') }} {{ stats?.overload_accounts ?? 0 }}</p>
        </UPageCard>
        <UPageCard variant="subtle" :title="t('opsCenter.attention')" :ui="statUi">
          <p class="text-2xl font-semibold tabular-nums" :class="attentionAll.length ? 'text-warning' : 'text-highlighted'">{{ attentionAll.length }}</p>
          <p class="text-xs text-muted">{{ t('opsCenter.alerts') }} {{ pendingEvents }} · {{ t('opsCenter.risks') }} {{ riskAccounts.length }} · {{ t('opsCenter.notifications') }} {{ settings ? (settings.config.enabled ? t('opsCenter.on') : t('opsCenter.off')) : '—' }}</p>
        </UPageCard>
      </section>

      <section v-if="navigation.length" class="rounded-xl border border-default" :aria-label="t('opsCenter.attention')">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-default px-5 py-4">
          <div>
            <h2 class="font-semibold text-highlighted">{{ t('opsCenter.attention') }}</h2>
            <p class="mt-1 text-sm text-muted">{{ t('opsCenter.attentionDescription') }}</p>
          </div>
          <UButton to="/admin/account-ops" color="neutral" variant="link" :label="t('opsCenter.openNotifications')" />
        </div>
        <ul v-if="attention.length" class="divide-y divide-default">
          <li v-for="item in attention" :key="item.key" class="flex flex-wrap items-center gap-3 px-5 py-3">
            <UIcon :name="item.severity === 'critical' ? 'i-lucide-circle-alert' : 'i-lucide-triangle-alert'" class="size-4 shrink-0" :class="item.severity === 'critical' ? 'text-error' : 'text-warning'" />
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium text-highlighted">{{ item.title }}</p>
              <p class="mt-0.5 break-words text-xs text-muted">{{ item.detail }}</p>
            </div>
            <span class="text-xs text-dimmed">#{{ item.accountId }}</span>
            <UButton :to="item.href" color="neutral" variant="outline" size="sm" :label="t('opsCenter.resolve')" />
          </li>
        </ul>
        <p v-else class="px-5 py-8 text-center text-sm text-muted" role="status">{{ loading ? t('common.loading') : t('opsCenter.noAttention') }}</p>
      </section>

      <section v-if="navigation.length" class="space-y-3" :aria-label="t('opsCenter.workspaces')">
        <h2 class="font-semibold text-highlighted">{{ t('opsCenter.workspaces') }}</h2>
        <div class="grid gap-4 md:grid-cols-2">
          <UPageCard v-for="area in workspaces" :key="area.id" variant="outline" :ui="{ container: 'gap-y-4 h-full' }" :data-testid="`ops-workspace-${area.id}`">
            <div class="flex items-center gap-3">
              <UIcon :name="area.icon" class="size-5 text-muted" />
              <h3 class="font-semibold text-highlighted">{{ t(area.labelKey) }}</h3>
              <UButton :to="area.items[0].path" icon="i-lucide-arrow-up-right" color="neutral" variant="ghost" class="ml-auto" :aria-label="`${t('opsCenter.openWorkspace')}: ${t(area.labelKey)}`" />
            </div>
            <p class="text-sm leading-6 text-muted">{{ t(area.descriptionKey) }}</p>
            <div class="flex flex-wrap gap-x-3 gap-y-1">
              <RouterLink v-for="item in area.items" :key="item.path" :to="item.path" class="rounded text-xs leading-6 text-toned underline-offset-4 hover:text-highlighted hover:underline focus-visible:outline-2 focus-visible:outline-primary">{{ t(item.labelKey) }}</RouterLink>
            </div>
          </UPageCard>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import { useOperationsNavigation } from '@/components/admin/operations/opsNavigation'
import {
  getAccountOpsEvents,
  getAccountOpsSettings,
  getAccountOpsThresholdAccounts,
  type AccountOpsEvent,
  type AccountOpsSettings,
  type AccountOpsThresholdAccount,
} from '@/api/admin/accountOps'
import { getStats } from '@/api/admin/dashboard'
import type { DashboardStats } from '@/types'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'
import { buildAttentionItems, maxUsedPercent, summarizeBalances } from '@/utils/opsCenter'

const { t } = useI18n()
const auth = useAuthStore()
const navigation = useOperationsNavigation()
// The overview is this page; a card that links to itself is noise.
const workspaces = computed(() => navigation.value.filter(area => area.id !== 'overview'))
const statUi = { container: 'gap-y-2', title: 'text-xs font-normal text-muted' }
const loading = ref(false)
const error = ref('')
const stats = ref<DashboardStats | null>(null)
const accounts = ref<AccountOpsThresholdAccount[]>([])
const events = ref<AccountOpsEvent[]>([])
const settings = ref<AccountOpsSettings | null>(null)
let alive = true
let timer: number | undefined
let sequence = 0

const observer = computed(() => auth.isAdminObserver)
const balanceTotals = computed(() => summarizeBalances(accounts.value))
const accountsWithBalance = computed(() => accounts.value.filter(account => typeof account.balance === 'number' && Number.isFinite(account.balance)).length)
const attentionAll = computed(() => buildAttentionItems(accounts.value, events.value, 100, t))
const attention = computed(() => attentionAll.value.slice(0, 8))
const pendingEvents = computed(() => events.value.filter(event => ['pending', 'sending', 'failed'].includes(event.state)).length)
const riskAccounts = computed(() => accounts.value.filter(account => riskScore(account) > 0))

function riskScore(account: AccountOpsThresholdAccount): number {
  let score = 0
  if (account.balance_status === 'failed') score += 100
  else if (account.balance_status === 'stale') score += 60
  const used = maxUsedPercent(account)
  if (used !== null) {
    if (used >= 95) score += 40
    else if (used >= 90) score += 30
    else if (used >= 70) score += 10
  }
  return score
}

function formatMoney(value: number | null | undefined, unit?: string): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  const symbols: Record<string, string> = { USD: '$', CNY: '¥', EUR: '€', GBP: '£' }
  const normalized = String(unit || '').trim().toUpperCase()
  const amount = value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  return symbols[normalized] ? `${symbols[normalized]}${amount}` : `${amount} ${normalized}`.trim()
}

function formatCost(value: number | null | undefined): string {
  const amount = Number(value)
  if (!Number.isFinite(amount)) return '0.0000'
  if (amount >= 1000) return `${(amount / 1000).toFixed(2)}K`
  if (amount >= 1) return amount.toFixed(2)
  if (amount >= 0.01) return amount.toFixed(3)
  return amount.toFixed(4)
}

async function load() {
  if (loading.value) return
  const current = ++sequence
  loading.value = true
  const problems: string[] = []
  const jobs: Promise<void>[] = []
  jobs.push(getStats()
    .then(value => { if (alive && current === sequence) stats.value = value })
    .catch(err => { problems.push(extractApiErrorMessage(err, t('opsCenter.loadOverviewFailed'))) }))
  if (!observer.value) {
    jobs.push(getAccountOpsThresholdAccounts()
      .then(value => { if (alive && current === sequence) accounts.value = value })
      .catch(err => { problems.push(extractApiErrorMessage(err, t('opsCenter.loadAssetsFailed'))) }))
    jobs.push(getAccountOpsEvents()
      .then(value => { if (alive && current === sequence) events.value = value.items })
      .catch(err => { problems.push(extractApiErrorMessage(err, t('opsCenter.loadAlertsFailed'))) }))
    jobs.push(getAccountOpsSettings()
      .then(value => { if (alive && current === sequence) settings.value = value })
      .catch(err => { problems.push(extractApiErrorMessage(err, t('opsCenter.loadSettingsFailed'))) }))
  }
  await Promise.allSettled(jobs)
  if (!alive || current !== sequence) return
  error.value = problems.join(' · ')
  loading.value = false
}
function refresh() { void load() }
onMounted(() => {
  void load()
  timer = window.setInterval(() => {
    if (document.visibilityState === 'visible' && !loading.value) void load()
  }, 60_000)
})
onBeforeUnmount(() => {
  alive = false
  sequence++
  if (timer) window.clearInterval(timer)
})
</script>
