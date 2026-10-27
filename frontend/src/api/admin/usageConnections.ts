import { apiClient } from '../client'
import type { RequestTiming } from './usageTiming'
export type ConnectionState = 'completed' | 'client_disconnected' | 'downstream_write_failed' | 'failed' | 'incomplete' | 'canceled' | 'unknown' | 'edge_completed' | 'edge_failed'
export interface ConnectionTrace {
  error_log_id: number; error_type: string; error_status: number
  trace_id: string; request_id: string; client_request_id: string
  source: 'application' | 'caddy' | 'nginx'; host: string; scheme: string; method: string; path: string; http_version: string; tls_version: string
  upstream_status: string; network_error: string
  user_id: number; api_key_id: number; account_id: number; group_id: number
  user_name?: string; user_email: string; api_key_name: string; account_name: string; group_name: string
  model: string; client_ip: string; user_agent: string; stream: boolean; started_at: string
  state: ConnectionState; legacy: boolean; detail: RequestTiming
}
export interface ConnectionQuery {
  start_time?: string; end_time?: string; user_id?: number; api_key_id?: number; account_id?: number; group_id?: number
  model?: string; request_id?: string; client_ip?: string; host?: string; source?: string; state?: string; page?: number; page_size?: number
}
export async function listConnections(params: ConnectionQuery, signal?: AbortSignal): Promise<{items: ConnectionTrace[]; has_more: boolean; page: number; page_size: number; retention_days: number}> {
  const { data } = await apiClient.get('/admin/usage/connections', { params, signal })
  return data
}
