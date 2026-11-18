import { describe, expect, it } from 'vitest'
import { createSmallRetentionDemo } from './small-retention'
import type { SmallDetail, SmallStats, SmallRecord } from '../src/api/admin/smallRequestRetention'

const now = new Date('2026-11-18T02:00:00Z')
const base = '/api/v1/admin/request-captures'
describe('local synthetic small retention evidence', () => {
  it('provides consistent statistics, exact filters and response variants without real credentials', () => {
    const api = createSmallRetentionDemo(now)
    const get = (path: string, params = '') => api('GET', base + path, new URLSearchParams(params), {})
    const rows = (get('/small') as { items: SmallRecord[] }).items
    const stats = get('/small/stats') as SmallStats
    expect(rows).toHaveLength(12)
    expect(stats.stored_limit_bytes).toBe(4294967296)
    expect(stats.quota_full).toBe(false)
    expect(stats.requests).toBe(rows.reduce((sum, row) => sum + row.repeat_count, 0))
    expect(stats.duplicates).toBe(stats.requests - stats.records)
    expect(Object.values(stats.repeat_distribution).reduce((a, b) => a + b, 0)).toBe(rows.length)
    expect((get('/small', 'status=502') as { items: SmallRecord[] }).items.map(row => row.status)).toEqual([502])
    expect((get('/small', 'min_repeats=100') as { items: SmallRecord[] }).items.every(row => row.repeat_count >= 100)).toBe(true)
    const detail = get('/small/1') as SmallDetail
    expect(detail.responses).toHaveLength(3)
    expect(detail.responses[0]!.body).toContain('event: response.completed')
    expect(detail.responses.reduce((sum, response) => sum + response.repeat_count, 0) + detail.record.omitted_responses).toBe(detail.record.repeat_count)
    expect(detail.curl).toContain('https://example.invalid/')
    expect(detail.request_headers.authorization).toEqual(['<REPLACE_CREDENTIAL>'])
  })

  it('changes scenarios and switches in memory without leaking between demo instances', () => {
    const api = createSmallRetentionDemo(now)
    api('POST', '/api/v1/__preview/small-retention', new URLSearchParams(), { state: 'empty' })
    expect(api('GET', base + '/small/stats', new URLSearchParams(), {})).toMatchObject({ records: 0 })
    expect(api('GET', base + '/config', new URLSearchParams(), {})).toMatchObject({ small_request_enabled: false })
    expect(api('PUT', base + '/small/config', new URLSearchParams(), { enabled: true })).toEqual({ enabled: true, ready: true })
    expect(createSmallRetentionDemo(now)('GET', base + '/small/stats', new URLSearchParams(), {})).toMatchObject({ records: 12 })
    api('POST', '/api/v1/__preview/small-retention', new URLSearchParams(), { state: 'not-ready' })
    expect(() => api('PUT', base + '/small/config', new URLSearchParams(), { enabled: true })).toThrow()
  })
})
