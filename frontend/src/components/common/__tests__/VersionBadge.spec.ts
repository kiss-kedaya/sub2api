import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import VersionBadge from '../VersionBadge.vue'

const { auth, app, performUpdate, restartService } = vi.hoisted(() => ({
  auth: { isAdmin: false, isAdminObserver: false },
  app: {
    versionLoading: false,
    currentVersion: '2.0.0',
    latestVersion: '',
    hasUpdate: false,
    updateStrategy: 'disabled',
    buildType: 'release',
    releaseInfo: null,
    fetchVersion: vi.fn(),
    clearVersionCache: vi.fn(),
  },
  performUpdate: vi.fn(),
  restartService: vi.fn(),
}))

vi.mock('@/stores', () => ({ useAuthStore: () => auth, useAppStore: () => app }))
vi.mock('@/api/admin/system', () => ({
  performUpdate,
  restartService,
  getRollbackVersions: vi.fn(),
  rollback: vi.fn(),
}))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() }),
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks()
  auth.isAdmin = false
  auth.isAdminObserver = false
})

describe('version controls follow the owner permission', () => {
  it.each([
    { label: 'observer administrator', admin: true, observer: true },
    { label: 'regular user', admin: false, observer: false },
  ])('shows only the public version to a $label', async ({ admin, observer }) => {
    auth.isAdmin = admin
    auth.isAdminObserver = observer
    const wrapper = mount(VersionBadge, { props: { version: '2.1.0' } })
    await flushPromises()

    expect(wrapper.text()).toBe('v2.1.0')
    expect(wrapper.find('button').exists()).toBe(false)
    expect(app.fetchVersion).not.toHaveBeenCalled()

    // A stale callback from an earlier owner session cannot start a mutation.
    await (wrapper.vm as unknown as { handleUpdate: () => Promise<void> }).handleUpdate()
    await (wrapper.vm as unknown as { handleRestart: () => Promise<void> }).handleRestart()
    expect(performUpdate).not.toHaveBeenCalled()
    expect(restartService).not.toHaveBeenCalled()
  })

  it('keeps the version check and refresh controls for the owner', async () => {
    auth.isAdmin = true
    const wrapper = mount(VersionBadge, { props: { version: '2.1.0' } })
    await flushPromises()
    expect(app.fetchVersion).toHaveBeenCalledWith(false)

    await wrapper.get('button').trigger('click')
    await flushPromises()
    // The details open in a popover teleported to <body>.
    document.body.querySelector<HTMLButtonElement>('[title="version.refresh"]')!.click()
    expect(app.fetchVersion).toHaveBeenCalledWith(true)
    wrapper.unmount()
    document.body.innerHTML = ''
  })
})
