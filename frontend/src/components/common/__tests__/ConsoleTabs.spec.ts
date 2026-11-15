import { describe, expect, it, vi } from 'vitest'
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

describe('ConsoleTabs administrator tab layout', () => {
  const layoutItems = [
    { key: 'rechargeCenter', label: 'Alipay / WeChat' },
    { key: 'recharge', label: 'Top up' },
  ]

  it('marks the default tab with a star for every user', () => {
    const wrapper = mount(ConsoleTabs, {
      props: { id: 'pay', label: 'Payment', modelValue: 'rechargeCenter', items: layoutItems, defaultKey: 'rechargeCenter', defaultLabel: 'Default tab' },
    })
    const stars = wrapper.findAll('[data-test="console-tabs-default-star"]')
    expect(stars).toHaveLength(1)
    expect(stars[0].attributes('title')).toBe('Default tab')
    // Non-administrators cannot change the default and cannot drag.
    expect(wrapper.findAll('[data-test="console-tabs-set-default"]')).toHaveLength(0)
    expect(wrapper.get('[data-tab-key="rechargeCenter"]').attributes('draggable')).toBe('false')
    wrapper.unmount()
  })

  it('renders the drag and set-default affordances only for administrators', () => {
    const wrapper = mount(ConsoleTabs, {
      props: {
        id: 'pay', label: 'Payment', modelValue: 'rechargeCenter', items: layoutItems,
        defaultKey: 'rechargeCenter', defaultLabel: 'Default tab', setDefaultLabel: 'Set as default tab',
        reorderable: true,
      },
    })
    expect(wrapper.findAll('[data-test="console-tabs-set-default"]')).toHaveLength(1)
    expect(wrapper.get('[data-tab-key="rechargeCenter"]').attributes('draggable')).toBe('true')
    expect(wrapper.get('[data-tab-key="recharge"]').attributes('draggable')).toBe('true')
    // The native button itself must be draggable so the drag starts from the tab.
    expect(wrapper.get('#pay-tab-recharge').attributes('draggable')).toBe('true')
    // The default tab carries the marker itself, so no promote button is rendered for it.
    expect(wrapper.find('[data-default-tab-key="rechargeCenter"]').exists()).toBe(false)
    expect(wrapper.find('[data-default-tab-key="recharge"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('emits the reordered keys after dropping a dragged tab', async () => {
    const wrapper = mount(ConsoleTabs, {
      props: {
        id: 'pay', label: 'Payment', modelValue: 'rechargeCenter', items: layoutItems,
        defaultKey: 'rechargeCenter', reorderable: true,
      },
    })
    const dataTransfer = { effectAllowed: '', dropEffect: '', setData: vi.fn(), getData: vi.fn() }
    await wrapper.get('[data-tab-key="rechargeCenter"]').trigger('dragstart', { dataTransfer })
    await wrapper.get('[data-tab-key="recharge"]').trigger('dragover', { dataTransfer })
    await wrapper.get('[data-tab-key="recharge"]').trigger('drop', { dataTransfer })
    expect(wrapper.emitted('reorder')).toEqual([[['recharge', 'rechargeCenter']]])
    wrapper.unmount()
  })

  it('emits set-default when an administrator clicks a star', async () => {
    const wrapper = mount(ConsoleTabs, {
      props: {
        id: 'pay', label: 'Payment', modelValue: 'rechargeCenter', items: layoutItems,
        defaultKey: 'rechargeCenter', setDefaultLabel: 'Set as default tab', reorderable: true,
      },
    })
    await wrapper.get('[data-default-tab-key="recharge"]').trigger('click')
    expect(wrapper.emitted('set-default')).toEqual([['recharge']])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    wrapper.unmount()
  })
})
