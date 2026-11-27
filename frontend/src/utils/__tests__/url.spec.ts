import { describe, expect, it } from 'vitest'
import { sanitizeRedirectPath } from '../url'

describe('sanitizeRedirectPath', () => {
  it('preserves safe internal relative paths with query parameters', () => {
    expect(sanitizeRedirectPath('/docs?page=agent-setup&agent=opencode')).toBe(
      '/docs?page=agent-setup&agent=opencode',
    )
    expect(sanitizeRedirectPath('/keys')).toBe('/keys')
  })

  it('rejects external URLs, protocol-relative paths, backslashes, and control characters', () => {
    expect(sanitizeRedirectPath('https://evil.example/phish')).toBe('/dashboard')
    expect(sanitizeRedirectPath('//evil.example/phish')).toBe('/dashboard')
    expect(sanitizeRedirectPath('/\\evil.example/phish')).toBe('/dashboard')
    expect(sanitizeRedirectPath('/docs\tevil')).toBe('/dashboard')
    expect(sanitizeRedirectPath('/docs\x00evil')).toBe('/dashboard')
    expect(sanitizeRedirectPath('/docs\revil')).toBe('/dashboard')
    expect(sanitizeRedirectPath('/docs\nevil')).toBe('/dashboard')
    expect(sanitizeRedirectPath('/docs\x7fevil')).toBe('/dashboard')
  })
})
