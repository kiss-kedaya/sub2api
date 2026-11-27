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

  it('sanitizeInternalRedirectPath handles array and non-string query values without throwing', () => {
    expect(sanitizeInternalRedirectPath(['/settings', '/orders'])).toBe('/settings')
    expect(sanitizeInternalRedirectPath(['', '   ', '/purchase?plan_id=9'])).toBe('/purchase?plan_id=9')
    expect(sanitizeInternalRedirectPath([null, undefined, '/orders'])).toBe('/orders')
    expect(sanitizeInternalRedirectPath(['//evil.example/phish', '/settings'], '/dashboard')).toBe('/dashboard')
    expect(sanitizeInternalRedirectPath(['', '   ', null], '/dashboard')).toBe('/dashboard')
    expect(sanitizeInternalRedirectPath([], '/dashboard')).toBe('/dashboard')
    expect(sanitizeInternalRedirectPath(null, '/dashboard')).toBe('/dashboard')
    expect(sanitizeInternalRedirectPath(undefined, '/dashboard')).toBe('/dashboard')
    expect(sanitizeInternalRedirectPath(123, '/dashboard')).toBe('/dashboard')
    expect(sanitizeInternalRedirectPath(true, '/dashboard')).toBe('/dashboard')
    expect(sanitizeInternalRedirectPath({ path: '/settings' }, '/dashboard')).toBe('/dashboard')
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
