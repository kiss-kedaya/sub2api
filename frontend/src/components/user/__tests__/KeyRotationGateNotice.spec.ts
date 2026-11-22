import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'

import KeyRotationGateNotice from '../KeyRotationGateNotice.vue'
import type { KeyRotationGateStatus } from '@/api/keyRotationGate'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

// 风险确认依赖全局确认弹窗、接口与全局门禁状态刷新，全部 mock，不写真实网络。
vi.mock('@/composables/useConfirmation', () => ({ confirm: vi.fn() }))
vi.mock('@/composables/useKeyRotationGate', () => ({ ensureKeyRotationGate: vi.fn() }))
vi.mock('@/api/keyRotationGate', async () => {
  const actual = await vi.importActual<typeof import('@/api/keyRotationGate')>('@/api/keyRotationGate')
  return {
    ...actual,
    keyRotationGateAPI: { ...actual.keyRotationGateAPI, acknowledgeRisk: vi.fn() },
  }
})

import { confirm } from '@/composables/useConfirmation'
import { ensureKeyRotationGate } from '@/composables/useKeyRotationGate'
import { keyRotationGateAPI } from '@/api/keyRotationGate'

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
  risk_ack_enabled: true,
  risk_ack_hours: 24,
}

let router: Router

beforeEach(async () => {
  vi.mocked(confirm).mockReset()
  vi.mocked(confirm).mockResolvedValue(true)
  vi.mocked(ensureKeyRotationGate).mockReset()
  vi.mocked(ensureKeyRotationGate).mockResolvedValue(undefined)
  vi.mocked(keyRotationGateAPI.acknowledgeRisk as Mock).mockReset()
  vi.mocked(keyRotationGateAPI.acknowledgeRisk as Mock).mockResolvedValue({
    expires_at: '2026-11-22T14:30:00+08:00',
    hours: 24,
  })

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
    // Without a status the skip action must not appear (feature state unknown).
    expect(wrapper.find('[data-testid="key-rotation-gate-risk-ack-action"]').exists()).toBe(false)
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

  describe('risk acknowledgement', () => {
    it('renders a quiet secondary button only when the feature is enabled', async () => {
      const enabled = mountNotice()
      const action = enabled.get('[data-testid="key-rotation-gate-risk-ack-action"]')
      expect(action.text()).toContain('keyRotationGate.riskAckAction')
      // 次级按钮不能是危险操作配色：保持 neutral + ghost。
      expect(action.html()).not.toContain('bg-warning')

      const disabled = mountNotice({ status: { ...status, risk_ack_enabled: false } })
      expect(disabled.find('[data-testid="key-rotation-gate-risk-ack-action"]').exists()).toBe(false)
    })

    it('asks for one explicit confirmation before calling the API', async () => {
      const wrapper = mountNotice()

      await wrapper.get('[data-testid="key-rotation-gate-risk-ack-action"]').trigger('click')
      await flushPromises()

      expect(confirm).toHaveBeenCalledTimes(1)
      const options = vi.mocked(confirm).mock.calls[0]![0] as { title: string; message: string; danger?: boolean }
      expect(options.title).toBe('keyRotationGate.riskAckConfirmTitle')
      expect(options.message).toBe('keyRotationGate.riskAckConfirmMessage')
      expect(options.danger).toBe(true)

      expect(keyRotationGateAPI.acknowledgeRisk).toHaveBeenCalledTimes(1)
      // 成功后刷新全局门禁状态，页面恢复充值/兑换表单。
      expect(ensureKeyRotationGate).toHaveBeenCalledTimes(1)
      expect(ensureKeyRotationGate).toHaveBeenCalledWith(true)
      expect(wrapper.find('[data-testid="key-rotation-gate-risk-ack-error"]').exists()).toBe(false)
    })

    it('does nothing when the confirmation is cancelled', async () => {
      vi.mocked(confirm).mockResolvedValue(false)
      const wrapper = mountNotice()

      await wrapper.get('[data-testid="key-rotation-gate-risk-ack-action"]').trigger('click')
      await flushPromises()

      expect(confirm).toHaveBeenCalledTimes(1)
      expect(keyRotationGateAPI.acknowledgeRisk).not.toHaveBeenCalled()
      expect(ensureKeyRotationGate).not.toHaveBeenCalled()
    })

    it('keeps the gate in place and shows an error when the API fails', async () => {
      vi.mocked(keyRotationGateAPI.acknowledgeRisk as Mock).mockRejectedValue(new Error('403'))
      const wrapper = mountNotice()

      await wrapper.get('[data-testid="key-rotation-gate-risk-ack-action"]').trigger('click')
      await flushPromises()

      expect(keyRotationGateAPI.acknowledgeRisk).toHaveBeenCalledTimes(1)
      expect(ensureKeyRotationGate).not.toHaveBeenCalled()
      expect(wrapper.get('[data-testid="key-rotation-gate-risk-ack-error"]').text()).toBe(
        'keyRotationGate.riskAckFailed',
      )
    })

    it('ships zh+en copy for the disclaimer and the validity hint', () => {
      expect(zh.keyRotationGate.riskAckAction).toBe('我已知晓风险，继续充值')
      expect(zh.keyRotationGate.riskAckConfirmMessage).toContain('盗刷')
      expect(zh.keyRotationGate.riskAckExpiry).toContain('{time}')
      expect(en.keyRotationGate.riskAckAction).toContain('I understand the risk')
      expect(en.keyRotationGate.riskAckExpiry).toContain('{time}')
    })
  })
})
