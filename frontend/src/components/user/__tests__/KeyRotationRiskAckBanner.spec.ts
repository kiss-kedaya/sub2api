import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'

import KeyRotationRiskAckBanner from '../KeyRotationRiskAckBanner.vue'
import type { KeyRotationGateStatus } from '@/api/keyRotationGate'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

function makeStatus(overrides: Partial<KeyRotationGateStatus> = {}): KeyRotationGateStatus {
  return {
    enabled: true,
    required: false,
    cutoff: '2026-11-21T00:00:00+08:00',
    active_key_count: 2,
    compliant_key_count: 0,
    pending_key_count: 2,
    reason: 'risk_acknowledged',
    remediation_path: '/keys',
    risk_ack_enabled: true,
    risk_ack_hours: 24,
    risk_ack_expires_at: '2026-11-22T14:30:00+08:00',
    ...overrides,
  }
}

function mountBanner(status: KeyRotationGateStatus | null): { wrapper: ReturnType<typeof mount>; router: Router } {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/keys', component: { template: '<div />' } },
    ],
  })
  const wrapper = mount(KeyRotationRiskAckBanner, {
    props: { status },
    global: { plugins: [router], stubs: { UIcon: true } },
  })
  return { wrapper, router }
}

describe('KeyRotationRiskAckBanner', () => {
  it('shows the local expiry time so the user knows the skip is temporary', () => {
    const { wrapper } = mountBanner(makeStatus())
    const text = wrapper.get('[data-testid="key-rotation-risk-ack-banner"]').text()
    expect(text).toContain('keyRotationGate.riskAckBanner')
    // The expiry is rendered as local HH:mm (time-zone independent assertion).
    expect(text).toMatch(/\d{2}:\d{2}/)
    expect(wrapper.get('[data-testid="key-rotation-risk-ack-banner-action"]').attributes('href')).toBe('/keys')
  })

  it('renders nothing without a valid acknowledgement', () => {
    expect(mountBanner(makeStatus({ risk_ack_expires_at: undefined })).wrapper.find('[data-testid="key-rotation-risk-ack-banner"]').exists()).toBe(false)
    expect(mountBanner(null).wrapper.find('[data-testid="key-rotation-risk-ack-banner"]').exists()).toBe(false)
  })

  it('ignores an unparsable expiry instead of printing Invalid Date', () => {
    const { wrapper } = mountBanner(makeStatus({ risk_ack_expires_at: 'not-a-date' }))
    expect(wrapper.find('[data-testid="key-rotation-risk-ack-banner"]').exists()).toBe(false)
  })
})
