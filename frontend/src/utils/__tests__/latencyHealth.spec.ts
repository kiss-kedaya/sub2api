import { describe, expect, it } from 'vitest'

import { durationSeverity, firstTokenSeverity, formatUsageOutputRate } from '../latencyHealth'

describe('latencyHealth', () => {
  it('classifies first-token latency at 10s/30s/60s boundaries', () => {
    expect(firstTokenSeverity(0)).toBe('good')
    expect(firstTokenSeverity(9_999)).toBe('good')
    expect(firstTokenSeverity(10_000)).toBe('warn')
    expect(firstTokenSeverity(29_999)).toBe('warn')
    expect(firstTokenSeverity(30_000)).toBe('slow')
    expect(firstTokenSeverity(59_999)).toBe('slow')
    expect(firstTokenSeverity(60_000)).toBe('critical')
  })

  it('classifies total duration at 1min/3min/5min boundaries', () => {
    expect(durationSeverity(0)).toBe('good')
    expect(durationSeverity(59_999)).toBe('good')
    expect(durationSeverity(60_000)).toBe('warn')
    expect(durationSeverity(179_999)).toBe('warn')
    expect(durationSeverity(180_000)).toBe('slow')
    expect(durationSeverity(299_999)).toBe('slow')
    expect(durationSeverity(300_000)).toBe('critical')
  })
})

describe('formatUsageOutputRate', () => {
  const row = {
    output_tokens: 1000,
    duration_ms: 20_000,
    image_count: 0,
    image_output_tokens: 0,
    billing_mode: 'token',
    request_type: 'stream' as const,
  }

  it('divides output tokens by the recorded total duration', () => {
    // 1000 Token / 20s，包含首字等待，不做"生成窗口"切分。
    expect(formatUsageOutputRate(row)).toBe('50.0 t/s')
    expect(formatUsageOutputRate({ ...row, output_tokens: 101, duration_ms: 1938 })).toBe('52.1 t/s')
    expect(formatUsageOutputRate({ ...row, output_tokens: 101, duration_ms: 3000 })).toBe('33.7 t/s')
  })

  it.each(['sync', 'stream', 'ws_v2', 'cyber', 'unknown', undefined] as const)(
    'works for %s requests',
    (request_type) => {
      expect(formatUsageOutputRate({ ...row, request_type })).toBe('50.0 t/s')
    },
  )

  it.each([null, 0, -1, Number.NaN, Number.POSITIVE_INFINITY])('hides rate for duration %s', (duration_ms) => {
    expect(formatUsageOutputRate({ ...row, duration_ms })).toBe('-')
  })

  it.each([0, -1, Number.NaN, Number.POSITIVE_INFINITY])('hides rate for output %s', (output_tokens) => {
    expect(formatUsageOutputRate({ ...row, output_tokens })).toBe('-')
  })

  it.each([
    { image_count: 1 },
    { image_output_tokens: 100 },
    { billing_mode: 'image' },
    { billing_mode: 'video' },
    { request_type: 'live' as const },
  ])('omits media request rates: %j', (fields) => {
    expect(formatUsageOutputRate({ ...row, ...fields })).toBe('-')
  })
})
