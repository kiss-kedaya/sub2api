import { apiClient } from './client'

export interface CFAllowlistItem {
  id: number
  ip: string
  created_at: string
}

export interface CFAllowlistStatus {
  eligible: boolean
  threshold: number
  total_recharged: number
  max_slots: number
  used_slots: number
  detected_ip: string
  configured: boolean
  items: CFAllowlistItem[]
}

export async function getStatus(): Promise<CFAllowlistStatus> {
  const { data } = await apiClient.get<CFAllowlistStatus>('/user/cf-allowlist')
  return data
}

export async function add(ip: string): Promise<CFAllowlistItem> {
  const { data } = await apiClient.post<CFAllowlistItem>('/user/cf-allowlist', { ip })
  return data
}

export async function remove(id: number): Promise<void> {
  await apiClient.delete(`/user/cf-allowlist/${id}`)
}

export const cfAllowlistAPI = { getStatus, add, remove }
export default cfAllowlistAPI
