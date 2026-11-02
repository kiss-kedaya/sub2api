import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ProxySelector from '../ProxySelector.vue'
import type { Proxy } from '@/types'
import { bodyFindAll, bodyGet } from './overlay-helpers'

const testProxy = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin', () => ({ adminAPI: { proxies: { testProxy } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
// The menu is teleported to <body>: unmount first (registered last), then clear what is left.
afterEach(() => { document.body.innerHTML = '' })
enableAutoUnmount(afterEach)
beforeEach(() => { vi.clearAllMocks() })

const testButtons = () => bodyFindAll('[data-test="proxy-test"]')
const batchButton = () => bodyGet('[data-test="proxy-batch-test"]')
const menuText = () => document.body.textContent ?? ''

async function openSelector() {
  const wrapper = mount(ProxySelector, {
    attachTo: document.body,
    props: { modelValue: null, proxies: [1, 2].map(id => ({
      id, name: `Proxy ${id}`, host: 'localhost', port: 8080, protocol: 'http'
    } as Proxy)) },
    global: { stubs: { Icon: true } }
  })
  await wrapper.get('[data-test="proxy-selector"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('proxy connection tests', () => {
  it('does not restart an individual test when a batch is started', async () => {
    let finish!: (result: object) => void
    testProxy.mockImplementation((id: number) => id === 1
      ? new Promise(resolve => { finish = resolve })
      : Promise.resolve({ success: true, country: 'GB' }))
    const wrapper = await openSelector()
    await testButtons()[0].trigger('click')
    await batchButton().trigger('click')
    await flushPromises()
    expect(testProxy.mock.calls.map(([id]) => id)).toEqual([1, 2])
    expect(testButtons()[0].attributes('disabled')).toBeDefined()
    finish({ success: true, country: 'US' })
    await flushPromises()
    expect(menuText()).toContain('US')
    expect(testButtons()[0].attributes('disabled')).toBeUndefined()
    // Testing a proxy never selects it.
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('shows per-proxy outcomes and allows another batch after a failure', async () => {
    testProxy.mockImplementation((id: number) => id === 1
      ? Promise.reject(new Error('offline'))
      : Promise.resolve({ success: true, country: 'GB' }))
    await openSelector()
    await batchButton().trigger('click')
    await flushPromises()
    expect(testProxy).toHaveBeenCalledTimes(2)
    expect(menuText()).toContain('admin.proxies.testFailed')
    expect(menuText()).toContain('GB')
    expect(batchButton().attributes('disabled')).toBeUndefined()
    await batchButton().trigger('click')
    await flushPromises()
    expect(testProxy).toHaveBeenCalledTimes(4)
  })

  it('selects a proxy and maps "no proxy" back to null', async () => {
    const wrapper = await openSelector()
    expect(wrapper.get('[data-test="proxy-selector"]').text()).toContain('admin.accounts.noProxy')
    await bodyFindAll('[role="option"]').find(option => option.text().includes('Proxy 2'))!.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')).toEqual([[2]])

    await wrapper.setProps({ modelValue: 2 })
    expect(wrapper.get('[data-test="proxy-selector"]').text()).toContain('Proxy 2 (http://localhost:8080)')
    await wrapper.get('[data-test="proxy-selector"]').trigger('click')
    await flushPromises()
    await bodyFindAll('[role="option"]').find(option => option.text().includes('admin.accounts.noProxy'))!.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')?.[1]).toEqual([null])
  })
})
