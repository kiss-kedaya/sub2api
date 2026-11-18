import { apiClient } from '../client'

const base = '/admin/request-captures/small'
export interface SmallUsage {
  request_id: string; user_id: number; api_key_id: number; account_id: number; group_id: number
  platform: string; model: string; upstream_model: string; stream: boolean
  input_tokens: number; output_tokens: number; cache_read_tokens: number; cache_creation_tokens: number
  actual_cost: string; calculated_cost: string; settlement: string; duration_ms: number
}
export interface SmallRecord {
  id: number; bucket: string; user_id: number; api_key_id: number; model: string; status: number
  first_seen: string; last_seen: string; expires_at: string; repeat_count: number
  input_tokens: number; output_tokens: number; cache_read_tokens: number; cache_creation_tokens: number
  actual_cost: string; raw_bytes: number; omitted_responses: number; request_ids: string[]
  data: { method: string; path: string; group_id: number; replay_reasons: string[] | null; first_usage?: SmallUsage }
}
export interface SmallStats {
  ready: boolean; records: number; requests: number; duplicates: number
  raw_bytes: number; stored_bytes: number; stored_limit_bytes: number; quota_full: boolean; saved_bytes: number
  oldest: string | null; newest: string | null; repeat_distribution: Record<string, number>; skipped: Record<string, number>
}
export interface SmallDetail {
  record: SmallRecord; request_headers: Record<string, string[]>; request_body: string; curl: string
  responses: Array<{ fingerprint: string; first_seen: string; last_seen: string; repeat_count: number
    data: { status: number; usage?: SmallUsage; partial: boolean; replay_reasons?: string[] }; headers: Record<string, string[]>; body: string }>
}
export interface SmallFilter {
  user_id?: number; api_key_id?: number; model?: string; request_id?: string
  status?: number; min_repeats?: number; from?: string; to?: string; page?: number; page_size?: number
}
export async function getSmallStats() { return (await apiClient.get<SmallStats>(base + '/stats')).data }
export async function listSmallRecords(params: SmallFilter) { return (await apiClient.get<{ items: SmallRecord[]; has_more: boolean }>(base, { params })).data }
export async function getSmallDetail(id: number) { return (await apiClient.get<SmallDetail>(base + '/' + id)).data }
export async function clearSmallRecords() { await apiClient.delete(base) }
