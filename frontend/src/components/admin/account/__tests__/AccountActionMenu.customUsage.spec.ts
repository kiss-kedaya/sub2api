import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import AccountActionMenu from '../AccountActionMenu.vue'
import type { Account } from '@/types'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
let wrapper: VueWrapper
const account = { id: 1, type: 'apikey', credentials: { base_url: 'https://example.test' } } as Account
function setup(value: Account) {
  wrapper = mount(AccountActionMenu, { props: { show: true, account: value, anchorRect: new DOMRect(50, 50, 30, 30) }, global: { stubs: { teleport: true } } })
}
afterEach(() => wrapper?.unmount())
describe('custom usage action eligibility', () => {
  it('opens configuration for a custom-base API-key account and closes the menu', async () => {
    setup(account)
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.accounts.customUsage.title')!
    expect(button).toBeDefined(); await button.trigger('click')
    expect(wrapper.emitted('custom-usage')).toEqual([[account]])
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
  it.each([{ ...account, type: 'oauth' }, { ...account, credentials: {} }, { ...account, credentials: { base_url: ' ' } }])('hides the entry for an ineligible account', value => {
    setup(value as Account)
    expect(wrapper.text()).not.toContain('admin.accounts.customUsage.title')
  })
})
