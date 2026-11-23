import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import USwitch from '@nuxt/ui/components/Switch.vue'

/**
 * Direct `<USwitch>` usages (33 files) bypass the shared `Toggle` wrapper, so the
 * theme matrix has to live in the app-level Nuxt UI theme (`nuxt-ui.config.ts`).
 * These assertions cover the component those pages actually render.
 */
const classesOf = (element: { classes: () => string[] }) => element.classes().join(' ')

describe('Nuxt UI switch theme', () => {
  it('defaults to the primary colour and renders the shared state matrix', () => {
    const wrapper = mount(USwitch, { props: { modelValue: false } })
    const track = wrapper.get('[role="switch"]')
    const classes = classesOf(track)

    expect(track.attributes('aria-checked')).toBe('false')
    expect(classes).toContain('data-[state=checked]:bg-primary')
    expect(classes).toContain('data-[state=checked]:border-primary-600')
    expect(classes).toContain('data-[state=unchecked]:bg-accented')
    expect(classes).toContain('data-[state=unchecked]:border-accented')
    expect(classes).toContain('outline-primary-600')
    expect(classes).toContain('dark:outline-primary/60')
    expect(classes).toContain('motion-reduce:transition-none')
    expect(classesOf(wrapper.get('[data-slot="thumb"]'))).toContain('dark:data-[state=unchecked]:bg-inverted')
  })

  it('flips aria-checked with the model and keeps the thumb slide reduced-motion safe', async () => {
    const wrapper = mount(USwitch, { props: { modelValue: false } })
    const track = wrapper.get('[role="switch"]')
    expect(track.attributes('data-state')).toBe('unchecked')
    await track.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([true])

    await wrapper.setProps({ modelValue: true })
    await flushPromises()
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[role="switch"]').attributes('data-state')).toBe('checked')
    expect(classesOf(wrapper.get('[data-slot="thumb"]'))).toContain('motion-reduce:transition-none')
  })

  it('does not emit from a disabled switch', async () => {
    const wrapper = mount(USwitch, { props: { modelValue: false, disabled: true } })
    const track = wrapper.get('[role="switch"]')
    expect(track.attributes('disabled')).toBeDefined()
    await track.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
})
