import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import UserApiKeysModal from '../UserApiKeysModal.vue'
import type { AdminUser } from '@/types'

const { getKeys } = vi.hoisted(() => ({ getKeys: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: {
  users: { getUserApiKeys: getKeys }, groups: { getAll: vi.fn().mockResolvedValue([]) },
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('@/utils/format', () => ({ formatDateTime: (value: string) => value }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
beforeEach(() => {
  localStorage.clear()
  delete window.__APP_CONFIG__
  getKeys.mockReset()
  vi.spyOn(console, 'error').mockImplementation(() => {})
})
afterEach(() => vi.restoreAllMocks())
function deferred() {
  let resolve!: (value: unknown) => void
  let reject!: (error: Error) => void
  const promise = new Promise((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const user = (id: number) => ({ id, email: `user${id}@example.com`, username: `user${id}` }) as AdminUser
const keys = (id: number, name: string, page = 1, total = 1, pageSize = 20) => ({
  items: [{ id, name, key: 'sk-example-key-value-for-tests', status: 'active', created_at: '2026-09-20', group_id: null }],
  total, page, page_size: pageSize, pages: Math.ceil(total / pageSize),
})
async function open() {
  const wrapper = mount(UserApiKeysModal, {
    props: { show: false, user: user(1) },
    global: { stubs: {
      BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
      GroupBadge: true, GroupOptionItem: true,
      Select: {
        props: ['modelValue', 'options'],
        template: `<select :value="modelValue" @change="$emit('update:model-value', $event.target.value)"><option v-for="option in options" :value="option.value">{{ option.label }}</option></select>`,
      },
    } },
  })
  await wrapper.setProps({ show: true })
  return wrapper
}
async function switchUser(wrapper: Awaited<ReturnType<typeof open>>) {
  await wrapper.setProps({ show: false })
  await wrapper.setProps({ show: true, user: user(2) })
}

describe('user API key loading', () => {
  it('does not display the previous user keys when the next load fails', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-user-key')).mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = await open(); await flushPromises()
    expect(wrapper.text()).toContain('first-user-key')
    await switchUser(wrapper); await flushPromises()
    expect(wrapper.text()).toContain('user2@example.com')
    expect(wrapper.text()).not.toContain('first-user-key')
  })

  it('does not replace current keys with a late previous response', async () => {
    const old = deferred()
    getKeys.mockReturnValueOnce(old.promise).mockResolvedValueOnce(keys(2, 'current-user-key'))
    const wrapper = await open()
    await switchUser(wrapper); await flushPromises()
    old.resolve(keys(1, 'old-user-key')); await flushPromises()
    expect(wrapper.text()).toContain('current-user-key')
    expect(wrapper.text()).not.toContain('old-user-key')
  })

  it('keeps the current request loading when an obsolete request fails', async () => {
    const old = deferred(); const current = deferred()
    getKeys.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = await open()
    await switchUser(wrapper)
    old.reject(new Error('obsolete')); await flushPromises()
    expect(wrapper.find('.animate-spin').exists()).toBe(true)
    current.resolve(keys(2, 'current-user-key')); await flushPromises()
    expect(wrapper.text()).toContain('current-user-key')
    expect(wrapper.find('.animate-spin').exists()).toBe(false)
  })

  it('loads keys when the selected user changes while the dialog is open', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-user-key')).mockResolvedValueOnce(keys(2, 'second-user-key'))
    const wrapper = await open(); await flushPromises()
    await wrapper.setProps({ user: user(2) }); await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(2, 1, 20)
    expect(wrapper.text()).toContain('second-user-key')
    expect(wrapper.text()).not.toContain('first-user-key')
  })
})


describe('user API key pagination', () => {
  it('loads another page only when requested and supports returning to the first page', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-page-key', 1, 21))
      .mockResolvedValueOnce(keys(21, 'second-page-key', 2, 21))
      .mockResolvedValueOnce(keys(1, 'first-page-key', 1, 21))
    const wrapper = await open()
    await flushPromises()
    expect(getKeys).toHaveBeenCalledTimes(1)
    expect(getKeys).toHaveBeenLastCalledWith(1, 1, 20)
    await wrapper.get('button[aria-label="pagination.next"]').trigger('click')
    await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(1, 2, 20)
    expect(wrapper.text()).toContain('second-page-key')
    expect(wrapper.text()).not.toContain('first-page-key')
    expect(wrapper.get('button[aria-label="pagination.next"]').attributes('disabled')).toBeDefined()
    await wrapper.get('button[aria-label="pagination.previous"]').trigger('click')
    await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(1, 1, 20)
    expect(wrapper.text()).toContain('first-page-key')
  })

  it('returns to page one when the page size changes', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-page-key', 1, 61))
      .mockResolvedValueOnce(keys(21, 'second-page-key', 2, 61))
      .mockResolvedValueOnce(keys(1, 'larger-page-key', 1, 61, 50))
    const wrapper = await open()
    await flushPromises()
    await wrapper.get('button[aria-label="pagination.next"]').trigger('click')
    await flushPromises()
    await wrapper.get('select').setValue('50')
    await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(1, 1, 50)
    expect(wrapper.text()).toContain('larger-page-key')
    expect(wrapper.get('button[aria-current="page"]').text()).toBe('1')
  })

  it('discards a previous user page response and starts the next user at page one', async () => {
    const stalePage = deferred()
    getKeys.mockResolvedValueOnce(keys(1, 'first-page-key', 1, 21))
      .mockReturnValueOnce(stalePage.promise)
      .mockResolvedValueOnce(keys(2, 'current-user-key'))
    const wrapper = await open()
    await flushPromises()
    await wrapper.get('button[aria-label="pagination.next"]').trigger('click')
    await wrapper.setProps({ user: user(2) })
    await flushPromises()
    stalePage.resolve(keys(21, 'stale-second-page-key', 2, 21))
    await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(2, 1, 20)
    expect(wrapper.text()).toContain('current-user-key')
    expect(wrapper.text()).not.toContain('stale-second-page-key')
    expect(wrapper.get('button[aria-current="page"]').text()).toBe('1')
  })

  it('uses the server effective page size for subsequent requests', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'server-capped-page', 1, 25, 10))
      .mockResolvedValueOnce(keys(11, 'next-capped-page', 2, 25, 10))
    const wrapper = await open()
    await flushPromises()
    await wrapper.get('button[aria-label="pagination.next"]').trigger('click')
    await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(1, 2, 10)
    expect(wrapper.text()).toContain('next-capped-page')
  })
})
