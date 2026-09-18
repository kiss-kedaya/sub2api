/**
 * Redeem code API endpoints
 * Handles redeem code redemption for users
 */

import { apiClient } from './client'
import type { PaginatedResponse, RedeemCodeRequest } from '@/types'

export interface RedeemHistoryItem {
  id: number
  code: string
  type: string
  value: number
  status: string
  used_at: string
  created_at: string
  // Notes from admin for admin_balance/admin_concurrency types
  notes?: string
  // Subscription-specific fields
  group_id?: number
  validity_days?: number
  group?: {
    id: number
    name: string
  }
}

/**
 * Redeem a code
 * @param code - Redeem code string
 * @returns Redemption result with updated balance or concurrency
 */
export async function redeem(code: string): Promise<{
  message: string
  type: string
  value: number
  new_balance?: number
  new_concurrency?: number
}> {
  const payload: RedeemCodeRequest = { code }

  const { data } = await apiClient.post<{
    message: string
    type: string
    value: number
    new_balance?: number
    new_concurrency?: number
  }>('/redeem', payload)

  return data
}

/**
 * Get user's redemption history
 * @returns The requested page of redeemed codes and the total count
 */
export async function getHistory(page = 1, pageSize = 20): Promise<PaginatedResponse<RedeemHistoryItem>> {
  const { data } = await apiClient.get<PaginatedResponse<RedeemHistoryItem> | RedeemHistoryItem[]>('/redeem/history', {
    params: { page, page_size: pageSize }
  })
  // Old instances return an array during rolling upgrades.
  if (Array.isArray(data)) {
    return { items: data.slice((page - 1) * pageSize, page * pageSize), total: data.length,
      page, page_size: pageSize, pages: Math.ceil(data.length / pageSize) }
  }
  return data
}

export const redeemAPI = {
  redeem,
  getHistory
}

export default redeemAPI
