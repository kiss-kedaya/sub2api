import { apiClient } from '@/api/client'

// Standalone crypto deposit API (independent of /purchase).

// One asset accepted on a deposit address. `native` marks the chain's mainnet
// coin (ETH on Ethereum, BNB on BSC), which has no token contract.
export interface DepositAsset {
  symbol: string
  native: boolean
  // True when the asset is credited at the market price observed at credit
  // time (ETH, BNB). The page must not render a fixed rate for it.
  market_priced?: boolean
  // Balance units per whole asset under the fixed rate. Null (or absent) for
  // market-priced assets, where a constant rate would misstate the payout.
  rate?: number | null
  // Display-only market quote. Best effort: absent whenever the price feed is
  // unavailable, and never used to compute a credit.
  reference_price_usd?: string | null
}

export interface DepositChain {
  chain: string
  address: string
  tokens: string[]
  // Older backends send only `tokens`; the view then treats every entry as an
  // ERC-20 token so it keeps working unchanged.
  assets?: DepositAsset[]
  // Seconds until a transfer on this chain is credited. Absent on backends
  // that predate the ETA contract; the page then shows no estimate instead of
  // inventing one.
  eta_seconds?: number | null
  // The chain credits as soon as the transfer is detected, and rolls that
  // credit back if the transaction ultimately fails. Absent means "unknown",
  // which is rendered as the regular confirmed-credit flow.
  instant_credit?: boolean
}

export interface DepositInfo {
  enabled: boolean
  chains: DepositChain[]
  // Deprecated: older backends send one asset-independent rate. New backends
  // send the rate per asset because native coins settle at market price.
  rate?: number
  min_amount: number
  max_amount: number
}

// 'slow' is a moderate, still self-healing backlog; 'delayed' is a real delay.
// 'unknown' is the absence of fresh evidence and must not be shown as an alarm.
const DEPOSIT_NETWORK_STATES = ['available', 'slow', 'delayed', 'paused', 'stalled', 'unknown'] as const

export type DepositNetworkState = (typeof DEPOSIT_NETWORK_STATES)[number]

export interface DepositNetworkStatus {
  chain: string
  status: DepositNetworkState
  observed_at?: string
  expires_at?: string
  // Seconds until the chain's backlog is cleared, when the backend can measure
  // it. Only positive values are meaningful; everything else is "no estimate".
  eta_seconds?: number
  // How far the scanner trails the chain head, in seconds.
  backlog_seconds?: number
  // Confirmations a transfer needs on this chain, for transparency. Zero means
  // the chain credits on detection.
  required_confirmations?: number
}

export interface DepositRecord {
  chain: string
  tx_hash: string
  token: string
  amount: string
  status: string
  reason?: string
  created_at: string
  credited: number
  // False means the backend could not resolve the original settlement amount.
  credited_known?: boolean
  // Confirmation progress. Older backends omit these fields; the page then
  // shows the plain status without a progress line.
  confirmations?: number
  required_confirmations?: number
  block_number?: number
  // Instant-credit state. The backend rewrites `status` to "provisional" while a
  // 0-confirmation credit is still inside the risk window, and to "revoked" once
  // the chain invalidated it and the credit was clawed back. Older backends omit
  // these fields, so they are optional.
  provisional?: boolean
  credited_at?: string
  revoked_at?: string
  revoke_reason?: string
}

export async function getDepositInfo(): Promise<DepositInfo> {
  const { data } = await apiClient.get('/crypto/deposit-info')
  return { ...data.data, chains: Array.isArray(data.data.chains) ? data.data.chains : [] }
}

function isDepositNetworkState(value: unknown): value is DepositNetworkState {
  return DEPOSIT_NETWORK_STATES.some(status => status === value)
}

// Optional numeric evidence. A malformed field must not discard the whole row:
// the status is still usable, only the estimate is lost.
function optionalNumber(value: unknown): number | undefined {
  const n = typeof value === 'number' ? value : typeof value === 'string' && value.trim() !== '' ? Number(value) : Number.NaN
  return Number.isFinite(n) ? n : undefined
}

export async function getDepositNetworkStatus(signal?: AbortSignal): Promise<DepositNetworkStatus[]> {
  const { data } = await apiClient.get<unknown>('/crypto/deposit-network-status', { signal })
  // Crypto's legacy envelope leaves `data` in place; the standard API envelope is
  // already unwrapped by apiClient. A legacy app shell is not status data.
  const payload = data && typeof data === 'object' && 'data' in data ? data.data : data
  if (!payload || typeof payload !== 'object' || !('networks' in payload) || !Array.isArray(payload.networks)) return []

  const networks: unknown[] = payload.networks
  const statuses: DepositNetworkStatus[] = []
  for (const network of networks) {
    if (!network || typeof network !== 'object' || !('chain' in network) ||
        typeof network.chain !== 'string' || !network.chain || !('status' in network) ||
        !isDepositNetworkState(network.status)) continue

    // `null` is how some encoders spell "absent"; both mean no expiry evidence.
    const raw_expires_at = 'expires_at' in network ? network.expires_at : undefined
    if (raw_expires_at !== undefined && raw_expires_at !== null && typeof raw_expires_at !== 'string') continue
    const expires_at = typeof raw_expires_at === 'string' ? raw_expires_at : undefined
    const observed_at = 'observed_at' in network && typeof network.observed_at === 'string' ? network.observed_at : undefined
    statuses.push({
      chain: network.chain,
      status: network.status,
      observed_at,
      expires_at,
      eta_seconds: 'eta_seconds' in network ? optionalNumber(network.eta_seconds) : undefined,
      backlog_seconds: 'backlog_seconds' in network ? optionalNumber(network.backlog_seconds) : undefined,
      required_confirmations: 'required_confirmations' in network ? optionalNumber(network.required_confirmations) : undefined
    })
  }
  return statuses
}

export async function getDepositHistory(): Promise<DepositRecord[]> {
  const { data } = await apiClient.get('/crypto/deposit-history')
  return data.data ?? []
}
