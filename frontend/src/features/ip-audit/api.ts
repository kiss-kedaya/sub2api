import { apiClient } from '@/api/client'
export interface AuditSource { id: string; scope: string; storage: string; enumeration: string; can_release: boolean; manage_url?: string }
export interface AuditEntry {
  check_ip?: string; id: string; source: string; origin_source?: string; network: string; scope: string; reason: string; state: string; effective: string
  duration?: string; manual: boolean; can_release: boolean; started_at: number | null; expires_at: number | null
  count: number | null; threshold: number | null; version?: string; user_id?: number; actor_id?: number; result?: string
}
export interface AuditQuery { source: string; ip?: string; state?: string; sort?: string; start?: number; end?: number; user_id?: number; cursor: number; limit: number }
export interface AuditPage { items: AuditEntry[]; sources: AuditSource[]; has_more: boolean; next_cursor: number; partial?: boolean; unrestricted: false }
export interface ReleaseResult { released: boolean; readback: 'released' | 'still_present' | 'unavailable'; remaining: AuditEntry[]; unknown_sources: string[]; unrestricted: false }
export async function listIPAudit(params: AuditQuery, signal?: AbortSignal) { return (await apiClient.get<AuditPage>('/admin/settings/ip-audit', { params, signal })).data }
export async function releaseIPAudit(entry: AuditEntry, reason: string) {
  return (await apiClient.post<ReleaseResult>('/admin/settings/ip-audit/release', { source: entry.source, id: entry.id, network: entry.network, check_ip: entry.check_ip, version: entry.version, user_id: entry.user_id, confirm: true, reason })).data
}
export type AuditTab = 'events' | 'config' | 'ip'
export const auditTab = (value: unknown): AuditTab => value === 'ip' || value === 'config' ? value : 'events'
