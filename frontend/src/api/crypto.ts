import { apiClient } from '@/api/client'

// Standalone crypto deposit API (independent of /purchase).

// One asset accepted on a deposit address. `native` marks the chain's mainnet
// coin (ETH on Ethereum, BNB on BSC), which has no token contract.
export interface DepositAsset {
  symbol: string
  native: boolean
}

export interface DepositChain {
  chain: string
  address: string
  tokens: string[]
  // Older backends send only `tokens`; the view then treats every entry as an
  // ERC-20 token so it keeps working unchanged.
  assets?: DepositAsset[]
}

export interface DepositInfo {
  enabled: boolean
  chains: DepositChain[]
  rate: number
  min_amount: number
  max_amount: number
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
}

export async function getDepositInfo(): Promise<DepositInfo> {
  const { data } = await apiClient.get('/crypto/deposit-info')
  return data.data
}

export async function getDepositHistory(): Promise<DepositRecord[]> {
  const { data } = await apiClient.get('/crypto/deposit-history')
  return data.data ?? []
}
