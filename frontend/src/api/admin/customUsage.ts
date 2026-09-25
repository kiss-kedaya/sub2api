import { apiClient } from '../client'

export type CustomUsageTemplate = 'custom' | 'general' | 'newapi' | 'sub2api'
export interface NumericExtractor { path: string; divisor?: number }
export interface TextExtractor { path?: string; value?: string }
export interface CustomUsageExtractor {
  remaining?: NumericExtractor
  used?: NumericExtractor
  total?: NumericExtractor
  unit?: TextExtractor
  plan_name?: TextExtractor
}
export interface CustomUsageConfig {
  enabled: boolean
  template: CustomUsageTemplate
  base_url: string
  api_key?: string
  access_token?: string
  user_id?: string
  timeout_seconds: number
  interval_minutes: number
  request: { url: string; method: 'GET'; headers: Record<string, string> }
  extractor: CustomUsageExtractor
  configured?: boolean
  has_api_key?: boolean
  has_access_token?: boolean
  clear_api_key?: boolean
  clear_access_token?: boolean
}
export interface CustomUsageResult {
  interval_minutes?: number
  enabled: boolean
  configured: boolean
  remaining?: number
  used?: number
  total?: number
  unit: string
  plan_name?: string
  updated_at?: string
  error?: string
  stale?: boolean
}
export interface CustomUsageBatch { items: Record<string, CustomUsageResult> }
const endpoint = (id: number) => '/admin/accounts/' + id

export async function getConfig(id: number, signal?: AbortSignal): Promise<CustomUsageConfig> {
  const { data } = await apiClient.get<CustomUsageConfig>(endpoint(id) + '/custom-usage-config', { signal })
  return data
}
export async function saveConfig(id: number, config: CustomUsageConfig, signal?: AbortSignal): Promise<CustomUsageConfig> {
  const { data } = await apiClient.put<CustomUsageConfig>(endpoint(id) + '/custom-usage-config', config, { signal })
  return data
}
export async function queryUsage(id: number, body: { force?: boolean; config?: CustomUsageConfig } = {}, signal?: AbortSignal): Promise<CustomUsageResult> {
  // The upstream timeout may be 30 seconds; leave room for the admin handler.
  const { data } = await apiClient.post<CustomUsageResult>(endpoint(id) + '/custom-usage-query', body, { signal, timeout: 35000 })
  return data
}
export async function getCachedUsage(accountIds: number[], signal?: AbortSignal): Promise<CustomUsageBatch> {
  const ids = [...new Set(accountIds)]
  if (ids.length > 50 || ids.some(id => !Number.isSafeInteger(id) || id <= 0)) throw new Error('Invalid account batch')
  if (!ids.length) return { items: {} }
  const { data } = await apiClient.post<CustomUsageBatch>('/admin/accounts/custom-usage-batch', { account_ids: ids }, { signal })
  return data
}
