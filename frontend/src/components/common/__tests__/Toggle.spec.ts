import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import Toggle from '../Toggle.vue'

describe('Toggle', () => {
  it('renders a switch whose state follows modelValue', async () => {
    const wrapper = mount(Toggle, { props: { modelValue: false } })
    const control = wrapper.get('[role="switch"]')
    expect(control.attributes('aria-checked')).toBe('false')
    await wrapper.setProps({ modelValue: true })
    expect(control.attributes('aria-checked')).toBe('true')
  })

  it('emits the opposite value once per click and stays controlled', async () => {
    const wrapper = mount(Toggle, { props: { modelValue: true } })
    await wrapper.get('[role="switch"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([[false]])
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('true')
  })

  it('passes test ids, labels and disabled to the switch button', async () => {
    const wrapper = mount(Toggle, {
      props: { modelValue: false },
      attrs: { 'data-testid': 'probe-toggle', 'aria-label': 'Auto probe', disabled: true }
    })
    const control = wrapper.get('[data-testid="probe-toggle"]')
    expect(control.element.tagName).toBe('BUTTON')
    expect(control.attributes('role')).toBe('switch')
    expect(control.attributes('aria-label')).toBe('Auto probe')
    expect(control.attributes('disabled')).toBeDefined()
    await control.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
})
