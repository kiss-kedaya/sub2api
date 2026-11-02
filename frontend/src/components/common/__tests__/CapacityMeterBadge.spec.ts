import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import CapacityMeterBadge from '../CapacityMeterBadge.vue'

const base = { label: 'Concurrency', hint: 'used / max of this account' }

describe('CapacityMeterBadge', () => {
  it('renders both numbers and carries the explanation on hover and on focus', () => {
    const wrapper = mount(CapacityMeterBadge, { props: { ...base, used: 3, max: 10 } })

    expect(wrapper.text()).toContain('3')
    expect(wrapper.text()).toContain('10')
    expect(wrapper.attributes('title')).toBe('Concurrency · used / max of this account')
    expect(wrapper.attributes('aria-label')).toBe('Concurrency · used / max of this account')
    expect(wrapper.attributes('tabindex')).toBe('0')
    expect(wrapper.attributes('data-tone')).toBe('active')
  })

  it('separates full, in-use and idle states', () => {
    expect(mount(CapacityMeterBadge, { props: { ...base, used: 10, max: 10 } }).attributes('data-tone')).toBe('full')
    expect(mount(CapacityMeterBadge, { props: { ...base, used: 1, max: 10 } }).attributes('data-tone')).toBe('active')
    expect(mount(CapacityMeterBadge, { props: { ...base, used: 0, max: 10 } }).attributes('data-tone')).toBe('idle')
  })

  it('shows an em dash, not a fake zero, when the counter is unknown', () => {
    const wrapper = mount(CapacityMeterBadge, { props: { ...base, used: null, max: 10 } })

    expect(wrapper.text().replace(/\s+/g, '')).toBe('—/10')
    expect(wrapper.attributes('data-tone')).toBe('unknown')
  })

  it('lets the caller keep its own thresholds', () => {
    const wrapper = mount(CapacityMeterBadge, { props: { ...base, used: 0, max: 10, tone: 'full' } })

    expect(wrapper.attributes('data-tone')).toBe('full')
  })

  it('hides the separator when the meter has no maximum', () => {
    const wrapper = mount(CapacityMeterBadge, { props: { ...base, used: 7, showMax: false } })

    expect(wrapper.text()).toContain('7')
    expect(wrapper.text()).not.toContain('/')
  })
})
