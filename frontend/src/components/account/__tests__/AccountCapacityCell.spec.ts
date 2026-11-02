import { describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import type { Account } from '@/types'
import AccountCapacityCell from '../AccountCapacityCell.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const buildAccount = (overrides: Partial<Account> = {}) => ({
  id: 1,
  platform: 'openai',
  type: 'apikey',
  concurrency: 10,
  current_concurrency: 3,
  status: 'active',
  ...overrides,
} as unknown as Account)

const compact = (text: string) => text.replace(/\s+/g, '')

const meters = (wrapper: VueWrapper) =>
  wrapper.findAll('[data-tone]').map((meter) => ({
    tone: meter.attributes('data-tone'),
    label: meter.attributes('aria-label') ?? '',
    text: compact(meter.text()),
  }))

describe('AccountCapacityCell', () => {
  it('shows live concurrency against capacity with an explanation', () => {
    const wrapper = mount(AccountCapacityCell, { props: { account: buildAccount() } })

    const rows = meters(wrapper)
    expect(rows).toHaveLength(1)
    expect(rows[0].text).toBe('3/10')
    expect(rows[0].label).toContain('admin.accounts.capacity.labels.concurrency')
    expect(rows[0].label).toContain('admin.accounts.capacity.concurrency.hint')
    expect(rows[0].tone).toBe('active')
  })

  it('adds the live user count above concurrency when the account tracks sessions', () => {
    const wrapper = mount(AccountCapacityCell, {
      props: {
        account: buildAccount({
          platform: 'anthropic',
          type: 'oauth',
          concurrency: 8,
          current_concurrency: 2,
          max_sessions: 60,
          active_sessions: 41,
          session_idle_timeout_minutes: 5,
        }),
      },
    })

    const rows = meters(wrapper)
    expect(rows.map((row) => row.text)).toEqual(['41/60', '2/8'])
    expect(rows[0].label).toContain('admin.accounts.capacity.labels.users')
    expect(rows[0].label).toContain('admin.accounts.capacity.sessions.normal')
    expect(rows[1].label).toContain('admin.accounts.capacity.labels.concurrency')
  })

  it('calls out a saturated account instead of leaving the badge colour-only', () => {
    const wrapper = mount(AccountCapacityCell, {
      props: { account: buildAccount({ concurrency: 4, current_concurrency: 4 }) },
    })

    const rows = meters(wrapper)
    expect(rows[0].tone).toBe('full')
    expect(rows[0].label).toContain('admin.accounts.capacity.concurrency.full')
  })

  it('keeps quota rows below the capacity rows', () => {
    const wrapper = mount(AccountCapacityCell, {
      props: { account: buildAccount({ quota_daily_limit: 5, quota_daily_used: 1, quota_used: 1, quota_limit: 20 }) },
    })

    const text = wrapper.text().replace(/\s+/g, ' ')
    expect(text).toContain('D')
    expect(meters(wrapper)).toHaveLength(1)
    expect(wrapper.findAll('[title]').length).toBeGreaterThan(2)
  })
})
