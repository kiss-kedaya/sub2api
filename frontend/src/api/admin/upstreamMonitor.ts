import { apiClient } from '../client'

export type UpstreamBalanceStatus = 'ok' | 'stale' | 'failed' | 'unsupported' | 'unknown'

export interface UpstreamMonitorAccount {
  account_id: number
  name: string
  platform: string
  type: string
  status: string
  balance: number | null
  unlimited: boolean
  unit: string
  balance_status: UpstreamBalanceStatus
  balance_read_at: string | null
  balance_fresh_until: string | null
  balance_error?: string
  rate: number | null
  rate_status?: string
  rate_synced?: number | null
  rate_read_at: string | null
  rate_error?: string
  rate_sync_enabled: boolean
  last_sampled_at?: string | null
}

export interface UpstreamCurrencyTotal {
  unit: string
  total: number
  accounts: number
}

export interface UpstreamMonitorOverview {
  generated_at: string
  total_accounts: number
  with_balance: number
  healthy: number
  stale: number
  failed: number
  unsupported: number
  rate_known: number
  rate_changes_24h: number
  currencies: UpstreamCurrencyTotal[]
  attention: UpstreamMonitorAccount[]
  last_sample_at?: string | null
}

export interface UpstreamMonitorPoint {
  balance: number
  unit: string
  sampled_at: string
}

export interface UpstreamRateChange {
  id: number
  account_id: number
  name: string
  old_rate: number
  new_rate: number
  changed_at: string
}

export interface PageResult<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface AccountQuery {
  page: number
  pageSize: number
  search?: string
  balanceStatus?: string
  platform?: string
  sort?: string
}

export interface RateQuery {
  page: number
  pageSize: number
  days?: number
  accountId?: number
}

export async function getOverview(): Promise<UpstreamMonitorOverview> {
  const { data } = await apiClient.get<UpstreamMonitorOverview>('/admin/upstream-monitor/overview')
  return data
}

export async function listAccounts(query: AccountQuery): Promise<PageResult<UpstreamMonitorAccount>> {
  const { data } = await apiClient.get<PageResult<UpstreamMonitorAccount>>('/admin/upstream-monitor/accounts', {
    params: {
      page: query.page,
      page_size: query.pageSize,
      search: query.search || undefined,
      balance_status: query.balanceStatus || undefined,
      platform: query.platform || undefined,
      sort: query.sort || undefined,
    },
  })
  return data
}

export async function getHistory(accountId: number, days = 7): Promise<{ account_id: number; days: number; points: UpstreamMonitorPoint[] }> {
  const { data } = await apiClient.get<{ account_id: number; days: number; points: UpstreamMonitorPoint[] }>(
    `/admin/upstream-monitor/accounts/${accountId}/history`,
    { params: { days } }
  )
  return data
}

export async function listRateChanges(query: RateQuery): Promise<PageResult<UpstreamRateChange>> {
  const { data } = await apiClient.get<PageResult<UpstreamRateChange>>('/admin/upstream-monitor/rate-changes', {
    params: {
      page: query.page,
      page_size: query.pageSize,
      days: query.days ?? 7,
      account_id: query.accountId || undefined,
    },
  })
  return data
}

export async function sampleNow(): Promise<{ scanned: number; sampled: number; rate_changes: number }> {
  const { data } = await apiClient.post<{ scanned: number; sampled: number; rate_changes: number }>(
    '/admin/upstream-monitor/sample'
  )
  return data
}
