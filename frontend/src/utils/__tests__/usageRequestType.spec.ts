import { describe, expect, it } from 'vitest'

import { requestTypeBadgeClass, requestTypeLabelKey } from '../errorBadges'
import { isUsageRequestType, requestTypeToLegacyStream, resolveUsageRequestType } from '../usageRequestType'

describe('usage request type claude_refusal', () => {
  it('is a known request type and is used as-is whatever the legacy stream flags say', () => {
    expect(isUsageRequestType('claude_refusal')).toBe(true)
    expect(isUsageRequestType('refusal')).toBe(false)
    for (const stream of [true, false]) {
      expect(resolveUsageRequestType({ request_type: 'claude_refusal', stream })).toBe('claude_refusal')
    }
    // 后端还没带 request_type 的历史行仍按 stream/ws 推导，不受影响
    expect(resolveUsageRequestType({ stream: true })).toBe('stream')
    expect(resolveUsageRequestType({ stream: false })).toBe('sync')
    expect(resolveUsageRequestType({ openai_ws_mode: true })).toBe('ws_v2')
  })

  it('is orthogonal to stream: filtering by it never maps onto the legacy stream dimension', () => {
    expect(requestTypeToLegacyStream('claude_refusal')).toBeNull()
    expect(requestTypeToLegacyStream('cyber')).toBeNull()
    expect(requestTypeToLegacyStream('stream')).toBe(true)
    expect(requestTypeToLegacyStream('sync')).toBe(false)
  })

  it('has its own label key and a warning badge distinct from cyber and unknown', () => {
    expect(requestTypeLabelKey('claude_refusal')).toBe('usage.claudeRefusal')
    const refusal = requestTypeBadgeClass('claude_refusal')
    expect(refusal).toContain('bg-orange-100')
    expect(refusal).not.toBe(requestTypeBadgeClass('cyber'))
    expect(refusal).not.toBe(requestTypeBadgeClass('unknown'))
    expect(refusal).not.toBe(requestTypeBadgeClass('sync'))
  })
})
