import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import USelect from '@nuxt/ui/components/Select.vue'
import IPRiskSettings from '../IPRiskSettings.vue'

const api = vi.hoisted(() => ({
  get: vi.fn(), save: vi.fn(), events: vi.fn(), bans: vi.fn(), anonymousBans: vi.fn(),
  unbanAnonymous: vi.fn(), unban: vi.fn(), clearEvents: vi.fn(), check: vi.fn()
}))
vi.mock('../api', () => ({ ipRiskAPI: api }))

const threshold = { limit: 10, window_seconds: 60 }
const config = () => ({
  anonymous_api: { enabled: false, threshold: 20, window_seconds: 60, ban_seconds: 600, max_tracked_ips: 10000, reset_on_success: false },
  enabled: true, observe_only: true, whitelist: ['203.0.113.10'], blacklist: [], allowed_countries: [], blocked_countries: [],
  block_unknown_country: false, deny_user_agents: [], block_empty_ua: false, ipv6_prefix: 64, ban_seconds: 3600,
  login_attempts: { ...threshold }, login_failures: { ...threshold }, register_attempts: { ...threshold }, register_failures: { ...threshold },
  verification_attempts: { ...threshold }, event_retention_hours: 72
})
const i18n = () => createI18n({ legacy: false, locale: 'zh', messages: { zh: {} }, missingWarn: false, fallbackWarn: false })

beforeEach(() => {
  vi.resetAllMocks()
  api.get.mockResolvedValue({ config: config(), health: { redis_available: true, emergency_disabled: false, config_error: '' }, current_ip: '203.0.113.10', trusted_proxies_configured: true })
  api.events.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
  api.bans.mockResolvedValue([])
  api.anonymousBans.mockResolvedValue([])
  api.save.mockImplementation(async (value: unknown) => value)
})

describe('IPRiskSettings', () => {
  it('loads when expanded and saves the toggled switch', async () => {
    const wrapper = mount(IPRiskSettings, { props: { expanded: true }, global: { plugins: [i18n()] } })
    await flushPromises()
    expect(api.get).toHaveBeenCalledOnce()
    const enabled = wrapper.get('[data-testid="risk-enabled"]')
    expect(enabled.attributes('role')).toBe('switch')
    expect(enabled.attributes('aria-checked')).toBe('true')
    await enabled.trigger('click')
    expect(enabled.attributes('aria-checked')).toBe('false')
    await wrapper.get('form#ip-risk-form').trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, whitelist: ['203.0.113.10'] }))
    wrapper.unmount()
  })

  it('maps the "all categories" option back to an empty filter', async () => {
    const wrapper = mount(IPRiskSettings, { props: { expanded: true }, global: { plugins: [i18n()] } })
    await flushPromises()
    const category = wrapper.findAllComponents(USelect).find(select => select.find('[data-testid="risk-category"]').exists())!
    const filter = wrapper.get('[data-testid="risk-filter"]')
    category.vm.$emit('update:modelValue', 'login')
    await flushPromises()
    await filter.trigger('click')
    await flushPromises()
    expect(api.events).toHaveBeenLastCalledWith('', '', 'login', 1, 20)
    category.vm.$emit('update:modelValue', '__all__')
    await flushPromises()
    await filter.trigger('click')
    await flushPromises()
    expect(api.events).toHaveBeenLastCalledWith('', '', '', 1, 20)
    wrapper.unmount()
  })

  it('stays collapsed and unloaded until opened when embedded', async () => {
    const wrapper = mount(IPRiskSettings, { global: { plugins: [i18n()] } })
    await flushPromises()
    expect(api.get).not.toHaveBeenCalled()
    const trigger = wrapper.get('button[aria-expanded]')
    expect(trigger.attributes('aria-expanded')).toBe('false')
    await trigger.trigger('click')
    await flushPromises()
    expect(trigger.attributes('aria-expanded')).toBe('true')
    expect(api.get).toHaveBeenCalledOnce()
    wrapper.unmount()
  })
})
