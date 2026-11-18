import { describe, expect, it } from 'vitest'
import { formatDateTime } from '@/utils/format'
import zh from '@/i18n/locales/zh/admin/requestCapture'
import en from '@/i18n/locales/en/admin/requestCapture'
import { retentionTime, retentionCost, retentionHours, retentionDistribution, retentionReasonKey, retentionSettlementKey } from '../smallRequestRetentionFormat'

function message(root: object, path: string): unknown {
  return path.replace(/^admin\./, '').split('.').reduce<unknown>((value, key) => value && typeof value === 'object' ? (value as Record<string, unknown>)[key] : undefined, root)
}

describe('small retention presentation', () => {
  it('reuses local time formatting and does not show raw timestamps or invalid dates', () => {
    const timestamp = '2026-11-18T00:01:00Z'
    expect(retentionTime(timestamp)).toBe(formatDateTime(timestamp))
    expect(retentionTime(timestamp)).not.toContain('T00:01:00Z')
    expect(retentionTime(null)).toBe('—')
    expect(retentionTime('invalid')).toBe('—')
    const now = Date.parse('2026-11-18T00:00:00Z')
    expect(retentionHours('2026-11-18T01:01:00Z', now)).toBe(2)
    expect(retentionHours('2026-11-17T00:00:00Z', now)).toBe(0)
    expect(retentionHours('invalid', now)).toBeNull()
  })

  it('keeps eight-decimal settlement evidence instead of rounding small charges to zero', () => {
    expect(retentionCost('0.00000009')).toBe('$0.00000009')
    expect(retentionCost('0.00001234')).toBe('$0.00001234')
    expect(retentionCost('0.00999999')).toBe('$0.00999999')
    expect(retentionCost('12.50')).toContain('12.50')
    expect(retentionCost('0.00000000')).toContain('0.00')
    expect(retentionCost('unavailable')).toBe('—')
  })

  it('keeps repeat buckets in numeric order on a shared zero baseline', () => {
    expect(retentionDistribution({ '100+': 2, '1': 4, '2-9': 1 })).toEqual([
      { bucket: '1', count: 4, width: 100 }, { bucket: '2-9', count: 1, width: 25 },
      { bucket: '10-99', count: 0, width: 0 }, { bucket: '100+', count: 2, width: 50 },
    ])
    expect(retentionDistribution({}).every(row => row.width === 0)).toBe(true)
  })

  it('maps every backend replay and settlement code in both locales and leaves unknown values intact', () => {
    for (const reason of ['external_context:previous_response_id', 'external_context:file_id', 'external_context:cachedcontent', 'external_context:cached_content', 'external_context:encrypted_content', 'client_delivery_incomplete', 'stream_terminal_missing']) {
      const key = retentionReasonKey(reason)!
      expect(message(zh, key)).toBeTypeOf('string')
      expect(message(en, key)).toBeTypeOf('string')
      expect(message(zh, key)).not.toContain(reason)
    }
    expect(retentionReasonKey('external_context:cachedContent')).toBe(retentionReasonKey('external_context:cachedcontent'))
    for (const value of ['settled', 'already_settled', 'failed', 'not_billed', 'not_billed_no_usage_row']) {
      expect(message(zh, retentionSettlementKey(value)!)).toBeTypeOf('string')
      expect(message(en, retentionSettlementKey(value)!)).toBeTypeOf('string')
    }
    expect(retentionReasonKey('future_reason')).toBeNull()
    expect(retentionSettlementKey('future_settlement')).toBeNull()
  })
})
