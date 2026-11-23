import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import type { ApiKeyVerificationFactors } from '@/types'
import KeyFactorVerification from '../KeyFactorVerification.vue'

const {
  beginKeyPasskeyVerification,
  finishKeyPasskeyVerification,
} = vi.hoisted(() => ({
  beginKeyPasskeyVerification: vi.fn(),
  finishKeyPasskeyVerification: vi.fn(),
}))

vi.mock('@/api', () => ({
  keysAPI: {
    beginKeyPasskeyVerification,
    finishKeyPasskeyVerification,
  },
}))

const messages: Record<string, string> = {
  'keys.factor.description': 'Verify your identity once.',
  'keys.factor.methodLabel': 'Verification method',
  'keys.factor.methodPassword': 'Login password',
  'keys.factor.methodTotp': 'Dynamic code',
  'keys.factor.methodPasskey': 'Passkey',
  'keys.factor.preferred': 'Recommended',
  'keys.factor.passwordLabel': 'Login password',
  'keys.factor.passwordPlaceholder': 'Enter your login password',
  'keys.factor.passwordHint': 'Only used for this verification.',
  'keys.factor.decryptPasswordLabel': 'Login password (for local decryption)',
  'keys.factor.decryptPasswordHint': 'Decryption happens in this browser.',
  'keys.factor.totpLabel': '6-digit dynamic code',
  'keys.factor.totpPlaceholder': 'Enter the 6-digit code',
  'keys.factor.passkeyHint': 'Verify with a passkey bound to this device.',
  'keys.factor.passkeyVerify': 'Verify with Passkey',
  'keys.factor.passkeyReverify': 'Verify Passkey again',
  'keys.factor.passkeyVerified': 'Verified, valid for {seconds}s',
  'keys.factor.passkeyUnsupported': 'This browser or device does not support passkeys.',
  'keys.factor.passkeyFailed': 'Passkey verification failed. Please retry.',
  'keys.factor.unavailable': 'No verification method is available for this account.',
  'keys.factor.requiredAll': 'Complete identity verification first.',
  'keys.factor.passwordRequired': 'Enter your login password.',
  'keys.factor.totpRequired': 'Enter the 6-digit dynamic code.',
  'keys.factor.passkeyRequired': 'Verify with Passkey first.',
  'keys.factor.factorUnavailable': 'This verification method is currently unavailable.',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        let text = messages[key] ?? key
        if (params) {
          for (const [name, value] of Object.entries(params)) {
            text = text.replace(`{${name}}`, String(value))
          }
        }
        return text
      },
    }),
  }
})

const factors = (
  methods: ApiKeyVerificationFactors['available_methods'],
  preferred?: ApiKeyVerificationFactors['preferred'],
): ApiKeyVerificationFactors => ({
  password_available: methods.includes('password'),
  totp_enabled: methods.includes('totp'),
  passkey_count: methods.includes('passkey') ? 1 : 0,
  preferred: preferred ?? methods[0] ?? '',
  available_methods: methods,
  seal_available: methods.includes('password'),
})

const mountComponent = (props: Record<string, unknown> = {}) =>
  mount(KeyFactorVerification, {
    props: { factors: factors(['password']), ...props },
  })

describe('KeyFactorVerification', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders only the methods the account actually has', async () => {
    const wrapper = mountComponent({ factors: factors(['password']) })
    expect(wrapper.text()).toContain('Login password')
    expect(wrapper.text()).not.toContain('Dynamic code')
    expect(wrapper.text()).not.toContain('Passkey')
    wrapper.unmount()
  })

  it('renders all available methods in server order and marks the preferred one', async () => {
    const wrapper = mountComponent({ factors: factors(['totp', 'password'], 'totp') })
    const text = wrapper.text()
    expect(text).toContain('Dynamic code')
    expect(text).toContain('Login password')
    expect(text.indexOf('Dynamic code')).toBeLessThan(text.indexOf('Login password'))
    expect(text).toContain('Recommended')
    wrapper.unmount()
  })

  it('password method: validates the input and builds the payload', async () => {
    const wrapper = mountComponent()
    const exposed = wrapper.vm as unknown as {
      buildVerification: () => Record<string, unknown> | null
      getDecryptPassword: () => string
    }

    expect(exposed.buildVerification()).toBeNull()
    await nextTick()
    expect(wrapper.get('[data-test="key-factor-error"]').text()).toBe('Enter your login password.')

    await wrapper.get('[data-test="key-factor-password-input"]').setValue('correct horse')
    expect(exposed.buildVerification()).toEqual({ method: 'password', password: 'correct horse' })
    expect(exposed.getDecryptPassword()).toBe('correct horse')
    wrapper.unmount()
  })

  it('uses password-manager-safe attributes on the local passphrase fields', async () => {
    const wrapper = mountComponent()
    const input = wrapper.get('[data-test="key-factor-password-input"]')
    expect(input.attributes('type')).toBe('password')
    expect(input.attributes('autocomplete')).toBe('new-password')
    expect(input.attributes('name')).toBe('key-factor-password')
    expect(input.attributes('data-lpignore')).toBe('true')
    expect(input.attributes('data-1p-ignore')).toBe('true')
    wrapper.unmount()
  })

  it('TOTP method: requires a six-digit code and builds the payload', async () => {
    const wrapper = mountComponent({ factors: factors(['totp']) })
    const exposed = wrapper.vm as unknown as {
      buildVerification: () => Record<string, unknown> | null
    }
    expect(exposed.buildVerification()).toBeNull()

    await wrapper.get('[data-test="key-factor-totp-input"]').setValue('1234')
    expect(exposed.buildVerification()).toBeNull()
    await nextTick()
    expect(wrapper.get('[data-test="key-factor-error"]').text()).toBe(
      'Enter the 6-digit dynamic code.',
    )

    await wrapper.get('[data-test="key-factor-totp-input"]').setValue('123456')
    expect(exposed.buildVerification()).toEqual({ method: 'totp', totp_code: '123456' })
    wrapper.unmount()
  })

  it('reveal mode: TOTP still asks for the local decryption password', async () => {
    const wrapper = mountComponent({ factors: factors(['totp']), mode: 'reveal' })
    expect(wrapper.find('[data-test="key-factor-decrypt-password-input"]').exists()).toBe(true)
    const exposed = wrapper.vm as unknown as {
      buildVerification: () => Record<string, unknown> | null
      getDecryptPassword: () => string
    }
    await wrapper.get('[data-test="key-factor-totp-input"]').setValue('123456')
    await wrapper.get('[data-test="key-factor-decrypt-password-input"]').setValue('decrypt-pass')
    expect(exposed.buildVerification()).toEqual({ method: 'totp', totp_code: '123456' })
    expect(exposed.getDecryptPassword()).toBe('decrypt-pass')
    wrapper.unmount()
  })

  it('Passkey method requires a completed assertion before building the payload', async () => {
    const wrapper = mountComponent({ factors: factors(['passkey']) })
    const exposed = wrapper.vm as unknown as {
      buildVerification: () => Record<string, unknown> | null
    }
    expect(exposed.buildVerification()).toBeNull()
    await nextTick()
    expect(wrapper.get('[data-test="key-factor-error"]').text()).toBe(
      'Verify with Passkey first.',
    )
    // 设备没有 WebAuthn：点击给出明确降级提示，而不是静默失败。
    await wrapper.get('[data-test="key-factor-passkey-verify"]').trigger('click')
    await flushPromises()
    await nextTick()
    expect(wrapper.get('[data-test="key-factor-error"]').text()).toBe(
      'This browser or device does not support passkeys.',
    )
    wrapper.unmount()
  })
})
