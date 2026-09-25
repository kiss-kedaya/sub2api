import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CustomUsageConfigModal from '../CustomUsageConfigModal.vue'
import { createUsageDraft } from '@/utils/customUsage'
import type { AccountListItem } from '@/types'
const api = vi.hoisted(() => ({ getConfig: vi.fn(), saveConfig: vi.fn(), queryUsage: vi.fn() }))
vi.mock('@/api/admin/customUsage', () => api)
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }) }))
const account = { id: 7, name: 'Custom upstream', type: 'apikey', credentials: { base_url: 'https://example.test' } } as AccountListItem
let wrapper: VueWrapper
const setup = async () => {
  wrapper = mount(CustomUsageConfigModal, { props: { show: true, account }, global: { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' } } } })
  await flushPromises()
  return wrapper
}
const testButton = () => wrapper.findAll('button').find(button => button.text() === 'admin.accounts.customUsage.test')!
describe('CustomUsageConfigModal', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.getConfig.mockResolvedValue({ ...createUsageDraft('https://example.test'), enabled: true, configured: true, has_api_key: true, has_access_token: true })
    api.saveConfig.mockResolvedValue({})
    api.queryUsage.mockResolvedValue({ enabled: true, configured: true, remaining: 12.5, unit: 'USD' })
  })
  afterEach(() => wrapper?.unmount())
  it('uses blank password inputs and saved placeholders, then tests without saving', async () => {
    await setup()
    expect(wrapper.get<HTMLInputElement>('#custom-usage-api_key').element.value).toBe('')
    expect(wrapper.get('#custom-usage-api_key').attributes('placeholder')).toBe('admin.accounts.customUsage.secretSaved')
    await wrapper.get('#custom-usage-api_key').setValue('draft-secret')
    await testButton().trigger('click'); await flushPromises()
    expect(api.queryUsage).toHaveBeenCalledWith(7, expect.objectContaining({ force: true, config: expect.objectContaining({ api_key: 'draft-secret' }) }), expect.any(AbortSignal))
    expect(api.saveConfig).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="custom-usage-test-result"]').text()).toContain('12.5')
    await wrapper.get('#custom-usage-timeout').setValue('12')
    expect(wrapper.find('[data-testid="custom-usage-test-result"]').exists()).toBe(false)
  })
  it('rejects JavaScript instead of pretending to run a JS extractor', async () => {
    await setup(); await wrapper.get('#custom-usage-extractor').setValue('return response.balance')
    await testButton().trigger('click')
    expect(api.queryUsage).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.accounts.customUsage.errors.invalidJson')
  })
  it('keeps blank secrets, supports explicit clearing and emits only nonsecret schedule data', async () => {
    await setup()
    await wrapper.findAll('input[type="checkbox"]')[0].setValue(true)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    const body = api.saveConfig.mock.calls[0][1]
    expect(body.clear_api_key).toBe(true)
    expect(body).not.toHaveProperty('api_key')
    expect(body).not.toHaveProperty('access_token')
    expect(body).not.toHaveProperty('has_api_key')
    expect(wrapper.emitted('saved')).toEqual([[7, { enabled: true, interval_minutes: 0 }]])
    expect(api.queryUsage).not.toHaveBeenCalled()
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
  it('selects NewAPI template and keeps secret values only in the draft', async () => {
    await setup(); await wrapper.get('[data-template="newapi"]').trigger('click')
    await wrapper.get('#custom-usage-access_token').setValue('private-token')
    expect(wrapper.get<HTMLTextAreaElement>('#custom-usage-headers').element.value).toContain('New-Api-User')
    expect(wrapper.get<HTMLTextAreaElement>('#custom-usage-extractor').element.value).toContain('500000')
    await wrapper.setProps({ show: false }); await flushPromises()
    expect(wrapper.find('#custom-usage-access_token').exists()).toBe(false)
  })
  it('aborts tests on close and never renders raw errors containing secrets', async () => {
    await setup(); api.queryUsage.mockRejectedValue({ message: 'Authorization: private-token' })
    await testButton().trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('admin.accounts.customUsage.queryFailed')
    expect(wrapper.text()).not.toContain('private-token')
    api.queryUsage.mockReturnValue(new Promise(() => {}))
    await testButton().trigger('click')
    const signal = api.queryUsage.mock.calls[1][2] as AbortSignal
    await wrapper.setProps({ show: false })
    expect(signal.aborted).toBe(true)
  })
  it('fills a first-time backend empty config from the account URL and does not auto-test', async () => {
    api.getConfig.mockResolvedValue({ enabled: false, configured: false, template: 'custom', base_url: '', timeout_seconds: 10, interval_minutes: 0, request: { method: 'GET', url: '', headers: {} }, extractor: {} })
    await setup()
    expect(wrapper.get<HTMLInputElement>('#custom-usage-base').element.value).toBe('https://example.test')
    expect(wrapper.get<HTMLInputElement>('#custom-usage-timeout').element.value).toBe('10')
    expect(wrapper.get<HTMLInputElement>('#custom-usage-interval').element.value).toBe('0')
    expect(api.queryUsage).not.toHaveBeenCalled()
  })
  it('does not overwrite an existing config after a load failure', async () => {
    api.getConfig.mockRejectedValue(new Error('private error'))
    await setup()
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.accounts.customUsage.loadFailed')
    expect(wrapper.text()).not.toContain('private error')
    expect(api.saveConfig).not.toHaveBeenCalled()
  })
})
