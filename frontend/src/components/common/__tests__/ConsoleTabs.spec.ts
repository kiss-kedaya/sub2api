import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ConsoleTabs from '../ConsoleTabs.vue'

const items = [{ key: 'overview', label: 'Overview' }, { key: 'platforms', label: 'Platforms' }]

describe('ConsoleTabs', () => {
  it('links labels, panels and a single tab stop', async () => {
    const wrapper = mount(ConsoleTabs, { props: { id: 'test', label: 'Views', modelValue: 'overview', items } })
    expect(wrapper.get('[role="tablist"]').attributes('aria-label')).toBe('Views')
    expect(wrapper.get('#test-tab-overview').attributes('tabindex')).toBe('0')
    expect(wrapper.get('#test-tab-platforms').attributes('tabindex')).toBe('-1')
    expect(wrapper.get('#test-tab-platforms').attributes('aria-controls')).toBe('test-panel-platforms')
    await wrapper.get('#test-tab-platforms').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([['platforms']])
    wrapper.unmount()
  })

  it.each([
    ['ArrowRight', 'overview', 'platforms'], ['ArrowRight', 'platforms', 'overview'],
    ['ArrowLeft', 'overview', 'platforms'], ['Home', 'platforms', 'overview'], ['End', 'overview', 'platforms'],
  ])('moves focus with %s from %s to %s', async (key, initial, target) => {
    const wrapper = mount(ConsoleTabs, { attachTo: document.body, props: {
      id: 'test', label: 'Views', modelValue: initial, items,
      'onUpdate:modelValue': value => wrapper.setProps({ modelValue: value }),
    } })
    await wrapper.get(`#test-tab-${initial}`).trigger('keydown', { key })
    expect(wrapper.emitted('update:modelValue')).toEqual([[target]])
    expect(document.activeElement?.id).toBe(`test-tab-${target}`)
    expect(wrapper.get(`#test-tab-${target}`).attributes('tabindex')).toBe('0')
    wrapper.unmount()
  })
})
