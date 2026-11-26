import { describe, expect, it, vi, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import PlatformUsageBreakdown from '../PlatformUsageBreakdown.vue'

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

describe('PlatformUsageBreakdown', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('teleports the breakdown tooltip to document.body above table overflow and closes on leave or scroll', async () => {
    const wrapper = mount(PlatformUsageBreakdown, {
      attachTo: document.body,
      props: {
        today: 1.5,
        total: 5.25,
        byPlatform: [
          { platform: 'anthropic', today_actual_cost: 1.0, total_actual_cost: 3.0 },
          { platform: 'openai', today_actual_cost: 0.5, total_actual_cost: 2.25 },
        ],
      },
      global: {
        stubs: { Icon: true },
      },
    })

    try {
      expect(document.body.querySelector('[data-testid="platform-usage-breakdown-tooltip"]')).toBeNull()

      await wrapper.trigger('mouseenter')
      const tooltip = document.body.querySelector('[data-testid="platform-usage-breakdown-tooltip"]')
      expect(tooltip).not.toBeNull()
      expect(tooltip?.parentElement).toBe(document.body)
      expect(tooltip?.className).toContain('fixed')
      expect(tooltip?.className).toContain('z-[60]')
      expect(tooltip?.textContent).toContain('Claude')
      expect(tooltip?.textContent).toContain('OpenAI')

      window.dispatchEvent(new Event('scroll'))
      await wrapper.vm.$nextTick()
      expect(document.body.querySelector('[data-testid="platform-usage-breakdown-tooltip"]')).toBeNull()

      await wrapper.trigger('focusin')
      expect(document.body.querySelector('[data-testid="platform-usage-breakdown-tooltip"]')).not.toBeNull()

      await wrapper.trigger('focusout')
      expect(document.body.querySelector('[data-testid="platform-usage-breakdown-tooltip"]')).toBeNull()
    } finally {
      wrapper.unmount()
    }
  })

  it('does not open a tooltip when there is no platform breakdown', async () => {
    const wrapper = mount(PlatformUsageBreakdown, {
      attachTo: document.body,
      props: {
        today: 0,
        total: 0,
        byPlatform: [],
      },
      global: {
        stubs: { Icon: true },
      },
    })

    try {
      await wrapper.trigger('mouseenter')
      expect(document.body.querySelector('[data-testid="platform-usage-breakdown-tooltip"]')).toBeNull()
    } finally {
      wrapper.unmount()
    }
  })
})
