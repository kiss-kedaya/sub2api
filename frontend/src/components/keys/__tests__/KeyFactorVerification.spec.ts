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

const REMEMBERED_METHOD_KEY = 'sub2api.key_verify_method'

describe('KeyFactorVerification', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // The last-used method is remembered in localStorage; do not let one test pick the next one's default.
    window.localStorage.clear()
    window.sessionStorage.clear()
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

  it('lets the browser password manager fill the login password', async () => {
    const wrapper = mountComponent({ accountEmail: 'user@example.com' })
    const input = wrapper.get('[data-test="key-factor-password-input"]')
    expect(input.attributes('type')).toBe('password')
    expect(input.attributes('autocomplete')).toBe('current-password')
    expect(input.attributes('name')).toBe('password')
    // The previous build blocked password managers on purpose, which is why users retyped it every time.
    expect(input.attributes('data-lpignore')).toBeUndefined()
    expect(input.attributes('data-1p-ignore')).toBeUndefined()

    // A password manager needs a username next to the password: hidden, read-only, the account email.
    const username = wrapper.get('[data-test="key-factor-username"]')
    expect(username.attributes('name')).toBe('username')
    expect(username.attributes('autocomplete')).toBe('username')
    expect(username.attributes('readonly')).toBeDefined()
    expect(username.attributes('aria-hidden')).toBe('true')
    expect((username.element as HTMLInputElement).value).toBe('user@example.com')
    wrapper.unmount()
  })

  it('does not render a hidden username when the account email is unknown', async () => {
    const wrapper = mountComponent()
    expect(wrapper.find('[data-test="key-factor-username"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('never writes the password into web storage', async () => {
    const wrapper = mountComponent({ accountEmail: 'user@example.com' })
    const exposed = wrapper.vm as unknown as { buildVerification: () => Record<string, unknown> | null }
    await wrapper.get('[data-test="key-factor-password-input"]').setValue('s3cret-pass-phrase')
    expect(exposed.buildVerification()).toEqual({ method: 'password', password: 's3cret-pass-phrase' })

    const dump = JSON.stringify({
      local: Object.fromEntries(Object.entries(window.localStorage)),
      session: Object.fromEntries(Object.entries(window.sessionStorage)),
    })
    expect(dump).not.toContain('s3cret-pass-phrase')
    // What is remembered is only which method was used.
    expect(window.localStorage.getItem(REMEMBERED_METHOD_KEY)).toBe('password')
    wrapper.unmount()
  })

  it('remembers the last used method after a valid verification', async () => {
    const wrapper = mountComponent({ factors: factors(['password', 'totp'], 'password') })
    expect(window.localStorage.getItem(REMEMBERED_METHOD_KEY)).toBeNull()

    window.localStorage.setItem(REMEMBERED_METHOD_KEY, 'totp')
    const second = mountComponent({ factors: factors(['password', 'totp'], 'password') })
    await second.get('[data-test="key-factor-totp-input"]').setValue('123456')
    const exposed = second.vm as unknown as { buildVerification: () => Record<string, unknown> | null }
    expect(exposed.buildVerification()).toEqual({ method: 'totp', totp_code: '123456' })
    expect(window.localStorage.getItem(REMEMBERED_METHOD_KEY)).toBe('totp')
    wrapper.unmount()
    second.unmount()
  })

  it('preselects the remembered method over the server recommendation', async () => {
    window.localStorage.setItem(REMEMBERED_METHOD_KEY, 'totp')
    const wrapper = mountComponent({ factors: factors(['password', 'totp'], 'password') })
    expect(wrapper.find('[data-test="factor-totp"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="factor-password"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('lets the caller override the remembered method (reveal must reuse the sealing factor)', async () => {
    window.localStorage.setItem(REMEMBERED_METHOD_KEY, 'totp')
    const wrapper = mountComponent({
      factors: factors(['password', 'totp'], 'totp'),
      preferredFactor: 'password',
    })
    expect(wrapper.find('[data-test="factor-password"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('ignores a remembered method this account does not have, and garbage values', async () => {
    window.localStorage.setItem(REMEMBERED_METHOD_KEY, 'passkey')
    const missing = mountComponent({ factors: factors(['password', 'totp'], 'totp') })
    expect(missing.find('[data-test="factor-totp"]').exists()).toBe(true)
    missing.unmount()

    window.localStorage.setItem(REMEMBERED_METHOD_KEY, '<script>')
    const garbage = mountComponent({ factors: factors(['password'], 'password') })
    expect(garbage.find('[data-test="factor-password"]').exists()).toBe(true)
    garbage.unmount()
  })

  it('can hide its own description when the dialog header already says it', async () => {
    const shown = mountComponent()
    expect(shown.text()).toContain('Verify your identity once.')
    shown.unmount()

    const hidden = mountComponent({ hideDescription: true })
    expect(hidden.text()).not.toContain('Verify your identity once.')
    hidden.unmount()
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
