import { describe, expect, it } from 'vitest'
import {
  UPSTREAM_OPS_AUTH_CONTRACT,
  buildUpstreamOpsDataPayload,
  buildUpstreamOpsImportRequest,
  buildUpstreamOpsPreview,
  describeUpstreamOpsAuthContract,
  inspectUpstreamOpsUrl,
  normalizeUpstreamOpsBaseUrl,
  parseUpstreamOpsSnapshot,
  resolveUpstreamOpsPlatform
} from '../upstreamOpsCompat'

const PLANTED_SECRET = 'planted-plaintext-secret-never-copy'

function channelRow(overrides: Record<string, unknown> = {}) {
  return {
    id: 7,
    name: 'kedaya',
    type: 'sub2api',
    site_url: 'https://Kedaya.AI/',
    username: 'ops@example.com',
    credential_mode: 'token',
    monitor_enabled: true,
    tags: ['prod', 'billing'],
    notes: '',
    // Secrets that must never be read or forwarded by the adapter.
    password: PLANTED_SECRET,
    password_cipher: 'cipher-blob',
    token_credential: PLANTED_SECRET,
    ...overrides
  }
}

function providerRow(overrides: Record<string, unknown> = {}) {
  return {
    id: 11,
    name: 'deepseek-direct',
    base_url: 'https://api.deepseek.com/',
    api_key_hint: 'hint-...wxyz',
    upstream_protocol: 'openai',
    default_billing_rate: 1,
    enabled: true,
    api_key: PLANTED_SECRET,
    ...overrides
  }
}

describe('parseUpstreamOpsSnapshot', () => {
  it('reads the GET /api/channels envelope and drops secret fields', () => {
    const snapshot = parseUpstreamOpsSnapshot({ data: [channelRow()] })

    expect(snapshot.kind).toBe('channels')
    expect(snapshot.issues).toEqual([])
    expect(snapshot.entries).toHaveLength(1)
    const entry = snapshot.entries[0]
    expect(entry).toMatchObject({
      kind: 'channels',
      id: '7',
      name: 'kedaya',
      sourceType: 'sub2api',
      siteUrl: 'https://kedaya.ai',
      credentialMode: 'token',
      monitorEnabled: true,
      tags: ['prod', 'billing']
    })
    expect(JSON.stringify(snapshot)).not.toContain(PLANTED_SECRET)
  })

  it('reads the paginated channels envelope', () => {
    const snapshot = parseUpstreamOpsSnapshot({
      data: { items: [channelRow()], total: 1, page: 1, page_size: 20, pages: 1 }
    })

    expect(snapshot.kind).toBe('channels')
    expect(snapshot.entries).toHaveLength(1)
  })

  it('reads a bare provider array from GET /api/gateway/providers data', () => {
    const snapshot = parseUpstreamOpsSnapshot([providerRow()])

    expect(snapshot.kind).toBe('providers')
    expect(snapshot.entries[0]).toMatchObject({
      kind: 'providers',
      id: '11',
      name: 'deepseek-direct',
      baseUrl: 'https://api.deepseek.com',
      apiKeyHint: 'hint-...wxyz',
      upstreamProtocol: 'openai',
      enabled: true
    })
    expect(JSON.stringify(snapshot)).not.toContain(PLANTED_SECRET)
  })

  it('reports unknown roots and rows without a URL instead of throwing', () => {
    expect(parseUpstreamOpsSnapshot({ hello: 'world' }).issues).toEqual(['root: no data/items array found'])
    expect(parseUpstreamOpsSnapshot(null).issues).toEqual(['root: expected an object or array'])
    expect(parseUpstreamOpsSnapshot({ data: [{ name: 'no-url' }] }).issues).toEqual([
      'row 1: neither site_url nor base_url'
    ])
  })
})

describe('normalizeUpstreamOpsBaseUrl', () => {
  it('lowercases host, drops trailing slashes and keeps the path', () => {
    expect(normalizeUpstreamOpsBaseUrl('  HTTPS://API.DeepSeek.com// ')).toBe('https://api.deepseek.com')
    expect(normalizeUpstreamOpsBaseUrl('https://example.com/v1/')).toBe('https://example.com/v1')
    expect(normalizeUpstreamOpsBaseUrl('')).toBe('')
    expect(normalizeUpstreamOpsBaseUrl(42)).toBe('')
  })
})

describe('resolveUpstreamOpsPlatform', () => {
  it('maps only the documented protocol values', () => {
    expect(resolveUpstreamOpsPlatform('openai')).toBe('openai')
    expect(resolveUpstreamOpsPlatform('openai_responses')).toBe('openai')
    expect(resolveUpstreamOpsPlatform('anthropic')).toBe('anthropic')
    expect(resolveUpstreamOpsPlatform('auto')).toBeNull()
    expect(resolveUpstreamOpsPlatform('')).toBeNull()
    expect(resolveUpstreamOpsPlatform(undefined)).toBeNull()
  })
})

describe('buildUpstreamOpsPreview', () => {
  it('keeps providers pending a key, and channels unsupported without inventing a mapping', () => {
    const snapshot = parseUpstreamOpsSnapshot({ data: [providerRow(), channelRow()] })
    const preview = buildUpstreamOpsPreview(snapshot)

    expect(preview.summary.total).toBe(2)
    const [provider, channel] = preview.rows
    expect(provider).toMatchObject({ kind: 'providers', platform: 'openai', accountType: 'upstream', status: 'needs-credential' })
    expect(channel).toMatchObject({ kind: 'channels', platform: null, accountType: null, status: 'unsupported' })
    expect(channel.reasons).toContain('channel_snapshot_has_no_credential')
  })

  it('requires an explicit platform when the protocol is auto', () => {
    const snapshot = parseUpstreamOpsSnapshot({ data: [providerRow({ upstream_protocol: 'auto' })] })
    const preview = buildUpstreamOpsPreview(snapshot)

    expect(preview.rows[0].status).toBe('needs-platform')
    expect(preview.rows[0].reasons).toContain('unknown_upstream_protocol')

    const overridden = buildUpstreamOpsPreview(snapshot, {
      platformOverrides: { [preview.rows[0].key]: 'anthropic' }
    })
    expect(overridden.rows[0]).toMatchObject({ platform: 'anthropic', status: 'needs-credential' })
    expect(overridden.rows[0].reasons).not.toContain('unknown_upstream_protocol')
  })

  it('dedupes against existing accounts by normalized URL and by name', () => {
    const snapshot = parseUpstreamOpsSnapshot({
      data: [providerRow(), providerRow({ id: 12, name: 'other', base_url: 'https://example.com' })]
    })
    const preview = buildUpstreamOpsPreview(snapshot, {
      existingAccounts: [
        { name: 'something', custom_base_url: 'https://api.deepseek.com/' },
        { name: '  OTHER ', custom_base_url: null }
      ]
    })

    expect(preview.rows[0].status).toBe('duplicate')
    expect(preview.rows[0].duplicateOf).toBe('existing')
    expect(preview.rows[1].status).toBe('duplicate')
    expect(preview.summary.duplicate).toBe(2)
  })

  it('dedupes duplicates inside one batch', () => {
    const snapshot = parseUpstreamOpsSnapshot({
      data: [
        providerRow({ id: 21, name: 'first' }),
        providerRow({ id: 22, name: 'second' })
      ]
    })
    const preview = buildUpstreamOpsPreview(snapshot)

    expect(preview.rows[0].status).toBe('needs-credential')
    expect(preview.rows[1].status).toBe('duplicate')
    expect(preview.rows[1].reasons).toContain('duplicate_batch')
  })

  it('keeps disabled providers visible but does not treat them as invalid', () => {
    const snapshot = parseUpstreamOpsSnapshot({
      data: [providerRow({ enabled: false }), channelRow({ monitor_enabled: false })]
    })
    const preview = buildUpstreamOpsPreview(snapshot)

    expect(preview.rows[0]).toMatchObject({ enabled: false, status: 'needs-credential' })
    expect(preview.rows[0].reasons).not.toContain('provider_disabled')
    expect(preview.rows[1].monitorEnabled).toBe(false)
  })
})

describe('buildUpstreamOpsDataPayload', () => {
  it('emits the existing sub2api-data v1 contract for keyed providers only', () => {
    const snapshot = parseUpstreamOpsSnapshot({
      data: [providerRow(), channelRow(), providerRow({ id: 13, name: 'no-key', base_url: 'https://api.other.dev' })]
    })
    const preview = buildUpstreamOpsPreview(snapshot)
    const [first, , third] = preview.rows
    const { payload, includedKeys, skipped } = buildUpstreamOpsDataPayload(preview.rows, {
      [first.key]: { apiKey: 'operator-typed-key-for-test' }
    })

    expect(payload.type).toBe('sub2api-data')
    expect(payload.version).toBe(1)
    expect(payload.proxies).toEqual([])
    expect(payload.accounts).toHaveLength(1)
    expect(payload.accounts[0]).toMatchObject({
      name: 'deepseek-direct',
      platform: 'openai',
      type: 'upstream',
      credentials: { base_url: 'https://api.deepseek.com', api_key: 'operator-typed-key-for-test' },
      concurrency: 1,
      priority: 0
    })
    expect(includedKeys).toEqual([first.key])
    expect(skipped).toEqual([
      { key: preview.rows[1].key, status: 'unsupported' },
      { key: third.key, status: 'needs-credential' }
    ])
  })

  it('never forwards a secret that only existed in the upstream-ops snapshot', () => {
    const snapshot = parseUpstreamOpsSnapshot({ data: [providerRow()] })
    const preview = buildUpstreamOpsPreview(snapshot)
    const first = preview.rows[0]

    const unsolicited = buildUpstreamOpsDataPayload(preview.rows)
    expect(unsolicited.payload.accounts).toEqual([])
    expect(JSON.stringify(unsolicited)).not.toContain(PLANTED_SECRET)

    const withOperatorKey = buildUpstreamOpsDataPayload(preview.rows, { [first.key]: { apiKey: 'operator-typed-key-for-test' } })
    const serialized = JSON.stringify(withOperatorKey)
    expect(serialized).not.toContain(PLANTED_SECRET)
    expect(serialized).not.toContain('api_key_hint')
    expect(serialized).not.toContain('password')
    expect(serialized).not.toContain('token_credential')
  })

  it('keeps duplicate and unsupported rows out even when a key is supplied', () => {
    const snapshot = parseUpstreamOpsSnapshot({ data: [providerRow(), providerRow({ id: 14 }), channelRow()] })
    const preview = buildUpstreamOpsPreview(snapshot)
    const credentials = Object.fromEntries(preview.rows.map((row) => [row.key, { apiKey: 'operator-typed-key-for-test' }]))
    const { payload } = buildUpstreamOpsDataPayload(preview.rows, credentials)

    expect(payload.accounts).toHaveLength(1)
    expect(payload.accounts[0].name).toBe('deepseek-direct')
  })
})

describe('buildUpstreamOpsImportRequest', () => {
  it('wraps the payload exactly as the existing import endpoint expects', () => {
    const snapshot = parseUpstreamOpsSnapshot({ data: [providerRow()] })
    const preview = buildUpstreamOpsPreview(snapshot)
    const row = preview.rows[0]

    const { request, includedKeys } = buildUpstreamOpsImportRequest(preview.rows, {
      [row.key]: { apiKey: 'operator-typed-key-for-test' }
    })

    expect(request.skip_default_group_bind).toBe(true)
    expect(request.data.type).toBe('sub2api-data')
    expect(request.data.version).toBe(1)
    expect(request.data.accounts).toHaveLength(1)
    expect(request.data.accounts[0].credentials.api_key).toBe('operator-typed-key-for-test')
    expect(includedKeys).toEqual([row.key])
  })

  it('never produces a request body when no key is supplied', () => {
    const snapshot = parseUpstreamOpsSnapshot({ data: [providerRow()] })
    const preview = buildUpstreamOpsPreview(snapshot)
    const { request } = buildUpstreamOpsImportRequest(preview.rows)

    expect(request.data.accounts).toEqual([])
    expect(JSON.stringify(request)).not.toContain(PLANTED_SECRET)
  })
})
describe('URL hardening', () => {
  it('rejects non-http(s) schemes instead of normalizing them into an importable URL', () => {
    expect(normalizeUpstreamOpsBaseUrl('javascript:alert(1)')).toBe('')
    expect(normalizeUpstreamOpsBaseUrl('data:text/html,hello')).toBe('')
    expect(normalizeUpstreamOpsBaseUrl('ftp://example.com')).toBe('')
    expect(normalizeUpstreamOpsBaseUrl('api.example.com')).toBe('')
    expect(inspectUpstreamOpsUrl('javascript:alert(1)')).toMatchObject({ url: '', invalidScheme: true })
    expect(inspectUpstreamOpsUrl('api.example.com')).toMatchObject({ url: '', invalidScheme: true })
  })

  it('strips query and hash so embedded secrets never reach preview or payload', () => {
    const inspected = inspectUpstreamOpsUrl('https://api.example.com/v1?key=SECRET_IN_QUERY#SECRET_FRAGMENT')
    expect(inspected.url).toBe('https://api.example.com/v1')
    expect(inspected.hadQueryOrHash).toBe(true)

    const snapshot = parseUpstreamOpsSnapshot({
      data: [
        {
          id: 31,
          name: 'query-secret',
          base_url: 'https://api.example.com/v1?key=SECRET_IN_QUERY',
          api_key_hint: 'hint-...wxyz',
          upstream_protocol: 'openai',
          enabled: true
        }
      ]
    })
    const preview = buildUpstreamOpsPreview(snapshot)
    expect(preview.rows[0].baseUrl).toBe('https://api.example.com/v1')
    expect(preview.rows[0].reasons).toContain('url_query_stripped')

    const { payload } = buildUpstreamOpsDataPayload(preview.rows, {
      [preview.rows[0].key]: { apiKey: 'operator-typed-key-for-test' }
    })
    expect(payload.accounts[0].credentials.base_url).toBe('https://api.example.com/v1')
    expect(JSON.stringify(preview)).not.toContain('SECRET_IN_QUERY')
    expect(JSON.stringify(payload)).not.toContain('SECRET_IN_QUERY')
  })

  it('marks a non-http(s) provider invalid and keeps it out of the payload', () => {
    const snapshot = parseUpstreamOpsSnapshot({
      data: [
        {
          id: 32,
          name: 'bad-scheme',
          base_url: 'javascript:alert(1)',
          api_key_hint: '',
          upstream_protocol: 'openai',
          enabled: true
        }
      ]
    })
    const preview = buildUpstreamOpsPreview(snapshot)
    expect(preview.rows[0].status).toBe('invalid')
    expect(preview.rows[0].reasons).toContain('url_scheme_required')

    const credentials = Object.fromEntries(preview.rows.map((row) => [row.key, { apiKey: 'operator-typed-key-for-test' }]))
    const { payload } = buildUpstreamOpsDataPayload(preview.rows, credentials)
    expect(payload.accounts).toEqual([])
  })
})
describe('described auth contract', () => {
  it('states the cookie / session_managed boundary explicitly', () => {
    expect(describeUpstreamOpsAuthContract()).toBe(UPSTREAM_OPS_AUTH_CONTRACT)
    expect(UPSTREAM_OPS_AUTH_CONTRACT).toMatchObject({
      browserSessionMode: 'session_managed',
      browserSessionTransport: 'http-only-cookie',
      connectorAuthMode: 'bearer-token-only',
      supportsCookieJar: false,
      supportsSessionManagedCookie: false,
      supportsCsrfToken: false,
      requiresAccessToken: true,
      disablesTwoFactor: false
    })
  })
})
