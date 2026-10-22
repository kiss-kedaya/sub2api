import { apiClient } from '@/api/client'

// Crypto deposit channel admin API. The channel is additive and gated by
// the server-side config; the UI simply reflects what the API returns.

export interface CryptoConfig {
  enabled: boolean
  chains: string[]
  rpc: Record<string, string>
  sweep_global_threshold: number
  sweep_min_per_address: Record<string, number>
  gas_topup: Record<string, number>
  hot_wallet_evm: string
  hot_wallet_solana: string
  hot_index: 0
  sweep_auto: boolean
  confirmations: Record<string, number>
  scan_range: Record<string, number>
  credit_min_amount: number
  credit_max_amount: number
  credit_multiplier: number
}

export type CryptoConfigPatch = Partial<Omit<CryptoConfig, 'hot_index'>>

export interface DepositAddress {
  id: number
  user_id: number
  family: string
  address: string
  status: string
  credited_total: number
  swept_total: number
  deposit_count: number
}

export interface SweepRecord {
  id: number
  chain: string
  from_address: string
  to_address: string
  token: string
  amount: string
  gas_tx_hash: string
  sweep_tx_hash: string
  status: string
  error: string
  created_at: string
}

export interface DepositRecord {
  id: number
  chain: string
  tx_hash: string
  log_index: number
  from_address: string
  to_address: string
  token: string
  token_symbol?: string
  amount: string
  status: string
  created_at: string
}

export interface SweepPlan {
  Entries: Array<{
    Chain: string
    Address: string
    Token: string
    AmountDecimal: number
  }> | null
  TotalUSD: number
  DryRun: boolean
}

export interface SweepEntry {
  chain: string
  address: string
  amount: string
  gas_tx_hash: string
  sweep_tx_hash: string
  status: string
  error?: string
}

export interface SweepSummary {
  dry_run: boolean
  planned: number
  swept: number
  failed: number
  pending: number
  gas_topups: number
  entries: SweepEntry[]
}

export async function getCryptoConfig(totpCode?: string): Promise<CryptoConfig> {
  const { data } = await apiClient.get('/admin/crypto/config', cryptoRequestOptions(totpCode ? { totpCode } : undefined))
  return data.data
}

// The backend applies a pointer patch: omitted keys keep their stored value.
// Sending only the changed fields is what prevents a full-object write from
// silently rewriting maps the console never rendered.
export async function updateCryptoConfig(cfg: CryptoConfigPatch, verification?: CryptoVerification): Promise<void> {
  await apiClient.put('/admin/crypto/config', cfg, cryptoRequestOptions(verification))
}

export async function listDepositAddresses(userId: number): Promise<DepositAddress[]> {
  const { data } = await apiClient.get('/admin/crypto/addresses', { params: { user_id: userId } })
  return data.data ?? []
}

// AdminAddressSummary totals the filtered set, not just the current page.
export interface AdminAddressSummary {
  address_count: number
  deposit_count: number
  credited_total: number
  swept_total: number
}

export type AddressSortKey = 'id' | 'user_id' | 'credited_total' | 'swept_total' | 'deposit_count'

export interface AddressQuery {
  userId?: number | null
  page?: number
  pageSize?: number
  sortBy?: AddressSortKey
  sortDir?: 'asc' | 'desc'
}

// listAllAddresses returns every address with aggregated credited/swept
// totals — the operator's main view. Sorting is server-side so it covers the
// whole set rather than the loaded page.
export async function listAllAddresses(
  query: AddressQuery = {}
): Promise<{ items: DepositAddress[]; total: number; summary: AdminAddressSummary }> {
  const { userId, page = 1, pageSize = 50, sortBy = 'id', sortDir = 'desc' } = query
  const { data } = await apiClient.get('/admin/crypto/addresses', {
    params: {
      page,
      page_size: pageSize,
      sort_by: sortBy,
      sort_dir: sortDir,
      ...(userId ? { user_id: userId } : {})
    }
  })
  return {
    items: data.data?.items ?? [],
    total: data.data?.total ?? 0,
    summary: data.data?.summary ?? {
      address_count: 0,
      deposit_count: 0,
      credited_total: 0,
      swept_total: 0
    }
  }
}



export async function setAddressStatus(id: number, status: string, verification?: CryptoVerification): Promise<void> {
  await apiClient.post(`/admin/crypto/addresses/${id}/status`, { status }, cryptoRequestOptions(verification))
}

export async function getSweepPlan(): Promise<SweepPlan> {
  const { data } = await apiClient.get('/admin/crypto/sweep/plan')
  return data.data
}

// runSweep executes the sweep. dryRun defaults to true server-side; the
// caller must pass dryRun=false to move funds.
export async function runSweep(dryRun: boolean, verification?: CryptoVerification): Promise<SweepSummary> {
  const { data } = await apiClient.post('/admin/crypto/sweep/run', null, {
    ...cryptoRequestOptions(verification),
    params: { dry_run: dryRun ? '1' : '0' }
  })
  return data.data
}



export async function listSweepRecords(chain?: string): Promise<SweepRecord[]> {
  const { data } = await apiClient.get('/admin/crypto/sweep/records', { params: { chain } })
  return data.data ?? []
}

export async function listDeposits(chain?: string, status?: string): Promise<DepositRecord[]> {
  const { data } = await apiClient.get('/admin/crypto/deposits', { params: { chain, status } })
  return data.data ?? []
}

export interface DepositSummary {
  total: number
  credited: number
  pending: number
  ignored: number
  amount: number
}

// listDepositsPaged returns one page of the deposit ledger plus whole-set
// counters, so the header stays correct while paging.
export async function listDepositsPaged(
  chain: string,
  status: string,
  page = 1,
  pageSize = 50
): Promise<{ items: DepositRecord[]; summary: DepositSummary }> {
  const { data } = await apiClient.get('/admin/crypto/deposits', {
    params: { chain, status, page, page_size: pageSize }
  })
  return {
    items: data.data?.items ?? [],
    summary: data.data?.summary ?? { total: 0, credited: 0, pending: 0, ignored: 0, amount: 0 }
  }
}

export interface WalletStatus {
  ok: boolean
  configured: boolean
  ready: boolean
  enabled: boolean
}

// The main service automatically loads its encrypted, configured seeds.
export async function getWalletStatus(): Promise<WalletStatus> {
  const { data } = await apiClient.get('/admin/crypto/wallet/status')
  return data.data
}

export async function initializeWallet(
  depositMnemonic: string,
  hotMnemonic: string,
  verification: CryptoVerification
): Promise<void> {
  await apiClient.post('/admin/crypto/wallet/initialize', {
    deposit_mnemonic: depositMnemonic,
    hot_mnemonic: hotMnemonic
  }, cryptoRequestOptions(verification))
}

export interface PoolStatus {
  free: Record<string, number>
  low_watermark: number
}

export interface HotWalletStatus {
  address: string
  chain: string
  balance: string
  symbol: string
  enough: boolean
  error?: string
}

export interface HotWalletAddress {
  index: number
  evm: string
  solana: string
}

// getHotWalletAddress returns the address the hot seed actually derives to.
export async function getHotWalletAddress(): Promise<HotWalletAddress> {
  const { data } = await apiClient.get('/admin/crypto/hot-wallet/address')
  return data.data
}

export interface RPCTestResult {
  endpoint: string
  ok: boolean
  chain_id?: string
  block?: number
  latency_ms: number
  error?: string
}

export interface ChainlistCandidate {
  url: string
  chain_id: number
  chain_name: string
}

// testRPCEndpoint probes one endpoint (chain id + head + latency).
export async function testRPCEndpoint(chain: string, endpoint: string, verification?: CryptoVerification): Promise<RPCTestResult> {
  const { data } = await apiClient.post('/admin/crypto/rpc/test', { chain, endpoint }, cryptoRequestOptions(verification))
  return data.data
}

// listRPCCandidates fetches the chain's public endpoints from chainlist.org.
export async function listRPCCandidates(chain: string): Promise<ChainlistCandidate[]> {
  const { data } = await apiClient.get('/admin/crypto/rpc/candidates', { params: { chain } })
  return data.data ?? []
}

export interface RPCAutoPickResult {
  picked: RPCTestResult[]
  probed: number
}

// autoPickRPC pulls the registry, probes every candidate in parallel and
// returns the fastest healthy endpoints, skipping ones already configured.
export async function autoPickRPC(
  chain: string,
  exclude: string[],
  want = 3,
  verification?: CryptoVerification
): Promise<RPCAutoPickResult> {
  const { data } = await apiClient.post(
    '/admin/crypto/rpc/auto-pick',
    { chain, exclude, want },
    { ...cryptoRequestOptions(verification), timeout: 60_000 }
  )
  return { picked: data.data?.picked ?? [], probed: data.data?.probed ?? 0 }
}



export interface RPCTestResult {
  endpoint: string
  ok: boolean
  chain_id?: string
  block?: number
  latency_ms: number
  error?: string
}

export interface ChainlistCandidate {
  url: string
  chain_id: number
  chain_name: string
}

// getPoolStatus reports remaining unassigned addresses. An empty pool means
// new users cannot receive a deposit address.
export async function getPoolStatus(): Promise<PoolStatus> {
  const { data } = await apiClient.get('/admin/crypto/pool')
  return data.data
}

// getHotWalletStatus reports gas readiness per chain.
export async function getHotWalletStatus(): Promise<HotWalletStatus[]> {
  const { data } = await apiClient.get('/admin/crypto/hot-wallet')
  return data.data ?? []
}

// exportDepositsCSV downloads the deposit ledger for reconciliation.
export async function exportDepositsCSV(chain = '', status = '', verification?: CryptoVerification): Promise<Blob> {
  const response = await apiClient.get<Blob>('/admin/crypto/deposits.csv', {
    ...cryptoRequestOptions(verification),
    params: { chain, status },
    responseType: 'blob',
    validateStatus: () => true
  })
  return checkedDownload(response)
}

export interface CryptoVerification {
  totpCode: string
}

// Per-request only. Never put a code in client defaults, interceptors or storage.
// _retry also prevents the shared client's 401 refresh path replaying this code.
function cryptoRequestOptions(verification?: CryptoVerification) {
  return { headers: verification ? { 'X-Crypto-TOTP': verification.totpCode } : {}, _retry: true }
}

export interface CryptoSecurityStatus {
  enabled: boolean
  feature_enabled: boolean
  can_setup: boolean
  read_only: boolean
}

export interface CryptoSecuritySetup {
  secret: string
  qr_code_url: string
  setup_token: string
  countdown: number
}

// Security uses the standard response envelope, already unwrapped by apiClient.
// Existing crypto endpoints above use the legacy {data: ...} envelope instead.
export async function getCryptoSecurityStatus(): Promise<CryptoSecurityStatus> {
  const { data } = await apiClient.get<CryptoSecurityStatus>('/admin/crypto/security/status')
  return data
}

export async function setupCryptoSecurity(): Promise<CryptoSecuritySetup> {
  const { data } = await apiClient.post<CryptoSecuritySetup>('/admin/crypto/security/setup', {}, cryptoRequestOptions())
  return data
}

export async function enableCryptoSecurity(totpCode: string, setupToken: string): Promise<{ success: true }> {
  const { data } = await apiClient.post<{ success: true }>('/admin/crypto/security/enable', {
    totp_code: totpCode,
    setup_token: setupToken
  }, cryptoRequestOptions({ totpCode }))
  return data
}

async function checkedDownload(response: { data: Blob; status: number }): Promise<Blob> {
  if (response.status >= 200 && response.status < 300) return response.data
  // Keep JSON rejection reasons for blob requests; the shared interceptor cannot
  // read a Blob and would otherwise discard expired/replayed/rate-limit errors.
  let detail: Record<string, unknown> = {}
  try { detail = JSON.parse(await response.data.text()) } catch { /* Non-JSON error. */ }
  throw { ...detail, status: response.status }
}

export interface CryptoMnemonicExport {
  deposit_mnemonic: string
  hot_mnemonic: string
}

// Plaintext is returned only for this verified request, without browser caching.
export async function exportWalletMnemonics(verification: CryptoVerification): Promise<CryptoMnemonicExport> {
  const request = cryptoRequestOptions(verification)
  const { data } = await apiClient.post<CryptoMnemonicExport>('/admin/crypto/wallet/export-mnemonics', {}, {
    ...request,
    headers: { ...request.headers, 'Cache-Control': 'no-store', Pragma: 'no-cache' }
  })
  return data
}

export async function importHotMnemonic(hotMnemonic: string, verification?: CryptoVerification): Promise<{ saved: true }> {
  const { data } = await apiClient.post<{ saved: true }>('/admin/crypto/wallet/hot-mnemonic', {
    hot_mnemonic: hotMnemonic
  }, cryptoRequestOptions(verification))
  return data
}

export function cryptoSecurityErrorKey(error: unknown): string | undefined {
  if (!error || typeof error !== 'object') return undefined
  const e = error as { reason?: string; code?: string | number; status?: number; message?: string }
  const reason = `${e.reason ?? ''} ${e.code ?? ''} ${e.message ?? ''}`.toLowerCase()
  if (/crypto_operator_required|configured payment administrator/.test(reason)) return 'securityOperatorRequired'
  if (e.status === 429 || /rate.limit|too.many/.test(reason)) return 'securityRateLimit'
  if (/replay|already.used|code.used/.test(reason)) return 'securityReplay'
  if (/expir/.test(reason)) return 'securityExpired'
  if (/not.configur|not.enabled|not.bound|setup.required|2fa.required|totp.required/.test(reason)) return 'securityNotConfigured'
  if (/totp|2fa|verification.code/.test(reason)) return 'securityInvalid'
  return undefined
}

