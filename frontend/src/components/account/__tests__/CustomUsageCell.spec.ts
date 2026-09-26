import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import CustomUsageCell from '../CustomUsageCell.vue'
import type { AccountListItem } from '@/types'
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }) }))
const account = { id: 3, type: 'apikey', credentials: { base_url: 'https://example.test' } } as AccountListItem
let wrapper: VueWrapper
afterEach(() => wrapper?.unmount())
describe('upstream balance cell', () => {
  it('renders zero, timestamp, stale and safe error feedback without leaking raw responses', async () => {
    wrapper = mount(CustomUsageCell, { props: { account, state: { result: { enabled: true, configured: true, remaining: 0, unit: 'USD', updated_at: '2026-09-24T00:00:00Z', stale: true, error: 'private-token' } } } })
    expect(wrapper.get('[data-testid="custom-usage-remaining"]').text()).toBe('0 USD')
    expect(wrapper.find('time').attributes('datetime')).toBe('2026-09-24T00:00:00Z')
    expect(wrapper.text()).toContain('admin.accounts.customUsage.stale')
    expect(wrapper.text()).not.toContain('private-token')
    await wrapper.get('[data-testid="custom-usage-refresh"]').trigger('click')
    expect(wrapper.emitted('refresh')).toHaveLength(1)
  })
  it('disables refresh during loading and does not coerce missing balance to zero', () => {
    wrapper = mount(CustomUsageCell, { props: { account, state: { loading: true, result: { enabled: true, configured: true, unit: 'USD' } } } })
    expect(wrapper.get('[data-testid="custom-usage-refresh"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="custom-usage-remaining"]').exists()).toBe(false)
  })
  it('does not expose query controls for OAuth accounts', () => {
    wrapper = mount(CustomUsageCell, { props: { account: { ...account, type: 'oauth' } } })
    expect(wrapper.text()).toBe('—')
    expect(wrapper.find('button').exists()).toBe(false)
  })
  it('opens configuration for unconfigured accounts', async () => {
    wrapper = mount(CustomUsageCell, { props: { account, state: { result: { enabled: false, configured: false, unit: '' } } } })
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('configure')).toHaveLength(1)
  })
})
