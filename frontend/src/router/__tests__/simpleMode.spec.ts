import { describe, expect, it } from 'vitest'
import { SIMPLE_MODE_RESTRICTED_PATHS, isRestrictedInSimpleMode } from '@/router/simpleMode'

describe('simple-mode route policy', () => {
  it('marks the paths the router guard sends back to the dashboard', () => {
    for (const path of SIMPLE_MODE_RESTRICTED_PATHS) {
      expect(isRestrictedInSimpleMode(path)).toBe(true)
    }
    expect(isRestrictedInSimpleMode('/redeem')).toBe(true)
    expect(isRestrictedInSimpleMode('/admin/redeem')).toBe(true)
    expect(isRestrictedInSimpleMode('/subscriptions')).toBe(true)
    expect(isRestrictedInSimpleMode('/admin/subscriptions')).toBe(true)
  })

  it('matches whole path segments only', () => {
    // A future route such as /redeem-code must not be caught by the /redeem prefix.
    expect(isRestrictedInSimpleMode('/redeem-code')).toBe(false)
    expect(isRestrictedInSimpleMode('/subscriptions-report')).toBe(false)
    expect(isRestrictedInSimpleMode('/redeem/confirm')).toBe(true)
  })

  it('leaves the dashboard quick-action destinations reachable', () => {
    for (const path of ['/dashboard', '/keys', '/infinite-canvas', '/usage', '/batch-image']) {
      expect(isRestrictedInSimpleMode(path)).toBe(false)
    }
  })
})
