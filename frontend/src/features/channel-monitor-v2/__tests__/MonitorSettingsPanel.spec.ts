import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia } from 'pinia'
import UCheckbox from '@nuxt/ui/components/Checkbox.vue'
import MonitorSettingsPanel from '../MonitorSettingsPanel.vue'
import * as monitor from '@/api/channelMonitorV2'
import { qualityCheckAPI } from '@/api/admin/qualityCheck'
import { adminAPI } from '@/api/admin'

vi.mock('vue-i18n', async original => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key, te: () => true }) }))
vi.mock('@/api/channelMonitorV2', async original => ({ ...await original<typeof import('@/api/channelMonitorV2')>(), getConfig: vi.fn(), updateConfig: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getAllIncludingInactive: vi.fn(async () => [{ id: 7, name: 'Demo group', platform: 'openai' }]) } } }))
vi.mock('@/api/admin/qualityCheck', () => ({ qualityCheckAPI: { list: vi.fn(), setEnabled: vi.fn() } }))
const config = (): monitor.MonitorConfig => ({
  version: 1, enabled: false, refresh_interval_seconds: 60, group_ids: [],
  platforms: [{ platform: 'openai', enabled: false, models: [] }], ignored_error_categories: [],
  health_thresholds: { minimum_sample: 50, warning_error_rate: 0.05, critical_error_rate: 0.2, target_ttft_ms: 3000, warning_ttft_ms: 3000, critical_ttft_ms: 10000, warning_cache_rate: 0.85, critical_cache_rate: 0.6, error_weight: 0.6, ttft_weight: 0.2, cache_weight: 0.2 },
})
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(monitor.getConfig).mockResolvedValue(config())
  vi.mocked(adminAPI.groups.getAllIncludingInactive).mockResolvedValue([{ id: 7, name: 'Demo group', platform: 'openai' }] as Awaited<ReturnType<typeof adminAPI.groups.getAllIncludingInactive>>)
  vi.mocked(monitor.updateConfig).mockImplementation(async value => value)
  vi.mocked(qualityCheckAPI.list).mockResolvedValue({})
  vi.mocked(qualityCheckAPI.setEnabled).mockResolvedValue({ group_id: 7, enabled: true, interval_minutes: 60, last_run_at: null, created_at: '', updated_at: '' })
})
async function render() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })
  await router.push('/admin/channels/monitor')
  const wrapper = mount(MonitorSettingsPanel, { global: { plugins: [createPinia(), router], stubs: { teleport: true } } })
  await flushPromises()
  return wrapper
}
describe('monitor settings presentation', () => {
  it('keeps model normalization and percentage storage unchanged until explicit save', async () => {
    const wrapper = await render()
    expect(wrapper.find('h1').exists()).toBe(false)
    expect(monitor.updateConfig).not.toHaveBeenCalled()
    const models = wrapper.get('input[type="text"]')
    await models.setValue('beta, alpha, beta')
    await models.trigger('change')
    const warning = wrapper.findAll('input[type="number"]').find(input => input.attributes('max') === '100')!
    await warning.setValue('12.5')
    await wrapper.findAll('button').find(button => button.text() === 'channelMonitorV2.settings.save')!.trigger('click')
    await flushPromises()
    expect(monitor.updateConfig).toHaveBeenCalledWith(expect.objectContaining({
      platforms: [{ platform: 'openai', enabled: false, models: ['alpha', 'beta'] }],
      health_thresholds: expect.objectContaining({ warning_error_rate: 0.125 }),
    }))
    wrapper.unmount()
  })
  it('keeps group selection local while quality changes still save independently', async () => {
    const wrapper = await render()
    wrapper.findAllComponents(UCheckbox).find(box => box.props('label') === 'Demo group')!.vm.$emit('update:modelValue', true)
    await flushPromises()
    expect(qualityCheckAPI.setEnabled).not.toHaveBeenCalled()
    await wrapper.get('[role="switch"][aria-label="Demo group"]').trigger('click')
    await flushPromises()
    expect(qualityCheckAPI.setEnabled).toHaveBeenCalledWith(7, true)
    expect(monitor.updateConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
