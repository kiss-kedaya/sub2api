import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { applyDocumentTitle } from '../documentTitle'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useAdminSettingsStore } from '@/stores/adminSettings'

const t = vi.hoisted(() => vi.fn((key: string) => key))
vi.mock('@/i18n', () => ({ i18n: { global: { t } } }))

const route = (meta: Record<string, unknown>, extra: Record<string, unknown> = {}) =>
  ({ name: 'x', params: {}, meta, ...extra }) as never

describe('applyDocumentTitle', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.title = ''
    t.mockImplementation((key: string) => key)
  })

  it('writes the translated title followed by the site name', () => {
    useAppStore().siteName = 'Check'
    t.mockImplementation((key: string) => (key === 'auth.login.title' ? '登录' : key))
    applyDocumentTitle(route({ title: 'Login', titleKey: 'auth.login.title' }))
    expect(document.title).toBe('登录 - Check')
  })

  it('falls back to the static route title while the messages have not arrived, then fixes itself when called again', () => {
    useAppStore().siteName = 'Check'
    // boot: the navigation guard runs while the locale chunk is still downloading, so t() returns the key itself
    applyDocumentTitle(route({ title: 'Login', titleKey: 'auth.login.title' }))
    expect(document.title).toBe('Login - Check')
    // the messages arrive; the boot sequence calls it once more before the first paint
    t.mockImplementation((key: string) => (key === 'auth.login.title' ? '登录' : key))
    applyDocumentTitle(route({ title: 'Login', titleKey: 'auth.login.title' }))
    expect(document.title).toBe('登录 - Check')
  })

  it('uses a custom menu page label for CustomPage routes', () => {
    const app = useAppStore()
    app.siteName = 'Check'
    app.cachedPublicSettings = { custom_menu_items: [{ id: 'p1', label: '帮助中心' }] } as never
    applyDocumentTitle(route({}, { name: 'CustomPage', params: { id: 'p1' } }))
    expect(document.title).toBe('帮助中心 - Check')
  })

  it('also looks at the admin-only custom menu items, but only for an administrator', () => {
    const app = useAppStore()
    app.siteName = 'Check'
    useAdminSettingsStore().customMenuItems = [{ id: 'a1', label: '内部页' }] as never
    const page = route({}, { name: 'CustomPage', params: { id: 'a1' } })

    applyDocumentTitle(page)
    expect(document.title).toBe('Check') // a visitor does not see the admin's item

    const auth = useAuthStore()
    auth.user = { id: 1, role: 'admin' } as never
    applyDocumentTitle(page)
    expect(document.title).toBe('内部页 - Check')
  })

  it('is a no-op when there is no document (server-side)', () => {
    const original = globalThis.document
    // @ts-expect-error simulate a non-browser environment
    delete globalThis.document
    try {
      expect(() => applyDocumentTitle(route({}))).not.toThrow()
    } finally {
      globalThis.document = original
    }
  })
})
