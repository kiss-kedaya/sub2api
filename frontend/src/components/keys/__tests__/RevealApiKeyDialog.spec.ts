import { webcrypto } from 'node:crypto'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { ApiKey, ApiKeySecretSeal, ApiKeyVerificationFactors } from '@/types'
import RevealApiKeyDialog from '../RevealApiKeyDialog.vue'
import { sealApiKey } from '@/utils/apikeySeal'

const {
  getSecretSeal,
  getVerificationFactors,
  authorizeReveal,
  beginKeyPasskeyVerification,
  finishKeyPasskeyVerification,
  copyToClipboard,
} = vi.hoisted(() => ({
  getSecretSeal: vi.fn(),
  getVerificationFactors: vi.fn(),
  authorizeReveal: vi.fn(),
  beginKeyPasskeyVerification: vi.fn(),
  finishKeyPasskeyVerification: vi.fn(),
  copyToClipboard: vi.fn().mockResolvedValue(true),
}))

vi.mock('@/api', () => ({
  keysAPI: {
    getSecretSeal,
    getVerificationFactors,
    authorizeReveal,
    beginKeyPasskeyVerification,
    finishKeyPasskeyVerification,
  },
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard,
  }),
}))

const messages: Record<string, string> = {
  'keys.reveal.title': 'Verify your identity to view the key',
  'keys.reveal.description': 'Verification and decryption happen in this browser.',
  'keys.reveal.submit': 'Verify and view',
  'keys.reveal.cancel': 'Cancel',
  'keys.reveal.done': 'Done',
  'keys.reveal.copy': 'Copy',
  'keys.reveal.copied': 'Copied',
  'keys.reveal.onlyThisTime': 'Shown this time only.',
  'keys.reveal.wrongPassword': 'Wrong password: this key cannot be decrypted.',
  'keys.reveal.notFound': 'The server has no ciphertext for this key.',
  'keys.reveal.broken': 'The saved ciphertext is damaged or incompatible.',
  'keys.reveal.failed': 'Could not read or decrypt the key.',
  'keys.reveal.rotate': 'Rotate a new key',
  'keys.reveal.factorsLoading': 'Loading available verification methods...',
  'keys.reveal.wrongTotp': 'The dynamic code is incorrect. Please re-enter it.',
  'keys.reveal.passkeyFailed': 'Passkey verification failed. Please retry.',
  'keys.reveal.reverify': 'Verification expired. Please verify again.',
  'keys.reveal.factorMismatch': 'This key was sealed with "{factor}" verification.',
  'keys.reveal.factorUnavailable': 'The "{factor}" method used when this key was created is unavailable.',
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

const KEY = 'sk-fixture-reveal'
const PASSWORD = 'correct horse battery staple'
const GRANT = 'reveal-grant-fixture-token'

const getRandomValues = <T extends ArrayBufferView>(array: T): T =>
  webcrypto.getRandomValues(array as unknown as Uint8Array) as unknown as T

let seal: ApiKeySecretSeal

const createApiKey = (sealAuthFactor: ApiKey['seal_auth_factor'] = 'password'): ApiKey =>
  ({
    id: 42,
    user_id: 1,
    key_prefix: 'sk-live-',
    key_masked: 'sk-live-****-cdef',
    name: 'sealed key',
    group_id: null,
    status: 'active',
    ip_whitelist: [],
    ip_blacklist: [],
    last_used_at: null,
    last_used_ip: null,
    quota: 0,
    quota_used: 0,
    expires_at: null,
    created_at: '2026-11-21T00:00:00Z',
    updated_at: '2026-11-21T00:00:00Z',
    current_concurrency: 0,
    rate_limit_5h: 0,
    rate_limit_1d: 0,
    rate_limit_7d: 0,
    usage_5h: 0,
    usage_1d: 0,
    usage_7d: 0,
    window_5h_start: null,
    window_1d_start: null,
    window_7d_start: null,
    reset_5h_at: null,
    reset_1d_at: null,
    reset_7d_at: null,
    has_sealed_secret: true,
    seal_auth_factor: sealAuthFactor,
  }) as unknown as ApiKey

const factorsWith = (
  methods: ApiKeyVerificationFactors['available_methods'],
): ApiKeyVerificationFactors => ({
  password_available: methods.includes('password'),
  totp_enabled: methods.includes('totp'),
  passkey_count: methods.includes('passkey') ? 1 : 0,
  preferred: methods[0] ?? '',
  available_methods: methods,
  seal_available: methods.includes('password'),
})

const mountDialog = async (apiKey: ApiKey = createApiKey()) => {
  const wrapper = mount(RevealApiKeyDialog, {
    attachTo: document.body,
    props: { show: true, apiKey },
    global: { stubs: { teleport: true } },
  })
  await flushPromises()
  return wrapper
}

/** WebCrypto runs on the libuv threadpool, so the result lands after several macrotasks. */
const waitForSelector = async (
  wrapper: Awaited<ReturnType<typeof mountDialog>>,
  selector: string,
) => {
  await vi.waitFor(
    () => {
      expect(wrapper.find(selector).exists()).toBe(true)
    },
    { timeout: 10000 },
  )
  await flushPromises()
}

const submit = async (wrapper: Awaited<ReturnType<typeof mountDialog>>) => {
  await wrapper.get('[data-test="api-key-reveal-dialog"]').trigger('submit')
  await flushPromises()
}

beforeAll(async () => {
  seal = await sealApiKey(KEY, PASSWORD, {
    subtle: webcrypto.subtle as unknown as SubtleCrypto,
    getRandomValues,
  })
})

beforeEach(() => {
  // 组件通过 globalThis.crypto 解密，与浏览器行为一致。
  vi.stubGlobal('crypto', webcrypto)
  vi.clearAllMocks()
  copyToClipboard.mockResolvedValue(true)
  getVerificationFactors.mockResolvedValue(factorsWith(['password']))
  getSecretSeal.mockResolvedValue(seal)
  authorizeReveal.mockResolvedValue({ grant: GRANT, factor: 'password', expires_in: 300 })
})

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('RevealApiKeyDialog three-way identity verification', () => {
  it('renders only available factors, authorizes, then fetches with the grant and decrypts locally', async () => {
    const wrapper = await mountDialog()

    expect(getVerificationFactors).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="key-factor-verification"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Login password')
    expect(wrapper.text()).not.toContain('Dynamic code')
    expect(wrapper.text()).not.toContain('Passkey')

    await wrapper.get('[data-test="key-factor-password-input"]').setValue(PASSWORD)
    await submit(wrapper)
    await waitForSelector(wrapper, '[data-test="api-key-reveal-value"]')

    expect(authorizeReveal).toHaveBeenCalledWith(42, { method: 'password', password: PASSWORD })
    expect(getSecretSeal).toHaveBeenCalledWith(42, GRANT)
    expect(getSecretSeal.mock.calls[0]).toHaveLength(2)
    expect(wrapper.get('[data-test="api-key-reveal-value"]').text()).toBe(KEY)
    expect(copyToClipboard).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('preselects the TOTP factor recorded at creation and asks for the local decrypt password', async () => {
    getVerificationFactors.mockResolvedValue(factorsWith(['totp', 'password']))
    authorizeReveal.mockResolvedValue({ grant: GRANT, factor: 'totp', expires_in: 300 })
    const wrapper = await mountDialog(createApiKey('totp'))

    expect(wrapper.find('[data-test="key-factor-totp-input"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="key-factor-decrypt-password-input"]').exists()).toBe(true)

    await wrapper.get('[data-test="key-factor-totp-input"]').setValue('123456')
    await wrapper.get('[data-test="key-factor-decrypt-password-input"]').setValue(PASSWORD)
    await submit(wrapper)
    await waitForSelector(wrapper, '[data-test="api-key-reveal-value"]')

    expect(authorizeReveal).toHaveBeenCalledWith(42, { method: 'totp', totp_code: '123456' })
    expect(getSecretSeal).toHaveBeenCalledWith(42, GRANT)
    expect(wrapper.get('[data-test="api-key-reveal-value"]').text()).toBe(KEY)
    wrapper.unmount()
  })

  it('shows a specific message when the password is incorrect', async () => {
    authorizeReveal.mockRejectedValue({ status: 400, reason: 'VERIFY_PASSWORD_INVALID' })
    const wrapper = await mountDialog()

    await wrapper.get('[data-test="key-factor-password-input"]').setValue('wrong')
    await submit(wrapper)
    await waitForSelector(wrapper, '[data-test="api-key-reveal-error"]')

    expect(wrapper.get('[data-test="api-key-reveal-error"]').text()).toBe(
      'Wrong password: this key cannot be decrypted.',
    )
    expect(getSecretSeal).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows a specific message when the dynamic code is incorrect', async () => {
    getVerificationFactors.mockResolvedValue(factorsWith(['totp']))
    authorizeReveal.mockRejectedValue({ status: 400, reason: 'VERIFY_TOTP_INVALID' })
    const wrapper = await mountDialog(createApiKey('totp'))

    await wrapper.get('[data-test="key-factor-totp-input"]').setValue('000000')
    await wrapper.get('[data-test="key-factor-decrypt-password-input"]').setValue(PASSWORD)
    await submit(wrapper)
    await waitForSelector(wrapper, '[data-test="api-key-reveal-error"]')

    expect(wrapper.get('[data-test="api-key-reveal-error"]').text()).toBe(
      'The dynamic code is incorrect. Please re-enter it.',
    )
    wrapper.unmount()
  })

  it('explains the same-factor requirement when the server rejects the factor', async () => {
    getVerificationFactors.mockResolvedValue(factorsWith(['totp', 'password']))
    authorizeReveal.mockRejectedValue({ status: 400, reason: 'VERIFY_FACTOR_MISMATCH' })
    const wrapper = await mountDialog(createApiKey('totp'))

    await wrapper.get('[data-test="key-factor-totp-input"]').setValue('123456')
    await wrapper.get('[data-test="key-factor-decrypt-password-input"]').setValue(PASSWORD)
    await submit(wrapper)
    await waitForSelector(wrapper, '[data-test="api-key-reveal-error"]')

    expect(wrapper.get('[data-test="api-key-reveal-error"]').text()).toContain(
      'This key was sealed with',
    )
    wrapper.unmount()
  })

  it('shows the wrong-password message when local decryption fails', async () => {
    getVerificationFactors.mockResolvedValue(factorsWith(['totp']))
    authorizeReveal.mockResolvedValue({ grant: GRANT, factor: 'totp', expires_in: 300 })
    const wrapper = await mountDialog(createApiKey('totp'))

    await wrapper.get('[data-test="key-factor-totp-input"]').setValue('123456')
    await wrapper.get('[data-test="key-factor-decrypt-password-input"]').setValue('wrong password')
    await submit(wrapper)
    await waitForSelector(wrapper, '[data-test="api-key-reveal-error"]')

    expect(wrapper.get('[data-test="api-key-reveal-error"]').text()).toBe(
      'Wrong password: this key cannot be decrypted.',
    )
    wrapper.unmount()
  })

  it('offers rotation when the server has no ciphertext', async () => {
    getSecretSeal.mockRejectedValueOnce({ status: 404, reason: 'API_KEY_SECRET_NOT_FOUND' })
    const wrapper = await mountDialog()

    await wrapper.get('[data-test="key-factor-password-input"]').setValue(PASSWORD)
    await submit(wrapper)
    await waitForSelector(wrapper, '[data-test="api-key-reveal-error"]')

    expect(wrapper.get('[data-test="api-key-reveal-error"]').text()).toBe(
      'The server has no ciphertext for this key.',
    )
    expect(wrapper.find('[data-test="api-key-reveal-rotate"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('Passkey factor: completes the assertion, exchanges the ticket, then authorizes', async () => {
    getVerificationFactors.mockResolvedValue(factorsWith(['passkey', 'password']))
    authorizeReveal.mockResolvedValue({ grant: GRANT, factor: 'passkey', expires_in: 300 })

    class FakePublicKeyCredential {
      id = 'credential-fixture'
      rawId = new Uint8Array([1, 2, 3]).buffer
      type = 'public-key'
      authenticatorAttachment = 'platform'
      response = {
        authenticatorData: new Uint8Array([1]).buffer,
        clientDataJSON: new Uint8Array([2]).buffer,
        signature: new Uint8Array([3]).buffer,
        userHandle: null,
      }
      getClientExtensionResults() {
        return {}
      }
    }
    const windowRecord = window as unknown as Record<string, unknown>
    const originalPublicKeyCredential = windowRecord.PublicKeyCredential
    const originalCredentials = navigator.credentials
    windowRecord.PublicKeyCredential = FakePublicKeyCredential
    Object.defineProperty(navigator, 'credentials', {
      configurable: true,
      value: { get: vi.fn().mockResolvedValue(new FakePublicKeyCredential()) },
    })
    beginKeyPasskeyVerification.mockResolvedValue({
      session_token: 'passkey-session-fixture',
      options: { publicKey: { challenge: 'AAAA', allowCredentials: [] } },
    })
    finishKeyPasskeyVerification.mockResolvedValue({
      passkey_session_token: 'passkey-ticket-fixture',
      factor: 'passkey',
      expires_in: 300,
    })

    try {
      const wrapper = await mountDialog(createApiKey('passkey'))
      await wrapper.get('[data-test="key-factor-passkey-verify"]').trigger('click')
      await flushPromises()

      expect(beginKeyPasskeyVerification).toHaveBeenCalledTimes(1)
      expect(finishKeyPasskeyVerification).toHaveBeenCalledWith(
        'passkey-session-fixture',
        expect.objectContaining({ id: 'credential-fixture', type: 'public-key' }),
      )
      expect(wrapper.find('[data-test="key-factor-passkey-verified"]').exists()).toBe(true)

      await wrapper.get('[data-test="key-factor-decrypt-password-input"]').setValue(PASSWORD)
      await submit(wrapper)
      await waitForSelector(wrapper, '[data-test="api-key-reveal-value"]')

      expect(authorizeReveal).toHaveBeenCalledWith(42, {
        method: 'passkey',
        passkey_session_token: 'passkey-ticket-fixture',
      })
      expect(wrapper.get('[data-test="api-key-reveal-value"]').text()).toBe(KEY)
      wrapper.unmount()
    } finally {
      windowRecord.PublicKeyCredential = originalPublicKeyCredential
      Object.defineProperty(navigator, 'credentials', {
        configurable: true,
        value: originalCredentials,
      })
    }
  })

  it('tells the user when the factor recorded at creation is no longer available', async () => {
    const wrapper = await mountDialog(createApiKey('passkey'))
    expect(wrapper.find('[data-test="api-key-reveal-factor-unavailable"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="api-key-reveal-factor-unavailable"]').text()).toContain(
      'Passkey',
    )
    wrapper.unmount()
  })
})
