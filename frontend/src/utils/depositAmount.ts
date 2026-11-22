import type { DepositInfo } from '@/api/crypto'

/**
 * Deposit amount pre-check for the on-chain deposit page.
 *
 * The backend only credits a transfer whose amount is inside
 * `credit_min_amount` / `credit_max_amount`; anything outside is recorded as
 * "ignored" and never reaches the balance. The user cannot undo a transfer once
 * it left the wallet, so the page checks the amount the user is about to send
 * *before* the money moves and blocks the transfer action while it is outside
 * the configured range.
 *
 * Thresholds come from `deposit-info`. Older deployments omit them, and for an
 * asset that settles at the market price the quote can be missing too: in both
 * cases the check degrades to "no opinion" instead of inventing numbers.
 */

export interface DepositAmountLimits {
  /** Minimum creditable amount in the limit unit. Null means "not enforced". */
  min: number | null
  /** Maximum creditable amount in the limit unit. Null means "not enforced". */
  max: number | null
}

/**
 * `unverifiable` is not a failure: the amount may be fine, but its USD notional
 * cannot be derived (no market quote), so the page must neither block nor warn.
 */
export type DepositAmountState = 'empty' | 'invalid' | 'below_min' | 'above_max' | 'ok' | 'unverifiable'

export interface DepositAmountCheck {
  state: DepositAmountState
  /** Parsed amount exactly as the user typed it (token amount). Null unless numeric. */
  amount: number | null
  /** USD notional for a market-priced asset. Null when it cannot be priced. */
  notional: number | null
  limits: DepositAmountLimits
  /** True only when the user explicitly typed something that cannot be credited. */
  blocked: boolean
}

export interface DepositAmountContext {
  /** Market-priced assets compare USD notional, not coin amount. */
  marketPriced?: boolean
  /** Display-only market quote, used to derive the notional. */
  priceUsd?: number | null
}

// Backends spell a numeric field as a JSON number or (via some encoders) a
// string; anything unparsable is treated as absent rather than as zero.
function readLimit(value: unknown): number | null | undefined {
  if (value === null || value === undefined) return undefined
  const n = typeof value === 'number' ? value : typeof value === 'string' && value.trim() !== '' ? Number(value) : Number.NaN
  if (!Number.isFinite(n)) return undefined
  // A non-positive threshold disables the check; it must not reject everything.
  return n > 0 ? n : null
}

// Explicit `credit_*` fields win when the backend sends them. Otherwise the
// existing `min_amount` / `max_amount` are the same credit thresholds, so an
// older response is still validated. Present-but-disabled fields do not fall
// back: an explicit 0 means "no limit", not "use the legacy field".
function normalizeLimits(minimum: number | null | undefined, maximum: number | null | undefined): DepositAmountLimits {
  return { min: minimum ?? null, max: maximum ?? null }
}

/**
 * Credit thresholds advertised by deposit-info. Every field is optional on the
 * wire; when none is usable the page performs no validation at all.
 */
export function depositAmountLimits(info: DepositInfo | null | undefined): DepositAmountLimits {
  if (!info) return { min: null, max: null }
  const explicitMin = readLimit(info.credit_min_amount)
  const explicitMax = readLimit(info.credit_max_amount)
  const legacyMin = readLimit(info.min_amount)
  const legacyMax = readLimit(info.max_amount)
  return normalizeLimits(
    explicitMin !== undefined ? explicitMin : legacyMin,
    explicitMax !== undefined ? explicitMax : legacyMax
  )
}

const DECIMAL_INPUT = /^(?:\d+(?:\.\d*)?|\.\d+)$/

/**
 * Parse the raw input into a positive decimal. Commas and spaces are common
 * when users paste an amount, so they are tolerated; anything else (signs,
 * exponents, hex) is invalid rather than silently coerced.
 */
export function parseDepositAmount(raw: string): number | null {
  const normalized = (raw ?? '').replace(/[\s,]/g, '')
  if (!DECIMAL_INPUT.test(normalized)) return null
  const amount = Number(normalized)
  return Number.isFinite(amount) && amount > 0 ? amount : null
}

/**
 * Validate the amount the user is about to transfer.
 *
 * `marketPriced` assets are compared by USD notional (amount × reference
 * price), because the backend credits them at the market price observed when
 * they land. Without a quote the notional is unknown → `unverifiable`.
 */
export function validateDepositAmount(
  raw: string,
  limits: DepositAmountLimits,
  context: DepositAmountContext = {}
): DepositAmountCheck {
  const base = { amount: null, notional: null, limits, blocked: false } as const
  if (!raw || raw.trim() === '') return { ...base, state: 'empty' }

  const amount = parseDepositAmount(raw)
  if (amount === null) return { ...base, state: 'invalid', blocked: true }

  let compared = amount
  let notional: number | null = null
  if (context.marketPriced) {
    const price = context.priceUsd
    if (typeof price !== 'number' || !Number.isFinite(price) || price <= 0) {
      return { ...base, state: 'unverifiable', amount, notional: null }
    }
    notional = amount * price
    compared = notional
  }

  if (limits.min !== null && compared < limits.min) {
    return { ...base, state: 'below_min', amount, notional, blocked: true }
  }
  if (limits.max !== null && compared > limits.max) {
    return { ...base, state: 'above_max', amount, notional, blocked: true }
  }
  return { ...base, state: 'ok', amount, notional }
}

/**
 * Balance units the transfer would credit at the current fixed rate. Returns
 * null when there is no usable rate (market-priced assets have none), so the
 * page never prints a made-up estimate.
 */
export function estimatedDepositCredit(amount: number | null, rate: number | null | undefined): number | null {
  if (typeof amount !== 'number' || !Number.isFinite(amount) || amount <= 0) return null
  if (typeof rate !== 'number' || !Number.isFinite(rate) || rate <= 0) return null
  return Number((amount * rate).toFixed(8))
}
