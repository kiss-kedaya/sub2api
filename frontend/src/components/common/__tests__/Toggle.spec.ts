import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import Toggle from '../Toggle.vue'

const classesOf = (element: { classes: () => string[] }) => element.classes().join(' ')

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

  it('renders the shared theme matrix: --ui-primary track, token surfaces, per-theme focus ring', () => {
    const wrapper = mount(Toggle, { props: { modelValue: true } })
    const track = classesOf(wrapper.get('[role="switch"]'))
    const thumb = classesOf(wrapper.get('[data-slot="thumb"]'))

    // Checked track follows the same --ui-primary token as every other control;
    // unchecked keeps the tokenised neutral (never a raw Tailwind gray).
    expect(track).toContain('data-[state=checked]:bg-primary')
    expect(track).toContain('data-[state=checked]:border-primary-600')
    expect(track).toContain('dark:data-[state=checked]:border-primary')
    expect(track).toContain('data-[state=unchecked]:bg-accented')
    expect(track).not.toMatch(/(^|\s)(dark:)?bg-gray-\d+/)
    expect(track).not.toMatch(/(^|\s)(dark:)?(bg|text)-(gray|slate|zinc|neutral)-\d+/)

    // Unchecked track gets a themed edge so it stays visible on the ink background.
    expect(track).toContain('data-[state=unchecked]:border-accented')
    expect(track).toContain('focus-visible:outline-3')
    expect(track).toContain('focus-visible:outline-offset-2')

    // The ring has one step per theme; dark does not reuse the light ring.
    expect(track).toContain('outline-primary-600')
    expect(track).toContain('dark:outline-primary/60')

    // Thumb: ink surface in dark (same token in light is white), inverted when the
    // dark track is dark so the two surfaces never collapse into each other.
    expect(thumb).toContain('bg-default')
    expect(thumb).toContain('dark:data-[state=unchecked]:bg-inverted')

    // Reduced motion: both the track cross-fade and the thumb slide are disabled.
    expect(track).toContain('motion-reduce:transition-none')
    expect(thumb).toContain('motion-reduce:transition-none')
  })

  it('exposes the xl scale for headline toggles and md by default', () => {
    const md = mount(Toggle, { props: { modelValue: false } })
    const xl = mount(Toggle, { props: { modelValue: false, size: 'xl' } })
    expect(classesOf(md.get('[role="switch"]'))).toContain('w-9')
    expect(classesOf(xl.get('[role="switch"]'))).toContain('w-11')
    md.unmount()
    xl.unmount()
  })
})
