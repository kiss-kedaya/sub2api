import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import InflightUsagePanel from '../InflightUsagePanel.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('InflightUsagePanel', () => {
  it('keeps the closed panel compact and exposes a named, controlled disclosure', async () => {
    const wrapper = mount(InflightUsagePanel, {
      props: { open: false, total: 0, shown: 0 },
      slots: { default: '<p>request-details</p>' },
    })
    const toggle = wrapper.get('[data-testid="inflight-toggle"]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(wrapper.text()).not.toContain('request-details')
    expect(wrapper.find('[data-testid="inflight-refresh"]').exists()).toBe(false)
    await toggle.trigger('click')
    expect(wrapper.emitted('toggle')).toHaveLength(1)

    await wrapper.setProps({ open: true, loaded: true, total: 2, shown: 1, hasMore: true })
    const region = wrapper.get('[role="region"]')
    expect(toggle.attributes('aria-controls')).toBe(region.attributes('id'))
    expect(region.attributes('aria-labelledby')).toBe(toggle.attributes('id'))
    expect(wrapper.text()).toContain('request-details')
    await wrapper.get('[data-testid="inflight-refresh"]').trigger('click')
    await wrapper.get('[data-testid="inflight-more"]').trigger('click')
    expect(wrapper.emitted('refresh')).toHaveLength(1)
    expect(wrapper.emitted('more')).toHaveLength(1)
    expect(wrapper.emitted('toggle')).toHaveLength(1)
    wrapper.unmount()
  })

  it('does not show an empty result while loading or after a failed fetch', async () => {
    const wrapper = mount(InflightUsagePanel, {
      props: { open: true, total: 0, shown: 0, loading: true },
    })
    expect(wrapper.get('[role="status"]').text()).toContain('common.loading')
    expect(wrapper.get('[role="region"]').attributes('aria-busy')).toBe('true')
    expect(wrapper.get('[data-testid="inflight-refresh"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="inflight-empty"]').exists()).toBe(false)

    await wrapper.setProps({ loading: false, error: 'request-failed' })
    expect(wrapper.get('[role="alert"]').text()).toBe('request-failed')
    expect(wrapper.find('[data-testid="inflight-empty"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="inflight-refresh"]').attributes('disabled')).toBeUndefined()

    await wrapper.setProps({ error: '', loaded: true })
    expect(wrapper.get('[data-testid="inflight-empty"]').text()).toContain('usage.inflightEmpty')
    wrapper.unmount()
  })

  it('paints hover and focus inside the pill so the border above stays visible', () => {
    const wrapper = mount(InflightUsagePanel, {
      props: { open: true, loaded: true, total: 98, shown: 0 },
    })

    const row = wrapper.get('[data-testid="inflight-toggle-row"]')
    const rowClass = row.attributes('class') ?? ''
    expect(rowClass).toContain('pt-1')

    const buttonClass = wrapper.get('[data-testid="inflight-toggle"]').attributes('class') ?? ''
    // A hover ring drawn inside the box cannot land on the card edge above the pill.
    expect(buttonClass).toContain('hover:ring-1')
    expect(buttonClass).toContain('hover:ring-inset')
    // The focus outline used to sit 2px outside the button.
    expect(buttonClass).toContain('focus-visible:outline-offset-1')
    // Only colour transitions; no size/transform animation on hover.
    expect(buttonClass).toContain('transition-[background-color,border-color,color]')
    expect(buttonClass).not.toContain('transition-all')
    expect(buttonClass).not.toContain('hover:scale')
    wrapper.unmount()
  })
})
