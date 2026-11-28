import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createSSRApp, nextTick } from 'vue'
import { renderToString } from 'vue/server-renderer'
import ProfilePasskeyCard from '../ProfilePasskeyCard.vue'

const { isSupported, list, register, rename, remove, totpStatus, showError, showSuccess, showWarning } = vi.hoisted(() => ({
  isSupported: vi.fn(),
  list: vi.fn(),
  register: vi.fn(),
  rename: vi.fn(),
  remove: vi.fn(),
  totpStatus: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn()
}))

vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/api', () => ({
  passkeyAPI: { isSupported, list, register, rename, remove },
  totpAPI: { getStatus: totpStatus }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess, showWarning }) }))
vi.mock('@/components/icons', () => ({ Icon: { render: () => null } }))

enableAutoUnmount(afterEach)

const savedCredential = {
  id: 3,
  name: 'ios',
  created_at: '2026-11-23T00:00:00Z',
  last_used_at: undefined,
  backup: false
}

function buttonByText(wrapper: ReturnType<typeof mount>, label: string) {
  return wrapper.findAll('button').find((button) => button.text() === label)
}

async function mountCard(credentials?: (typeof savedCredential)[]) {
  isSupported.mockReturnValue(true)
  totpStatus.mockResolvedValue({ enabled: false, feature_enabled: true })
  if (credentials) list.mockResolvedValue(credentials)
  const wrapper = mount(ProfilePasskeyCard, {
    props: { enabled: true },
    global: { stubs: { teleport: true } }
  })
  await flushPromises()
  return wrapper
}

describe('ProfilePasskeyCard SSR capability detection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    isSupported.mockReturnValue(true)
    totpStatus.mockResolvedValue({ enabled: false, feature_enabled: true })
    list.mockResolvedValue([])
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('does not detect browser support or load credentials during SSR, even when enabled', async () => {
    vi.stubGlobal('window', undefined)
    vi.stubGlobal('document', undefined)
    vi.stubGlobal('navigator', undefined)
    try {
      const html = await renderToString(createSSRApp(ProfilePasskeyCard, { enabled: true }))
      expect(html).toContain('profile.passkey.title')
      expect(html).toContain('profile.passkey.unsupported')
      expect(isSupported).not.toHaveBeenCalled()
      expect(list).not.toHaveBeenCalled()
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('hydrates the same initial markup, then enables WebAuthn and loads once on mount', async () => {
    const html = await renderToString(createSSRApp(ProfilePasskeyCard, { enabled: true }))
    const container = document.createElement('div')
    container.innerHTML = html
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const app = createSSRApp(ProfilePasskeyCard, { enabled: true })
    app.mount(container)
    try {
      await flushPromises()
      await nextTick()
      expect(isSupported).toHaveBeenCalledTimes(1)
      expect(list).toHaveBeenCalledTimes(1)
      expect(container.textContent).toContain('profile.passkey.add')
      expect(container.textContent).not.toContain('profile.passkey.unsupported')
      expect([...warn.mock.calls, ...error.mock.calls].flat().join(' ')).not.toMatch(/hydration/i)
    } finally {
      app.unmount()
    }
  })

  it('keeps unsupported browsers disabled after mount', async () => {
    isSupported.mockReturnValue(false)
    const wrapper = mount(ProfilePasskeyCard, { props: { enabled: true } })
    await flushPromises()
    expect(wrapper.text()).toContain('profile.passkey.unsupported')
    expect(wrapper.find('button').exists()).toBe(false)
  })

  it('loads credentials only after the feature is enabled on the client', async () => {
    const wrapper = mount(ProfilePasskeyCard, { props: { enabled: false } })
    await flushPromises()
    expect(list).not.toHaveBeenCalled()
    await wrapper.setProps({ enabled: true })
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(1)
    await wrapper.setProps({ enabled: false })
    expect(wrapper.text()).toContain('profile.passkey.featureDisabled')
  })
})

describe('ProfilePasskeyCard 列表状态', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    isSupported.mockReturnValue(true)
    totpStatus.mockResolvedValue({ enabled: false, feature_enabled: true })
  })

  it('keeps the empty state distinct from a load failure', async () => {
    const wrapper = await mountCard([])
    expect(wrapper.find('[data-testid="passkey-empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="passkey-load-error"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('profile.passkey.empty')
    expect(showError).not.toHaveBeenCalled()
  })

  it('shows an inline error with a retry button when loading fails, then recovers', async () => {
    list.mockRejectedValueOnce({ status: 500, message: 'boom' })
    const wrapper = await mountCard()

    expect(wrapper.find('[data-testid="passkey-load-error"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('profile.passkey.loadFailed')
    // 加载失败不能被当成「尚未添加任何 Passkey」。
    expect(wrapper.find('[data-testid="passkey-empty"]').exists()).toBe(false)

    list.mockResolvedValueOnce([savedCredential])
    await buttonByText(wrapper, 'profile.passkey.retry')!.trigger('click')
    await flushPromises()

    expect(list).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="passkey-load-error"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('ios')
  })
})

describe('ProfilePasskeyCard 创建 Passkey', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    isSupported.mockReturnValue(true)
    totpStatus.mockResolvedValue({ enabled: false, feature_enabled: true })
    list.mockResolvedValue([])
  })

  async function submitCreateForm(wrapper: ReturnType<typeof mount>, name: string, password: string) {
    await buttonByText(wrapper, 'profile.passkey.add')!.trigger('click')
    await wrapper.find('#passkey-name').setValue(name)
    await wrapper.find('#passkey-add-password').setValue(password)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
  }

  it('shows a success toast and immediately refreshes the list', async () => {
    list.mockResolvedValueOnce([]).mockResolvedValueOnce([savedCredential])
    register.mockResolvedValue(savedCredential)
    const wrapper = await mountCard([])

    await submitCreateForm(wrapper, 'ios', 'correct-password')

    expect(register).toHaveBeenCalledWith('ios', 'correct-password')
    expect(showSuccess).toHaveBeenCalledWith('profile.passkey.added')
    expect(list).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('ios')
    expect(showError).not.toHaveBeenCalled()
  })

  it.each([
    ['PASSKEY_ORIGIN_MISMATCH', 'profile.passkey.originMismatch'],
    ['PASSKEY_RP_ID_MISMATCH', 'profile.passkey.rpIdMismatch'],
    ['PASSKEY_CHALLENGE_EXPIRED', 'profile.passkey.challengeExpired'],
    ['PASSKEY_VERIFICATION_FAILED', 'profile.passkey.verificationFailed'],
    ['PASSWORD_INCORRECT', 'profile.passkey.passwordIncorrect']
  ])('surfaces the backend reason %s instead of failing silently', async (reason, messageKey) => {
    register.mockRejectedValue({ status: 400, code: 400, reason, message: 'backend message' })
    const wrapper = await mountCard([])

    await submitCreateForm(wrapper, 'ios', 'correct-password')

    expect(showError).toHaveBeenCalledWith(messageKey)
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('falls back to the backend message when the reason is unknown', async () => {
    register.mockRejectedValue({ status: 400, code: 400, reason: 'SOMETHING_NEW', message: '详细原因' })
    const wrapper = await mountCard([])

    await submitCreateForm(wrapper, 'ios', 'correct-password')

    expect(showError).toHaveBeenCalledWith('详细原因')
  })

  it('shows a cancellation notice when the authenticator ceremony is aborted', async () => {
    register.mockRejectedValue(new DOMException('The operation was aborted', 'NotAllowedError'))
    const wrapper = await mountCard([])

    await submitCreateForm(wrapper, 'ios', 'correct-password')

    expect(showWarning).toHaveBeenCalledWith('profile.passkey.cancelled')
    expect(showError).not.toHaveBeenCalled()
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('shows the 2FA hint only when the account has TOTP enabled', async () => {
    list.mockResolvedValue([])
    totpStatus.mockResolvedValue({ enabled: true, feature_enabled: true })
    const wrapper = mount(ProfilePasskeyCard, { props: { enabled: true } })
    await flushPromises()
    await buttonByText(wrapper, 'profile.passkey.add')!.trigger('click')
    expect(wrapper.text()).toContain('profile.passkey.stepUpHint')
  })
})

describe('ProfilePasskeyCard 删除 Passkey', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    isSupported.mockReturnValue(true)
    totpStatus.mockResolvedValue({ enabled: false, feature_enabled: true })
  })

  it('deletes a passkey, shows a toast and reloads the list', async () => {
    list.mockResolvedValueOnce([savedCredential]).mockResolvedValueOnce([])
    remove.mockResolvedValue(undefined)
    const wrapper = await mountCard()

    await buttonByText(wrapper, 'common.delete')!.trigger('click')
    expect(wrapper.text()).toContain('profile.passkey.deleteTitle')
    await wrapper.find('#passkey-delete-password').setValue('correct-password')
    const forms = wrapper.findAll('form')
    await forms[forms.length - 1].trigger('submit')
    await flushPromises()

    expect(remove).toHaveBeenCalledWith(savedCredential.id, 'correct-password')
    expect(showSuccess).toHaveBeenCalledWith('profile.passkey.deleted')
    expect(list).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).not.toContain('ios')
  })

  it('surfaces delete failures and keeps the dialog open for retry', async () => {
    list.mockResolvedValue([savedCredential])
    remove.mockRejectedValue({ status: 400, code: 400, reason: 'STEP_UP_REQUIRED', message: 'step up' })
    const wrapper = await mountCard()

    await buttonByText(wrapper, 'common.delete')!.trigger('click')
    await wrapper.find('#passkey-delete-password').setValue('wrong-password')
    const forms = wrapper.findAll('form')
    await forms[forms.length - 1].trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('profile.passkey.stepUpRequired')
    expect(wrapper.text()).toContain('profile.passkey.deleteTitle')
    expect(list).toHaveBeenCalledTimes(1)
  })
})
