<template>
  <div class="flex flex-wrap items-start gap-1.5">
    <!-- Rate limited (429): badge + resume time below -->
    <div v-if="isRateLimited" class="flex flex-col items-start gap-0.5">
      <UBadge color="warning" variant="subtle" size="sm" data-status="warning">{{ t('admin.accounts.status.rateLimited') }}</UBadge>
      <span class="text-xs text-muted">{{ rateLimitResumeText }}</span>
    </div>

    <!-- Overloaded (529): badge + countdown below -->
    <div v-else-if="isOverloaded" class="flex flex-col items-start gap-0.5">
      <UBadge color="error" variant="subtle" size="sm" data-status="error">{{ t('admin.accounts.status.overloaded') }}</UBadge>
      <span class="text-xs text-muted">{{ overloadCountdown }}</span>
    </div>

    <!-- Main status (shown when not rate limited / overloaded) -->
    <template v-else>
      <div v-if="isTempUnschedulable" class="flex flex-col items-start gap-0.5">
        <UButton
          color="warning"
          variant="subtle"
          size="xs"
          trailing-icon="i-lucide-chevron-right"
          class="rounded-md px-1.5 py-0.5"
          :data-status="statusColor"
          :title="t('admin.accounts.status.viewTempUnschedDetails')"
          @click="handleTempUnschedClick"
        >
          {{ statusText }}
        </UButton>
        <span class="max-w-[180px] text-xs leading-4 text-muted">
          {{ tempUnschedRecoveryText }}
        </span>
      </div>
      <UBadge v-else :color="statusColor" variant="subtle" size="sm" :data-status="statusColor">
        {{ statusText }}
      </UBadge>
    </template>

    <!-- Error details on hover / focus -->
    <UPopover v-if="hasError && account.error_message" mode="hover" :content="{ side: 'bottom', align: 'start' }">
      <span
        tabindex="0"
        class="inline-flex cursor-help items-center text-error"
        :aria-label="t('admin.accounts.status.error')"
        data-test="account-error-info"
      >
        <UIcon name="i-lucide-circle-help" class="size-4" />
      </span>
      <template #content>
        <div class="max-w-[300px] whitespace-pre-wrap wrap-break-word px-3 py-2 text-xs leading-relaxed text-default">
          {{ account.error_message }}
        </div>
      </template>
    </UPopover>

    <!-- 429 indicator -->
    <UBadge
      v-if="isRateLimited"
      color="warning"
      variant="outline"
      size="sm"
      icon="i-lucide-triangle-alert"
      class="tabular-nums"
      :title="t('admin.accounts.status.rateLimitedUntil', { time: formatDateTime(account.rate_limit_reset_at) })"
    >
      429
    </UBadge>

    <!-- Model status (model rate limit / credits exhausted / running on credits) -->
    <div
      v-if="activeModelStatuses.length > 0"
      :class="[
        activeModelStatuses.length <= 4
          ? 'flex flex-col items-start gap-1'
          : activeModelStatuses.length <= 8
            ? 'columns-2 gap-x-2'
            : 'columns-3 gap-x-2'
      ]"
    >
      <div v-for="item in activeModelStatuses" :key="`${item.kind}-${item.model}`" class="mb-1 break-inside-avoid">
        <UBadge
          :color="item.kind === 'credits_exhausted' ? 'error' : item.kind === 'credits_active' ? 'warning' : 'neutral'"
          variant="subtle"
          size="sm"
          :icon="item.kind === 'credits_active' ? undefined : 'i-lucide-triangle-alert'"
          :title="item.kind === 'credits_exhausted'
            ? t('admin.accounts.status.creditsExhaustedUntil', { time: formatDateTimeToMinute(item.reset_at) })
            : item.kind === 'credits_active'
              ? t('admin.accounts.status.modelCreditOveragesUntil', { model: formatScopeName(item.model), time: formatDateTimeToMinute(item.reset_at) })
              : t('admin.accounts.status.modelRateLimitedUntil', { model: formatScopeName(item.model), time: formatDateTimeToMinute(item.reset_at) })"
        >
          <span v-if="item.kind === 'credits_active'">⚡</span>
          {{ item.kind === 'credits_exhausted' ? t('admin.accounts.status.creditsExhausted') : formatScopeName(item.model) }}
          <span class="tabular-nums opacity-70">{{ formatCountdown(item.reset_at) }}</span>
        </UBadge>
      </div>
    </div>

    <!-- 529 indicator -->
    <UBadge
      v-if="isOverloaded"
      color="error"
      variant="outline"
      size="sm"
      icon="i-lucide-triangle-alert"
      class="tabular-nums"
      :title="t('admin.accounts.status.overloadedUntil', { time: formatTime(account.overload_until) })"
    >
      529
    </UBadge>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import { formatCountdown, formatDateTime, formatDateTimeToMinute, formatCountdownWithSuffix, formatTime } from '@/utils/format'

const { t } = useI18n()

const props = defineProps<{
  account: Account
}>()

const emit = defineEmits<{
  (e: 'show-temp-unsched', account: Account): void
}>()

// Computed: is rate limited (429)
const isRateLimited = computed(() => {
  if (!props.account.rate_limit_reset_at) return false
  return new Date(props.account.rate_limit_reset_at) > new Date()
})

type AccountModelStatusItem = {
  kind: 'rate_limit' | 'credits_exhausted' | 'credits_active'
  model: string
  reset_at: string
}

// Computed: active model statuses (普通模型限流 + 积分耗尽 + 走积分中)
const activeModelStatuses = computed<AccountModelStatusItem[]>(() => {
  const extra = props.account.extra as Record<string, unknown> | undefined
  const modelLimits = extra?.model_rate_limits as
    | Record<string, { rate_limited_at: string; rate_limit_reset_at: string }>
    | undefined
  const now = new Date()
  const items: AccountModelStatusItem[] = []

  if (!modelLimits) return items

  // 检查 AICredits key 是否生效（积分是否耗尽）
  const aiCreditsEntry = modelLimits['AICredits']
  const hasActiveAICredits = aiCreditsEntry && new Date(aiCreditsEntry.rate_limit_reset_at) > now
  const allowOverages = !!(extra?.allow_overages)

  for (const [model, info] of Object.entries(modelLimits)) {
    if (new Date(info.rate_limit_reset_at) <= now) continue

    if (model === 'AICredits') {
      // AICredits key → 积分已用尽
      items.push({ kind: 'credits_exhausted', model, reset_at: info.rate_limit_reset_at })
    } else if (allowOverages && !hasActiveAICredits) {
      // 普通模型限流 + overages 启用 + 积分可用 → 正在走积分
      items.push({ kind: 'credits_active', model, reset_at: info.rate_limit_reset_at })
    } else {
      // 普通模型限流
      items.push({ kind: 'rate_limit', model, reset_at: info.rate_limit_reset_at })
    }
  }

  return items
})

const formatScopeName = (scope: string): string => {
  const aliases: Record<string, string> = {
    // Claude 系列
    'claude-fable-5-1': 'CFable51',
    'claude-fable-5': 'CFable5',
    'claude-opus-4-6': 'COpus46',
    'claude-opus-4-6-thinking': 'COpus46T',
    'claude-opus-4-7': 'COpus47',
    'claude-opus-4-8': 'COpus48',
    'claude-opus-5': 'COpus5',
    'claude-sonnet-4-6': 'CSon46',
    'claude-sonnet-4-5': 'CSon45',
    'claude-sonnet-4-5-thinking': 'CSon45T',
    'claude-sonnet-5': 'CSon5',
    // Gemini 2.5 系列
    'gemini-2.5-flash': 'G25F',
    'gemini-2.5-flash-lite': 'G25FL',
    'gemini-2.5-flash-thinking': 'G25FT',
    'gemini-2.5-pro': 'G25P',
    'gemini-2.5-flash-image': 'G25I',
    // Gemini 3.5 系列
    'gemini-3.5-flash': 'G35F',
    // Gemini 3 系列
    'gemini-3-flash': 'G3F',
    'gemini-3.1-pro-high': 'G3PH',
    'gemini-3.1-pro-low': 'G3PL',
    'gemini-3-pro-image': 'G3PI',
    'gemini-3.1-flash-image': 'G31FI',
    // 其他
    'gpt-oss-120b-medium': 'GPT120',
    'tab_flash_lite_preview': 'TabFL',
    // 旧版 scope 别名（兼容）
    claude: 'Claude',
    claude_sonnet: 'CSon',
    claude_opus: 'COpus',
    claude_haiku: 'CHaiku',
    gemini_text: 'Gemini',
    gemini_image: 'GImg',
    gemini_flash: 'GFlash',
    gemini_pro: 'GPro',
  }
  return aliases[scope] || scope
}

// Computed: is overloaded (529)
const isOverloaded = computed(() => {
  if (!props.account.overload_until) return false
  return new Date(props.account.overload_until) > new Date()
})

// Computed: is temp unschedulable
const isTempUnschedulable = computed(() => {
  if (!props.account.temp_unschedulable_until) return false
  return new Date(props.account.temp_unschedulable_until) > new Date()
})

// Computed: has error status
const hasError = computed(() => {
  return props.account.status === 'error'
})

const isQuotaExceeded = computed(() => {
  const exceeded = (used?: number | null, limit?: number | null) =>
    typeof limit === 'number' && limit > 0 && typeof used === 'number' && used >= limit
  return (
    exceeded(props.account.quota_used, props.account.quota_limit) ||
    exceeded(props.account.quota_daily_used, props.account.quota_daily_limit) ||
    exceeded(props.account.quota_weekly_used, props.account.quota_weekly_limit)
  )
})

// Computed: countdown text for rate limit (429)
const rateLimitCountdown = computed(() => {
  return formatCountdown(props.account.rate_limit_reset_at)
})

const rateLimitResumeText = computed(() => {
  if (!rateLimitCountdown.value) return ''
  return t('admin.accounts.status.rateLimitedAutoResume', { time: rateLimitCountdown.value })
})

// Computed: countdown text for overload (529)
const overloadCountdown = computed(() => {
  return formatCountdownWithSuffix(props.account.overload_until)
})

const tempUnschedRecoveryText = computed(() => {
  if (!isTempUnschedulable.value || !props.account.temp_unschedulable_until) return ''
  return t('admin.accounts.status.tempUnschedulableUntil', {
    time: formatDateTime(props.account.temp_unschedulable_until)
  })
})

// Computed: status badge colour (UBadge subtle)
const statusColor = computed<'error' | 'warning' | 'neutral' | 'success'>(() => {
  if (hasError.value) {
    return 'error'
  }
  if (isTempUnschedulable.value) {
    return 'warning'
  }
  if (props.account.status !== 'active') {
    return props.account.status === 'error' ? 'error' : 'neutral'
  }
  if (isQuotaExceeded.value) {
    return 'warning'
  }
  if (!props.account.schedulable) {
    return 'neutral'
  }
  return 'success'
})

// Computed: status text
const statusText = computed(() => {
  if (hasError.value) {
    return t('admin.accounts.status.error')
  }
  if (isTempUnschedulable.value) {
    return t('admin.accounts.status.tempUnschedulable')
  }
  if (props.account.status !== 'active') {
    return t(`admin.accounts.status.${props.account.status}`)
  }
  if (isQuotaExceeded.value) {
    return t('admin.accounts.status.quotaExceeded')
  }
  if (!props.account.schedulable) {
    return t('admin.accounts.status.paused')
  }
  return t(`admin.accounts.status.${props.account.status}`)
})

const handleTempUnschedClick = () => {
  if (!isTempUnschedulable.value) return
  emit('show-temp-unsched', props.account)
}
</script>
