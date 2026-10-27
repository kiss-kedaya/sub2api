// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
import { apiClient } from './client'

export type TrafficPeriod = 'day' | 'week' | 'month' | 'year'
export interface TrafficTotals {
  upload_bytes: number
  download_bytes: number
  total_bytes: number
  requests: number
}
export interface TrafficEvent {
  event_id: string
  user_id: number
  api_key_id: number
  group_id: number
  request_id: string
  endpoint: string
  method: string
  started_at: string
  finished_at: string
  upload_bytes: number
  download_bytes: number
  status_code: number
  outcome: string
  request_complete: boolean
  measurement_version: number
}
export interface TrafficReport extends TrafficTotals {
  items: TrafficEvent[]
  trend: (TrafficTotals & { bucket: string })[]
  has_more: boolean
  next_cursor?: string
  page: number
  page_size: number
  period: TrafficPeriod
  start: string
  end: string
  timezone: string
  recorded_since: string | null
  measurement_version: number
}
export interface TrafficQuery {
  period: TrafficPeriod
  date?: string
  user_id?: number
  api_key_id?: number
  page?: number
  page_size: number
  cursor?: string
  view?: 'all' | 'summary' | 'details'
}
export interface TrafficKeyOption { id: number; name: string; deleted: boolean }
export interface TrafficDeliveryHealth {
  status: 'ok' | 'delayed' | 'degraded'
  pending_events: number
  quarantined_events: number
  oldest_pending_at: string | null
  oldest_pending_age_seconds: number
  delivered_events: number
  delivery_failures: number
  staging_failures: number
  persistence_failures: number
  last_delivery_at: string | null
  checked_at: string
  queued_events: number
  queue_capacity: number
  processing_events: number
  dropped_events: number
  queue_dropped_events: number
  shutdown_dropped_events: number
  spool_limit_dropped_events: number
  invalid_dropped_events: number
  unavailable_dropped_events: number
  worker_panics: number
  worker_running: boolean
  storage_available: boolean
}
export async function getTrafficKeys(admin: boolean, userId?: number, query = '', signal?: AbortSignal) {
  const { data } = await apiClient.get<TrafficKeyOption[]>(admin ? '/admin/usage/traffic/keys' : '/usage/traffic/keys', {
    params: { q: query, ...(admin && userId ? { user_id: userId } : {}) }, signal
  })
  return data
}
export async function getTrafficHealth(signal?: AbortSignal) {
  const { data } = await apiClient.get<TrafficDeliveryHealth>('/admin/usage/traffic/health', { signal })
  return data
}
export async function getTraffic(admin: boolean, params: TrafficQuery, signal?: AbortSignal) {
  const { data } = await apiClient.get<TrafficReport>(admin ? '/admin/usage/traffic' : '/usage/traffic', { params, signal })
  return data
}

export function formatTrafficBytes(value: number): string {
  if (value < 1024) return `${value} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let scaled = value / 1024
  let index = 0
  while (scaled >= 1024 && index < units.length - 1) { scaled /= 1024; index++ }
  return `${scaled.toFixed(2)} ${units[index]}`
}
