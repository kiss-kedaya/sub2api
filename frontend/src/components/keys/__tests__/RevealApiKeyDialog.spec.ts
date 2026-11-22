import { webcrypto } from 'node:crypto'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { ApiKey, ApiKeySecretSeal } from '@/types'
import RevealApiKeyDialog from '../RevealApiKeyDialog.vue'
import { sealApiKey } from '@/utils/apikeySeal'

const { getSecretSeal, copyToClipboard } = vi.hoisted(() => ({
  getSecretSeal: vi.fn(),
  copyToClipboard: vi.fn().mockResolvedValue(true),
}))

vi.mock('@/api', () => ({
  keysAPI: {
    getSecretSeal,
  },
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard,
  }),
}))

const messages: Record<string, string> = {
  'keys.reveal.title': 'Enter your login password to view the key',
  'keys.reveal.description': 'Decryption happens in this browser.',
  'keys.reveal.passwordLabel': 'Login password',
  'keys.reveal.passwordHint': 'Valid for this view only.',
  'keys.reveal.submit': 'Decrypt locally',
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
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const KEY = 'sk-fixture-reveal'
const PASSWORD = 'correct horse battery staple'
const getRandomValues = <T extends ArrayBufferView>(array: T): T =>
  webcrypto.getRandomValues(array as unknown as Uint8Array) as unknown as T

let seal: ApiKeySecretSeal

const createApiKey = (): ApiKey => ({
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
})

const mountDialog = async (props: Record<string, unknown> = {}) =>
  mount(RevealApiKeyDialog, {
    attachTo: document.body,
    props: { show: true, apiKey: createApiKey(), ...props },
    global: { stubs: { teleport: true } },
  })

const submit = async (wrapper: Awaited<ReturnType<typeof mountDialog>>, password: string) => {
  await wrapper.get('[data-test="api-key-reveal-password"]').setValue(password)
  await wrapper.get('[data-test="api-key-reveal-dialog"]').trigger('submit')
  await flushPromises()
}

/** WebCrypto runs on the libuv threadpool, so the result lands after several macrotasks. */
const waitForSelector = async (wrapper: Awaited<ReturnType<typeof mountDialog>>, selector: string) => {
  await vi.waitFor(() => {
    expect(wrapper.find(selector).exists()).toBe(true)
  }, { timeout: 10000 })
  await flushPromises()
}

describe('RevealApiKeyDialog', () => {
  beforeAll(async () => {
    seal = await sealApiKey(KEY, PASSWORD, {
      subtle: webcrypto.subtle as unknown as SubtleCrypto,
      getRandomValues,
    })
  })

  beforeEach(() => {
    // Component decrypts through globalThis.crypto, exactly like the browser does.
    vi.stubGlobal('crypto', webcrypto)
    getSecretSeal.mockReset()
    getSecretSeal.mockResolvedValue(seal)
    copyToClipboard.mockClear()
    copyToClipboard.mockResolvedValue(true)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('decrypts locally with the right password, copies it and never sends the password', async () => {
    const wrapper = await mountDialog()
    await submit(wrapper, PASSWORD)
    await waitForSelector(wrapper, '[data-test="api-key-reveal-value"]')

    expect(wrapper.get('[data-test="api-key-reveal-value"]').text()).toBe(KEY)
    expect(wrapper.get('[data-test="api-key-reveal-only-this-time"]').text()).toBe('Shown this time only.')
    expect(copyToClipboard).toHaveBeenCalledWith(KEY, 'Copied')
    // Only the ciphertext request is made; the password is never part of an API call.
    expect(getSecretSeal).toHaveBeenCalledWith(42)
    expect(getSecretSeal.mock.calls[0]).toHaveLength(1)
    expect(JSON.stringify(getSecretSeal.mock.calls)).not.toContain(PASSWORD)
    wrapper.unmount()
  })

  it('shows an alert when the password is wrong and keeps the dialog open', async () => {
    const wrapper = await mountDialog()
    await submit(wrapper, 'wrong password')
    await waitForSelector(wrapper, '[data-test="api-key-reveal-error"]')

    const error = wrapper.get('[data-test="api-key-reveal-error"]')
    expect(error.attributes('role')).toBe('alert')
    expect(error.text()).toBe('Wrong password: this key cannot be decrypted.')
    expect(wrapper.find('[data-test="api-key-reveal-value"]').exists()).toBe(false)
    expect(copyToClipboard).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('maps a missing ciphertext to the rotation path', async () => {
    getSecretSeal.mockRejectedValueOnce({ status: 404, reason: 'API_KEY_SECRET_NOT_FOUND' })
    const wrapper = await mountDialog()
    await submit(wrapper, PASSWORD)

    expect(wrapper.get('[data-test="api-key-reveal-error"]').text()).toBe('The server has no ciphertext for this key.')
    await wrapper.get('[data-test="api-key-reveal-rotate"]').trigger('click')
    expect(wrapper.emitted('rotate')).toHaveLength(1)
    expect(wrapper.emitted('close')).toBeUndefined()
    wrapper.unmount()
  })

  it('reports an incompatible ciphertext instead of a wrong-password error', async () => {
    getSecretSeal.mockResolvedValueOnce({ version: 1, kdf: { algo: 'PBKDF2-SHA256', iterations: 1000, salt: 'AAAA' }, sealed: 'AAAA' })
    const wrapper = await mountDialog()
    await submit(wrapper, PASSWORD)

    expect(wrapper.get('[data-test="api-key-reveal-error"]').text()).toBe('The saved ciphertext is damaged or incompatible.')
    wrapper.unmount()
  })

  it('closes and clears the typed password so reopening starts clean', async () => {
    const wrapper = await mountDialog()
    await wrapper.get('[data-test="api-key-reveal-password"]').setValue('typed-secret')
    await wrapper.get('[data-test="api-key-reveal-cancel"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect((wrapper.get('[data-test="api-key-reveal-password"]').element as HTMLInputElement).value).toBe('')
    wrapper.unmount()
  })
})