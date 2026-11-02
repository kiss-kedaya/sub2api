import type { AccountPlatform, AccountType, AdminDataAccount, AdminDataPayload } from '@/types'

/**
 * Adapter for https://github.com/bejix/upstream-ops payloads.
 *
 * Contract sources (upstream-ops @ 821e613, verified 2026-11-02). No upstream
 * source code is copied; the project has no declared license, so only the
 * published HTTP/JSON contract is implemented here.
 *
 *  - GET /api/channels            -> { data: Channel[] } | { data: { items, total, page, page_size, pages } }
 *  - GET /api/gateway/providers   -> { data: Provider[] }
 *  - Provider rows only ever carry `api_key_hint`; the plaintext key is behind
 *    POST /api/gateway/providers/:id/reveal. This adapter never calls reveal and
 *    never reads password / token_credential / api_key / cookie fields.
 *
 * Output feeds the existing admin import flow (POST /api/v1/admin/accounts/data,
 * `sub2api-data` v1). Nothing here touches balance, rates, payments or deletes.
 */

export type UpstreamOpsSnapshotKind = 'channels' | 'providers'

export interface UpstreamOpsChannel {
  kind: 'channels'
  id: string
  name: string
  sourceType: string
  siteUrl: string
  username: string
  credentialMode: 'password' | 'token' | ''
  monitorEnabled: boolean
  tags: string[]
  notes: string
  /** True when the source URL carried a query/hash that was dropped before use. */
  urlQueryStripped: boolean
  /** True when the source URL was non-empty but not an http(s) URL. */
  urlSchemeInvalid: boolean
}

export interface UpstreamOpsProvider {
  kind: 'providers'
  id: string
  name: string
  baseUrl: string
  apiKeyHint: string
  upstreamProtocol: string
  enabled: boolean
  /** True when the source URL carried a query/hash that was dropped before use. */
  urlQueryStripped: boolean
  /** True when the source URL was non-empty but not an http(s) URL. */
  urlSchemeInvalid: boolean
}

export type UpstreamOpsEntry = UpstreamOpsChannel | UpstreamOpsProvider

export interface UpstreamOpsSnapshot {
  kind: UpstreamOpsSnapshotKind | null
  entries: UpstreamOpsEntry[]
  issues: string[]
}

/** Reason codes stay language-neutral; the UI maps them to zh/en copy. */
export type UpstreamOpsReasonCode =
  | 'channel_snapshot_has_no_credential'
  | 'missing_name'
  | 'missing_base_url'
  | 'url_scheme_required'
  | 'url_query_stripped'
  | 'unknown_upstream_protocol'
  | 'provider_disabled'
  | 'duplicate_existing'
  | 'duplicate_batch'

export type UpstreamOpsRowStatus =
  | 'needs-credential'
  | 'needs-platform'
  | 'duplicate'
  | 'unsupported'
  | 'invalid'

export interface UpstreamOpsPreviewRow {
  key: string
  kind: UpstreamOpsSnapshotKind
  id: string
  name: string
  /** base_url for providers, site_url for channels (normalized, sanitized, display only). */
  baseUrl: string
  sourceType: string
  credentialMode: string
  apiKeyHint: string
  /** Channel passwords are never part of this snapshot; monitor flags stay informational. */
  monitorEnabled: boolean
  enabled: boolean
  platform: AccountPlatform | null
  accountType: AccountType | null
  status: UpstreamOpsRowStatus
  reasons: UpstreamOpsReasonCode[]
  duplicateOf: string | null
}

export interface UpstreamOpsExistingAccountKey {
  name?: string | null
  custom_base_url?: string | null
}

export interface UpstreamOpsPreviewOptions {
  existingAccounts?: UpstreamOpsExistingAccountKey[]
  platformOverrides?: Record<string, AccountPlatform>
}

export interface UpstreamOpsPreviewSummary {
  total: number
  needsCredential: number
  needsPlatform: number
  duplicate: number
  unsupported: number
  invalid: number
}

export interface UpstreamOpsPreview {
  rows: UpstreamOpsPreviewRow[]
  summary: UpstreamOpsPreviewSummary
}

export interface UpstreamOpsOperatorCredential {
  apiKey?: string
}

export interface UpstreamOpsPayloadResult {
  payload: AdminDataPayload
  includedKeys: string[]
  skipped: Array<{ key: string; status: UpstreamOpsRowStatus }>
}

/**
 * What this repo's browser login looks like to any external monitor.
 *
 * The Nuxt BFF keeps the bearer tokens server-side: login responses reach the
 * browser with `access_token` / `refresh_token` blanked and `session_managed: true`,
 * while the session lives in an HttpOnly cookie. upstream-ops' sub2api connector
 * has no cookie jar, no CSRF handling and no `session_managed` support: it only
 * accepts an access_token (token mode) or the raw bearer login response from a
 * server-to-server call. 2FA is never bypassed on either side.
 */
export const UPSTREAM_OPS_AUTH_CONTRACT = {
  browserSessionMode: 'session_managed',
  browserSessionTransport: 'http-only-cookie',
  connectorAuthMode: 'bearer-token-only',
  supportsCookieJar: false,
  supportsSessionManagedCookie: false,
  supportsCsrfToken: false,
  requiresAccessToken: true,
  disablesTwoFactor: false
} as const

export function describeUpstreamOpsAuthContract(): typeof UPSTREAM_OPS_AUTH_CONTRACT {
  return UPSTREAM_OPS_AUTH_CONTRACT
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function pickString(record: Record<string, unknown>, key: string): string {
  const value = record[key]
  return typeof value === 'string' ? value.trim() : ''
}

function pickStringArray(record: Record<string, unknown>, key: string): string[] {
  const value = record[key]
  if (Array.isArray(value)) {
    return value.filter((item): item is string => typeof item === 'string' && item.trim() !== '').map((item) => item.trim())
  }
  // upstream-ops stores tags as a comma/space separated string in some versions.
  if (typeof value === 'string') {
    return value.split(/[,，\s]+/).map((item) => item.trim()).filter(Boolean)
  }
  return []
}

function pickId(record: Record<string, unknown>): string {
  const value = record.id
  if (typeof value === 'string') return value.trim()
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  return ''
}

function extractRows(input: unknown, issues: string[]): unknown[] {
  if (Array.isArray(input)) return input
  const root = asRecord(input)
  if (!root) {
    issues.push('root: expected an object or array')
    return []
  }
  if (Array.isArray(root.data)) return root.data
  if (Array.isArray(root.items)) return root.items
  const data = asRecord(root.data)
  if (data && Array.isArray(data.items)) return data.items
  issues.push('root: no data/items array found')
  return []
}

export interface UpstreamOpsUrlInspection {
  /** Sanitized URL; empty when the scheme is missing or not http(s). */
  url: string
  scheme: string
  /** Query/hash present in the source; dropped so embedded secrets never travel. */
  hadQueryOrHash: boolean
  /** Source was non-empty but not a usable http(s) URL. */
  invalidScheme: boolean
}

const UPSTREAM_OPS_ALLOWED_SCHEMES = new Set(['http:', 'https:'])

/**
 * Inspects an upstream-ops URL: only http(s) is accepted, trailing slashes are
 * dropped, and query/hash are stripped so a token embedded in a base_url (for
 * example `?key=...`) never reaches the preview, logs or the import payload.
 */
export function inspectUpstreamOpsUrl(value: unknown): UpstreamOpsUrlInspection {
  if (typeof value !== 'string') {
    return { url: '', scheme: '', hadQueryOrHash: false, invalidScheme: false }
  }
  const raw = value.trim()
  if (!raw) {
    return { url: '', scheme: '', hadQueryOrHash: false, invalidScheme: false }
  }
  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch {
    return { url: '', scheme: '', hadQueryOrHash: false, invalidScheme: true }
  }
  const scheme = parsed.protocol.toLowerCase()
  if (!UPSTREAM_OPS_ALLOWED_SCHEMES.has(scheme)) {
    return { url: '', scheme, hadQueryOrHash: false, invalidScheme: true }
  }
  const path = parsed.pathname.replace(/\/+$/, '')
  return {
    url: `${scheme}//${parsed.host.toLowerCase()}${path}`,
    scheme,
    hadQueryOrHash: parsed.search !== '' || parsed.hash !== '',
    invalidScheme: false
  }
}

/** Lowercases scheme/host, drops trailing slashes and query/hash; non-http(s) becomes ''. */
export function normalizeUpstreamOpsBaseUrl(value: unknown): string {
  return inspectUpstreamOpsUrl(value).url
}

function toChannel(record: Record<string, unknown>, index: number): UpstreamOpsEntry {
  const rawMode = pickString(record, 'credential_mode').toLowerCase()
  // Only the site URL is read. password / token_credential are deliberately ignored.
  const site = inspectUpstreamOpsUrl(pickString(record, 'site_url'))
  return {
    kind: 'channels',
    id: pickId(record) || `channels-${index + 1}`,
    name: pickString(record, 'name'),
    sourceType: pickString(record, 'type').toLowerCase(),
    siteUrl: site.url,
    username: pickString(record, 'username'),
    credentialMode: rawMode === 'password' || rawMode === 'token' ? rawMode : '',
    monitorEnabled: record.monitor_enabled !== false,
    tags: pickStringArray(record, 'tags'),
    notes: pickString(record, 'notes'),
    urlQueryStripped: site.hadQueryOrHash,
    urlSchemeInvalid: site.invalidScheme
  }
}

function toProvider(record: Record<string, unknown>, index: number): UpstreamOpsEntry {
  // api_key_hint is a masked hint produced by upstream-ops; the key itself is never read.
  const base = inspectUpstreamOpsUrl(pickString(record, 'base_url'))
  return {
    kind: 'providers',
    id: pickId(record) || `providers-${index + 1}`,
    name: pickString(record, 'name'),
    baseUrl: base.url,
    apiKeyHint: pickString(record, 'api_key_hint'),
    upstreamProtocol: pickString(record, 'upstream_protocol').toLowerCase(),
    enabled: record.enabled !== false,
    urlQueryStripped: base.hadQueryOrHash,
    urlSchemeInvalid: base.invalidScheme
  }
}

/** Parses one upstream-ops snapshot. Unknown roots accumulate into `issues` instead of throwing. */
export function parseUpstreamOpsSnapshot(input: unknown): UpstreamOpsSnapshot {
  const issues: string[] = []
  const rows = extractRows(input, issues)
  const entries: UpstreamOpsEntry[] = []
  rows.forEach((item, index) => {
    const record = asRecord(item)
    if (!record) {
      issues.push(`row ${index + 1}: not an object`)
      return
    }
    if (typeof record.site_url === 'string') {
      entries.push(toChannel(record, index))
      return
    }
    if (typeof record.base_url === 'string') {
      entries.push(toProvider(record, index))
      return
    }
    issues.push(`row ${index + 1}: neither site_url nor base_url`)
  })
  const kind = entries.length > 0 ? entries[0].kind : null
  return { kind, entries, issues }
}

function normalizeName(value: string): string {
  return value.trim().toLowerCase().replace(/\s+/g, ' ')
}

/**
 * Maps an upstream-ops provider protocol onto a sub2api platform.
 * `auto` (and anything unknown) returns null so the operator has to choose;
 * no silent platform substitution.
 */
export function resolveUpstreamOpsPlatform(protocol: unknown): AccountPlatform | null {
  const value = typeof protocol === 'string' ? protocol.trim().toLowerCase() : ''
  switch (value) {
    case 'openai':
    case 'openai_chat':
    case 'chat':
    case 'openai_responses':
      return 'openai'
    case 'anthropic':
    case 'claude':
    case 'messages':
      return 'anthropic'
    default:
      return null
  }
}

function buildRowKey(entry: UpstreamOpsEntry, index: number): string {
  const base = entry.kind === 'providers' ? entry.baseUrl : entry.siteUrl
  return `${entry.kind}:${entry.id || index}:${base || 'no-url'}`
}

function resolveStatus(reasons: UpstreamOpsReasonCode[], row: Pick<UpstreamOpsPreviewRow, 'kind' | 'platform'>): UpstreamOpsRowStatus {
  if (reasons.includes('missing_name') || reasons.includes('missing_base_url')) return 'invalid'
  if (reasons.includes('url_scheme_required')) return 'invalid'
  if (reasons.includes('channel_snapshot_has_no_credential')) return 'unsupported'
  if (reasons.includes('duplicate_existing') || reasons.includes('duplicate_batch')) return 'duplicate'
  if (row.kind === 'providers' && !row.platform) return 'needs-platform'
  return 'needs-credential'
}

/**
 * Builds the import preview. Never calls the network and never mutates inputs.
 */
export function buildUpstreamOpsPreview(
  snapshot: UpstreamOpsSnapshot,
  options: UpstreamOpsPreviewOptions = {}
): UpstreamOpsPreview {
  const platformOverrides = options.platformOverrides ?? {}
  const existing = options.existingAccounts ?? []

  const existingUrls = new Set<string>()
  const existingNames = new Set<string>()
  for (const account of existing) {
    const url = normalizeUpstreamOpsBaseUrl(account.custom_base_url)
    if (url) existingUrls.add(url)
    const name = normalizeName(account.name ?? '')
    if (name) existingNames.add(name)
  }

  const batchUrls = new Set<string>()
  const batchNames = new Set<string>()
  const rows: UpstreamOpsPreviewRow[] = []

  snapshot.entries.forEach((entry, index) => {
    const key = buildRowKey(entry, index)
    const reasons: UpstreamOpsReasonCode[] = []
    const isProvider = entry.kind === 'providers'
    const baseUrl = isProvider ? entry.baseUrl : entry.siteUrl
    const name = entry.name.trim()
    const override = isProvider ? platformOverrides[key] : undefined
    const platform = isProvider ? override ?? resolveUpstreamOpsPlatform(entry.upstreamProtocol) : null

    if (!name) reasons.push('missing_name')
    if (!baseUrl) {
      reasons.push(entry.urlSchemeInvalid ? 'url_scheme_required' : 'missing_base_url')
    }
    if (entry.urlQueryStripped) reasons.push('url_query_stripped')
    if (isProvider && !platform) reasons.push('unknown_upstream_protocol')
    if (!isProvider) reasons.push('channel_snapshot_has_no_credential')
    if (isProvider && !entry.enabled) reasons.push('provider_disabled')

    let duplicateOf: string | null = null
    const dupName = name ? normalizeName(name) : ''
    if (baseUrl && existingUrls.has(baseUrl)) {
      reasons.push('duplicate_existing')
      duplicateOf = 'existing'
    } else if (dupName && existingNames.has(dupName)) {
      reasons.push('duplicate_existing')
      duplicateOf = 'existing'
    } else if (baseUrl && batchUrls.has(baseUrl)) {
      reasons.push('duplicate_batch')
      duplicateOf = 'batch'
    } else if (!baseUrl && dupName && batchNames.has(dupName)) {
      reasons.push('duplicate_batch')
      duplicateOf = 'batch'
    }

    if (baseUrl) batchUrls.add(baseUrl)
    if (dupName) batchNames.add(dupName)

    const status = resolveStatus(reasons, { kind: entry.kind, platform })
    // provider_disabled is informational only and stays out of the visible reasons;
    // url_query_stripped must stay visible so the operator knows the URL was sanitized.
    const displayReasons = status === 'invalid' || status === 'unsupported'
      ? reasons
      : reasons.filter((reason) => reason !== 'provider_disabled')

    rows.push({
      key,
      kind: entry.kind,
      id: entry.id,
      name,
      baseUrl,
      sourceType: isProvider ? entry.upstreamProtocol : entry.sourceType,
      credentialMode: isProvider ? 'api-key' : entry.credentialMode,
      apiKeyHint: isProvider ? entry.apiKeyHint : '',
      monitorEnabled: isProvider ? true : entry.monitorEnabled,
      enabled: isProvider ? entry.enabled : true,
      platform,
      accountType: isProvider ? 'upstream' : null,
      status,
      reasons: displayReasons,
      duplicateOf
    })
  })

  const summary: UpstreamOpsPreviewSummary = {
    total: rows.length,
    needsCredential: 0,
    needsPlatform: 0,
    duplicate: 0,
    unsupported: 0,
    invalid: 0
  }
  for (const row of rows) {
    if (row.status === 'needs-credential') summary.needsCredential += 1
    else if (row.status === 'needs-platform') summary.needsPlatform += 1
    else if (row.status === 'duplicate') summary.duplicate += 1
    else if (row.status === 'unsupported') summary.unsupported += 1
    else summary.invalid += 1
  }

  return { rows, summary }
}

/**
 * Converts providers with an operator-supplied API key into the existing
 * `sub2api-data` v1 import payload. Keys arrive from the operator form only;
 * nothing from the upstream-ops snapshot is treated as a secret.
 *
 * Conservative scheduling placeholders (concurrency 1, priority 0) keep the
 * imported account from outranking or flooding existing traffic; the admin can
 * raise them afterwards.
 */
export interface UpstreamOpsImportRequest {
  data: AdminDataPayload
  skip_default_group_bind: true
}

export interface UpstreamOpsImportRequestResult {
  request: UpstreamOpsImportRequest
  includedKeys: string[]
  skipped: Array<{ key: string; status: UpstreamOpsRowStatus }>
}

/**
 * Wraps the payload in the exact body the existing admin import endpoint expects
 * (POST /api/v1/admin/accounts/data) with default-group binding left off, so an
 * import never silently joins a group.
 */
export function buildUpstreamOpsImportRequest(
  rows: UpstreamOpsPreviewRow[],
  credentials: Record<string, UpstreamOpsOperatorCredential> = {}
): UpstreamOpsImportRequestResult {
  const result = buildUpstreamOpsDataPayload(rows, credentials)
  return {
    request: { data: result.payload, skip_default_group_bind: true },
    includedKeys: result.includedKeys,
    skipped: result.skipped
  }
}
export function buildUpstreamOpsDataPayload(
  rows: UpstreamOpsPreviewRow[],
  credentials: Record<string, UpstreamOpsOperatorCredential> = {}
): UpstreamOpsPayloadResult {
  const accounts: AdminDataAccount[] = []
  const includedKeys: string[] = []
  const skipped: Array<{ key: string; status: UpstreamOpsRowStatus }> = []

  for (const row of rows) {
    const apiKey = (credentials[row.key]?.apiKey ?? '').trim()
    const eligible = row.kind === 'providers'
      && Boolean(row.platform)
      && Boolean(row.accountType)
      && Boolean(row.baseUrl)
      && Boolean(row.name)
      && row.status !== 'duplicate'
      && row.status !== 'invalid'
      && row.status !== 'unsupported'
    if (!eligible || !apiKey) {
      skipped.push({ key: row.key, status: eligible && !apiKey ? 'needs-credential' : row.status })
      continue
    }
    accounts.push({
      name: row.name,
      platform: row.platform as AccountPlatform,
      type: row.accountType as AccountType,
      credentials: { base_url: row.baseUrl, api_key: apiKey },
      notes: `imported from upstream-ops ${row.kind}#${row.id}`,
      concurrency: 1,
      priority: 0
    })
    includedKeys.push(row.key)
  }

  return {
    payload: {
      type: 'sub2api-data',
      version: 1,
      exported_at: new Date().toISOString(),
      proxies: [],
      accounts
    },
    includedKeys,
    skipped
  }
}
