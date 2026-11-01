import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
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
})
