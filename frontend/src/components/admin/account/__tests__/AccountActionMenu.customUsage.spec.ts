import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import AccountActionMenu from '../AccountActionMenu.vue'
import type { Account } from '@/types'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
let wrapper: VueWrapper
const account = { id: 1, type: 'apikey', credentials: { base_url: 'https://example.test' } } as Account
// The menu is a UDropdownMenu teleported to <body>; items are role="menuitem" buttons there.
const menuText = () => document.body.querySelector('.action-menu-content')?.textContent ?? ''
async function setup(value: Account) {
  wrapper = mount(AccountActionMenu, { attachTo: document.body, props: { show: true, account: value, anchorRect: new DOMRect(50, 50, 30, 30) } })
  await flushPromises()
}
afterEach(() => wrapper?.unmount())
describe('custom usage action eligibility', () => {
  it('opens configuration for a custom-base API-key account and closes the menu', async () => {
    await setup(account)
    const button = Array.from(document.body.querySelectorAll<HTMLElement>('.action-menu-content [role="menuitem"]'))
      .find(item => item.textContent?.trim() === 'admin.accounts.customUsage.title')!
    expect(button).toBeDefined(); button.click(); await flushPromises()
    expect(wrapper.emitted('custom-usage')).toEqual([[account]])
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
  it.each([{ ...account, type: 'oauth' }, { ...account, credentials: {} }, { ...account, credentials: { base_url: ' ' } }])('hides the entry for an ineligible account', async value => {
    await setup(value as Account)
    expect(menuText()).not.toBe('')
    expect(menuText()).not.toContain('admin.accounts.customUsage.title')
  })
})
