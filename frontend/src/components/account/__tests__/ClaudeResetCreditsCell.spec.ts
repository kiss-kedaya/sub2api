import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ClaudeResetCreditsCell from '../ClaudeResetCreditsCell.vue'
import type { Account } from '@/types'
const getCredits = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/claudeResetCredits', () => ({ getClaudeResetCredits: getCredits }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const account = { id: 1, platform: 'anthropic', type: 'oauth' } as Account
const credit = {
  selection_token: 'tok', label: 'Launch reset', resets_left: 1,
  expires_at: '2026-10-22T16:00:00Z', clears: ['five_hour', 'seven_day'],
  percent_used: {}, blocking: [], use_requires_limit: false, redeemable: true
}
const snapshot = { eligible: true, available_count: 1, credits: [credit], fetched_at: '2026-09-25T00:00:00Z' }
const countButton = (wrapper: ReturnType<typeof mount>) => wrapper.find('[data-testid="claude-reset-count"]')

describe('Claude reset credit status', () => {
  beforeEach(() => getCredits.mockReset())

  it('queries only on explicit request and shows count and expiry', async () => {
    getCredits.mockResolvedValue(snapshot)
    const wrapper = mount(ClaudeResetCreditsCell, { props: { account } })
    expect(getCredits).not.toHaveBeenCalled()
    expect(countButton(wrapper).text()).toBe('admin.accounts.claudeResetCredits.count')
    await countButton(wrapper).trigger('click')
    await flushPromises()
    expect(getCredits).toHaveBeenCalledWith(1)
    expect(countButton(wrapper).text()).toBe('admin.accounts.claudeResetCredits.count1')
    const expiry = wrapper.get('[data-testid="claude-reset-expiry"]')
    expect(expiry.attributes('title')).toContain('Launch reset')
  })

  it('offers no redeem action', () => {
    const wrapper = mount(ClaudeResetCreditsCell, { props: { account } })
    expect(wrapper.findAll('button')).toHaveLength(1)
  })

  it('keeps slot content for setup tokens and discards responses after account changes', async () => {
    let resolve!: (value: typeof snapshot) => void
    getCredits.mockReturnValue(new Promise(r => { resolve = r }))
    const wrapper = mount(ClaudeResetCreditsCell, {
      props: { account },
      slots: { 'pre-actions': '<button data-testid="local-query">q</button>' }
    })
    await countButton(wrapper).trigger('click')
    await wrapper.setProps({ account: { ...account, id: 2, type: 'setup-token' } })
    resolve(snapshot)
    await flushPromises()
    expect(countButton(wrapper).exists()).toBe(false)
    expect(wrapper.find('[data-testid="local-query"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="claude-reset-expiry"]').exists()).toBe(false)
  })
})
