import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'

import type { ApiKey } from '@/types'
import ApiKeySecretDialog from '../ApiKeySecretDialog.vue'

const copyToClipboard = vi.hoisted(() => vi.fn().mockResolvedValue(true))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard,
  }),
}))

const { putSecretSeal, sealApiKey } = vi.hoisted(() => ({
  putSecretSeal: vi.fn().mockResolvedValue(undefined),
  sealApiKey: vi.fn(),
}))

vi.mock('@/api', () => ({
  keysAPI: {
    putSecretSeal,
  },
}))

vi.mock('@/utils/apikeySeal', async () => {
  const actual = await vi.importActual<typeof import('@/utils/apikeySeal')>('@/utils/apikeySeal')
  return {
    ...actual,
    sealApiKey,
  }
})

const messages: Record<string, string> = {
  'keys.secretDialog.title': 'Save your key',
  'keys.secretDialog.description': 'Shown once. Copy it now.',
  'keys.secretDialog.waitHint': 'Copy and save the key first',
  'keys.secretDialog.copy': 'Copy',
  'keys.secretDialog.copied': 'Copied',
  'keys.secretDialog.use': 'Use key',
  'keys.secretDialog.importCcs': 'Import to CCS',
  'keys.secretDialog.done': 'Done',
  'keys.seal.optIn': 'View later',
  'keys.seal.optInHint': 'Encrypted in this browser with your login password.',
  'keys.seal.info': 'Details',
  'keys.seal.passwordLabel': 'Login password',
  'keys.seal.passwordRequired': 'Enter your login password',
  'keys.seal.failed': 'Could not save. Try again later.',
  'keys.seal.wrongPassword': 'Incorrect login password',
  'keys.factor.passwordRateLimited': 'Too many incorrect password attempts. Please wait a minute and try again.',
  'keys.seal.unsupported': 'This browser cannot encrypt the key',
  'keys.seal.later': 'Later',
  'keys.seal.unavailable': 'Bind an email and set a login password first',
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

enableAutoUnmount(afterEach)

const SECRET = 'sk-once-abc123'

const createApiKey = (): ApiKey => ({
  id: 1,
  user_id: 1,
  key_prefix: 'sk-once-',
  key_masked: 'sk-once-****-6789',
  name: 'prod key',
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
})

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

const mountDialog = async (props: Record<string, unknown> = {}): Promise<VueWrapper> => {
  const wrapper = mount(ApiKeySecretDialog, {
    attachTo: document.body,
    props: { show: true, secret: SECRET, apiKey: createApiKey(), ...props },
    global: {
      // The password field fades in and out; stubbing Transition keeps "is the field there" synchronous.
      stubs: { teleport: true, Icon: true, transition: true },
    },
  })
  await flushPromises()
  await nextTick()
  return wrapper
}

const dialogBody = (wrapper: VueWrapper) => wrapper.get('[data-test="api-key-secret-dialog"]')
const sealSwitch = (wrapper: VueWrapper) => wrapper.get('[data-test="api-key-seal-checkbox"]')
const hintHidden = (wrapper: VueWrapper) =>
  (wrapper.get('[data-test="api-key-seal-hint"]').element as HTMLElement).style.display === 'none'
const doneButton = (wrapper: VueWrapper) => wrapper.get('[data-test="api-key-secret-done"]')
const closeButton = (wrapper: VueWrapper) => wrapper.get('.base-dialog-header [data-slot="close"]')
const clickNatively = (element: { element: Element }) =>
  (element.element as HTMLButtonElement).click()

describe('ApiKeySecretDialog minimum display window', () => {
  beforeEach(() => {
    copyToClipboard.mockClear()
    copyToClipboard.mockResolvedValue(true)
    putSecretSeal.mockClear()
    putSecretSeal.mockResolvedValue(undefined)
    sealApiKey.mockReset()
    localStorage.clear()
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows the plaintext, one short notice and nothing else to read on open', async () => {
    const wrapper = await mountDialog()

    expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe(SECRET)
    expect(wrapper.get('[data-test="api-key-secret-notice"]').text()).toBe('Shown once. Copy it now.')
    expect(dialogBody(wrapper).attributes('data-can-close')).toBe('false')
  })

  it('keeps the screen quiet: the key name, no masked copy of the key, no warning paragraphs', async () => {
    const wrapper = await mountDialog({ canSeal: true })

    expect(wrapper.get('[data-test="api-key-secret-name"]').text()).toBe('prod key')
    // The full key is right there; the masked form of the same key would be pure noise.
    expect(wrapper.text()).not.toContain('sk-once-****-6789')
    // The old amber box and its two paragraphs are gone: nothing announces itself as an alert on open.
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('.bg-amber-50').exists()).toBe(false)
    // The only explanation of "view later" is hidden until asked for.
    expect(hintHidden(wrapper)).toBe(true)
  })

  it('leads with icons: the notice, the key name, the copy button and the footer actions', async () => {
    const wrapper = await mountDialog({ canSeal: true })

    // The suite stubs the icon component, so an icon is either a rendered <svg> or its stub.
    const icons = (selector: string) => wrapper.get(selector).element.querySelectorAll('svg, icon-stub').length
    expect(icons('[data-test="api-key-secret-notice"]')).toBeGreaterThan(0)
    expect(icons('[data-test="api-key-secret-copy"]')).toBeGreaterThan(0)
    expect(icons('[data-test="api-key-secret-use"]')).toBeGreaterThan(0)
    expect(icons('[data-test="api-key-secret-import-ccs"]')).toBeGreaterThan(0)
    expect(icons('[data-test="api-key-secret-done"]')).toBeGreaterThan(0)
  })

  it('shows the wait cue to screen readers only; sighted users just see the disabled button', async () => {
    const wrapper = await mountDialog()

    const hint = wrapper.get('[data-test="api-key-secret-wait-hint"]')
    expect(hint.classes()).toContain('sr-only')
    expect(hint.attributes('role')).toBe('status')
    expect(hint.text()).toBe('Copy and save the key first')
    expect(doneButton(wrapper).attributes('disabled')).toBeDefined()
  })

  it('blocks X, Escape, the veil and the footer close button during the first second', async () => {
    const wrapper = await mountDialog()

    const x = closeButton(wrapper)
    expect(x.attributes('disabled')).toBeDefined()
    expect(doneButton(wrapper).attributes('disabled')).toBeDefined()

    clickNatively(x)
    clickNatively(doneButton(wrapper))
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await wrapper.get('.modal-overlay').trigger('click')
    await nextTick()

    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe(SECRET)
    expect(dialogBody(wrapper).attributes('data-can-close')).toBe('false')
  })

  it('unlocks the footer close button and header X after one second', async () => {
    const wrapper = await mountDialog()

    await sleep(1100)
    await nextTick()
    await flushPromises()

    expect(dialogBody(wrapper).attributes('data-can-close')).toBe('true')
    expect(wrapper.find('[data-test="api-key-secret-wait-hint"]').exists()).toBe(false)
    expect(doneButton(wrapper).attributes('disabled')).toBeUndefined()
    expect(closeButton(wrapper).attributes('disabled')).toBeUndefined()

    clickNatively(closeButton(wrapper))
    await nextTick()
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('unlocks Escape after one second', async () => {
    const wrapper = await mountDialog()

    await sleep(1100)
    await nextTick()

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()

    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('unlocks clicking the veil after one second', async () => {
    const wrapper = await mountDialog()

    await sleep(1100)
    await nextTick()

    await wrapper.get('.modal-overlay').trigger('click')
    await nextTick()

    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('restarts the one-second window when the dialog is reopened for another key', async () => {
    const wrapper = await mountDialog()

    await sleep(1100)
    await nextTick()
    expect(dialogBody(wrapper).attributes('data-can-close')).toBe('true')

    await wrapper.setProps({ show: false })
    await nextTick()
    await wrapper.setProps({ show: true, secret: 'sk-second-xyz' })
    await nextTick()

    expect(dialogBody(wrapper).attributes('data-can-close')).toBe('false')
    expect(wrapper.get('[data-test="api-key-secret-wait-hint"]').exists()).toBe(true)
  })

  it('copies the one-time plaintext from inside the dialog', async () => {
    const wrapper = await mountDialog()

    await wrapper.get('[data-test="api-key-secret-copy"]').trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith(SECRET, 'Copied')
    expect(wrapper.get('[data-test="api-key-secret-copy"]').text()).toContain('Copied')
  })

  it('never persists the plaintext to browser storage', async () => {
    const wrapper = await mountDialog()

    await wrapper.get('[data-test="api-key-secret-copy"]').trigger('click')
    await flushPromises()

    const stored = [
      ...Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index)),
      ...Array.from({ length: sessionStorage.length }, (_, index) => sessionStorage.key(index)),
    ]
      .filter((key): key is string => key !== null)
      .map((key) => `${key}=${localStorage.getItem(key) ?? sessionStorage.getItem(key) ?? ''}`)
      .join('\n')

    expect(stored).not.toContain(SECRET)
  })
})

describe('ApiKeySecretDialog password-encrypted opt-in', () => {
  const SEAL_PAYLOAD = {
    version: 1 as const,
    kdf: { algo: 'PBKDF2-SHA256' as const, iterations: 310000, salt: 'c2FsdA==' },
    sealed: 'c2VhbGVk',
  }

  beforeEach(() => {
    copyToClipboard.mockClear()
    copyToClipboard.mockResolvedValue(true)
    putSecretSeal.mockClear()
    putSecretSeal.mockResolvedValue(undefined)
    sealApiKey.mockReset()
    sealApiKey.mockResolvedValue(SEAL_PAYLOAD)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  const unlock = async () => {
    await sleep(1100)
    await nextTick()
  }
  const toggleSwitch = async (wrapper: VueWrapper) => {
    await sealSwitch(wrapper).trigger('click')
    await flushPromises()
  }

  // 账号支持封存时「以后可查看」默认开启；关闭弹窗时由 BaseDialog 卸载 DOM，不影响列表页。
  it('defaults the switch on when the account can seal and lets the user turn it off', async () => {
    const wrapper = await mountDialog({ canSeal: true })
    expect(wrapper.get('[data-test="api-key-seal-optin"]').exists()).toBe(true)
    expect(sealSwitch(wrapper).attributes('role')).toBe('switch')
    expect(sealSwitch(wrapper).attributes('aria-checked')).toBe('true')
    const password = wrapper.get('[data-test="api-key-seal-password"]')
    expect(password.attributes('autocomplete')).toBe('new-password')
    expect(password.attributes('name')).toBe('apikey-seal-passphrase')
    expect(password.attributes('data-lpignore')).toBe('true')
    expect(password.attributes('data-1p-ignore')).toBe('true')
    // No separate label paragraph: the placeholder and the aria-label say what it is.
    expect(password.attributes('placeholder')).toBe('Login password')
    expect(password.attributes('aria-label')).toBe('Login password')

    await toggleSwitch(wrapper)
    expect(sealSwitch(wrapper).attributes('aria-checked')).toBe('false')
    expect(wrapper.find('[data-test="api-key-seal-password"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('puts the cursor in the password field when the user toggles the switch back on', async () => {
    const wrapper = await mountDialog({ canSeal: true })

    await toggleSwitch(wrapper) // off
    await toggleSwitch(wrapper) // back on
    await nextTick()

    expect(document.activeElement).toBe(wrapper.get('[data-test="api-key-seal-password"]').element)
    wrapper.unmount()
  })

  it('toggles from the label text as well as from the switch', async () => {
    const wrapper = await mountDialog({ canSeal: true })

    expect(sealSwitch(wrapper).attributes('aria-checked')).toBe('true')
    await wrapper.get('label[for="api-key-seal-switch"]').trigger('click')
    await flushPromises()
    expect(sealSwitch(wrapper).attributes('aria-checked')).toBe('false')

    await wrapper.get('label[for="api-key-seal-switch"]').trigger('click')
    await flushPromises()
    expect(sealSwitch(wrapper).attributes('aria-checked')).toBe('true')
    wrapper.unmount()
  })

  it('keeps the explanation behind the (i) button, one sentence, opened on demand', async () => {
    const wrapper = await mountDialog({ canSeal: true })
    const info = wrapper.get('[data-test="api-key-seal-info"]')
    expect(info.attributes('aria-label')).toBe('Details')
    expect(info.attributes('aria-expanded')).toBe('false')
    expect(hintHidden(wrapper)).toBe(true)

    await info.trigger('click')
    expect(info.attributes('aria-expanded')).toBe('true')
    expect(hintHidden(wrapper)).toBe(false)
    expect(wrapper.get('[data-test="api-key-seal-hint"]').text()).toBe('Encrypted in this browser with your login password.')

    await info.trigger('click')
    expect(hintHidden(wrapper)).toBe(true)
    wrapper.unmount()
  })

  it('does not show the row at all when the account has no login password', async () => {
    const wrapper = await mountDialog({ canSeal: false })

    expect(wrapper.find('[data-test="api-key-seal-optin"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="api-key-seal-checkbox"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="api-key-seal-info"]').exists()).toBe(false)
    // Closing still works and never tries to seal.
    await unlock()
    await doneButton(wrapper).trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(sealApiKey).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('ignores a remembered password when the account cannot seal', async () => {
    const wrapper = await mountDialog({ canSeal: false, verifiedPassword: 'typed-once' })

    expect(wrapper.find('[data-test="api-key-seal-optin"]').exists()).toBe(false)
    await unlock()
    await doneButton(wrapper).trigger('click')
    expect(sealApiKey).not.toHaveBeenCalled()
    expect(putSecretSeal).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('requires a password before sealing when none was typed earlier', async () => {
    const wrapper = await mountDialog({ canSeal: true })
    await unlock()

    await doneButton(wrapper).trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="api-key-seal-error"]').text()).toBe('Enter your login password')
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(sealApiKey).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('seals locally, verifies the password server-side and reports the sealed row', async () => {
    const wrapper = await mountDialog({ canSeal: true, authFactor: 'totp' })
    await unlock()

    await wrapper.get('[data-test="api-key-seal-password"]').setValue('correct horse')
    await doneButton(wrapper).trigger('click')
    await flushPromises()

    // 密码只参与本地 KDF，同时随封存请求发给服务端做 bcrypt 校验；
    // 授权因子来自创建时选择的方式，二次查看要求同一因子。
    expect(sealApiKey).toHaveBeenCalledWith(SECRET, 'correct horse')
    expect(putSecretSeal).toHaveBeenCalledWith(1, SEAL_PAYLOAD, {
      password: 'correct horse',
      authFactor: 'totp',
    })
    expect(wrapper.emitted('sealed')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('saves on Enter in the password field, like pressing Done', async () => {
    const wrapper = await mountDialog({ canSeal: true })
    await unlock()

    const field = wrapper.get('[data-test="api-key-seal-password"]')
    await field.setValue('correct horse')
    await field.trigger('keydown', { key: 'Enter' })
    await flushPromises()

    expect(putSecretSeal).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('does not save twice when Enter is pressed again while the first save is still running', async () => {
    let finish!: () => void
    putSecretSeal.mockReturnValueOnce(new Promise<void>((resolve) => { finish = resolve }))
    const wrapper = await mountDialog({ canSeal: true })
    await unlock()

    const field = wrapper.get('[data-test="api-key-seal-password"]')
    await field.setValue('correct horse')
    await field.trigger('keydown', { key: 'Enter' })
    await field.trigger('keydown', { key: 'Enter' })
    await flushPromises()

    expect(putSecretSeal).toHaveBeenCalledTimes(1)
    finish()
    await flushPromises()
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('does not save on Enter while the receipt is still locked', async () => {
    const wrapper = await mountDialog({ canSeal: true })

    const field = wrapper.get('[data-test="api-key-seal-password"]')
    await field.setValue('correct horse')
    await field.trigger('keydown', { key: 'Enter' })
    await flushPromises()

    expect(putSecretSeal).not.toHaveBeenCalled()
    expect(wrapper.emitted('close')).toBeUndefined()
    wrapper.unmount()
  })

  describe('when the login password was already typed to create this key', () => {
    it('asks for nothing more: switch defaults on, no field, and Done seals with the remembered password', async () => {
      const wrapper = await mountDialog({ canSeal: true, verifiedPassword: 'typed-once' })
      await unlock()

      expect(sealSwitch(wrapper).attributes('aria-checked')).toBe('true')
      expect(wrapper.find('[data-test="api-key-seal-password"]').exists()).toBe(false)

      await doneButton(wrapper).trigger('click')
      await flushPromises()

      expect(sealApiKey).toHaveBeenCalledWith(SECRET, 'typed-once')
      expect(putSecretSeal).toHaveBeenCalledWith(1, SEAL_PAYLOAD, { password: 'typed-once', authFactor: 'password' })
      expect(wrapper.emitted('sealed')).toHaveLength(1)
      expect(wrapper.emitted('close')).toHaveLength(1)
      wrapper.unmount()
    })

    it('still lets the user decline: turning the switch off means nothing is sealed', async () => {
      const wrapper = await mountDialog({ canSeal: true, verifiedPassword: 'typed-once' })
      await unlock()

      await toggleSwitch(wrapper)
      await doneButton(wrapper).trigger('click')
      await flushPromises()

      expect(sealApiKey).not.toHaveBeenCalled()
      expect(putSecretSeal).not.toHaveBeenCalled()
      expect(wrapper.emitted('close')).toHaveLength(1)
      wrapper.unmount()
    })

    it('never renders or stores the remembered password', async () => {
      const wrapper = await mountDialog({ canSeal: true, verifiedPassword: 'typed-once' })
      await wrapper.get('[data-test="api-key-seal-info"]').trigger('click')

      expect(wrapper.html()).not.toContain('typed-once')
      const stored = [
        ...Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index)),
        ...Array.from({ length: sessionStorage.length }, (_, index) => sessionStorage.key(index)),
      ]
        .filter((key): key is string => key !== null)
        .map((key) => `${key}=${localStorage.getItem(key) ?? sessionStorage.getItem(key) ?? ''}`)
        .join('\n')
      expect(stored).not.toContain('typed-once')
      wrapper.unmount()
    })

    it('hands over to typing if the server refuses the remembered password', async () => {
      putSecretSeal.mockRejectedValueOnce({ status: 400, reason: 'VERIFY_PASSWORD_INVALID' })
      const wrapper = await mountDialog({ canSeal: true, verifiedPassword: 'typed-once' })
      await unlock()

      await doneButton(wrapper).trigger('click')
      await flushPromises()

      expect(wrapper.get('[data-test="api-key-seal-error"]').text()).toBe('Incorrect login password')
      // The user can now type the right one instead of being stuck behind a field that is not there.
      const field = wrapper.get('[data-test="api-key-seal-password"]')
      expect(wrapper.emitted('close')).toBeUndefined()

      await field.setValue('the right one')
      await doneButton(wrapper).trigger('click')
      await flushPromises()
      expect(putSecretSeal).toHaveBeenLastCalledWith(1, SEAL_PAYLOAD, { password: 'the right one', authFactor: 'password' })
      expect(wrapper.emitted('sealed')).toHaveLength(1)
      wrapper.unmount()
    })

    it('goes back to reusing the password for the next key after having fallen back to typing', async () => {
      putSecretSeal.mockRejectedValueOnce({ status: 400, reason: 'VERIFY_PASSWORD_INVALID' })
      const wrapper = await mountDialog({ canSeal: true, verifiedPassword: 'typed-once' })
      await unlock()
      await doneButton(wrapper).trigger('click')
      await flushPromises()
      expect(wrapper.find('[data-test="api-key-seal-password"]').exists()).toBe(true)

      await wrapper.setProps({ show: false })
      await nextTick()
      await wrapper.setProps({ show: true, secret: 'sk-second-xyz', verifiedPassword: 'typed-again' })
      await nextTick()

      expect(sealSwitch(wrapper).attributes('aria-checked')).toBe('true')
      expect(wrapper.find('[data-test="api-key-seal-password"]').exists()).toBe(false)
      wrapper.unmount()
    })

    it('uses the remembered password again for the next receipt only if the parent passes it again', async () => {
      const wrapper = await mountDialog({ canSeal: true, verifiedPassword: 'typed-once' })
      await unlock()

      await wrapper.setProps({ show: false, verifiedPassword: '' })
      await nextTick()
      await wrapper.setProps({ show: true, secret: 'sk-second-xyz' })
      await nextTick()

      // The parent cleared it: the user is asked, because the dialog kept nothing.
      expect(sealSwitch(wrapper).attributes('aria-checked')).toBe('true')
      expect(wrapper.find('[data-test="api-key-seal-password"]').exists()).toBe(true)
      wrapper.unmount()
    })
  })

  it('shows a specific error and keeps the dialog open when the login password is wrong', async () => {
    putSecretSeal.mockRejectedValueOnce({ status: 400, reason: 'VERIFY_PASSWORD_INVALID' })
    const wrapper = await mountDialog({ canSeal: true })
    await unlock()

    await wrapper.get('[data-test="api-key-seal-password"]').setValue('wrong password')
    await doneButton(wrapper).trigger('click')
    await flushPromises()

    const error = wrapper.get('[data-test="api-key-seal-error"]')
    expect(error.attributes('role')).toBe('alert')
    expect(error.text()).toBe('Incorrect login password')
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.emitted('sealed')).toBeUndefined()

    await wrapper.get('[data-test="api-key-seal-later"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('tells the user to wait (not "wrong password") when the password attempts are used up', async () => {
    putSecretSeal.mockRejectedValueOnce({ status: 429, reason: 'VERIFY_PASSWORD_RATE_LIMITED' })
    const wrapper = await mountDialog({ canSeal: true })
    await unlock()

    await wrapper.get('[data-test="api-key-seal-password"]').setValue('correct horse')
    await doneButton(wrapper).trigger('click')
    await flushPromises()

    const error = wrapper.get('[data-test="api-key-seal-error"]')
    expect(error.attributes('role')).toBe('alert')
    expect(error.text()).toBe('Too many incorrect password attempts. Please wait a minute and try again.')
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.emitted('sealed')).toBeUndefined()
    wrapper.unmount()
  })

  it('shows a retryable error when saving the ciphertext fails and lets the user postpone', async () => {
    putSecretSeal.mockRejectedValueOnce({ status: 500, message: 'boom' })
    const wrapper = await mountDialog({ canSeal: true })
    await unlock()

    await wrapper.get('[data-test="api-key-seal-password"]').setValue('whatever')
    await doneButton(wrapper).trigger('click')
    await flushPromises()

    const error = wrapper.get('[data-test="api-key-seal-error"]')
    expect(error.attributes('role')).toBe('alert')
    expect(error.text()).toBe('Could not save. Try again later.')
    expect(wrapper.emitted('close')).toBeUndefined()

    await wrapper.get('[data-test="api-key-seal-later"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.emitted('sealed')).toBeUndefined()
    wrapper.unmount()
  })

  it('clears a stale error when the switch is turned off again', async () => {
    putSecretSeal.mockRejectedValueOnce({ status: 500, message: 'boom' })
    const wrapper = await mountDialog({ canSeal: true })
    await unlock()

    await wrapper.get('[data-test="api-key-seal-password"]').setValue('whatever')
    await doneButton(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="api-key-seal-error"]').exists()).toBe(true)

    await toggleSwitch(wrapper) // off
    expect(wrapper.find('[data-test="api-key-seal-error"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
