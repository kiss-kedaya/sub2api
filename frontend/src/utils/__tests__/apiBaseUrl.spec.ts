import { describe, expect, it } from 'vitest'
import { apiBaseUrlWithV1, apiSiteRoot } from '../apiBaseUrl'

describe('apiSiteRoot', () => {
  it.each([
    ['https://kedaya.ai', 'https://kedaya.ai'],
    ['https://kedaya.ai/', 'https://kedaya.ai'],
    ['https://kedaya.ai///', 'https://kedaya.ai'],
    ['https://kedaya.ai/v1', 'https://kedaya.ai'],
    ['https://kedaya.ai/v1/', 'https://kedaya.ai'],
    ['https://kedaya.ai/V1', 'https://kedaya.ai'],
    ['  https://kedaya.ai/v1  ', 'https://kedaya.ai'],
    ['http://127.0.0.1:17777/v1', 'http://127.0.0.1:17777'],
    ['https://example.test/gateway/v1', 'https://example.test/gateway'],
  ])('%s -> %s', (input, expected) => {
    expect(apiSiteRoot(input)).toBe(expected)
  })

  it('removes a /v1 that was typed more than once', () => {
    expect(apiSiteRoot('https://kedaya.ai/v1/v1')).toBe('https://kedaya.ai')
    expect(apiSiteRoot('https://kedaya.ai/v1/v1/v1/')).toBe('https://kedaya.ai')
  })

  it('only removes a whole trailing /v1 segment', () => {
    expect(apiSiteRoot('https://example.test/v10')).toBe('https://example.test/v10')
    expect(apiSiteRoot('https://example.test/v1beta')).toBe('https://example.test/v1beta')
    expect(apiSiteRoot('https://example.test/v1x/v1')).toBe('https://example.test/v1x')
    expect(apiSiteRoot('https://example.test/api-v1')).toBe('https://example.test/api-v1')
  })

  it('is empty for nothing', () => {
    expect(apiSiteRoot('')).toBe('')
    expect(apiSiteRoot('   ')).toBe('')
    expect(apiSiteRoot(null)).toBe('')
    expect(apiSiteRoot(undefined)).toBe('')
  })
})

describe('apiBaseUrlWithV1', () => {
  it('always ends in exactly one /v1, whichever way the address was written', () => {
    for (const input of ['https://kedaya.ai', 'https://kedaya.ai/', 'https://kedaya.ai/v1', 'https://kedaya.ai/v1/v1/']) {
      expect(apiBaseUrlWithV1(input)).toBe('https://kedaya.ai/v1')
    }
  })

  it('gives nothing to build from when there is no address', () => {
    expect(apiBaseUrlWithV1('')).toBe('')
    expect(apiBaseUrlWithV1(undefined)).toBe('')
  })
})
