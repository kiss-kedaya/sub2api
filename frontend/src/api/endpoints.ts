import { apiClient } from './client'
import type { CustomEndpoint } from '@/types'

/**
 * Returns the admin-configured extra endpoints visible to the current user.
 * The backend applies the cumulative-recharge eligibility check.
 */
export async function getCustomEndpoints(): Promise<CustomEndpoint[]> {
  const { data } = await apiClient.get<CustomEndpoint[]>('/user/custom-endpoints')
  return data
}

export const endpointsAPI = {
  getCustomEndpoints,
}

export default endpointsAPI
