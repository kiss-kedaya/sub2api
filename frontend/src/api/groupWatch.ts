/**
 * Group Watch Subscriptions & Notifications API
 *
 * Users can subscribe to individual groups on the channel status board to be
 * notified when a group changes price, turns unavailable (black), or recovers
 * to healthy (green).
 */

import { apiClient } from './client'

export type GroupWatchEventType = 'price_changed' | 'status_black' | 'status_green'

export interface GroupWatchNotification {
  id: number
  group_id: number
  event_type: GroupWatchEventType
  title: string
  content: string
  metadata?: Record<string, string>
  read_at?: string | null
  created_at: string
}

export interface GroupWatchNotificationSummary {
  total: number
  unread: number
}

interface RequestOptions {
  signal?: AbortSignal
}

export async function listSubscriptions(options?: RequestOptions): Promise<number[]> {
  const { data } = await apiClient.get<{ group_ids?: number[] }>('/group-watch/subscriptions', {
    signal: options?.signal,
  })
  return Array.isArray(data?.group_ids) ? data.group_ids : []
}

export async function subscribe(groupId: number): Promise<{ group_id: number; subscribed: boolean }> {
  const { data } = await apiClient.put<{ group_id: number; subscribed: boolean }>(
    `/group-watch/subscriptions/${groupId}`
  )
  return data
}

export async function unsubscribe(groupId: number): Promise<{ group_id: number; subscribed: boolean }> {
  const { data } = await apiClient.delete<{ group_id: number; subscribed: boolean }>(
    `/group-watch/subscriptions/${groupId}`
  )
  return data
}

export async function getNotificationSummary(options?: RequestOptions): Promise<GroupWatchNotificationSummary> {
  const { data } = await apiClient.get<GroupWatchNotificationSummary>('/group-watch/notifications/summary', {
    signal: options?.signal,
  })
  return {
    total: Number(data?.total ?? 0),
    unread: Number(data?.unread ?? 0),
  }
}

export async function listNotifications(
  unreadOnly = false,
  limit = 30,
  options?: RequestOptions
): Promise<GroupWatchNotification[]> {
  const { data } = await apiClient.get<GroupWatchNotification[]>('/group-watch/notifications', {
    params: {
      ...(unreadOnly ? { unread_only: 1 } : {}),
      limit,
    },
    signal: options?.signal,
  })
  return Array.isArray(data) ? data : []
}

export async function markNotificationRead(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(`/group-watch/notifications/${id}/read`)
  return data
}

export async function markAllNotificationsRead(): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>('/group-watch/notifications/read-all')
  return data
}

export const groupWatchAPI = {
  listSubscriptions,
  subscribe,
  unsubscribe,
  getNotificationSummary,
  listNotifications,
  markNotificationRead,
  markAllNotificationsRead,
}

export default groupWatchAPI
