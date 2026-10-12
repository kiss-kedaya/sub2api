import { apiClient } from '@/api/client'

// Crypto deposit channel admin API. The channel is additive and gated by
// the server-side config; the UI simply reflects what the API returns.

export interface CryptoConfig {
  enabled: boolean
  chains: string[]
  rpc: Record<string, string>
  sweep_global_threshold: number
  sweep_min_per_address: Record<string, number>
  gas_topup: Record<string, number>
  hot_wallet_evm: string
  hot_wallet_solana: string
  hot_index: number
  sweep_auto: boolean
  confirmations: Record<string, number>
  credit_min_amount: number
  credit_multiplier: number
  signer_socket: string
}

export interface DepositAddress {
  id: number
  user_id: number
  family: string
  address: string
  status: string
  credited_total: number
  swept_total: number
  deposit_count: number
}

export interface SweepRecord {
  id: number
  chain: string
  from_address: string
  to_address: string
  token: string
  amount: string
  gas_tx_hash: string
  sweep_tx_hash: string
  status: string
  error: string
  created_at: string
}

export interface DepositRecord {
  id: number
  chain: string
  tx_hash: string
  log_index: number
  from_address: string
  to_address: string
  token: string
  amount: string
  status: string
  created_at: string
}

export interface SweepPlan {
  Entries: Array<{
    Chain: string
    Address: string
    Token: string
    AmountDecimal: number
  }> | null
  TotalUSD: number
  DryRun: boolean
}

export interface SweepEntry {
  chain: string
  address: string
  amount: string
  gas_tx_hash: string
  sweep_tx_hash: string
  status: string
  error?: string
}

export interface SweepSummary {
  dry_run: boolean
  planned: number
  swept: number
  failed: number
  gas_topups: number
  entries: SweepEntry[]
}

export interface SweepEntry {
  chain: string
  address: string
  amount: string
  gas_tx_hash: string
  sweep_tx_hash: string
  status: string
  error?: string
}

export interface SweepSummary {
  dry_run: boolean
  planned: number
  swept: number
  failed: number
  gas_topups: number
  entries: SweepEntry[]
}

export async function getCryptoConfig(): Promise<CryptoConfig> {
  const { data } = await apiClient.get('/admin/crypto/config')
  return data.data
}

export async function updateCryptoConfig(cfg: CryptoConfig): Promise<void> {
  await apiClient.put('/admin/crypto/config', cfg)
}

export async function listDepositAddresses(userId: number): Promise<DepositAddress[]> {
  const { data } = await apiClient.get('/admin/crypto/addresses', { params: { user_id: userId } })
  return data.data ?? []
}

// listAllAddresses returns every address with aggregated credited/swept
// totals — the operator's main view.
export async function listAllAddresses(page = 1, pageSize = 50): Promise<{ items: DepositAddress[]; total: number }> {
  const { data } = await apiClient.get('/admin/crypto/addresses', {
    params: { page, page_size: pageSize }
  })
  return { items: data.data?.items ?? [], total: data.data?.total ?? 0 }
}



export async function setAddressStatus(id: number, status: string): Promise<void> {
  await apiClient.post(`/admin/crypto/addresses/${id}/status`, { status })
}

export async function getSweepPlan(): Promise<SweepPlan> {
  const { data } = await apiClient.get('/admin/crypto/sweep/plan')
  return data.data
}

// runSweep executes the sweep. dryRun defaults to true server-side; the
// caller must pass dryRun=false to move funds.
export async function runSweep(dryRun: boolean): Promise<SweepSummary> {
  const { data } = await apiClient.post('/admin/crypto/sweep/run', null, {
    params: { dry_run: dryRun ? '1' : '0' }
  })
  return data.data
}



export async function listSweepRecords(chain?: string): Promise<SweepRecord[]> {
  const { data } = await apiClient.get('/admin/crypto/sweep/records', { params: { chain } })
  return data.data ?? []
}

export async function listDeposits(chain?: string, status?: string): Promise<DepositRecord[]> {
  const { data } = await apiClient.get('/admin/crypto/deposits', { params: { chain, status } })
  return data.data ?? []
}
