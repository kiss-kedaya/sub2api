import { describe, expect, it } from 'vitest'
import { sanitizeInternalRedirectPath, sanitizeUrl } from '../url'

describe('url sanitization helpers', () => {
  it.each([
    '//evil.example/phish',
    '/\\evil.example/phish',
    '/\t/evil.example/phish',
    '/\n/evil.example/phish',
    '/\r/evil.example/phish',
    '/\x00/evil.example/phish',
    'https://evil.example/phish',
    'javascript:alert(1)',
    '/dashboard?next=https://evil.example',
    'dashboard',
  ])('sanitizeInternalRedirectPath rejects unsafe redirect (%s)', (bad) => {
    expect(sanitizeInternalRedirectPath(bad, '/dashboard')).toBe('/dashboard')
  })

  it('sanitizeInternalRedirectPath preserves valid relative paths', () => {
    expect(sanitizeInternalRedirectPath('/dashboard?tab=keys')).toBe('/dashboard?tab=keys')
    expect(sanitizeInternalRedirectPath(' /purchase?plan_id=7 ', '/purchase')).toBe('/purchase?plan_id=7')
  })

  it.each([
    '//evil.example/logo.png',
    '/\\evil.example/logo.png',
    '/\t/evil.example/logo.png',
    '/\n/evil.example/logo.png',
    '/\r/evil.example/logo.png',
    'javascript:alert(1)',
  ])('sanitizeUrl rejects unsafe relative/scheme URL (%s)', (bad) => {
    expect(sanitizeUrl(bad, { allowRelative: true })).toBe('')
  })

  it('sanitizeUrl accepts safe relative and absolute URLs', () => {
    expect(sanitizeUrl('/assets/logo.png', { allowRelative: true })).toBe('/assets/logo.png')
    expect(sanitizeUrl('https://app.example.com/docs')).toBe('https://app.example.com/docs')
  })
})
