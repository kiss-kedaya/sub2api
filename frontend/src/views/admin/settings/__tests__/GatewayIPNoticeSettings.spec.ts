import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import GatewayIPNoticeSettings from '../GatewayIPNoticeSettings.vue'

const { getPolicy, updatePolicy, showSuccess } = vi.hoisted(() => ({ getPolicy: vi.fn(), updatePolicy: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/gatewayIpNotice', () => ({ getGatewayIPNotice: getPolicy, updateGatewayIPNotice: updatePolicy }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: (action: () => Promise<unknown>) => action() }), isStepUpCancelled: () => false }))
vi.mock('vue-i18n', async (original) => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key }) }))

const policy = { enabled: true, ips: ['203.0.113.1'], message: 'operator notice', rpm: 100 }
function render() { return mount(GatewayIPNoticeSettings, { global: { stubs: { TotpStepUpDialog: true } } }) }

describe('GatewayIPNoticeSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getPolicy.mockResolvedValue({ ...policy, ips: [...policy.ips] })
    updatePolicy.mockImplementation(async (value) => value)
  })

  it('saves only IP settings with normalized addresses and preserves the exact notice', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#gateway-ip-notice-ips').setValue('203.0.113.1\n203.0.113.2，203.0.113.1')
    await wrapper.get('#gateway-ip-notice-message').setValue('notice https://example.test — check this credential')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(updatePolicy).toHaveBeenCalledWith({ enabled: true, ips: ['203.0.113.1', '203.0.113.2'], message: 'notice https://example.test — check this credential', rpm: 100 })
    expect(showSuccess).toHaveBeenCalledOnce()
  })

  it('cannot overwrite existing settings after a failed load', async () => {
    getPolicy.mockRejectedValueOnce(new Error('load failed'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('load failed')
    expect(updatePolicy).not.toHaveBeenCalled()
    await wrapper.get('button.btn-secondary').trigger('click')
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeUndefined()
  })

  it('does not submit an empty enabled rule or fractional RPM', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#gateway-ip-notice-rpm').setValue(0.5)
    await wrapper.get('button.btn-primary').trigger('click')
    expect(updatePolicy).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
  })
})
