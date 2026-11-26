/**
 * Shared model for a record / ledger row.
 *
 * One row is always "type icon + title/subtitle + signed amount". The amount
 * semantics live here instead of in each page, so money in, money out and a
 * rolled-back credit look the same wherever a record list is rendered:
 *
 *   positive → money arrived (green, "+")
 *   negative → money left the account (ink, "-")
 *   reversal → an earlier credit was taken back (rose, "-")
 *   pending  → not settled yet (muted, no sign)
 *   neutral  → no balance effect (muted, no sign)
 *
 * Colours come from the `amount-*` / `icon-orb-*` utilities in src/style.css,
 * which read the `--app-amount-*` tokens, so a page never hand-picks a colour.
 */

export type TransactionTone = 'positive' | 'negative' | 'reversal' | 'pending' | 'neutral'

/** Record families an icon can be picked for without the caller naming one. */
export type TransactionType =
  | 'deposit'
  | 'chain'
  | 'exchange'
  | 'reversal'
  | 'subscription'
  | 'usage'
  | 'adjustment'
  | 'refund'
  | 'other'

export type TransactionStatusColor =
  | 'primary'
  | 'secondary'
  | 'success'
  | 'info'
  | 'warning'
  | 'error'
  | 'neutral'

export interface TransactionStatus {
  label: string
  color?: TransactionStatusColor
  /** `data-testid` for page-level specs that assert on the status copy. */
  testId?: string
}

export interface TransactionItem {
  /** Stable key for the list; an index is not enough when rows refresh. */
  id: string | number
  title: string
  subtitle?: string
  /** Third, quieter line (hash, note, source reference). */
  meta?: string
  /** UIcon / Iconify name. Falls back to the icon mapped from `type`. */
  icon?: string
  type?: TransactionType
  /** Amount text or number. Numbers are rendered as their absolute value. */
  amount?: string | number
  /** e.g. "$", "USDT ". */
  amountPrefix?: string
  /** e.g. " USDT". */
  amountSuffix?: string
  /** Overrides the sign derived from the tone. An explicit "" means no sign. */
  amountSign?: '+' | '-' | ''
  /** Overrides the tone derived from the amount's sign. */
  tone?: TransactionTone
  status?: TransactionStatus
  /** Highlights and animates a newly arrived record when the list animates updates. */
  isNew?: boolean
  /** `data-testid` for the row element. */
  testId?: string
}

const TYPE_ICONS: Record<TransactionType, string> = {
  deposit: 'i-lucide-arrow-down-left',
  chain: 'i-lucide-link-2',
  exchange: 'i-lucide-repeat-2',
  reversal: 'i-lucide-undo-2',
  subscription: 'i-lucide-badge-check',
  usage: 'i-lucide-zap',
  adjustment: 'i-lucide-wand-2',
  refund: 'i-lucide-rotate-ccw',
  other: 'i-lucide-circle-dot'
}

const TONE_AMOUNT_CLASS: Record<TransactionTone, string> = {
  positive: 'amount-positive',
  negative: 'amount-negative',
  reversal: 'amount-reversal',
  pending: 'amount-pending',
  neutral: 'amount-pending'
}

const TONE_ORB_CLASS: Record<TransactionTone, string> = {
  positive: 'icon-orb-positive',
  negative: 'icon-orb-negative',
  reversal: 'icon-orb-reversal',
  pending: 'icon-orb-pending',
  neutral: 'icon-orb-neutral'
}

const TONE_SIGN: Record<TransactionTone, '+' | '-' | ''> = {
  positive: '+',
  negative: '-',
  reversal: '-',
  pending: '',
  neutral: ''
}

export function transactionIcon(item: TransactionItem): string {
  return item.icon || TYPE_ICONS[item.type ?? 'other'] || TYPE_ICONS.other
}

/** Explicit tone wins; otherwise the numeric sign decides, text defaults neutral. */
export function resolveTransactionTone(item: TransactionItem): TransactionTone {
  if (item.tone) return item.tone
  if (typeof item.amount === 'number' && Number.isFinite(item.amount)) {
    if (item.amount > 0) return 'positive'
    if (item.amount < 0) return 'negative'
  }
  return 'neutral'
}

export function resolveTransactionSign(item: TransactionItem, tone: TransactionTone): '+' | '-' | '' {
  if (item.amountSign !== undefined) return item.amountSign
  return TONE_SIGN[tone]
}

/**
 * Composes the visible amount. Returns null when the row has no trustworthy
 * amount (the list then renders the em dash placeholder instead of inventing a 0).
 */
export function formatTransactionAmount(item: TransactionItem): string | null {
  const raw = item.amount
  if (raw === undefined || raw === null || raw === '') return null

  const tone = resolveTransactionTone(item)
  const sign = resolveTransactionSign(item, tone)
  const value = typeof raw === 'number'
    ? (Number.isFinite(raw) ? Math.abs(raw).toString() : '')
    : raw.replace(/^[+-]\s*/, '').trim()
  if (!value) return null

  return `${sign}${item.amountPrefix ?? ''}${value}${item.amountSuffix ?? ''}`
}

export function transactionAmountClass(item: TransactionItem): string {
  return TONE_AMOUNT_CLASS[resolveTransactionTone(item)]
}

export function transactionOrbClass(item: TransactionItem): string {
  return TONE_ORB_CLASS[resolveTransactionTone(item)]
}

/** Tone names are exposed as `data-tone` so specs never assert on CSS classes. */
export function transactionToneAttribute(item: TransactionItem): TransactionTone {
  return resolveTransactionTone(item)
}
