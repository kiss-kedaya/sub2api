import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import InfiniteCanvasView from '../InfiniteCanvasView.vue'

const { ensureInfiniteCanvasApiKey, showSuccess, showWarning, fetchPublicSettings, getVerificationFactors, buildVerification } = vi.hoisted(() => ({
  ensureInfiniteCanvasApiKey: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  fetchPublicSettings: vi.fn(),
  getVerificationFactors: vi.fn(),
  buildVerification: vi.fn(),
}))

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
    locale: { value: 'zh-CN' },
  }),
}))
vi.mock('vue-router', () => ({
  RouterLink: { props: ['to'], template: '<a :href="to" data-router-link><slot /></a>' },
}))
vi.mock('@/stores', () => ({
  useAppStore: () => ({
    publicSettingsLoaded: true,
    cachedPublicSettings: {},
    fetchPublicSettings,
    showSuccess,
    showWarning,
  }),
}))
vi.mock('@/api', () => ({ keysAPI: { getVerificationFactors } }))
// 身份校验组件本身有自己的测试；这里只关心页面如何使用它：渲染它、用它的 buildVerification 取载荷。
vi.mock('@/components/keys/KeyFactorVerification.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      props: { factors: { type: Object, default: null }, mode: { type: String, default: 'create' } },
      setup(_props, { expose }) {
        expose({ buildVerification: () => buildVerification() })
        return () => h('div', { 'data-test': 'factor-stub' })
      },
    }),
  }
})
vi.mock('@/utils/infiniteCanvasSession', () => ({
  InfiniteCanvasSetupError: class InfiniteCanvasSetupError extends Error {
    constructor(public code: string, message?: string, public rotationTarget?: unknown, public reason?: string) {
      super(message)
    }
  },
  ensureInfiniteCanvasApiKey,
}))

const wrappers: ReturnType<typeof mount>[] = []

const NO_FACTORS = { available_methods: [], password_available: false, totp_enabled: false, passkey_count: 0, preferred: '', seal_available: false }
const PASSWORD_FACTOR = { ...NO_FACTORS, available_methods: ['password'], password_available: true, preferred: 'password', seal_available: true }
const TOTP_FACTOR = { ...NO_FACTORS, available_methods: ['totp'], totp_enabled: true, preferred: 'totp' }
const PASSKEY_FACTOR = { ...NO_FACTORS, available_methods: ['passkey'], passkey_count: 1, preferred: 'passkey' }
const ROTATION_TARGET = { keyId: 7, keyMasked: 'sk-abcd****wxyz', groupCount: 4 }

async function setupError(code: string, message = '', rotationTarget?: unknown, reason?: string) {
  const { InfiniteCanvasSetupError } = await import('@/utils/infiniteCanvasSession')
  return new InfiniteCanvasSetupError(code as never, message, rotationTarget as never, reason)
}

function mountView() {
  const wrapper = mount(InfiniteCanvasView, { global: { stubs: { Icon: true } } })
  wrappers.push(wrapper)
  return wrapper
}

describe('InfiniteCanvasView', () => {
  beforeEach(() => {
    ensureInfiniteCanvasApiKey.mockReset()
    showSuccess.mockReset()
    showWarning.mockReset()
    getVerificationFactors.mockReset()
    getVerificationFactors.mockResolvedValue(NO_FACTORS)
    buildVerification.mockReset()
  })

  afterEach(() => {
    wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  })

  it('embeds Infinite Canvas with the smart-routing key and OpenAI-compatible base URL', async () => {
    ensureInfiniteCanvasApiKey.mockResolvedValue({
      apiKey: 'sk-canvas',
      groupIds: [2, 5],
      truncated: false,
      created: false,
      keyId: 9,
    })
    const wrapper = mountView()
    await flushPromises()

    const src = wrapper.get('iframe').attributes('src') || ''
    const url = new URL(src)
    expect(url.pathname).toBe('/canvas/')
    expect(url.searchParams.get('apiKey')).toBe('sk-canvas')
    expect(url.searchParams.get('baseUrl')).toBe(window.location.origin)
    expect(url.searchParams.get('lang')).toBe('zh-CN')
    expect(wrapper.find('a[target="_blank"]').exists()).toBe(false)
    expect(showSuccess).not.toHaveBeenCalled()
    // 进入页面永远不允许轮换、不创建。
    expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledTimes(1)
    expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledWith(undefined, { createIfMissing: false, allowRotate: false })
    expect(getVerificationFactors).not.toHaveBeenCalled()
  })

  it('shows a retryable error when the user has no groups', async () => {
    ensureInfiniteCanvasApiKey.mockRejectedValue(await setupError('no-groups', 'none'))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('infiniteCanvas.noGroups')
  })

  describe('first-time creation', () => {
    it('waits for the create action when no key exists and the account has no verification factor', async () => {
      ensureInfiniteCanvasApiKey.mockRejectedValueOnce(await setupError('not-configured', 'none'))
        .mockResolvedValueOnce({ apiKey: 'sk-new-canvas', created: true, groupIds: [2], keyId: 10, truncated: false })
      const wrapper = mountView()
      await flushPromises()
      expect(wrapper.find('iframe').exists()).toBe(false)
      expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledTimes(1)
      expect(wrapper.find('[data-testid="canvas-verification"]').exists()).toBe(false)
      expect(wrapper.get('[data-testid="canvas-confirm-submit"]').text()).toContain('infiniteCanvas.createAndEnter')

      await wrapper.get('[data-testid="canvas-confirm-submit"]').trigger('click')
      await flushPromises()
      expect(ensureInfiniteCanvasApiKey).toHaveBeenLastCalledWith(undefined, { createIfMissing: true, allowRotate: false })
      expect(new URL(wrapper.get('iframe').attributes('src')!).searchParams.get('apiKey')).toBe('sk-new-canvas')
      expect(showSuccess).toHaveBeenCalledTimes(1)
    })

    it('sends the identity verification when the account has an available factor', async () => {
      getVerificationFactors.mockResolvedValue(PASSWORD_FACTOR)
      buildVerification.mockReturnValue({ method: 'password', password: 'fixture-password' })
      ensureInfiniteCanvasApiKey.mockRejectedValueOnce(await setupError('not-configured', 'none'))
        .mockResolvedValueOnce({ apiKey: 'sk-new-canvas', created: true, groupIds: [2], keyId: 10, truncated: false })
      const wrapper = mountView()
      await flushPromises()
      expect(wrapper.find('[data-testid="canvas-verification"]').exists()).toBe(true)

      await wrapper.get('[data-testid="canvas-confirm-submit"]').trigger('click')
      await flushPromises()

      expect(ensureInfiniteCanvasApiKey).toHaveBeenLastCalledWith(undefined, {
        createIfMissing: true,
        allowRotate: false,
        verification: { method: 'password', password: 'fixture-password' },
      })
      expect(wrapper.find('iframe').exists()).toBe(true)
    })
  })

  describe('rotating the Infinite Canvas key', () => {
    it('asks first and sends no rotation request just because the page was entered or refreshed', async () => {
      ensureInfiniteCanvasApiKey.mockRejectedValue(await setupError('rotation-required', 'x', ROTATION_TARGET))
      const wrapper = mountView()
      await flushPromises()

      expect(wrapper.find('iframe').exists()).toBe(false)
      expect(wrapper.get('[data-testid="canvas-rotate-title"]').text()).toContain('infiniteCanvas.rotateTitle')
      expect(wrapper.text()).toContain('infiniteCanvas.rotateTarget')
      // 页面只发了一次「不允许轮换」的请求；没有点按钮之前不会再有第二次。
      expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledTimes(1)
      expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledWith(undefined, { createIfMissing: false, allowRotate: false })
      expect(showSuccess).not.toHaveBeenCalled()
    })

    it('rotates only after the user confirms', async () => {
      ensureInfiniteCanvasApiKey.mockRejectedValueOnce(await setupError('rotation-required', 'x', ROTATION_TARGET))
        .mockResolvedValueOnce({ apiKey: 'sk-rotated', created: false, rotated: true, groupIds: [2], keyId: 7, truncated: false })
      const wrapper = mountView()
      await flushPromises()

      await wrapper.get('[data-testid="canvas-confirm-submit"]').trigger('click')
      await flushPromises()

      expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledTimes(2)
      expect(ensureInfiniteCanvasApiKey).toHaveBeenLastCalledWith(undefined, { createIfMissing: false, allowRotate: true })
      expect(new URL(wrapper.get('iframe').attributes('src')!).searchParams.get('apiKey')).toBe('sk-rotated')
      expect(showSuccess).toHaveBeenCalledWith('infiniteCanvas.keyRotated')
    })

    it('includes the identity verification in the confirmed rotation', async () => {
      getVerificationFactors.mockResolvedValue(PASSWORD_FACTOR)
      buildVerification.mockReturnValue({ method: 'password', password: 'fixture-password' })
      ensureInfiniteCanvasApiKey.mockRejectedValueOnce(await setupError('rotation-required', 'x', ROTATION_TARGET))
        .mockResolvedValueOnce({ apiKey: 'sk-rotated', created: false, rotated: true, groupIds: [2], keyId: 7, truncated: false })
      const wrapper = mountView()
      await flushPromises()

      await wrapper.get('[data-testid="canvas-confirm-submit"]').trigger('click')
      await flushPromises()

      expect(ensureInfiniteCanvasApiKey).toHaveBeenLastCalledWith(undefined, {
        createIfMissing: false,
        allowRotate: true,
        verification: { method: 'password', password: 'fixture-password' },
      })
    })

    it('does not send anything while the verification form is incomplete', async () => {
      getVerificationFactors.mockResolvedValue(PASSWORD_FACTOR)
      buildVerification.mockReturnValue(null)
      ensureInfiniteCanvasApiKey.mockRejectedValue(await setupError('rotation-required', 'x', ROTATION_TARGET))
      const wrapper = mountView()
      await flushPromises()

      await wrapper.get('[data-testid="canvas-confirm-submit"]').trigger('click')
      await flushPromises()

      expect(buildVerification).toHaveBeenCalledTimes(1)
      expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledTimes(1)
      expect(wrapper.find('[data-testid="canvas-confirm"]').exists()).toBe(true)
    })

    it('stays on the confirmation with the reason when verification fails, then succeeds on retry', async () => {
      getVerificationFactors.mockResolvedValue(PASSWORD_FACTOR)
      buildVerification.mockReturnValue({ method: 'password', password: 'fixture-password' })
      ensureInfiniteCanvasApiKey.mockRejectedValueOnce(await setupError('rotation-required', 'x', ROTATION_TARGET))
        .mockRejectedValueOnce(await setupError('verification-failed', 'bad', undefined, 'VERIFY_PASSWORD_INVALID'))
        .mockResolvedValueOnce({ apiKey: 'sk-rotated', created: false, rotated: true, groupIds: [2], keyId: 7, truncated: false })
      const wrapper = mountView()
      await flushPromises()

      await wrapper.get('[data-testid="canvas-confirm-submit"]').trigger('click')
      await flushPromises()
      expect(wrapper.find('iframe').exists()).toBe(false)
      expect(wrapper.get('[data-testid="canvas-verify-error"]').text()).toBe('keys.factor.passwordInvalid')
      expect(wrapper.get('[data-testid="canvas-confirm-submit"]').text()).toContain('infiniteCanvas.rotateConfirm')

      await wrapper.get('[data-testid="canvas-confirm-submit"]').trigger('click')
      await flushPromises()
      expect(wrapper.find('iframe').exists()).toBe(true)
      expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledTimes(3)
    })

    it('leaving through the cancel link changes no key', async () => {
      ensureInfiniteCanvasApiKey.mockRejectedValue(await setupError('rotation-required', 'x', ROTATION_TARGET))
      const wrapper = mountView()
      await flushPromises()

      const cancel = wrapper.get('[data-testid="canvas-rotate-cancel"]')
      expect(cancel.attributes('href')).toBe('/dashboard')
      expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledTimes(1)
    })

    it('shows a retryable error instead of guessing when the verification methods cannot be loaded', async () => {
      getVerificationFactors.mockRejectedValue(new Error('network'))
      ensureInfiniteCanvasApiKey.mockRejectedValue(await setupError('rotation-required', 'x', ROTATION_TARGET))
      const wrapper = mountView()
      await flushPromises()

      expect(wrapper.find('[data-testid="canvas-confirm"]').exists()).toBe(false)
      expect(wrapper.text()).toContain('infiniteCanvas.requestFailed')
      expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledTimes(1)
    })
  })

  // 密码输错次数用尽（服务端 429 VERIFY_PASSWORD_RATE_LIMITED）：要告诉用户等一会儿，
  // 而不是笼统的「准备无限画布密钥失败」，并且留在确认卡上，等过了再点同一个按钮。
  describe('password attempts used up', () => {
    it.each([
      { action: 'create', first: 'not-configured', submitKey: 'infiniteCanvas.createAndEnter' },
      { action: 'rotate', first: 'rotation-required', submitKey: 'infiniteCanvas.rotateConfirm' },
    ])('says to wait and keeps the $action confirmation open', async ({ first, submitKey }) => {
      getVerificationFactors.mockResolvedValue(PASSWORD_FACTOR)
      buildVerification.mockReturnValue({ method: 'password', password: 'fixture-password' })
      ensureInfiniteCanvasApiKey
        .mockRejectedValueOnce(await setupError(first, 'x', first === 'rotation-required' ? ROTATION_TARGET : undefined))
        .mockRejectedValueOnce(await setupError('verification-failed', 'slow down', undefined, 'VERIFY_PASSWORD_RATE_LIMITED'))
      const wrapper = mountView()
      await flushPromises()

      await wrapper.get('[data-testid="canvas-confirm-submit"]').trigger('click')
      await flushPromises()

      expect(wrapper.get('[data-testid="canvas-verify-error"]').text()).toBe('keys.factor.passwordRateLimited')
      expect(wrapper.get('[data-testid="canvas-confirm-submit"]').text()).toContain(submitKey)
      expect(wrapper.find('iframe').exists()).toBe(false)
    })
  })

  // 创建 / 轮换都会下发明文：后端按账号实际绑定的因子要求「密码 / TOTP / Passkey 三选一」，
  // 没有任何因子的账号放行。页面必须按服务端报的可用因子决定出不出校验步，
  // 并把对应的校验材料原样交给创建 / 轮换（TOTP 码、Passkey 一次性票据都只能用一次，所以绝不缓存、绝不复用）。
  describe.each([
    { account: 'TOTP', factors: TOTP_FACTOR, verification: { method: 'totp', totp_code: '123456' } },
    { account: 'Passkey', factors: PASSKEY_FACTOR, verification: { method: 'passkey', passkey_session_token: 'fixture-ticket' } },
    { account: 'no-factor', factors: NO_FACTORS, verification: undefined },
  ])('$account account', ({ factors, verification }) => {
    const expectedOptions = (extra: Record<string, unknown>) => ({ ...extra, ...(verification ? { verification } : {}) })

    it('creates the canvas key', async () => {
      getVerificationFactors.mockResolvedValue(factors)
      buildVerification.mockReturnValue(verification ?? null)
      ensureInfiniteCanvasApiKey
        .mockRejectedValueOnce(await setupError('not-configured', 'none'))
        .mockResolvedValueOnce({ apiKey: 'sk-new-canvas', created: true, groupIds: [2], keyId: 10, truncated: false })
      const wrapper = mountView()
      await flushPromises()

      expect(wrapper.find('[data-testid="canvas-verification"]').exists()).toBe(verification !== undefined)
      await wrapper.get('[data-testid="canvas-confirm-submit"]').trigger('click')
      await flushPromises()

      expect(ensureInfiniteCanvasApiKey).toHaveBeenLastCalledWith(
        undefined,
        expectedOptions({ createIfMissing: true, allowRotate: false }),
      )
      expect(new URL(wrapper.get('iframe').attributes('src')!).searchParams.get('apiKey')).toBe('sk-new-canvas')
    })

    it('rotates the canvas key', async () => {
      getVerificationFactors.mockResolvedValue(factors)
      buildVerification.mockReturnValue(verification ?? null)
      ensureInfiniteCanvasApiKey
        .mockRejectedValueOnce(await setupError('rotation-required', 'x', ROTATION_TARGET))
        .mockResolvedValueOnce({ apiKey: 'sk-rotated', created: false, rotated: true, groupIds: [2], keyId: 7, truncated: false })
      const wrapper = mountView()
      await flushPromises()

      expect(wrapper.find('[data-testid="canvas-verification"]').exists()).toBe(verification !== undefined)
      await wrapper.get('[data-testid="canvas-confirm-submit"]').trigger('click')
      await flushPromises()

      expect(ensureInfiniteCanvasApiKey).toHaveBeenLastCalledWith(
        undefined,
        expectedOptions({ createIfMissing: false, allowRotate: true }),
      )
      expect(new URL(wrapper.get('iframe').attributes('src')!).searchParams.get('apiKey')).toBe('sk-rotated')
      expect(showSuccess).toHaveBeenCalledWith('infiniteCanvas.keyRotated')
    })

    it('never rotates or creates just by entering the page', async () => {
      getVerificationFactors.mockResolvedValue(factors)
      ensureInfiniteCanvasApiKey.mockRejectedValue(await setupError('rotation-required', 'x', ROTATION_TARGET))
      const wrapper = mountView()
      await flushPromises()

      expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledTimes(1)
      expect(ensureInfiniteCanvasApiKey).toHaveBeenCalledWith(undefined, { createIfMissing: false, allowRotate: false })
      expect(buildVerification).not.toHaveBeenCalled()
      expect(wrapper.find('iframe').exists()).toBe(false)
    })
  })
})
