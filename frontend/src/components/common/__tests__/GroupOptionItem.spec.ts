import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import GroupOptionItem from '../GroupOptionItem.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: null }),
}))

describe('GroupOptionItem description layout', () => {
  it('applies multiline and overflow-safe text styles', () => {
    const description = 'First section\nvery-long-unbroken-description-value-that-must-not-overflow'
    const wrapper = mount(GroupOptionItem, {
      props: {
        name: 'Example group',
        platform: 'openai',
        description,
      },
      global: {
        stubs: {
          GroupBadge: true,
        },
      },
    })

    const descriptionElement = wrapper
      .findAll('span')
      .find((element) => element.text() === description)

    expect(descriptionElement).toBeDefined()
    expect(descriptionElement?.classes()).toContain('whitespace-pre-line')
    expect(descriptionElement?.classes()).toContain('wrap-anywhere')
    expect(descriptionElement?.classes()).toContain('line-clamp-3')
    expect(wrapper.find('[title]').attributes('title')).toBe(description)
  })
})

describe('GroupOptionItem rate hints', () => {
  const mountItem = (props: Record<string, unknown> = {}) =>
    mount(GroupOptionItem, {
      props: { name: 'Example group', platform: 'openai', rateMultiplier: 0.08, ...props },
      global: { stubs: { GroupBadge: true } },
    })

  const hints = {
    recent_rate_change: { direction: 'down' as const, delta: 0.03, previous_rate: 0.11, changed_at: '2026-11-04T10:30:00Z' },
    is_new: true,
    launched_at: '2026-11-05T01:00:00Z',
    launch_rate: 0.08,
  }

  it('renders the hints as passive badges, never as a nested control', () => {
    const wrapper = mountItem({ signals: hints })
    expect(wrapper.find('[data-testid="group-rate-signal-rate"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="group-rate-signal-new"]').exists()).toBe(true)
    expect(wrapper.find('button').exists()).toBe(false)
    expect(wrapper.get('[data-testid="group-rate-signal-rate"]').attributes('role')).toBe('img')
  })

  it('shows no hint without signals', () => {
    expect(mountItem().find('[data-group-signals]').exists()).toBe(false)
  })

  it('leaves out the arrow when the host shows no per-token rate', () => {
    const wrapper = mountItem({ signals: hints, showRateChange: false })
    expect(wrapper.find('[data-testid="group-rate-signal-rate"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="group-rate-signal-new"]').exists()).toBe(true)
  })
})
