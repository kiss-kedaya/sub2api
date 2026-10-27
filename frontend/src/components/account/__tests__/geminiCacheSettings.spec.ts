import { describe, expect, it } from 'vitest'
import { applyGeminiCacheSettings, readGeminiCacheSettings } from '../geminiCacheSettings'

describe('Gemini explicit cache account settings', () => {
  it('does not silently opt custom gateways in', () => {
    expect(readGeminiCacheSettings()).toEqual({ enabled: false, maxTTLSeconds: 0 })
    expect(readGeminiCacheSettings({ explicit_cache_upstream: 'true' }).enabled).toBe(false)
  })
  it('limits TTL while preserving unrelated credentials', () => {
    const credentials: Record<string, unknown> = { base_url: 'https://example.com', api_key: 'fixture' }
    applyGeminiCacheSettings(credentials, { enabled: true, maxTTLSeconds: 90000 }, 'gemini', true)
    expect(credentials.explicit_cache_upstream).toBe(true)
    expect(credentials.explicit_cache_upstream_max_ttl_seconds).toBe(86400)
    expect(credentials.api_key).toBe('fixture')
  })
  it('removes stale capabilities when the protocol is changed', () => {
    const credentials: Record<string, unknown> = { explicit_cache_upstream: true, explicit_cache_upstream_max_ttl_seconds: 3600 }
    applyGeminiCacheSettings(credentials, { enabled: true, maxTTLSeconds: 3600 }, 'gemini', false)
    expect(credentials).toEqual({})
  })
})
