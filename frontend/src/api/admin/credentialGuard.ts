import { apiClient } from '../client'
export interface CredentialGuardConfig { enabled: boolean; auto_relogin: boolean; interval_seconds: number; cooldown_seconds: number; max_failures: number; relogin_endpoint: string; allowed_origin: string; login_email: string }
export interface CredentialGuardRecord { account_id: number; account_name: string; config: CredentialGuardConfig; revision: string; secrets_configured: boolean; state: string; code: string; http_status: number; failures: number; blocked: boolean; last_checked_at: string | null; next_check_at: string }
export interface CredentialGuardInput extends CredentialGuardConfig { revision: string; password?: string; totp_secret?: string; api_token?: string; clear_secrets?: boolean; reset_failures?: boolean }
export const credentialGuardAPI = {
  async list() { return (await apiClient.get<{ items: CredentialGuardRecord[] }>('/admin/credential-guard')).data },
  async save(id: number, input: CredentialGuardInput) { return (await apiClient.put<CredentialGuardRecord>(`/admin/credential-guard/${id}`, input)).data },
  async probe(id: number) { return (await apiClient.post<CredentialGuardRecord>(`/admin/credential-guard/${id}/probe`, undefined, { timeout: 150000 })).data },
  async relogin(id: number) { return (await apiClient.post<CredentialGuardRecord>(`/admin/credential-guard/${id}/relogin`, undefined, { timeout: 150000 })).data }
}
