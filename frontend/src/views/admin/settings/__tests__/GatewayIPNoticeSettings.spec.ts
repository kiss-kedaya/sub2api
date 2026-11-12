import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import GatewayIPNoticeSettings from '../GatewayIPNoticeSettings.vue'

const { getPolicy, updatePolicy, showSuccess } = vi.hoisted(() => ({ getPolicy: vi.fn(), updatePolicy: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/gatewayIpNotice', () => ({ getGatewayIPNotice: getPolicy, updateGatewayIPNotice: updatePolicy }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: (action: () => Promise<unknown>) => action() }), isStepUpCancelled: () => false }))
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  // Interpolation params are appended so a test can assert the counters the
  // component actually passes, not just which key it asked for.
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key} ${JSON.stringify(params)}` : key) }),
}))

const policy = {
  enabled: true,
  ips: ['203.0.113.1'],
  message: 'operator notice',
  rpm: 100,
  auto_add_enabled: true,
  sharing_window_minutes: 10,
  sharing_threshold: 3,
  key_disable_enabled: false,
  auto_entries: [],
}
function render() { return mount(GatewayIPNoticeSettings, { global: { stubs: { TotpStepUpDialog: true } } }) }

describe('GatewayIPNoticeSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getPolicy.mockResolvedValue({ ...policy, ips: [...policy.ips], auto_entries: [] })
    updatePolicy.mockImplementation(async (value) => value)
  })

  it('saves only IP settings with normalized addresses and preserves the exact notice', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#gateway-ip-notice-ips').setValue('203.0.113.1\n203.0.113.2，203.0.113.1')
    await wrapper.get('#gateway-ip-notice-message').setValue('notice https://example.test — check this credential')
    await wrapper.get('[data-testid="gateway-ip-notice-save"]').trigger('click')
    await flushPromises()
    expect(updatePolicy).toHaveBeenCalledWith({
      enabled: true,
      ips: ['203.0.113.1', '203.0.113.2'],
      message: 'notice https://example.test — check this credential',
      rpm: 100,
      auto_add_enabled: true,
      sharing_window_minutes: 10,
      sharing_threshold: 3,
      key_disable_enabled: false,
      auto_entries: [],
    })
    expect(showSuccess).toHaveBeenCalledOnce()
  })

  it('cannot overwrite existing settings after a failed load', async () => {
    getPolicy.mockRejectedValueOnce(new Error('load failed'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="gateway-ip-notice-save"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('load failed')
    expect(updatePolicy).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="gateway-ip-notice-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="gateway-ip-notice-save"]').attributes('disabled')).toBeUndefined()
  })

  it('does not submit an empty enabled rule or fractional RPM', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#gateway-ip-notice-rpm').setValue(0.5)
    await wrapper.get('[data-testid="gateway-ip-notice-save"]').trigger('click')
    expect(updatePolicy).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
  })

  it('shows automatic entries separately and removes them through the normal save path', async () => {
    getPolicy.mockResolvedValueOnce({
      ...policy,
      ips: ['203.0.113.1'],
      auto_entries: [
        { ip: '203.0.113.9', source: 'auto', first_seen_at: '2026-11-12T04:00:00Z', distinct_users: 3, window_minutes: 10, key_prefixes: ['sk-aaaaa'] },
      ],
    })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="gateway-ip-notice-auto-entry-203.0.113.9"]').text()).toContain('203.0.113.9')
    await wrapper.get('[data-testid="gateway-ip-notice-remove-auto-203.0.113.9"]').trigger('click')
    await wrapper.get('[data-testid="gateway-ip-notice-save"]').trigger('click')
    await flushPromises()
    expect(updatePolicy).toHaveBeenCalledWith(expect.objectContaining({ auto_entries: [], enabled: true }))
    expect(showSuccess).toHaveBeenCalledOnce()
  })

  it('fills defaults for policies stored before auto detection existed', async () => {
    getPolicy.mockResolvedValueOnce({ enabled: false, ips: [], message: '', rpm: 100 })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="gateway-ip-notice-save"]').trigger('click')
    await flushPromises()
    expect(updatePolicy).toHaveBeenCalledWith(
      expect.objectContaining({ auto_add_enabled: true, sharing_threshold: 3, sharing_window_minutes: 10, key_disable_enabled: false, auto_entries: [] }),
    )
  })

  it('renders the key-disable switch off by default and submits it once switched on', async () => {
    const wrapper = render()
    await flushPromises()
    const toggle = wrapper.get('[data-testid="gateway-ip-notice-key-disable"]')
    expect(toggle.attributes('aria-checked')).toBe('false')

    await wrapper.get('[data-testid="gateway-ip-notice-save"]').trigger('click')
    await flushPromises()
    expect(updatePolicy).toHaveBeenLastCalledWith(expect.objectContaining({ key_disable_enabled: false }))

    await toggle.trigger('click')
    expect(toggle.attributes('aria-checked')).toBe('true')
    await wrapper.get('[data-testid="gateway-ip-notice-save"]').trigger('click')
    await flushPromises()
    expect(updatePolicy).toHaveBeenLastCalledWith(expect.objectContaining({ key_disable_enabled: true }))
    expect(showSuccess).toHaveBeenCalledTimes(2)
  })

  it('rejects a threshold below two', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#gateway-ip-notice-auto-threshold').setValue(1)
    await wrapper.get('[data-testid="gateway-ip-notice-save"]').trigger('click')
    expect(updatePolicy).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
  })

  it('shows first seen, peak and the accumulated key total for an entry', async () => {
    getPolicy.mockResolvedValueOnce({
      ...policy,
      auto_entries: [
        {
          ip: '203.0.113.7',
          source: 'auto',
          first_seen_at: '2026-11-12T04:00:00Z',
          distinct_users: 6,
          window_minutes: 5,
          key_prefixes: ['sk-aaaa1', 'sk-aaaa2'],
          first_seen_users: 4,
          peak_users: 6,
          total_keys: 10,
          last_seen_at: '2026-11-12T04:20:00Z',
        },
      ],
    })
    const wrapper = render()
    await flushPromises()
    const text = wrapper.get('[data-testid="gateway-ip-notice-auto-entry-203.0.113.7"]').text()
    expect(text).toContain('"first":4')
    expect(text).toContain('"peak":6')
    expect(text).toContain('"total":10')
    expect(text).toContain('gatewayIPNotice.autoWindowInfo')
  })

  it('degrades to the legacy fields for entries written before the counters existed', async () => {
    getPolicy.mockResolvedValueOnce({
      ...policy,
      auto_entries: [
        {
          ip: '207.32.218.140',
          source: 'auto',
          first_seen_at: '2026-11-12T04:00:00Z',
          distinct_users: 4,
          window_minutes: 5,
          key_prefixes: ['sk-bbbb1', 'sk-bbbb2', 'sk-bbbb3'],
        },
      ],
    })
    const wrapper = render()
    await flushPromises()
    const text = wrapper.get('[data-testid="gateway-ip-notice-auto-entry-207.32.218.140"]').text()
    // first_seen_users and peak_users fall back to distinct_users, and
    // total_keys falls back to the number of masked prefixes.
    expect(text).toContain('"first":4')
    expect(text).toContain('"peak":4')
    expect(text).toContain('"total":3')
    expect(text).not.toContain('undefined')
  })
})
