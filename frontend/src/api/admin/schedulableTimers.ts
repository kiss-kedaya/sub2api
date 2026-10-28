import { apiClient } from '../client'

export type TimerAction = 'open' | 'close' | 'recurring'
export interface TimerInput {
  mode: 'relative' | 'absolute' | 'recurring'
  hours: number
  minutes: number
  date: string
  time: string
  timezone: string
  weekdays_only?: boolean
  periods?: Array<{ start: string; end: string }>
}
export interface TimerPreview {
  execute_at: string
  action?: 'open' | 'close'
  timezone: string
  local_date: string
  local_time: string
  offset: string
  server_now: string
}
export interface SchedulableTimer {
  account_id: number
  action: TimerAction
  version: number
  execute_at: string
  timezone: string
  local_date?: string
  local_time?: string
  offset?: string
  requested_by: number
  status: 'pending' | 'executed' | 'skipped' | 'cancelled' | 'failed'
  attempts: number
  actual_at: string | null
  reason: string
}
export interface TimerAudit {
  action: TimerAction
  version: number
  actor_id: number
  execute_at: string
  timezone: string
  actual_at: string
  result: string
  reason: string
}
export interface TimerSnapshot {
  tasks: SchedulableTimer[]
  recurring?: RecurringSchedule | null
  history: TimerAudit[]
  server_now: string
  poll_seconds: number
  open_grace_seconds: number
  max_delay_minutes: number
}
export interface RecurringSchedule {
  account_id: number
  version: number
  timezone: string
  weekdays_only: boolean
  periods: Array<{ start: string; end: string }>
  requested_by: number
  enabled: boolean
  updated_at: string
}
export const schedulableTimersAPI = {
  async list(id: number) {
    return (await apiClient.get<TimerSnapshot>(`/admin/accounts/${id}/schedulable-timers`)).data
  },
  async preview(id: number, input: TimerInput) {
    return (await apiClient.post<TimerPreview>(`/admin/accounts/${id}/schedulable-timers/preview`, input)).data
  },
  async save(id: number, input: TimerInput & { action: TimerAction; expected_version: number; confirmed_at?: string }) {
    return (await apiClient.post<SchedulableTimer>(`/admin/accounts/${id}/schedulable-timers`, input)).data
  },
  async cancel(id: number, action: TimerAction, version: number) {
    await apiClient.delete(`/admin/accounts/${id}/schedulable-timers/${action}`, { params: { version } })
  }
}
