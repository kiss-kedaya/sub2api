import { apiClient } from './client'

export interface PenaltyConfig {
  enabled: boolean; observe_only: boolean; policy: 'session_block' | 'user_block' | 'session_route_then_block'
  threshold: number; window_hours: number; duration_hours: number; target_group_id: number | null
  all_groups: boolean; group_ids: number[]; exempt_user_ids: number[]; version: number; risk_control_enabled: boolean
}
export interface PenaltyState {
  scope: string; subject_key: string; count: number; blocked_until?: string; route_until?: string
  window_started_at: string; action: string; reason: string
}
export interface PenaltySummary { enabled: boolean; observe_only: boolean; policy: string; threshold: number; restricted: boolean; states: PenaltyState[] }
export interface PenaltyEvent { id: number; user_id: number; request_id: string; scope: string; policy: string; reason: string; model: string; source: string; count: number; created_at: string }
export interface PenaltyEventPage { items: PenaltyEvent[]; total: number; page: number; page_size: number }
export const getPenaltyConfig = async () => (await apiClient.get<PenaltyConfig>('/admin/security-penalties/config')).data
export const savePenaltyConfig = async (config: PenaltyConfig) => (await apiClient.put<PenaltyConfig>('/admin/security-penalties/config', config)).data
export const adminPenaltyEvents = async (page: number, userID?: number) => (await apiClient.get<PenaltyEventPage>('/admin/security-penalties/events', { params: { page, page_size: 20, user_id: userID } })).data
export const releasePenalty = async (userID: number, reason: string) => { await apiClient.post('/admin/security-penalties/' + userID + '/release', { reason }) }
export const myPenaltySummary = async () => (await apiClient.get<PenaltySummary>('/user/security/summary')).data
export const myPenaltyEvents = async (page: number) => (await apiClient.get<PenaltyEventPage>('/user/security/events', { params: { page, page_size: 20 } })).data
