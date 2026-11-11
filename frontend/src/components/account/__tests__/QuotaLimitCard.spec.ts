import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import QuotaLimitCard from '../QuotaLimitCard.vue'

// Specs in this repo replace useI18n rather than installing the plugin. This one translates the
// handful of keys the card uses, with {name} interpolation, so assertions read real text.
const messages: Record<string, string> = {
  'admin.accounts.quotaLimitToggle': '启用配额限制',
  'admin.accounts.quotaLimitToggleHint': '达到额度后暂停调度',
  'admin.accounts.quotaSummary.daily': '日 {value}',
  'admin.accounts.quotaSummary.weekly': '周 {value}',
  'admin.accounts.quotaSummary.total': '总 {value}',
}
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params: Record<string, unknown> = {}) =>
        (messages[key] ?? key).replace(/\{(\w+)\}/g, (_match, name: string) => String(params[name] ?? '')),
    }),
  }
})

const base = {
  totalLimit: null,
  dailyLimit: null,
  weeklyLimit: null,
  dailyResetMode: null,
  dailyResetHour: null,
  weeklyResetMode: null,
  weeklyResetDay: null,
  weeklyResetHour: null,
  resetTimezone: null,
}

function mountCard(props: Record<string, unknown> = {}) {
  return mount(QuotaLimitCard, {
    props: { ...base, ...props },
    global: {
      stubs: {
        // The three dimension rows have their own behaviour; here they only need to be countable.
        QuotaDimensionRow: { props: ['dim'], template: '<div class="dimension" :data-dim="dim" />' },
      },
    },
  })
}

describe('QuotaLimitCard', () => {
  it('is one dense row, off and without fields, when no limit is set', () => {
    const wrapper = mountCard()

    const row = wrapper.get('[data-testid="quota-limit-row"]')
    expect(row.classes()).toContain('setting-row')
    expect(row.attributes('data-on')).toBeUndefined()
    expect(row.get('.setting-row__label').text()).toBe('启用配额限制')
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.findAll('.dimension')).toHaveLength(0)
    // An off row reports nothing inline.
    expect(wrapper.find('.setting-row__summary').exists()).toBe(false)
  })

  it('summarises the limits that are set, in daily / weekly / total order', () => {
    const wrapper = mountCard({ dailyLimit: 5, totalLimit: 100 })

    expect(wrapper.get('[data-testid="quota-limit-row"]').attributes('data-on')).toBe('true')
    expect(wrapper.get('.setting-row__summary').text()).toBe('日 $5 · 总 $100')
    expect(wrapper.findAll('.dimension').map((d) => d.attributes('data-dim'))).toEqual(['daily', 'weekly', 'total'])
  })

  it('leaves a zero or missing limit out of the summary', () => {
    const wrapper = mountCard({ dailyLimit: 0, weeklyLimit: 20 })

    expect(wrapper.get('.setting-row__summary').text()).toBe('周 $20')
  })

  it('keeps the explanation behind the row instead of always on screen', async () => {
    const wrapper = mountCard({ hint: '任一维度达到限额后暂停' })

    const button = wrapper.get('.setting-row__button')
    expect(button.attributes('aria-expanded')).toBe('false')
    expect(wrapper.get('.setting-row__body').attributes('data-open')).toBe('false')

    await button.trigger('click')

    expect(button.attributes('aria-expanded')).toBe('true')
    expect(wrapper.findAll('.setting-row__hint').map((p) => p.text())).toEqual(['达到额度后暂停调度', '任一维度达到限额后暂停'])
  })

  it('clears every limit and reset setting when switched off', async () => {
    const wrapper = mountCard({ dailyLimit: 5, weeklyLimit: 20, totalLimit: 100, dailyResetMode: 'fixed', resetTimezone: 'UTC' })

    await wrapper.get('[role="switch"]').trigger('click')
    await nextTick()

    const emitted = wrapper.emitted()
    for (const event of [
      'update:totalLimit', 'update:dailyLimit', 'update:weeklyLimit',
      'update:dailyResetMode', 'update:dailyResetHour',
      'update:weeklyResetMode', 'update:weeklyResetDay', 'update:weeklyResetHour',
      'update:resetTimezone',
    ]) {
      expect(emitted[event]?.at(-1), event).toEqual([null])
    }
  })

  it('shows the three dimensions as soon as it is switched on', async () => {
    const wrapper = mountCard()

    await wrapper.get('[role="switch"]').trigger('click')
    await nextTick()

    expect(wrapper.findAll('.dimension')).toHaveLength(3)
  })
})
