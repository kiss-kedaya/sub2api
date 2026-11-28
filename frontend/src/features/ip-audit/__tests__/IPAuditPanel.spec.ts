import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import USelect from '@nuxt/ui/components/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import IPAuditPanel from '../IPAuditPanel.vue'

const api = vi.hoisted(() => ({ listIPAudit: vi.fn(), releaseIPAudit: vi.fn() }))
vi.mock('../api', () => api)

const entry = { id: 'anonymous_api-1', source: 'anonymous_api', network: '192.0.2.88/32', scope: 'invalid_credentials', reason: 'anonymous_api_unknown', state: 'active', effective: 'block', manual: false, can_release: true, started_at: 1, expires_at: 2, count: 24, threshold: 20, version: 'v1' }
const sources = [{ id: 'anonymous_api', scope: 'invalid_credentials', storage: 'redis', enumeration: 'indexed', can_release: true }]
const i18n = () => createI18n({ legacy: false, locale: 'zh', messages: { zh: { common: { search: '搜索', refresh: '刷新', loading: '加载中', cancel: '取消', actions: '操作' } } }, missingWarn: false, fallbackWarn: false })

beforeEach(() => {
  vi.resetAllMocks()
  localStorage.clear()
  api.listIPAudit.mockResolvedValue({ items: [entry], sources, has_more: false, next_cursor: 0, unrestricted: false })
  api.releaseIPAudit.mockResolvedValue({ released: true, readback: 'released', remaining: [], unknown_sources: [], unrestricted: false })
})

describe('IPAuditPanel', () => {
  it('maps "all states" to no state filter and applies a chosen state', async () => {
    const wrapper = mount(IPAuditPanel, { global: { plugins: [i18n()] } })
    await flushPromises()
    expect(api.listIPAudit.mock.calls[0][0]).toMatchObject({ source: 'anonymous_api', cursor: 0 })
    expect(api.listIPAudit.mock.calls[0][0].state).toBeUndefined()
    const state = wrapper.findAllComponents(USelect).find(select => select.find('[data-testid="audit-state"]').exists())!
    state.vm.$emit('update:modelValue', 'active')
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.listIPAudit).toHaveBeenLastCalledWith(expect.objectContaining({ state: 'active' }), expect.anything())
    state.vm.$emit('update:modelValue', '__all__')
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.listIPAudit.mock.lastCall![0].state).toBeUndefined()
    wrapper.unmount()
  })

  it('releases a source through the confirmation dialog', async () => {
    const wrapper = mount(IPAuditPanel, { attachTo: document.body, global: { plugins: [i18n()] } })
    await flushPromises()
    await wrapper.get('[data-testid="release-source"]').trigger('click')
    await flushPromises()
    expect(document.querySelector('[data-testid="release-dialog"]')).not.toBeNull()
    ;(document.querySelector('[data-testid="release-confirm"]') as HTMLButtonElement).click()
    await flushPromises()
    expect(api.releaseIPAudit).toHaveBeenCalledWith(expect.objectContaining({ id: 'anonymous_api-1' }), 'operator_review')
    expect(wrapper.get('[data-testid="release-outcome"]').attributes('role')).toBe('status')
    // The dialog closes (its leave transition may still be running in jsdom).
    expect(wrapper.findComponent(BaseDialog).props('show')).toBe(false)
    wrapper.unmount()
  })

  it('uses wrap-anywhere on IP/account subject identifiers instead of break-all', async () => {
    const wrapper = mount(IPAuditPanel, { global: { plugins: [i18n()] } })
    await flushPromises()
    const html = wrapper.html()
    expect(html).toContain('wrap-anywhere')
    expect(html).not.toContain('break-all')
    wrapper.unmount()
  })

  it('handles responses missing items or sources without crashing', async () => {
    api.listIPAudit.mockResolvedValueOnce({ items: undefined, sources: undefined, has_more: false, next_cursor: 0 } as never)
    const wrapper = mount(IPAuditPanel, { global: { plugins: [i18n()] } })
    await flushPromises()
    expect(wrapper.find('[data-testid="ip-audit-panel"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('unmounts collapsed source inventory content until opened and keeps table headers nowrap', async () => {
    api.listIPAudit.mockResolvedValueOnce({
      items: [],
      sources: [{ ...sources[0], manage_url: '/admin/ip-risk' }],
      has_more: false,
      next_cursor: 0,
      unrestricted: false,
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }],
    })
    await router.push('/admin/ip-audit')
    await router.isReady()
    const wrapper = mount(IPAuditPanel, { global: { plugins: [i18n(), router] } })
    await flushPromises()
    const inventory = wrapper.get('[data-testid="ip-source-inventory"]')
    expect(inventory.find('ul').exists()).toBe(false)
    await inventory.get('button').trigger('click')
    await flushPromises()
    expect(inventory.find('ul').exists()).toBe(true)
    expect(wrapper.findAll('thead th').every(th => th.classes().includes('whitespace-nowrap'))).toBe(true)
    wrapper.unmount()
  })
})
