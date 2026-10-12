import { apiClient } from '@/api/client'

// Standalone crypto deposit API (independent of /purchase).

export interface DepositChain {
  chain: string
  address: string
  token: string
}

export interface DepositInfo {
  enabled: boolean
  chains: DepositChain[]
  rate: number
  min_amount: number
}

export interface DepositRecord {
  chain: string
  tx_hash: string
  amount: string
  status: string
  created_at: string
  credited: number
}

export async function getDepositInfo(): Promise<DepositInfo> {
  const { data } = await apiClient.get('/crypto/deposit-info')
  return data.data
}

export async function getDepositHistory(): Promise<DepositRecord[]> {
  const { data } = await apiClient.get('/crypto/deposit-history')
  return data.data ?? []
}
