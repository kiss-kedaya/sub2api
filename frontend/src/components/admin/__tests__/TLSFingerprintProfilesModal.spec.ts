import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TLSFingerprintProfilesModal from '../TLSFingerprintProfilesModal.vue'

const { list, showError } = vi.hoisted(() => ({ list: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { tlsFingerprintProfiles: { list } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }),
}))
enableAutoUnmount(afterEach)
afterEach(() => vi.restoreAllMocks())

const fixture = (name: string) => ({ id: 1, name, enable_grease: false, cipher_suites: [], curves: [], point_formats: [], alpn_protocols: [], signature_algorithms: [], supported_versions: [], key_share_groups: [], psk_modes: [], extensions: [] })
type Rows = ReturnType<typeof fixture>[]
const deferred = () => {
  let resolve!: (value: Rows) => void
  let reject!: (error: Error) => void
  const promise = new Promise<Rows>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}
const open = (show = true) => mount(TLSFingerprintProfilesModal, {
  props: { show },
  global: { stubs: {
    BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
    ConfirmDialog: true, Icon: { template: '<span />' },
  } },
})
beforeEach(() => {
  list.mockReset().mockResolvedValue([fixture('initial-row')])
  showError.mockReset()
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

describe('TLSFingerprintProfilesModal conditional mounting', () => {
  it('loads existing rows when first mounted visible', async () => {
    const wrapper = open()
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('initial-row')
  })

  it('waits while hidden and loads when opened', async () => {
    const wrapper = open(false)
    await flushPromises()
    expect(list).not.toHaveBeenCalled()
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('initial-row')
  })

  it('ignores a successful load that finishes after closing', async () => {
    const pending = deferred()
    list.mockReturnValueOnce(pending.promise)
    const wrapper = open()
    await wrapper.setProps({ show: false })
    pending.resolve([fixture('closed-row')])
    await flushPromises()
    expect(wrapper.text()).not.toContain('closed-row')
    expect(showError).not.toHaveBeenCalled()
  })

  it.each(['close', 'unmount'] as const)('suppresses a late load error after %s', async (action) => {
    const pending = deferred()
    list.mockReturnValueOnce(pending.promise)
    const wrapper = open()
    if (action === 'close') await wrapper.setProps({ show: false })
    else wrapper.unmount()
    pending.reject(new Error('dismissed-request'))
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
    expect(console.error).not.toHaveBeenCalled()
  })

  it('keeps the reopened result when the old request finishes later', async () => {
    const pending = deferred()
    list.mockReturnValueOnce(pending.promise).mockResolvedValueOnce([fixture('reopened-row')])
    const wrapper = open()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    pending.resolve([fixture('stale-row')])
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('reopened-row')
    expect(wrapper.text()).not.toContain('stale-row')
  })

  it('does not clear the new loading state when the old request finishes', async () => {
    const old = deferred()
    const current = deferred()
    list.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = open()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    old.resolve([fixture('stale-row')])
    await flushPromises()
    expect(wrapper.find('.animate-spin').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('stale-row')
    current.resolve([fixture('current-row')])
    await flushPromises()
    expect(wrapper.find('.animate-spin').exists()).toBe(false)
    expect(wrapper.text()).toContain('current-row')
  })

  it('reports a load failure while still visible', async () => {
    list.mockRejectedValueOnce(new Error('visible-request'))
    const wrapper = open()
    await flushPromises()
    expect(showError).toHaveBeenCalledTimes(1)
    expect(wrapper.find('.animate-spin').exists()).toBe(false)
  })
})
