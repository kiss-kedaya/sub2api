import type { Account, UpstreamBalanceData, UpstreamBalanceSnapshot, UpstreamKind } from '@/types'

// Field contract: backend/internal/service/upstream_kind_probe.go and upstream_balance_probe.go.

export type UpstreamKindKey = UpstreamKind | 'unknown'

const KNOWN_KINDS: readonly UpstreamKind[] = ['sub2api', 'newapi', 'openai_compatible', 'official', 'custom']

export function upstreamKindOf(account: Pick<Account, 'type' | 'extra'>): UpstreamKindKey | null {
  // Only API-key accounts are probed; the other types have no upstream site to identify.
  if (account.type !== 'apikey') return null
  const kind = account.extra?.upstream_billing_probe?.upstream_kind
  return typeof kind === 'string' && (KNOWN_KINDS as readonly string[]).includes(kind) ? kind as UpstreamKind : 'unknown'
}

// Each kind carries its own icon and short label, so the column is readable without colour.
export const UPSTREAM_KIND_ICON: Record<UpstreamKindKey, string> = {
  sub2api: 'i-lucide-layers',
  newapi: 'i-lucide-box',
  openai_compatible: 'i-lucide-plug',
  official: 'i-lucide-badge-check',
  custom: 'i-lucide-wrench',
  unknown: 'i-lucide-circle-help'
}

export const UPSTREAM_KIND_COLOR: Record<UpstreamKindKey, 'primary' | 'info' | 'success' | 'warning' | 'neutral'> = {
  sub2api: 'primary',
  newapi: 'info',
  openai_compatible: 'warning',
  official: 'success',
  custom: 'neutral',
  unknown: 'neutral'
}

export type UpstreamBalanceState =
  | 'not_applicable' // not an API-key account
  | 'not_read' // never probed, or the probe never reached a balance endpoint
  | 'official' // provider's own API: nothing is requested
  | 'unsupported' // reached the upstream, no known balance endpoint answered
  | 'failed' // read failed and there is no earlier value to show
  | 'unlimited'
  | 'value' // remaining (key quota) is the headline number
  | 'wallet' // only the account wallet balance is known

export interface UpstreamBalanceView {
  state: UpstreamBalanceState
  balance?: UpstreamBalanceSnapshot
  data?: UpstreamBalanceData
  // The number shown as the headline (remaining, or wallet balance in the 'wallet' state).
  amount?: number
  // Shown on a second line when it is a different number from the headline.
  walletBalance?: number
  usedPercent?: number
  // Value comes from an earlier read: past fresh_until, or the latest read failed.
  stale: boolean
  // The latest read failed while an older value is still on screen.
  lastReadFailed: boolean
  unitAssumed: boolean
}

const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value)

export function upstreamBalanceView(account: Pick<Account, 'type' | 'extra'>, now: number): UpstreamBalanceView {
  const base = { stale: false, lastReadFailed: false, unitAssumed: false }
  const kind = upstreamKindOf(account)
  if (kind === null) return { ...base, state: 'not_applicable' }
  const snapshot = account.extra?.upstream_billing_probe
  const balance = snapshot?.balance
  const data = balance?.data
  const hasData = !!data && (data.unlimited === true || finite(data.remaining) || finite(data.wallet_balance))

  if (!hasData) {
    if (kind === 'official') return { ...base, state: 'official', balance }
    if (!balance) return { ...base, state: 'not_read' }
    if (balance.status === 'unsupported' || kind === 'custom') return { ...base, state: 'unsupported', balance }
    if (balance.status === 'failed') return { ...base, state: 'failed', balance }
    return { ...base, state: 'not_read', balance }
  }

  const freshUntil = typeof balance?.fresh_until === 'string' ? Date.parse(balance.fresh_until) : Number.NaN
  const lastReadFailed = balance?.status !== 'ok'
  const stale = lastReadFailed || (Number.isFinite(freshUntil) && now > freshUntil)
  const unitAssumed = data.unit_assumed === true
  const walletBalance = finite(data.wallet_balance) ? data.wallet_balance : undefined
  const limit = finite(data.quota_limit) && data.quota_limit > 0 ? data.quota_limit : undefined
  let usedPercent: number | undefined
  if (limit !== undefined) {
    const used = finite(data.quota_used) ? data.quota_used : finite(data.remaining) ? limit - data.remaining : undefined
    if (used !== undefined) usedPercent = Math.min(100, Math.max(0, (used / limit) * 100))
  }
  const common = { balance, data, stale, lastReadFailed, unitAssumed, usedPercent }

  if (data.unlimited === true) return { ...common, state: 'unlimited', walletBalance }
  if (finite(data.remaining)) return { ...common, state: 'value', amount: data.remaining, walletBalance }
  return { ...common, state: 'wallet', amount: walletBalance, walletBalance: undefined }
}

// Every amount in the column uses two decimals so the column compares at a glance; a positive amount
// below one cent is shown as "< $0.01" rather than rounding a non-empty balance down to zero.
export function formatUpstreamAmount(value: number, unit: string | undefined, locale: string): string {
  // The upstream decides the unit; without one the number is shown bare instead of guessing dollars.
  const code = (unit ?? '').trim().toUpperCase()
  const number = new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  const abs = Math.abs(value)
  const tiny = abs > 0 && abs < 0.005
  const body = number.format(tiny ? 0.01 : abs)
  const sign = value < 0 ? '-' : ''
  const prefix = tiny ? (value < 0 ? '> ' : '< ') : ''
  if (code === 'USD') return `${prefix}${sign}$${body}`
  if (code === '') return `${prefix}${sign}${body}`
  return `${prefix}${sign}${body} ${code}`
}

export const UPSTREAM_BALANCE_FAILURE_KEYS = [
  'unsupported',
  'http_error',
  'invalid_response',
  'request_failed',
  'request_build_failed',
  'empty_response',
  'response_read_failed',
  'response_too_large'
] as const
