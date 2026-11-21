import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'

import KeyRotationGateNotice from '../KeyRotationGateNotice.vue'
import type { KeyRotationGateStatus } from '@/api/keyRotationGate'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const status: KeyRotationGateStatus = {
  enabled: true,
  required: true,
  cutoff: '2026-11-21T00:00:00+08:00',
  active_key_count: 3,
  compliant_key_count: 1,
  pending_key_count: 2,
  reason: 'rotation_required',
  error_code: 'KEY_ROTATION_REQUIRED',
  message: 'blocked',
  remediation_path: '/keys',
}

let router: Router

beforeEach(async () => {
  router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/keys', component: { template: '<div />' } },
      { path: '/custom-keys', component: { template: '<div />' } },
    ],
  })
  await router.push('/')
  await router.isReady()
})

afterEach(() => {
  vi.restoreAllMocks()
})

function mountNotice(props: { status?: KeyRotationGateStatus | null } = { status }) {
  return mount(KeyRotationGateNotice, {
    props,
    global: {
      plugins: [router],
      stubs: { UIcon: true },
    },
  })
}

describe('KeyRotationGateNotice', () => {
  it('renders the remediation card with the security message and a link to the API keys page', () => {
    const wrapper = mountNotice()

    expect(wrapper.get('[data-testid="key-rotation-gate-notice"]').attributes('role')).toBe('alert')
    expect(wrapper.text()).toContain('keyRotationGate.title')
    expect(wrapper.text()).toContain('keyRotationGate.description')
    expect(wrapper.text()).toContain('keyRotationGate.hint')
    // Nuxt UI's link renders the SPA destination as a real anchor href.
    const action = wrapper.get('[data-testid="key-rotation-gate-action"]')
    expect(action.text()).toContain('keyRotationGate.action')
    expect(action.attributes('href')).toBe('/keys')
  })

  it('shows the active key count and pending key count but never the cutoff time', () => {
    const wrapper = mountNotice()
    const text = wrapper.text()

    expect(text).toContain('keyRotationGate.activeKeysLabel')
    expect(text).toContain('keyRotationGate.pendingKeysLabel')
    expect(text).toContain('3')
    expect(text).toContain('2')
    // The incident remediation card must not expose the internal cutoff instant.
    expect(text).not.toContain('keyRotationGate.cutoffLabel')
    expect(text).not.toContain('2026-11-21T00:00:00+08:00')
  })

  it('honours the remediation path returned by the backend', () => {
    const wrapper = mountNotice({ status: { ...status, remediation_path: '/custom-keys' } })
    expect(wrapper.get('[data-testid="key-rotation-gate-action"]').attributes('href')).toBe('/custom-keys')
  })

  it('falls back to the safe defaults when no status is available', () => {
    const wrapper = mountNotice({ status: null })

    expect(wrapper.get('[data-testid="key-rotation-gate-notice"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="key-rotation-gate-action"]').attributes('href')).toBe('/keys')
    expect(wrapper.text()).not.toContain('keyRotationGate.pendingKeysLabel')
  })

  it('does not render any cutoff value even when the backend still returns one', () => {
    const wrapper = mountNotice({ status: { ...status, cutoff: 'not-a-date' } })
    expect(wrapper.text()).not.toContain('not-a-date')
  })

  it('ships the exact Chinese remediation copy promised by the incident requirement', () => {
    expect(zh.keyRotationGate.description).toBe(
      '为保护您的账户安全，请先更换 API Key 或为其设置 IP 白名单后再充值/兑换。',
    )
    expect(zh.keyRotationGate.action).toBe('前往 API 密钥')
    expect(en.keyRotationGate.action).toBe('Go to API Keys')
  })

  it('drops the cutoff label from both locales', () => {
    const zhGate = zh.keyRotationGate as Record<string, unknown>
    const enGate = en.keyRotationGate as Record<string, unknown>
    expect(zhGate.cutoffLabel).toBeUndefined()
    expect(enGate.cutoffLabel).toBeUndefined()
  })
})
