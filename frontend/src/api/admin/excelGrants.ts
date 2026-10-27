import { apiClient } from '../client'
export interface ExcelGrantRecord { account_id: number; account_name: string; config: { enabled: boolean; models: string[] }; revision: string; configured: boolean; expires_at: string | null }
export const excelGrantsAPI = {
  async list() { return (await apiClient.get<{ items: ExcelGrantRecord[] }>('/admin/excel-grants')).data },
  async save(id: number, enabled: boolean, models: string[], revision: string) { return (await apiClient.put<ExcelGrantRecord>(`/admin/excel-grants/${id}`, { enabled, models, revision })).data },
  async authorize(id: number) { return (await apiClient.post<{ auth_url: string; session_id: string }>(`/admin/excel-grants/${id}/authorize`)).data },
  async callback(id: number, session_id: string, callback_url: string) { return (await apiClient.post<ExcelGrantRecord>(`/admin/excel-grants/${id}/callback`, { session_id, callback_url }, { timeout: 90000 })).data }
}
