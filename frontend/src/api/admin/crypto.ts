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

export async function setAddressStatus(id: number, status: string): Promise<void> {
  await apiClient.post(`/admin/crypto/addresses/${id}/status`, { status })
}

export async function getSweepPlan(): Promise<SweepPlan> {
  const { data } = await apiClient.get('/admin/crypto/sweep/plan')
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
