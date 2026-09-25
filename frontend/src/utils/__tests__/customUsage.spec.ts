import { describe, expect, it } from 'vitest'
import { createUsageDraft, parseUsageEditors, supportsCustomUsage, usagePayload, usageTemplate, validateUsageDraft } from '../customUsage'
const draft = () => ({ ...createUsageDraft('https://example.test/'), enabled: true })
describe('custom usage drafts', () => {
  it('only allows API-key accounts with an explicit base URL', () => {
    expect(supportsCustomUsage({ type: 'apikey', credentials: { base_url: 'https://example.test' } })).toBe(true)
    expect(supportsCustomUsage({ type: 'oauth', credentials: { base_url: 'https://example.test' } })).toBe(false)
    expect(supportsCustomUsage({ type: 'apikey', credentials: { base_url: ' ' } })).toBe(false)
  })
  it('defaults to a ten-second timeout and scheduled refresh', () => {
    expect(draft()).toMatchObject({ enabled: true, timeout_seconds: 10, interval_minutes: 10 })
    expect(validateUsageDraft(draft())).toBeNull()
  })
  it('uses the contracted NewAPI user endpoint and conversion', () => {
    expect(usageTemplate('newapi')).toEqual({ request: { url: '{{baseUrl}}/api/user/self', method: 'GET', headers: { Authorization: 'Bearer {{accessToken}}', 'New-Api-User': '{{userId}}' } }, extractor: { remaining: { path: 'data.quota', divisor: 500000 }, used: { path: 'data.used_quota', divisor: 500000 }, unit: { value: 'USD' } } })
  })
  it('uses the actual unwrapped Sub2API response, without invented data paths', () => {
    expect(usageTemplate('sub2api').extractor).toEqual({ remaining: { path: 'remaining' }, unit: { path: 'unit' }, plan_name: { path: 'planName' } })
  })
  it('omits empty secrets and presence flags, but explicitly sends clear operations', () => {
    expect(usagePayload({ ...draft(), api_key: '', access_token: ' ', has_api_key: true, has_access_token: true })).not.toHaveProperty('api_key')
    expect(usagePayload({ ...draft(), api_key: 'do-not-send', clear_api_key: true })).toMatchObject({ clear_api_key: true })
    expect(usagePayload({ ...draft(), api_key: 'do-not-send', clear_api_key: true })).not.toHaveProperty('api_key')
    expect(usagePayload({ ...draft(), api_key: 'draft-value' }).api_key).toBe('draft-value')
  })
  it.each([0, 31, 1.1, NaN])('rejects invalid timeout %s', timeout_seconds => expect(validateUsageDraft({ ...draft(), timeout_seconds })).toBe('invalidTimeout'))
  it.each([-1, 1, 4, 5.5, 525601])('rejects invalid interval %s', interval_minutes => expect(validateUsageDraft({ ...draft(), interval_minutes })).toBe('invalidInterval'))
  it.each([0, 5, 30])('accepts interval %s', interval_minutes => expect(validateUsageDraft({ ...draft(), interval_minutes })).toBeNull())
  it('validates JSON objects, headers, paths and positive divisors without evaluating code', () => {
    const parse = (extractor: unknown) => parseUsageEditors('{}', JSON.stringify(extractor)).error
    expect(parseUsageEditors('{', '{}').error).toBe('invalidJson')
    expect(parseUsageEditors('[]', '{}').error).toBe('invalidHeaders')
    expect(parseUsageEditors('{"Host":"example.test"}', '{}').error).toBe('invalidHeaders')
    expect(parseUsageEditors('{"X-Test":12}', '{}').error).toBe('invalidHeaders')
    expect(parse({ remaining: { path: 'data.quota', divisor: 0 } })).toBe('invalidExtractor')
    expect(parse({ remaining: { path: '(()=>alert(1))()' } })).toBe('invalidExtractor')
    expect(parse({ script: 'return response.balance' })).toBe('invalidExtractor')
    expect(parse({ remaining: { path: 'data.__proto__.balance' } })).toBe('invalidExtractor')
    expect(parse({ remaining: { path: '$.data.items[0].quota', divisor: 500000 } })).toBeUndefined()
    expect(parse({ remaining: { path: 'balance' }, unit: { path: 'unit', value: 'USD' } })).toBe('invalidExtractor')
  })
  it('validates URLs and only permits exact supported placeholders', () => {
    const config = draft()
    expect(validateUsageDraft({ ...config, base_url: 'javascript:alert(1)' })).toBe('invalidUrl')
    expect(validateUsageDraft({ ...config, base_url: 'https://name:password@example.test' })).toBe('invalidUrl')
    expect(validateUsageDraft({ ...config, request: { ...config.request, url: '{{unknown}}/usage' } })).toBe('invalidPlaceholder')
    expect(validateUsageDraft({ ...config, request: { ...config.request, url: '{{ baseUrl }}/usage' } })).toBe('invalidPlaceholder')
  })
})
