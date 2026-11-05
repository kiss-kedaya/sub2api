import { apiClient } from '../client'

// Managed upstream sites: stored credentials, sessions, announcement sync,
// upstream API key CRUD, recharge, redeem and subscriptions. Everything here is
// admin-only and every money-spending call needs confirm=true.

export type UpstreamChannelStatus = 'active' | 'disabled' | string

export interface UpstreamChannel {
  id: number
  name: string
  site_url: string
  username: string
  has_password: boolean
  has_session: boolean
  session_expires_at?: string | null
  session_error?: string
  turnstile_enabled: boolean
  captcha_provider?: string
  captcha_endpoint?: string
  has_captcha_key: boolean
  proxy_url?: string
  sync_announcements: boolean
  status: UpstreamChannelStatus
  last_login_at?: string | null
  last_login_error?: string
  last_sync_at?: string | null
  last_sync_error?: string
  tags: string[]
  notes?: string
  created_at: string
  updated_at: string
}

export interface UpstreamChannelInput {
  name: string
  site_url: string
  username: string
  password?: string
  turnstile_enabled?: boolean
  captcha_provider?: string
  captcha_api_key?: string
  captcha_endpoint?: string
  proxy_url?: string
  sync_announcements?: boolean
  status?: string
  tags?: string[]
  notes?: string
}

export interface PageResult<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface UpstreamAnnouncement {
  id: number
  channel_id: number
  channel_name: string
  source_key: string
  title: string
  content: string
  notify_mode: string
  published_at?: string | null
  source_updated_at?: string | null
  first_seen_at: string
  pushed_at?: string | null
}

export interface UpstreamAnnouncementSyncResult {
  channel_id: number
  fetched: number
  added: number
  updated: number
  seeded: number
  pushed: number
  unread: number
}

export interface UpstreamAPIKey {
  id: number
  key?: string
  name: string
  status: string
  group_id: number | null
  group_name?: string
  group_ratio?: number
  quota: number
  quota_used: number
  expires_at?: string | null
  created_at?: string | null
  updated_at?: string | null
  last_used_at?: string | null
  ip_whitelist: string[]
  ip_blacklist: string[]
  rate_limit_5h: number
  rate_limit_1d: number
  rate_limit_7d: number
  usage_5h: number
  usage_1d: number
  usage_7d: number
}

export interface UpstreamAPIKeyGroup {
  id: number
  name: string
  description: string
  ratio: number
}

export interface UpstreamAPIKeyCreateInput {
  name: string
  group_id?: number | null
  custom_key?: string
  quota?: number | null
  expires_in_days?: number | null
  rate_limit_5h?: number | null
  rate_limit_1d?: number | null
  rate_limit_7d?: number | null
  ip_whitelist?: string[]
  ip_blacklist?: string[]
}

export interface UpstreamAPIKeyUpdateInput {
  name?: string
  group_id?: number | null
  status?: string
  quota?: number | null
  expires_at?: string | null
  ip_whitelist?: string[]
  ip_blacklist?: string[]
  reset_quota?: boolean
  rate_limit_5h?: number | null
  rate_limit_1d?: number | null
  rate_limit_7d?: number | null
  reset_rate_limit_usage?: boolean
}

export interface UpstreamRechargeMethod {
  type: string
  name: string
  min_amount: number
  max_amount: number
}

export interface UpstreamRechargeInfo {
  min_amount: number
  max_amount: number
  methods: UpstreamRechargeMethod[]
  help_text?: string
  help_image_url?: string
  alipay_force_qrcode: boolean
}

export interface UpstreamPaymentLaunch {
  pay_url?: string
  qr_code?: string
  expires_at?: string | null
  result_type?: string
  payment_mode?: string
}

export interface UpstreamRedeemResult {
  message: string
  type: string
  value: number
  new_balance?: number | null
  new_concurrency?: number | null
  group_name?: string | null
  validity_days?: number | null
}

export interface UpstreamSubscriptionPlan {
  id: string
  name: string
  description: string
  price: number
  currency: string
  validity: string
  group_name: string
  daily_limit_usd?: number | null
  weekly_limit_usd?: number | null
  monthly_limit_usd?: number | null
  features: string[]
  payment_methods: string[]
}

export interface UpstreamSubscriptionInfo {
  plans: UpstreamSubscriptionPlan[]
  methods: UpstreamRechargeMethod[]
}

export interface UpstreamSubscriptionWindow {
  limit_usd: number
  used_usd: number
  remaining_usd: number
  percentage: number
  resets_at?: string | null
}

export interface UpstreamSubscriptionUsage {
  id: number
  group_name: string
  status: string
  expires_at?: string | null
  expires_in_days: number
  daily?: UpstreamSubscriptionWindow | null
  weekly?: UpstreamSubscriptionWindow | null
  monthly?: UpstreamSubscriptionWindow | null
}

export async function listChannels(query: {
  page?: number
  pageSize?: number
  search?: string
  status?: string
}): Promise<PageResult<UpstreamChannel>> {
  const { data } = await apiClient.get<PageResult<UpstreamChannel>>('/admin/upstream-monitor/channels', {
    params: {
      page: query.page ?? 1,
      page_size: query.pageSize ?? 20,
      search: query.search || undefined,
      status: query.status || undefined,
    },
  })
  return data
}

export async function getChannel(id: number): Promise<UpstreamChannel> {
  const { data } = await apiClient.get<UpstreamChannel>(`/admin/upstream-monitor/channels/${id}`)
  return data
}

export async function createChannel(input: UpstreamChannelInput): Promise<UpstreamChannel> {
  const { data } = await apiClient.post<UpstreamChannel>('/admin/upstream-monitor/channels', input)
  return data
}

export async function updateChannel(id: number, input: UpstreamChannelInput): Promise<UpstreamChannel> {
  const { data } = await apiClient.put<UpstreamChannel>(`/admin/upstream-monitor/channels/${id}`, input)
  return data
}

export async function deleteChannel(id: number): Promise<{ deleted: number }> {
  const { data } = await apiClient.delete<{ deleted: number }>(`/admin/upstream-monitor/channels/${id}`)
  return data
}

export async function loginChannel(id: number): Promise<UpstreamChannel> {
  const { data } = await apiClient.post<UpstreamChannel>(`/admin/upstream-monitor/channels/${id}/login`)
  return data
}

export async function channelBalance(id: number): Promise<{ channel_id: number; balance: number; fetched_at?: string }> {
  const { data } = await apiClient.get<{ channel_id: number; balance: number; fetched_at?: string }>(
    `/admin/upstream-monitor/channels/${id}/balance`
  )
  return data
}

export async function listChannelAnnouncements(
  id: number,
  query: { page?: number; pageSize?: number }
): Promise<PageResult<UpstreamAnnouncement>> {
  const { data } = await apiClient.get<PageResult<UpstreamAnnouncement>>('/admin/upstream-monitor/announcements', {
    params: { channel_id: id || undefined, page: query.page ?? 1, page_size: query.pageSize ?? 20 },
  })
  return data
}

export async function syncChannelAnnouncements(id: number): Promise<UpstreamAnnouncementSyncResult> {
  const { data } = await apiClient.post<UpstreamAnnouncementSyncResult>(
    `/admin/upstream-monitor/channels/${id}/announcements/sync`
  )
  return data
}

export async function listUpstreamAPIKeys(
  id: number,
  query: { page?: number; pageSize?: number; search?: string; status?: string; groupId?: string }
): Promise<PageResult<UpstreamAPIKey>> {
  const { data } = await apiClient.get<PageResult<UpstreamAPIKey>>(
    `/admin/upstream-monitor/channels/${id}/api-keys`,
    {
      params: {
        page: query.page ?? 1,
        page_size: query.pageSize ?? 20,
        search: query.search || undefined,
        status: query.status || undefined,
        group_id: query.groupId || undefined,
      },
    }
  )
  return data
}

export async function listUpstreamAPIKeyGroups(id: number): Promise<{ items: UpstreamAPIKeyGroup[] }> {
  const { data } = await apiClient.get<{ items: UpstreamAPIKeyGroup[] }>(
    `/admin/upstream-monitor/channels/${id}/api-key-groups`
  )
  return data
}

export async function createUpstreamAPIKey(id: number, input: UpstreamAPIKeyCreateInput): Promise<UpstreamAPIKey> {
  const { data } = await apiClient.post<UpstreamAPIKey>(`/admin/upstream-monitor/channels/${id}/api-keys`, input)
  return data
}

export async function updateUpstreamAPIKey(
  id: number,
  keyId: number,
  input: UpstreamAPIKeyUpdateInput
): Promise<UpstreamAPIKey> {
  const { data } = await apiClient.put<UpstreamAPIKey>(
    `/admin/upstream-monitor/channels/${id}/api-keys/${keyId}`,
    input
  )
  return data
}

export async function deleteUpstreamAPIKey(id: number, keyId: number): Promise<{ deleted: number }> {
  const { data } = await apiClient.delete<{ deleted: number }>(
    `/admin/upstream-monitor/channels/${id}/api-keys/${keyId}`
  )
  return data
}

export async function revealUpstreamAPIKey(id: number, keyId: number): Promise<{ key: string }> {
  const { data } = await apiClient.post<{ key: string }>(
    `/admin/upstream-monitor/channels/${id}/api-keys/${keyId}/reveal`
  )
  return data
}

export async function channelRechargeInfo(id: number): Promise<UpstreamRechargeInfo> {
  const { data } = await apiClient.get<UpstreamRechargeInfo>(`/admin/upstream-monitor/channels/${id}/recharge`)
  return data
}

export async function createChannelRecharge(
  id: number,
  input: { amount: number; payment_method: string; is_mobile?: boolean; confirm: boolean }
): Promise<UpstreamPaymentLaunch> {
  const { data } = await apiClient.post<UpstreamPaymentLaunch>(
    `/admin/upstream-monitor/channels/${id}/recharge`,
    input
  )
  return data
}

export async function redeemOnChannel(
  id: number,
  input: { code: string; confirm: boolean }
): Promise<UpstreamRedeemResult> {
  const { data } = await apiClient.post<UpstreamRedeemResult>(`/admin/upstream-monitor/channels/${id}/redeem`, input)
  return data
}

export async function channelSubscriptionInfo(id: number): Promise<UpstreamSubscriptionInfo> {
  const { data } = await apiClient.get<UpstreamSubscriptionInfo>(
    `/admin/upstream-monitor/channels/${id}/subscriptions`
  )
  return data
}

export async function channelSubscriptionUsage(
  id: number
): Promise<{ items: UpstreamSubscriptionUsage[]; fetched_at: string }> {
  const { data } = await apiClient.get<{ items: UpstreamSubscriptionUsage[]; fetched_at: string }>(
    `/admin/upstream-monitor/channels/${id}/subscriptions/usage`
  )
  return data
}

export async function createChannelSubscription(
  id: number,
  input: { plan_id: string; payment_method: string; is_mobile?: boolean; confirm: boolean }
): Promise<UpstreamPaymentLaunch> {
  const { data } = await apiClient.post<UpstreamPaymentLaunch>(
    `/admin/upstream-monitor/channels/${id}/subscriptions`,
    input
  )
  return data
}
