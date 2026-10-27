import { apiClient } from '../client'
export interface RegionalPod { id: string; endpoint: string; enabled: boolean }
export interface RegionalConfig { revision: string; enabled: boolean; established: boolean; pods: RegionalPod[]; regions: Array<{ country: string; pod_ids: string[] }> }
export interface RegionalStatus { config: RegionalConfig; pods: Array<{ id: string; region: string; version: string; ready: boolean; at: number }>; stats: Record<string, string>; runtime: { node_id: string; gateway_node: boolean; secret_configured: boolean; redis_configured: boolean; private_network_configured: boolean } }
export const getRegionalRouting = async () => (await apiClient.get<RegionalStatus>('/admin/serverless')).data
export const saveRegionalRouting = async (config: RegionalConfig) => (await apiClient.put<RegionalStatus>('/admin/serverless', config)).data
