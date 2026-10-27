import { apiClient } from '../client'
export interface OAuthDefaults { enabled: boolean; platform: string; concurrency: number; priority: number; load_factor: number; group_ids: number[] }
export interface OpsAutomationConfig {
  revision: string; updated_at: string; oauth: OAuthDefaults[]; ramp_suspended?: boolean
  ramp: { enabled: boolean; group_ids: number[]; successes_per_step: number; step: number; maximum: number; cooldown_seconds: number }
  ranking: { enabled: boolean; group_ids: number[]; mode: string; window_minutes: number; minimum_samples: number; quality_weight: number; latency_weight: number; cost_weight: number }
}
export interface LocalReasoningPlan { id: number; account_id: number; model: string; enabled: boolean; interval_minutes: number; action: 'observe' | 'pause'; auto_restore: boolean; revision: number; last_outcome: string; next_run_at: string; judge_kind?: 'local' | 'llm' | 'jev'; judge_group_id?: number; judge_model?: string }
export interface LocalOwnership { plan_id: number; account_id: number; owned: boolean; can_restore: boolean; fence: string }
export const getAutomation = async () => (await apiClient.get<OpsAutomationConfig>('/admin/ops-automation')).data
export const saveAutomation = async (config: OpsAutomationConfig) => (await apiClient.put<OpsAutomationConfig>('/admin/ops-automation', config)).data
export const getReasoningPlans = async () => (await apiClient.get<LocalReasoningPlan[]>('/admin/ops-automation/reasoning')).data
export const saveReasoningPlan = async (plan: LocalReasoningPlan) => (await apiClient.put<LocalReasoningPlan>('/admin/ops-automation/reasoning', plan)).data
export const runReasoningPlan = async (id: number) => (await apiClient.post(`/admin/ops-automation/reasoning/${id}/run`)).data
export const getReasoningOwnership = async (id: number) => (await apiClient.get<LocalOwnership>(`/admin/ops-automation/reasoning/${id}/ownership`)).data
export const updateReasoningOwnership = async (id: number, fence: string, restore: boolean, remove: boolean) => (await apiClient.post<LocalOwnership>(`/admin/ops-automation/reasoning/${id}/ownership`, { fence, confirm: true, restore, delete: remove })).data
