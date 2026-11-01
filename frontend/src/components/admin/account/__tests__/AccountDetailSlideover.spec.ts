import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import USlideover from '@nuxt/ui/components/Slideover.vue'
import AccountDetailSlideover from '../AccountDetailSlideover.vue'
import type { Account } from '@/types'
import { formatMultiplier } from '@/utils/formatters'

// Keep the real exports: utils/format builds its i18n instance with createI18n.
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))

const account = {
  id: 42, name: 'compact row', platform: 'openai', type: 'apikey', status: 'active', schedulable: true,
  priority: 3, rate_multiplier: 1.5, concurrency: 2, created_at: '2026-10-01T00:00:00Z', last_used_at: null,
  notes: 'keep warm', extra: {}, credentials: {}
} as unknown as Account

// The heavy cells have their own specs; here they only need to be placed in the right tab.
const stubs = {
  AccountStatusIndicator: { template: '<span data-test="status-cell" />' },
  AccountCapacityCell: true,
  AccountGroupsCell: true,
  AccountUsageCell: { template: '<span data-test="usage-cell" />' },
  AccountTodayStatsCell: true,
  UpstreamBillingRateCell: { template: '<span data-test="billing-cell" />' },
  SchedulableTimerControl: { template: '<span data-test="timer-cell" />' },
  PlatformTypeBadge: true
}

let wrapper: VueWrapper | null = null
afterEach(() => { wrapper?.unmount(); wrapper = null })

const mountDetail = async (props: Record<string, unknown> = {}) => {
  wrapper = mount(AccountDetailSlideover, { attachTo: document.body, props: { open: true, account, ...props }, global: { stubs } })
  await flushPromises()
  return wrapper
}
const tab = (label: string) => Array.from(document.body.querySelectorAll<HTMLElement>('[role="tab"]')).find(el => el.textContent?.includes(label))

describe('AccountDetailSlideover', () => {
  it('shows the account with overview, usage and schedule tabs', async () => {
    await mountDetail()
    expect(document.body.textContent).toContain('compact row')
    expect(document.body.textContent).toContain('#42')
    expect(tab('admin.accounts.accountDetail.overview')).toBeDefined()
    expect(tab('admin.accounts.accountDetail.usage')).toBeDefined()
    expect(tab('admin.accounts.accountDetail.schedule')).toBeDefined()
    // Overview is the first tab: status and the account fields are visible.
    expect(document.body.querySelector('[data-test="status-cell"]')).not.toBeNull()
    expect(document.body.textContent).toContain('keep warm')
    expect(document.body.textContent).toContain(`${formatMultiplier(1.5)}x`)
  })

  it('moves the usage windows and the upstream rate into the usage tab', async () => {
    await mountDetail()
    expect(document.body.querySelector('[data-test="usage-cell"]')).toBeNull()
    tab('admin.accounts.accountDetail.usage')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0 }))
    await flushPromises()
    expect(document.body.querySelector('[data-test="usage-cell"]')).not.toBeNull()
    expect(document.body.querySelector('[data-test="billing-cell"]')).not.toBeNull()
  })

  it('offers edit and stats and closes through the slideover', async () => {
    const w = await mountDetail()
    const button = (label: string) => Array.from(document.body.querySelectorAll<HTMLElement>('button')).find(el => el.textContent?.trim() === label)
    button('common.edit')!.click()
    button('admin.accounts.viewStats')!.click()
    expect(w.emitted('edit')).toEqual([[account]])
    expect(w.emitted('stats')).toEqual([[account]])
    w.getComponent(USlideover).vm.$emit('update:open', false)
    expect(w.emitted('close')).toHaveLength(1)
  })

  it('renders nothing without an account', async () => {
    await mountDetail({ account: null })
    expect(tab('admin.accounts.accountDetail.overview')).toBeUndefined()
  })
})
