import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createSSRApp, nextTick } from 'vue'
import { renderToString } from 'vue/server-renderer'
import ProfilePasskeyCard from '../ProfilePasskeyCard.vue'

const { isSupported, list, showError } = vi.hoisted(() => ({
  isSupported: vi.fn(), list: vi.fn(), showError: vi.fn()
}))

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api', () => ({ passkeyAPI: { isSupported, list } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError }) }))
vi.mock('@/components/icons', () => ({ Icon: { render: () => null } }))

enableAutoUnmount(afterEach)

describe('ProfilePasskeyCard SSR capability detection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    isSupported.mockReturnValue(true)
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
