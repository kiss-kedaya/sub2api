import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account, TempUnschedulableStatus } from '@/types'
import TempUnschedStatusModal from '../TempUnschedStatusModal.vue'

const { getStatus, showError } = vi.hoisted(() => ({ getStatus: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getTempUnschedulableStatus: getStatus } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }),
}))
enableAutoUnmount(afterEach)
const account = (id: number) => ({ id, name: 'fixture-' + id } as Account)
const status = (message: string): TempUnschedulableStatus => ({
  active: true,
  state: { until_unix: Math.floor(Date.now() / 1000) + 3600,
    triggered_at_unix: Math.floor(Date.now() / 1000), status_code: 502,
    matched_keyword: 'fixture', rule_index: 0, error_message: message },
})
const deferred = () => {
  let resolve!: (value: TempUnschedulableStatus) => void
  let reject!: (error: Error) => void
  const promise = new Promise<TempUnschedulableStatus>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}
const open = (show = true, selected: Account | null = account(1)) => mount(TempUnschedStatusModal, {
  props: { show, account: selected },
  global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' } } },
})
beforeEach(() => { getStatus.mockReset().mockResolvedValue(status('initial-status')); showError.mockReset() })

describe('TempUnschedStatusModal conditional mounting', () => {
  it('loads the initial account when first mounted visible', async () => {
    const wrapper = open()
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(1)
    expect(getStatus).toHaveBeenCalledWith(1)
    expect(wrapper.text()).toContain('initial-status')
  })

  it('waits while hidden or missing an account', async () => {
    const wrapper = open(false, null)
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(getStatus).not.toHaveBeenCalled()
    await wrapper.setProps({ account: account(2) })
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(1)
    expect(getStatus).toHaveBeenCalledWith(2)
  })

  it('does not replace the new account with a stale status', async () => {
    const pending = deferred()
    getStatus.mockReturnValueOnce(pending.promise).mockResolvedValueOnce(status('current-account'))
    const wrapper = open()
    await wrapper.setProps({ account: account(2) })
    await flushPromises()
    pending.resolve(status('old-account'))
    await flushPromises()
    expect(getStatus).toHaveBeenLastCalledWith(2)
    expect(wrapper.text()).toContain('current-account')
    expect(wrapper.text()).not.toContain('old-account')
  })

  it('clears the previous status while loading another account', async () => {
    const wrapper = open()
    await flushPromises()
    const pending = deferred()
    getStatus.mockReturnValueOnce(pending.promise)
    await wrapper.setProps({ account: account(2) })
    expect(wrapper.text()).not.toContain('initial-status')
    expect(wrapper.find('.animate-spin').exists()).toBe(true)
    pending.resolve(status('new-status'))
    await flushPromises()
    expect(wrapper.text()).toContain('new-status')
  })

  it('ignores a late success after closing and reloads on reopening', async () => {
    const pending = deferred()
    getStatus.mockReturnValueOnce(pending.promise)
    const wrapper = open()
    await wrapper.setProps({ show: false })
    pending.resolve(status('closed-status'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('closed-status')
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(getStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('initial-status')
  })

  it.each(['close', 'unmount'] as const)('suppresses stale errors after %s', async (action) => {
    const pending = deferred()
    getStatus.mockReturnValueOnce(pending.promise)
    const wrapper = open()
    if (action === 'close') await wrapper.setProps({ show: false })
    else wrapper.unmount()
    pending.reject(new Error('dismissed-request'))
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
  })

  it('clears the displayed status when the account is removed', async () => {
    const wrapper = open()
    await flushPromises()
    await wrapper.setProps({ account: null })
    expect(wrapper.text()).not.toContain('initial-status')
    expect(wrapper.find('.animate-spin').exists()).toBe(false)
  })

  it('shows current load errors and stops loading', async () => {
    getStatus.mockRejectedValueOnce(new Error('visible-error'))
    const wrapper = open()
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('visible-error')
    expect(wrapper.find('.animate-spin').exists()).toBe(false)
  })
})
