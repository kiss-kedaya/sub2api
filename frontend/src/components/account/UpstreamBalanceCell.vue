<template>
  <span v-if="view.state === 'not_applicable'" class="block w-36 text-right text-sm text-dimmed" data-testid="upstream-balance-na">
    <span aria-hidden="true">—</span>
    <span class="sr-only">{{ t('admin.accounts.upstreamBalance.notApplicable') }}</span>
  </span>
  <!-- Fixed width, right aligned: every state takes the same box, so the column does not jump
       when a read finishes and the numbers line up for comparison. -->
  <div
    v-else
    class="flex items-center gap-0.5"
    :class="align === 'start' ? 'justify-start' : 'w-36 justify-end'"
    :data-balance-state="view.state"
    data-testid="upstream-balance-cell"
  >
    <HelpTooltip trigger="auto" class="!ml-0 min-w-0" :class="align === 'start' ? '' : 'flex-1 justify-end'" width-class="w-72 max-w-[calc(100vw-2rem)]">
      <template #trigger>
        <button
          type="button"
          class="flex min-w-0 cursor-help flex-col rounded-sm leading-tight focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
          :class="align === 'start' ? 'items-start text-left' : 'items-end text-right'"
          :aria-label="ariaLabel"
          data-testid="upstream-balance-trigger"
        >
          <span class="flex items-center gap-1 text-sm" :class="headlineClass">
            <UIcon v-if="headlineIcon" :name="headlineIcon" class="size-3.5 shrink-0" aria-hidden="true" />
            <span
              :class="isAmount ? 'font-mono tabular-nums' : 'font-medium'"
              class="whitespace-nowrap border-b border-dotted border-accented"
              data-testid="upstream-balance-value"
            >{{ headline }}</span>
          </span>
          <span v-if="subline" class="mt-0.5 whitespace-nowrap text-xs" :class="sublineClass" data-testid="upstream-balance-subline">
            {{ subline }}
          </span>
        </button>
      </template>
      <dl class="space-y-1" data-testid="upstream-balance-details">
        <div v-for="row in detailRows" :key="row.key" class="flex gap-3" :data-detail="row.key">
          <dt class="shrink-0 text-muted">{{ row.label }}</dt>
          <dd class="ml-auto min-w-0 text-right [overflow-wrap:anywhere]" :class="row.mono ? 'font-mono tabular-nums' : ''">{{ row.value }}</dd>
        </div>
      </dl>
      <p v-if="view.unitAssumed" class="mt-2 border-t border-default pt-2 text-warning" data-testid="upstream-balance-unit-assumed">
        {{ t('admin.accounts.upstreamBalance.unitAssumed') }}
      </p>
      <p v-if="view.walletBalance !== undefined && view.state !== 'wallet'" class="mt-2 border-t border-default pt-2 text-muted" data-testid="upstream-balance-wallet-note">
        {{ t('admin.accounts.upstreamBalance.walletNote') }}
      </p>
      <p v-if="noteText" class="mt-2 border-t border-default pt-2 text-muted" data-testid="upstream-balance-note">{{ noteText }}</p>
    </HelpTooltip>
    <UButton
      color="neutral"
      variant="ghost"
      size="xs"
      icon="i-lucide-refresh-cw"
      square
      class="shrink-0"
      :loading="probing"
      :disabled="probing"
      :aria-label="t('admin.accounts.upstreamBalance.manualProbe')"
      :title="t('admin.accounts.upstreamBalance.manualProbe')"
      data-testid="upstream-balance-probe"
      @click="$emit('probe')"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import type { Account } from '@/types'
import {
  UPSTREAM_BALANCE_FAILURE_KEYS,
  formatUpstreamAmount,
  upstreamBalanceView,
  upstreamKindOf
} from './upstreamBalance'

const props = withDefaults(defineProps<{
  account: Account
  now: number
  probing?: boolean
  // 'end' (list column): fixed width, right aligned so amounts line up. 'start' (detail panel): follows its heading.
  align?: 'end' | 'start'
}>(), { align: 'end', probing: false })

defineEmits<{ (event: 'probe'): void }>()

const i18n = useI18n()
const t = i18n.t
const locale = computed(() => (typeof i18n.locale?.value === 'string' && i18n.locale.value) || 'en')

const view = computed(() => upstreamBalanceView(props.account, props.now))
const kind = computed(() => upstreamKindOf(props.account))
const snapshot = computed(() => props.account.extra?.upstream_billing_probe)
const unit = computed(() => view.value.data?.unit)
const money = (value: number) => formatUpstreamAmount(value, unit.value, locale.value)
const isAmount = computed(() => view.value.state === 'value' || view.value.state === 'wallet')

const headline = computed(() => {
  const v = view.value
  switch (v.state) {
    case 'value':
    case 'wallet':
      return money(v.amount ?? 0)
    case 'unlimited':
      return t('admin.accounts.upstreamBalance.unlimited')
    case 'official':
      return t('admin.accounts.upstreamBalance.official')
    case 'unsupported':
      return t('admin.accounts.upstreamBalance.unsupported')
    case 'failed':
      return t('admin.accounts.upstreamBalance.failed')
    default:
      return t('admin.accounts.upstreamBalance.notRead')
  }
})

const headlineIcon = computed(() => {
  const v = view.value
  if (v.state === 'failed' || v.lastReadFailed) return 'i-lucide-triangle-alert'
  if (v.stale) return 'i-lucide-clock'
  if (v.state === 'unlimited') return 'i-lucide-infinity'
  return ''
})

const headlineClass = computed(() => {
  const v = view.value
  if (v.state === 'failed') return 'text-error'
  if (v.stale) return 'text-muted'
  if (isAmount.value) return (v.amount ?? 0) < 0 ? 'text-error' : 'text-highlighted'
  if (v.state === 'unlimited') return 'text-highlighted'
  return 'text-dimmed'
})

const elapsed = (iso?: string) => {
  const at = typeof iso === 'string' ? Date.parse(iso) : Number.NaN
  if (!Number.isFinite(at)) return ''
  const minutes = Math.max(0, Math.floor((props.now - at) / 60_000))
  if (minutes < 1) return t('admin.accounts.upstreamBilling.justNow')
  if (minutes < 60) return t('admin.accounts.upstreamBilling.minutesAgo', { count: minutes })
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return t('admin.accounts.upstreamBilling.hoursAgo', { count: hours })
  return t('admin.accounts.upstreamBilling.daysAgo', { count: Math.floor(hours / 24) })
}

const subline = computed(() => {
  const v = view.value
  if (v.state === 'value' || v.state === 'unlimited') {
    if (v.lastReadFailed) return t('admin.accounts.upstreamBalance.lastReadFailedShort')
    if (v.stale) return t('admin.accounts.upstreamBalance.staleShort', { value: elapsed(v.balance?.received_at) })
    if (v.walletBalance !== undefined) return t('admin.accounts.upstreamBalance.walletShort', { value: money(v.walletBalance) })
    if (v.usedPercent !== undefined) return t('admin.accounts.upstreamBalance.usedPercent', { value: Math.round(v.usedPercent) })
    return v.state === 'value' ? t('admin.accounts.upstreamBalance.keyQuotaShort') : ''
  }
  if (v.state === 'wallet') {
    if (v.lastReadFailed) return t('admin.accounts.upstreamBalance.lastReadFailedShort')
    if (v.stale) return t('admin.accounts.upstreamBalance.staleShort', { value: elapsed(v.balance?.received_at) })
    return t('admin.accounts.upstreamBalance.walletOnlyShort')
  }
  if (v.state === 'failed' && v.balance?.http_status) return `HTTP ${v.balance.http_status}`
  return ''
})

const sublineClass = computed(() => {
  const v = view.value
  if (v.lastReadFailed) return 'text-error'
  if (v.stale) return 'text-warning'
  return 'text-muted'
})

const formatDate = (value?: string) => {
  const at = typeof value === 'string' ? Date.parse(value) : Number.NaN
  if (!Number.isFinite(at)) return ''
  return new Date(at).toLocaleString(locale.value, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

const failureText = (code?: string) => {
  if (!code) return ''
  return (UPSTREAM_BALANCE_FAILURE_KEYS as readonly string[]).includes(code)
    ? t(`admin.accounts.upstreamBalance.failure.${code}`)
    : code
}

const sourceText = (source?: string) => {
  if (!source) return ''
  return ['sub2api_usage', 'newapi_token_usage', 'openai_billing_subscription'].includes(source)
    ? t(`admin.accounts.upstreamBalance.source.${source}`)
    : source
}

type DetailRow = { key: string; label: string; value: string; mono?: boolean }

const detailRows = computed<DetailRow[]>(() => {
  const v = view.value
  const data = v.data
  const balance = v.balance
  const rows: DetailRow[] = []
  const push = (key: string, value: string | undefined, mono = false) => {
    if (value) rows.push({ key, label: t(`admin.accounts.upstreamBalance.detail.${key}`), value, mono })
  }
  if (kind.value) push('kind', t(`admin.accounts.upstreamKind.label.${kind.value}`))
  push('source', sourceText(balance?.source))
  push('plan', data?.plan_name)
  if (data?.unlimited) push('remaining', t('admin.accounts.upstreamBalance.unlimited'))
  else if (typeof data?.remaining === 'number') push('remaining', money(data.remaining), true)
  if (typeof data?.quota_limit === 'number') push('limit', money(data.quota_limit), true)
  if (typeof data?.quota_used === 'number') push('used', money(data.quota_used), true)
  if (v.usedPercent !== undefined) push('usedPercent', `${Math.round(v.usedPercent)}%`, true)
  if (typeof data?.wallet_balance === 'number') push('wallet', money(data.wallet_balance), true)
  if (typeof data?.wallet_used === 'number') push('walletUsed', money(data.wallet_used), true)
  push('expiresAt', formatDate(data?.expires_at))
  push('receivedAt', formatDate(balance?.received_at))
  push('freshUntil', formatDate(balance?.fresh_until))
  if (balance && balance.status !== 'ok') {
    push('lastAttempt', formatDate(balance.last_attempt_at))
    push('reason', failureText(balance.last_error))
    if (balance.http_status) push('httpStatus', String(balance.http_status), true)
  }
  push('nextProbe', formatDate(snapshot.value?.next_probe_at))
  return rows
})

const noteText = computed(() => {
  const v = view.value
  if (v.state === 'official') return t('admin.accounts.upstreamBalance.officialNote')
  if (v.state === 'unsupported') return t('admin.accounts.upstreamBalance.unsupportedNote')
  if (v.state === 'not_read') return t('admin.accounts.upstreamBalance.notReadNote')
  if (v.state === 'unlimited') return t('admin.accounts.upstreamBalance.unlimitedNote')
  if (v.lastReadFailed) return t('admin.accounts.upstreamBalance.keptLastValueNote')
  if (v.stale) return t('admin.accounts.upstreamBalance.staleNote')
  return ''
})

const ariaLabel = computed(() => {
  const parts = [t('admin.accounts.columns.upstreamBalance'), headline.value]
  if (subline.value) parts.push(subline.value)
  return parts.join('，')
})
</script>
