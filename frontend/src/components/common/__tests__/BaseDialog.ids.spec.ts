import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createSSRApp, defineComponent, h, nextTick } from 'vue'
import { renderToString } from 'vue/server-renderer'
import BaseDialog from '../BaseDialog.vue'
import ProfilePasskeyCard from '@/components/user/profile/ProfilePasskeyCard.vue'
import KeyUsageView from '@/views/KeyUsageView.vue'
import SetupWizardView from '@/views/setup/SetupWizardView.vue'

// Keep the related SSR regressions in this existing critical CI entry point:
// CI runs an explicit allowlist rather than discovering every new spec.
const { isSupported, list, testDatabase, testRedis, install } = vi.hoisted(() => ({
  isSupported: vi.fn(() => true),
  list: vi.fn(async () => []),
  testDatabase: vi.fn(async () => {}),
  testRedis: vi.fn(async () => {}),
  install: vi.fn(async () => { throw new Error('Stop before restart polling') })
}))
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } })
}))
vi.mock('@/api', () => ({ passkeyAPI: { isSupported, list } }))
vi.mock('@/api/setup', () => ({ testDatabase, testRedis, install }))
vi.mock('@/api/client', () => ({ buildGatewayUrl: (path: string) => path }))
vi.mock('@/stores', () => ({
  useAppStore: () => ({ publicSettingsLoaded: true, siteName: 'Test site', showError: vi.fn() })
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('@/components/common/LocaleSwitcher.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/common/Select.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/common/Toggle.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/icons/Icon.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/icons', () => ({ Icon: { render: () => null } }))

const RouterLinkStub = defineComponent({
  setup(_props, { slots }) { return () => h('a', null, slots.default?.()) }
})

enableAutoUnmount(afterEach)

function openDialog(title: string) {
  return mount(BaseDialog, { props: { show: true, title }, global: { stubs: { Icon: true } } })
}

describe('dialog accessible titles', () => {
  it('renders initial show=true on the server without browser access or timers', async () => {
    const timer = vi.spyOn(globalThis, 'setTimeout')
    vi.stubGlobal('window', undefined)
    vi.stubGlobal('document', undefined)
    try {
      const html = await renderToString(createSSRApp(BaseDialog, { show: true, title: 'SSR dialog' }))
      expect(html).not.toContain('modal-overlay')
      expect(timer).not.toHaveBeenCalled()
    } finally {
      vi.unstubAllGlobals()
      timer.mockRestore()
    }
  })

  it('activates an initially open dialog after mount and restores focus on close', async () => {
    const trigger = document.createElement('button')
    document.body.append(trigger)
    trigger.focus()
    const wrapper = mount(BaseDialog, {
      props: { show: true, title: 'Initially open', showCloseButton: false },
      slots: { default: '<input aria-label="Dialog field" />' }
    })
    try {
      await flushPromises()
      expect(document.body.classList.contains('modal-open')).toBe(true)
      expect(document.activeElement?.getAttribute('aria-label')).toBe('Dialog field')
      await wrapper.setProps({ show: false })
      await flushPromises()
      expect(document.body.classList.contains('modal-open')).toBe(false)
      expect(document.activeElement).toBe(trigger)
    } finally {
      wrapper.unmount()
      trigger.remove()
    }
  })

  it('associates simultaneous dialogs with their own title', async () => {
    openDialog('Edit account')
    openDialog('Confirm deletion')
    await nextTick()
    const dialogs = Array.from(document.body.querySelectorAll('[role="dialog"]'))
    expect(dialogs).toHaveLength(2)
    const titles = dialogs.map(dialog => document.getElementById(dialog.getAttribute('aria-labelledby')!)?.textContent)
    expect(titles).toEqual(['Edit account', 'Confirm deletion'])
  })

  it('keeps the title association stable when a dialog is reopened', async () => {
    const wrapper = openDialog('Details')
    await nextTick()
    const id = document.body.querySelector('[role="dialog"]')!.getAttribute('aria-labelledby')!
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, title: 'Updated details' })
    expect(document.body.querySelector('[role="dialog"]')!.getAttribute('aria-labelledby')).toBe(id)
    expect(document.getElementById(id)?.textContent).toBe('Updated details')
  })
})

describe('SSR page setup and hydration guards', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    vi.clearAllMocks()
    document.documentElement.classList.remove('dark')
    localStorage.clear()
  })

  it.each([
    ['profile passkeys', ProfilePasskeyCard, { enabled: true }],
    ['key usage', KeyUsageView, {}],
    ['setup wizard', SetupWizardView, {}]
  ] as const)('renders %s with no browser globals', async (_name, component, props) => {
    vi.stubGlobal('window', undefined)
    vi.stubGlobal('document', undefined)
    vi.stubGlobal('navigator', undefined)
    const timer = vi.spyOn(globalThis, 'setInterval')
    try {
      const app = createSSRApp(component, props)
      app.component('RouterLink', RouterLinkStub)
      expect(await renderToString(app)).not.toBe('')
      expect(isSupported).not.toHaveBeenCalled()
      expect(list).not.toHaveBeenCalled()
      expect(testDatabase).not.toHaveBeenCalled()
      expect(testRedis).not.toHaveBeenCalled()
      expect(install).not.toHaveBeenCalled()
      expect(timer).not.toHaveBeenCalled()
    } finally {
      vi.unstubAllGlobals()
      timer.mockRestore()
    }
  })

  it('hydrates passkeys before detecting WebAuthn and loading credentials once', async () => {
    const html = await renderToString(createSSRApp(ProfilePasskeyCard, { enabled: true }))
    const container = document.createElement('div')
    container.innerHTML = html
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const app = createSSRApp(ProfilePasskeyCard, { enabled: true })
    app.mount(container)
    try {
      await flushPromises()
      expect(isSupported).toHaveBeenCalledTimes(1)
      expect(list).toHaveBeenCalledTimes(1)
      expect(container.textContent).toContain('profile.passkey.add')
      expect(container.textContent).not.toContain('profile.passkey.unsupported')
      expect([...warn.mock.calls, ...error.mock.calls].flat().join(' ')).not.toMatch(/hydration/i)
    } finally {
      app.unmount()
    }
  })

  it('hydrates key usage with the same initial theme, then reads the browser theme', async () => {
    const createPage = () => {
      const app = createSSRApp(KeyUsageView)
      app.component('RouterLink', RouterLinkStub)
      return app
    }
    const html = await renderToString(createPage())
    expect(html).toContain('home.switchToDark')
    document.documentElement.classList.add('dark')
    localStorage.setItem('theme', 'dark')
    const container = document.createElement('div')
    container.innerHTML = html
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const app = createPage()
    app.mount(container)
    try {
      await nextTick()
      const toggle = container.querySelector<HTMLButtonElement>('button[title="home.switchToLight"]')
      expect(toggle).not.toBeNull()
      toggle!.click()
      await nextTick()
      expect(localStorage.getItem('theme')).toBe('light')
      expect(document.documentElement.classList.contains('dark')).toBe(false)
      expect([...warn.mock.calls, ...error.mock.calls].flat().join(' ')).not.toMatch(/hydration/i)
    } finally {
      app.unmount()
    }
  })

  it.each([
    [{ port: '8443', protocol: 'https:' }, 8443],
    [{ port: '', protocol: 'https:' }, 443],
    [{ port: '', protocol: 'http:' }, 80]
  ])('resolves the mounted browser port before installation: %j', async (location, expectedPort) => {
    const html = await renderToString(createSSRApp(SetupWizardView))
    const container = document.createElement('div')
    container.innerHTML = html
    const browserWindow = window
    vi.stubGlobal('window', new Proxy(browserWindow, {
      get(target, key) { return key === 'location' ? location : Reflect.get(target, key, target) }
    }))
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const app = createSSRApp(SetupWizardView)
    app.mount(container)
    try {
      await nextTick()
      const click = async (text: string) => {
        const button = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes(text))
        expect(button).toBeDefined()
        expect(button!.disabled).toBe(false)
        button!.click()
        await flushPromises()
      }
      await click('setup.status.testConnection')
      await click('common.next')
      await click('setup.status.testConnection')
      await click('common.next')
      const fields = container.querySelectorAll<HTMLInputElement>('input')
      for (const [index, value] of ['admin@example.com', 'test-only-password', 'test-only-password'].entries()) {
        fields[index].value = value
        fields[index].dispatchEvent(new Event('input', { bubbles: true }))
      }
      await nextTick()
      await click('common.next')
      await click('setup.status.completeInstallation')
      expect(install).toHaveBeenCalledTimes(1)
      expect(install).toHaveBeenCalledWith(expect.objectContaining({
        server: { host: '0.0.0.0', port: expectedPort, mode: 'release' }
      }))
      expect([...warn.mock.calls, ...error.mock.calls].flat().join(' ')).not.toMatch(/hydration/i)
    } finally {
      app.unmount()
    }
  })
})
