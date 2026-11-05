import { apiClient } from '../client'

export type UpstreamBalanceStatus = 'ok' | 'stale' | 'failed' | 'unsupported' | 'unknown'

export interface UpstreamMonitorAccount {
  account_id: number
  name: string
  notes?: string
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
  alert_threshold?: number | null
  alert_enabled?: boolean
}

export interface UpstreamCurrencyTotal {
  unit: string
  total: number
  accounts: number
}

export interface UpstreamSpendTotal {
  unit: string
  today: number
  last_7_days: number
  last_30_days: number
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
  spend: UpstreamSpendTotal[]
  open_alerts: number
}

export interface UpstreamMonitorPoint {
  balance: number
  unit: string
  sampled_at: string
}

export interface UpstreamTrendPoint {
  day: string
  unit: string
  spend: number
  balance_close?: number | null
  accounts: number
}

export interface UpstreamRateChange {
  id: number
  account_id: number
  name: string
  old_rate: number
  new_rate: number
  change_pct: number
  changed_at: string
}

export type UpstreamAlertKind = 'balance_low' | 'balance_depleted' | 'rate_jump'

export interface UpstreamAlert {
  id: number
  account_id: number
  name: string
  kind: UpstreamAlertKind
  level: 'warning' | 'critical' | string
  balance?: number | null
  threshold?: number | null
  unit: string
  old_rate?: number | null
  new_rate?: number | null
  message: string
  created_at: string
  acked_at?: string | null
}

export interface UpstreamAlertRule {
  account_id: number
  threshold: number
  unit: string
  rate_min_pct: number
  enabled: boolean
  updated_at: string
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
  minPct?: number
}

export interface AlertQuery {
  page: number
  pageSize: number
  days?: number
  status?: string
  kind?: string
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

export async function getTrend(days = 7): Promise<{ days: number; points: UpstreamTrendPoint[] }> {
  const { data } = await apiClient.get<{ days: number; points: UpstreamTrendPoint[] }>('/admin/upstream-monitor/trend', {
    params: { days },
  })
  return data
}

export async function listRateChanges(query: RateQuery): Promise<PageResult<UpstreamRateChange>> {
  const { data } = await apiClient.get<PageResult<UpstreamRateChange>>('/admin/upstream-monitor/rate-changes', {
    params: {
      page: query.page,
      page_size: query.pageSize,
      days: query.days ?? 7,
      account_id: query.accountId || undefined,
      min_pct: query.minPct || undefined,
    },
  })
  return data
}

export async function listAlerts(query: AlertQuery): Promise<PageResult<UpstreamAlert>> {
  const { data } = await apiClient.get<PageResult<UpstreamAlert>>('/admin/upstream-monitor/alerts', {
    params: {
      page: query.page,
      page_size: query.pageSize,
      days: query.days ?? 7,
      status: query.status && query.status !== 'all' ? query.status : undefined,
      kind: query.kind || undefined,
      account_id: query.accountId || undefined,
    },
  })
  return data
}

export async function ackAlert(id: number): Promise<{ id: number; acked: boolean }> {
  const { data } = await apiClient.post<{ id: number; acked: boolean }>(`/admin/upstream-monitor/alerts/${id}/ack`)
  return data
}

export async function listAlertRules(): Promise<{ items: UpstreamAlertRule[] }> {
  const { data } = await apiClient.get<{ items: UpstreamAlertRule[] }>('/admin/upstream-monitor/alert-rules')
  return data
}

export async function saveAlertRule(rule: {
  account_id: number
  threshold: number
  unit: string
  rate_min_pct: number
  enabled: boolean
}): Promise<UpstreamAlertRule> {
  const { data } = await apiClient.put<UpstreamAlertRule>('/admin/upstream-monitor/alert-rules', rule)
  return data
}

export async function deleteAlertRule(accountId: number): Promise<{ account_id: number; deleted: boolean }> {
  const { data } = await apiClient.delete<{ account_id: number; deleted: boolean }>(`/admin/upstream-monitor/alert-rules/${accountId}`)
  return data
}

export async function exportMonitorCsv(query: Omit<AccountQuery, 'page' | 'pageSize'>): Promise<Blob> {
  const { data } = await apiClient.get('/admin/upstream-monitor/export', {
    params: {
      search: query.search || undefined,
      balance_status: query.balanceStatus || undefined,
      platform: query.platform || undefined,
      sort: query.sort || undefined,
    },
    responseType: 'blob',
  })
  return data as Blob
}

export async function sampleNow(): Promise<{ scanned: number; sampled: number; rate_changes: number; alerts: number }> {
  const { data } = await apiClient.post<{ scanned: number; sampled: number; rate_changes: number; alerts: number }>(
    '/admin/upstream-monitor/sample'
  )
  return data
}
