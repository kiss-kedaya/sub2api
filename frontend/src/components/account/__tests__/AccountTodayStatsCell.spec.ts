import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { WindowStats } from '@/types'
import AccountTodayStatsCell from '../AccountTodayStatsCell.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const stats = (overrides: Partial<WindowStats> = {}): WindowStats => ({
  requests: 185_800,
  tokens: 12_363_690_000,
  cost: 10314.69,
  user_cost: 1453.49,
  ...overrides
})

describe('AccountTodayStatsCell inline', () => {
  it('puts requests, tokens and both costs on one line with compact numbers', () => {
    const text = mount(AccountTodayStatsCell, { props: { stats: stats(), inline: true } }).text()
    expect(text).toContain('185.8K req')
    expect(text).toContain('12.4B')
    expect(text).toContain('A $10314.69')
    expect(text).toContain('U $1453.49')
  })

  it('leaves out the user cost when the stats carry none', () => {
    const wrapper = mount(AccountTodayStatsCell, { props: { stats: stats({ user_cost: undefined }), inline: true } })
    expect(wrapper.text()).not.toContain('U $')
    expect(wrapper.find('[title="usage.userBilled"]').exists()).toBe(false)
  })

  it('shows a skeleton while loading, and the numbers as soon as they are in', () => {
    expect(mount(AccountTodayStatsCell, { props: { stats: null, loading: true, inline: true } }).findAll('.animate-pulse').length).toBeGreaterThan(0)
    const loaded = mount(AccountTodayStatsCell, { props: { stats: stats(), loading: true, inline: true } })
    expect(loaded.findAll('.animate-pulse')).toHaveLength(0)
    expect(loaded.text()).toContain('185.8K req')
  })

  it('renders nothing when there is nothing to show, so the parent can pick its own fallback', () => {
    const wrapper = mount(AccountTodayStatsCell, { props: { stats: null, inline: true } })
    expect(wrapper.text()).toBe('')
    expect(wrapper.find('div').exists()).toBe(false)
  })
})

describe('AccountTodayStatsCell list', () => {
  it('lists labelled rows and formats the numbers the same way as the inline line', () => {
    const text = mount(AccountTodayStatsCell, { props: { stats: stats() } }).text()
    expect(text).toContain('admin.accounts.stats.requests:')
    expect(text).toContain('185.8K')
    expect(text).toContain('12.4B')
    expect(text).toContain('usage.accountBilled:')
    expect(text).toContain('10,314.69')
    expect(text).toContain('usage.userBilled:')
    expect(text).toContain('1,453.49')
  })

  it('skips the user-billed row when the stats carry no user cost', () => {
    const text = mount(AccountTodayStatsCell, { props: { stats: stats({ user_cost: undefined }) } }).text()
    expect(text).not.toContain('usage.userBilled')
  })

  it('shows a skeleton while loading, the error when loading failed, and a dash when empty', () => {
    expect(mount(AccountTodayStatsCell, { props: { loading: true } }).findAll('.animate-pulse').length).toBeGreaterThan(0)
    expect(mount(AccountTodayStatsCell, { props: { error: 'Failed' } }).text()).toBe('Failed')
    expect(mount(AccountTodayStatsCell, { props: {} }).text()).toBe('-')
  })

  it('keeps showing numbers it already has when a refresh fails', () => {
    const text = mount(AccountTodayStatsCell, { props: { stats: stats(), error: 'Failed' } }).text()
    expect(text).toContain('185.8K')
    expect(text).not.toContain('Failed')
  })
})
