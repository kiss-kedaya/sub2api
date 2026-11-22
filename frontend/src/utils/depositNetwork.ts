import type { DepositNetworkState, DepositNetworkStatus } from '@/api/crypto'

// Deposit network status → customer copy.
//
// The deposit page has to answer one question without alarming the user: "how
// long until this transfer lands?" A network that is merely working is not a
// delay, so the badge and the notice never say "delayed" unless the backend
// observed a delay. And because a status row is evidence with an expiry, stale
// evidence is treated exactly like missing evidence: "checking", never
// "delayed". Everything here is pure so the mapping can be tested without
// mounting the page.

export type DepositTranslate = (key: string, params?: Record<string, unknown>) => string

// Badge colour follows the observation, not the mood. A slow-but-working
// network stays neutral; only an observed delay/failure is a warning.
export type DepositBadgeColor = 'success' | 'neutral' | 'warning'

export interface DepositNetworkBadge {
  color: DepositBadgeColor
  text: string
}

export interface DepositNetworkView {
  state: DepositNetworkState
  // Seconds until credit, from the freshest evidence. Null when unknown.
  etaSeconds?: number | null
  // The chain credits as soon as the transfer is detected (provisional credit,
  // rolled back if the transaction ultimately fails).
  instantCredit?: boolean
}

// Below 90 seconds users think in seconds ("about 35 seconds"); above that the
// seconds figure is noise, so it is rounded to whole minutes.
const ETA_SECONDS_BELOW_MINUTES = 90
// Past half an hour the exact figure stops being useful and starts to look like
// a promise, so it is capped at "30+ minutes".
const ETA_MAX_MINUTES = 30

// Seconds until credit from backend evidence. Non-positive, non-numeric and
// absent values mean "no estimate", never "0 seconds".
export function parseEtaSeconds(value: unknown): number | null {
  const n = typeof value === 'number'
    ? value
    : typeof value === 'string' && value.trim() !== '' ? Number(value) : Number.NaN
  if (!Number.isFinite(n) || n <= 0) return null
  return n
}

// Friendly duration only ("35 秒", "3 分钟", "30+ 分钟"); the surrounding copy
// adds "约" / "预计". Null means no estimate should be shown at all.
export function formatEtaDuration(seconds: number | null | undefined, t: DepositTranslate): string | null {
  const value = parseEtaSeconds(seconds)
  if (value === null) return null
  if (value < ETA_SECONDS_BELOW_MINUTES) return t('deposit.eta.seconds', { n: Math.round(value) })
  if (value > ETA_MAX_MINUTES * 60) return t('deposit.eta.overThirtyMinutes')
  return t('deposit.eta.minutes', { n: Math.max(1, Math.round(value / 60)) })
}

export interface PrunedNetworkStatuses {
  rows: DepositNetworkStatus[]
  // Earliest moment one of the kept rows stops being evidence, so the caller
  // can drop it locally without waiting for another read. Null when none of
  // them expires.
  nextExpiry: number | null
}

// Keep only rows the backend still vouches for. A row without an expiry is only
// trustworthy when it is an explicit "unknown"; a confirmed state without an
// age would be stale evidence dressed up as fresh.
export function pruneNetworkStatuses(
  rows: readonly DepositNetworkStatus[],
  now: number
): PrunedNetworkStatuses {
  let nextExpiry: number | null = null
  const kept: DepositNetworkStatus[] = []
  for (const row of rows) {
    if (!row || typeof row.chain !== 'string' || !row.chain) continue
    if (row.expires_at === undefined || row.expires_at === null) {
      if (row.status === 'unknown') kept.push(row)
      continue
    }
    // An unparsable expiration is not evidence of anything: fall back to unknown.
    const expiry = Date.parse(row.expires_at)
    if (!(expiry > now)) continue
    nextExpiry = nextExpiry === null ? expiry : Math.min(nextExpiry, expiry)
    kept.push(row)
  }
  return { rows: kept, nextExpiry }
}

// A chain missing from the fresh set has no current evidence; that is
// "checking", not "available" and not "delayed".
export function networkStateOf(
  rows: ReadonlyMap<string, DepositNetworkStatus>,
  chain: string
): DepositNetworkState {
  return rows.get(chain)?.status ?? 'unknown'
}

// Badge copy. The ETA is part of the badge because it is the answer users came
// for; an instant-credit chain says so instead of quoting a number.
export function networkBadge(view: DepositNetworkView, t: DepositTranslate): DepositNetworkBadge {
  const { state, instantCredit } = view
  const eta = formatEtaDuration(view.etaSeconds, t)
  switch (state) {
    case 'available':
      if (instantCredit) return { color: 'success', text: t('deposit.networkStatusDetail.availableInstant') }
      if (eta) return { color: 'success', text: t('deposit.networkStatusDetail.availableEta', { time: eta }) }
      return { color: 'success', text: t('deposit.networkStatus.available') }
    case 'slow':
      if (eta) return { color: 'neutral', text: t('deposit.networkStatusDetail.slowEta', { time: eta }) }
      return { color: 'neutral', text: t('deposit.networkStatus.slow') }
    case 'delayed':
      if (eta) return { color: 'warning', text: t('deposit.networkStatusDetail.delayedEta', { time: eta }) }
      return { color: 'warning', text: t('deposit.networkStatus.delayed') }
    case 'paused':
      return { color: 'warning', text: t('deposit.networkStatus.paused') }
    case 'stalled':
      return { color: 'warning', text: t('deposit.networkStatus.stalled') }
    case 'unknown':
    default:
      return { color: 'neutral', text: t('deposit.networkStatus.unknown') }
  }
}

// A network that cannot take a transfer has no arrival estimate to show.
function estimatesTransfer(state: DepositNetworkState): boolean {
  return state !== 'unknown' && state !== 'paused' && state !== 'stalled'
}

// Line under each network card: "预计 35 秒到账", or "即时到账" for a chain that
// credits on detection.
export function cardEtaText(view: DepositNetworkView, t: DepositTranslate): string | null {
  if (!estimatesTransfer(view.state)) return null
  if (view.instantCredit && view.state === 'available') return t('deposit.eta.cardInstant')
  const eta = formatEtaDuration(view.etaSeconds, t)
  return eta ? t('deposit.eta.card', { time: eta }) : null
}

// Top notice for a network that is taking transfers: the arrival estimate, or
// the instant-credit rule when the chain credits on detection and can roll that
// credit back if the transaction ultimately fails.
//
// Problem states (slow/delayed/paused/stalled) carry their own notice with the
// same timing information, so this stays quiet for them instead of stacking two
// banners about the same transfer.
export function etaNoticeText(view: DepositNetworkView, t: DepositTranslate): string | null {
  if (view.state !== 'available') return null
  if (view.instantCredit) return t('deposit.instantCreditNotice')
  const eta = formatEtaDuration(view.etaSeconds, t)
  return eta ? t('deposit.eta.notice', { time: eta }) : null
}
