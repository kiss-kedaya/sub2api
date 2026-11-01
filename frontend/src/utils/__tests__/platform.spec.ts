import { describe, expect, it } from 'vitest'
import { detectPlatform } from '../platform'

describe('platform detection', () => {
  it.each([
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64)', 'windows'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 14_4)', 'macos'],
    ['Mozilla/5.0 (X11; Ubuntu; Linux x86_64)', 'linux'],
    ['Mozilla/5.0 (Linux; Android 14; Pixel 8)', 'android'],
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X)', 'ios'],
    ['Mozilla/5.0 (iPad; CPU OS 17_4 like Mac OS X)', 'ios'],
  ] as const)('detects %s as %s', (userAgent, expected) => {
    expect(detectPlatform(userAgent)).toBe(expected)
  })

  it('leaves unrecognized user agents neutral', () => {
    expect(detectPlatform('Some embedded browser')).toBe('unknown')
  })
})
