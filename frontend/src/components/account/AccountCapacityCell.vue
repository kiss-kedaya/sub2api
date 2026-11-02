<template>
  <div class="flex min-w-0 flex-col items-start gap-1">
    <!-- 使用人数（活跃会话）：仅启用会话数限制的账号会返回该计数 -->
    <CapacityMeterBadge
      v-if="showUsers"
      :label="t('admin.accounts.capacity.labels.users')"
      :hint="sessionLimitTooltip"
      :used="activeSessions"
      :max="account.max_sessions"
      icon="i-lucide-users"
    />

    <!-- 并发槽位 -->
    <CapacityMeterBadge
      :label="t('admin.accounts.capacity.labels.concurrency')"
      :hint="concurrencyHint"
      :used="currentConcurrency"
      :max="account.concurrency"
      :tone="toneOf(concurrencyClass)"
      icon="i-lucide-layout-grid"
    />

    <!-- 5h窗口费用限制 -->
    <CapacityMeterBadge
      v-if="showWindowCost"
      :label="t('admin.accounts.capacity.labels.windowCost')"
      :hint="windowCostTooltip"
      :used="'$' + formatCost(currentWindowCost)"
      :max="'$' + formatCost(account.window_cost_limit)"
      :tone="toneOf(windowCostClass)"
      icon="i-lucide-circle-dollar-sign"
    />

    <!-- RPM 限制 -->
    <CapacityMeterBadge
      v-if="showRpmLimit"
      :label="t('admin.accounts.capacity.labels.rpm')"
      :hint="rpmTooltip"
      :used="currentRPM"
      :max="account.base_rpm!"
      :suffix="rpmStrategyTag"
      :tone="toneOf(rpmClass)"
      icon="i-lucide-clock"
    />

    <!-- API Key 账号配额限制 -->
    <QuotaBadge v-if="showDailyQuota" :used="account.quota_daily_used ?? 0" :limit="account.quota_daily_limit!" label="D" />
    <QuotaBadge v-if="showWeeklyQuota" :used="account.quota_weekly_used ?? 0" :limit="account.quota_weekly_limit!" label="W" />
    <QuotaBadge v-if="showTotalQuota" :used="account.quota_used ?? 0" :limit="account.quota_limit!" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import CapacityMeterBadge from '@/components/common/CapacityMeterBadge.vue'
import QuotaBadge from '@/components/account/QuotaBadge.vue'

type BadgeTone = 'full' | 'active' | 'idle'

const props = defineProps<{
  account: Account
}>()

const { t } = useI18n()

// The thresholds below were tuned per meter; keep them and only translate the result
// into the badge's tone so every cell uses the same three states.
const toneOf = (colorClass: string): BadgeTone => {
  if (colorClass === 'text-error') return 'full'
  if (colorClass === 'text-warning') return 'active'
  return 'idle'
}

// ====== 使用人数（活跃会话） ======
const showUsers = computed(() =>
  (props.account.max_sessions != null && props.account.max_sessions > 0) ||
  props.account.active_sessions != null
)

const activeSessions = computed(() => props.account.active_sessions ?? 0)

// ====== 并发 ======
const currentConcurrency = computed(() => props.account.current_concurrency || 0)

const concurrencyClass = computed(() => {
  const current = currentConcurrency.value
  const max = props.account.concurrency
  if (current >= max) return 'text-error'
  if (current > 0) return 'text-warning'
  return 'text-muted'
})

const concurrencyHint = computed(() => {
  const base = t('admin.accounts.capacity.concurrency.hint')
  const current = currentConcurrency.value
  const max = props.account.concurrency || 0
  if (max > 0 && current >= max) {
    return `${base} ${t('admin.accounts.capacity.concurrency.full')}`
  }
  return base
})

// ====== 窗口费用 ======
const isAnthropicOAuthOrSetupToken = computed(() =>
  props.account.platform === 'anthropic' &&
  (props.account.type === 'oauth' || props.account.type === 'setup-token')
)

const showWindowCost = computed(() =>
  isAnthropicOAuthOrSetupToken.value &&
  props.account.window_cost_limit != null &&
  props.account.window_cost_limit > 0
)

const currentWindowCost = computed(() => props.account.current_window_cost ?? 0)

const windowCostClass = computed(() => {
  if (!showWindowCost.value) return ''
  const current = currentWindowCost.value
  const limit = props.account.window_cost_limit || 0
  const reserve = props.account.window_cost_sticky_reserve || 10
  if (current >= limit + reserve) return 'text-error'
  if (current >= limit) return 'text-warning'
  if (current >= limit * 0.8) return 'text-warning'
  return 'text-muted'
})

const windowCostTooltip = computed(() => {
  if (!showWindowCost.value) return ''
  const current = currentWindowCost.value
  const limit = props.account.window_cost_limit || 0
  const reserve = props.account.window_cost_sticky_reserve || 10
  if (current >= limit + reserve) return t('admin.accounts.capacity.windowCost.blocked')
  if (current >= limit) return t('admin.accounts.capacity.windowCost.stickyOnly')
  return t('admin.accounts.capacity.windowCost.normal')
})

// ====== 会话限制 ======
const sessionLimitTooltip
 = computed(() => {
  if (!showUsers.value) return ''
  const current = activeSessions.value
  const max = props.account.max_sessions || 0
  const idle = props.account.session_idle_timeout_minutes || 5
  if (max > 0 && current >= max) return t('admin.accounts.capacity.sessions.full', { idle })
  return t('admin.accounts.capacity.sessions.normal', { idle })
})

// ====== RPM ======
const showRpmLimit = computed(() =>
  isAnthropicOAuthOrSetupToken.value &&
  props.account.base_rpm != null &&
  props.account.base_rpm > 0
)

const currentRPM = computed(() => props.account.current_rpm ?? 0)
const rpmStrategy = computed(() => props.account.rpm_strategy || 'tiered')
const rpmStrategyTag = computed(() => rpmStrategy.value === 'sticky_exempt' ? '[S]' : '[T]')

const rpmBuffer = computed(() => {
  const base = props.account.base_rpm || 0
  return props.account.rpm_sticky_buffer ?? (base > 0 ? Math.max(1, Math.floor(base / 5)) : 0)
})

const rpmClass = computed(() => {
  if (!showRpmLimit.value) return ''
  const current = currentRPM.value
  const base = props.account.base_rpm ?? 0
  const buffer = rpmBuffer.value
  if (rpmStrategy.value === 'tiered') {
    if (current >= base + buffer) return 'text-error'
    if (current >= base) return 'text-warning'
  } else {
    if (current >= base) return 'text-warning'
  }
  if (current >= base * 0.8) return 'text-warning'
  return 'text-muted'
})

const rpmTooltip = computed(() => {
  if (!showRpmLimit.value) return ''
  const current = currentRPM.value
  const base = props.account.base_rpm ?? 0
  const buffer = rpmBuffer.value
  if (rpmStrategy.value === 'tiered') {
    if (current >= base + buffer) return t('admin.accounts.capacity.rpm.tieredBlocked', { buffer })
    if (current >= base) return t('admin.accounts.capacity.rpm.tieredStickyOnly', { buffer })
    if (current >= base * 0.8) return t('admin.accounts.capacity.rpm.tieredWarning')
    return t('admin.accounts.capacity.rpm.tieredNormal')
  } else {
    if (current >= base) return t('admin.accounts.capacity.rpm.stickyExemptOver')
    if (current >= base * 0.8) return t('admin.accounts.capacity.rpm.stickyExemptWarning')
    return t('admin.accounts.capacity.rpm.stickyExemptNormal')
  }
})

// 格式化费用显示
const formatCost = (value: number | null | undefined) => {
  if (value === null || value === undefined) return '0'
  return value.toFixed(2)
}

// ====== 配额 ======
const isQuotaEligible = computed(() => props.account.type === 'apikey' || props.account.type === 'bedrock')

const showDailyQuota = computed(() =>
  isQuotaEligible.value && props.account.quota_daily_limit != null && props.account.quota_daily_limit > 0
)
const showWeeklyQuota = computed(() =>
  isQuotaEligible.value && props.account.quota_weekly_limit != null && props.account.quota_weekly_limit > 0
)
const showTotalQuota = computed(() =>
  isQuotaEligible.value && props.account.quota_limit != null && props.account.quota_limit > 0
)
</script>
