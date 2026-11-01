/**
 * User Announcements API endpoints
 *
 * The timeline never carries the body: the first paint only needs counts, the
 * bell timeline needs titles plus a short excerpt, and the body is fetched when
 * a detail is opened.
 */

import { apiClient } from './client'
import type { UserAnnouncement } from '@/types'

/** Timeline row: same shape as the detail, minus the body. */
export type AnnouncementListItem = Omit<UserAnnouncement, 'content'> & {
  content?: string
  excerpt?: string
}

export interface AnnouncementSummary {
  total: number
  unread: number
}

interface RequestOptions {
  signal?: AbortSignal
}

export async function list(
  unreadOnly: boolean = false,
  options?: RequestOptions
): Promise<AnnouncementListItem[]> {
  const { data } = await apiClient.get<AnnouncementListItem[]>('/announcements', {
    params: unreadOnly ? { unread_only: 1 } : {},
    signal: options?.signal
  })
  return data
}

/** Badge counts only; the first paint must not download the timeline. */
export async function summary(options?: RequestOptions): Promise<AnnouncementSummary> {
  const { data } = await apiClient.get<AnnouncementSummary>('/announcements/summary', {
    signal: options?.signal
  })
  return data
}

/** Unread popup announcements (bounded on the server) with their bodies. */
export async function popups(options?: RequestOptions): Promise<UserAnnouncement[]> {
  const { data } = await apiClient.get<UserAnnouncement[]>('/announcements/popups', {
    signal: options?.signal
  })
  return data
}

/** One visible announcement with its body. */
export async function getById(id: number, options?: RequestOptions): Promise<UserAnnouncement> {
  const { data } = await apiClient.get<UserAnnouncement>(`/announcements/item/${id}`, {
    signal: options?.signal
  })
  return data
}

export async function markRead(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(`/announcements/${id}/read`)
  return data
}

const announcementsAPI = {
  list,
  summary,
  popups,
  getById,
  markRead
}

export default announcementsAPI
