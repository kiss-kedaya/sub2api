<template>
  <!-- Account detail: the full usage windows, today's stats, the upstream rate and the
       schedule live here, so the list keeps only its key columns. -->
  <USlideover
    :open="open && !!account"
    :title="account?.name ?? ''"
    :description="account ? `${account.platform} · ${account.type} · #${account.id}` : ''"
    :ui="{ content: 'max-w-xl' }"
    @update:open="onOpenChange"
  >
    <template #body>
      <UTabs
        v-if="account"
        :items="tabItems"
        default-value="overview"
        variant="link"
        class="w-full"
        :ui="{ list: 'mb-4' }"
        data-testid="account-detail-tabs"
      >
        <template #overview>
          <dl class="divide-y divide-default text-sm">
            <div class="detail-row">
              <dt>{{ t('admin.accounts.columns.status') }}</dt>
              <dd><AccountStatusIndicator :account="account" @show-temp-unsched="emit('show-temp-unsched', $event)" /></dd>
            </div>
            <div class="detail-row">
              <dt>{{ t('admin.accounts.columns.platformType') }}</dt>
              <dd><PlatformTypeBadge :platform="account.platform" :type="account.type" /></dd>
            </div>
            <div class="detail-row">
              <dt>{{ t('admin.accounts.columns.capacity') }}</dt>
              <dd><AccountCapacityCell :account="account" /></dd>
            </div>
            <div class="detail-row">
              <dt>{{ t('admin.accounts.columns.groups') }}</dt>
              <dd><AccountGroupsCell :groups="groups" :max-display="12" /></dd>
            </div>
            <div class="detail-row">
              <dt>{{ t('admin.accounts.columns.proxy') }}</dt>
              <dd>
                <span v-if="account.proxy" class="text-default">{{ account.proxy.name }}<span v-if="account.proxy.country_code" class="text-muted"> ({{ account.proxy.country_code }})</span></span>
                <span v-else class="text-dimmed">-</span>
              </dd>
            </div>
            <div class="detail-row">
              <dt>{{ t('admin.accounts.columns.priority') }}</dt>
              <dd class="tabular-nums">{{ account.priority }}</dd>
            </div>
            <div class="detail-row">
              <dt>{{ t('admin.accounts.columns.billingRateMultiplier') }}</dt>
              <dd class="font-mono tabular-nums">{{ formatMultiplier(account.rate_multiplier ?? 1) }}x</dd>
            </div>
            <div class="detail-row">
              <dt>{{ t('admin.accounts.columns.lastUsed') }}</dt>
              <dd class="text-muted">{{ formatRelativeTime(account.last_used_at) }}</dd>
            </div>
            <div class="detail-row">
              <dt>{{ t('admin.accounts.columns.createdAt') }}</dt>
              <dd class="tabular-nums text-muted">{{ formatDateTime(account.created_at) }}</dd>
            </div>
            <div class="detail-row">
              <dt>{{ t('admin.accounts.columns.notes') }}</dt>
              <dd class="whitespace-pre-wrap wrap-break-word">{{ account.notes || '-' }}</dd>
            </div>
          </dl>
        </template>

        <template #usage>
          <div class="space-y-5">
            <section class="space-y-2">
              <h4 class="text-xs font-medium text-muted">{{ t('admin.accounts.columns.usageWindows') }}</h4>
              <AccountUsageCell :account="account" :today-stats="todayStats" :manual-refresh-token="usageRefreshKey" @account-updated="emit('account-updated', $event)" />
            </section>
            <USeparator />
            <section class="space-y-2">
              <h4 class="text-xs font-medium text-muted">{{ t('admin.accounts.columns.todayStats') }}</h4>
              <AccountTodayStatsCell :stats="todayStats" />
            </section>
            <template v-if="account.type === 'apikey'">
              <USeparator />
              <section class="space-y-2">
                <h4 class="text-xs font-medium text-muted">{{ t('admin.accounts.columns.upstreamBillingRate') }}</h4>
                <UpstreamBillingRateCell :account="account" :now="now" :global-probe-enabled="globalProbeEnabled" @probe="emit('probe-upstream-billing', account)" />
              </section>
            </template>
          </div>
        </template>

        <template #schedule>
          <div class="space-y-5">
            <section class="space-y-2">
              <h4 class="text-xs font-medium text-muted">{{ t('admin.accounts.columns.schedulable') }}</h4>
              <div class="flex flex-wrap items-center gap-2">
                <UBadge :color="account.schedulable ? 'success' : 'neutral'" variant="subtle">
                  {{ account.schedulable ? t('admin.accounts.schedulableEnabled') : t('admin.accounts.schedulableDisabled') }}
                </UBadge>
                <SchedulableTimerControl v-if="showTimer" :account-id="account.id" :refresh-key="timerRefreshKey" @executed="emit('timer-executed')" />
              </div>
            </section>
            <USeparator />
            <div class="flex flex-wrap gap-2">
              <UButton color="neutral" variant="outline" icon="i-lucide-play" :label="t('admin.accounts.testConnection')" @click="emit('test', account)" />
              <UButton color="neutral" variant="outline" icon="i-lucide-calendar-clock" :label="t('admin.scheduledTests.schedule')" @click="emit('schedule', account)" />
            </div>
          </div>
        </template>
      </UTabs>
    </template>

    <template #footer>
      <div v-if="account" class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="outline" icon="i-lucide-chart-column" :label="t('admin.accounts.viewStats')" @click="emit('stats', account)" />
        <UButton icon="i-lucide-pencil" :label="t('common.edit')" @click="emit('edit', account)" />
      </div>
    </template>
  </USlideover>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account, AdminGroup, WindowStats } from '@/types'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import AccountTodayStatsCell from '@/components/account/AccountTodayStatsCell.vue'
import UpstreamBillingRateCell from '@/components/account/UpstreamBillingRateCell.vue'
import SchedulableTimerControl from '@/components/account/SchedulableTimerControl.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import { formatMultiplier } from '@/utils/formatters'

withDefaults(defineProps<{
  open: boolean
  account: Account | null
  groups?: AdminGroup[]
  todayStats?: WindowStats | null
  showTimer?: boolean
  timerRefreshKey?: number
  usageRefreshKey?: number
  globalProbeEnabled?: boolean
  now?: number
}>(), {
  groups: () => [],
  todayStats: null,
  showTimer: false,
  timerRefreshKey: 0,
  usageRefreshKey: 0,
  globalProbeEnabled: true,
  now: () => Date.now()
})

const emit = defineEmits<{
  close: []
  edit: [account: Account]
  test: [account: Account]
  stats: [account: Account]
  schedule: [account: Account]
  'timer-executed': []
  'show-temp-unsched': [account: Account]
  'account-updated': [account: Account]
  'probe-upstream-billing': [account: Account]
}>()

const { t } = useI18n()
const onOpenChange = (value: boolean) => { if (!value) emit('close') }

const tabItems = computed(() => [
  { label: t('admin.accounts.accountDetail.overview'), slot: 'overview' as const, value: 'overview' },
  { label: t('admin.accounts.accountDetail.usage'), slot: 'usage' as const, value: 'usage' },
  { label: t('admin.accounts.accountDetail.schedule'), slot: 'schedule' as const, value: 'schedule' }
])
</script>

<style scoped>
.detail-row {
  display: grid;
  grid-template-columns: 7.5rem minmax(0, 1fr);
  align-items: start;
  gap: 12px;
  padding-block: 10px;
}

.detail-row dt {
  color: var(--ui-text-muted);
  font-size: 0.75rem;
  line-height: 1.25rem;
}
</style>
