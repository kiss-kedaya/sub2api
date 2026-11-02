import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import TimeWheelPicker from '../TimeWheelPicker.vue'
import { bodyFindAll } from './overlay-helpers'

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: { common: { done: 'Done' } } } })

// The wheel panel is a Nuxt UI popover teleported to <body>.
afterEach(() => { document.body.innerHTML = '' })
enableAutoUnmount(afterEach)

const panelButtons = () => bodyFindAll('[data-test="time-wheel-panel"] button')

describe('TimeWheelPicker', () => {
  it('renders the current value and emits a full HH:mm on selection', async () => {
    const wrapper = mount(TimeWheelPicker, {
      attachTo: document.body,
      props: { modelValue: '08:30' },
      global: { plugins: [i18n] },
    })
    expect(wrapper.text()).toContain('08:30')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    const hourNine = panelButtons().find((b) => b.text() === '09')
    expect(hourNine).toBeTruthy()
    await hourNine!.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['09:30'])
  })

  it('keeps the minute when the hour changes and respects the minute step', async () => {
    const wrapper = mount(TimeWheelPicker, { attachTo: document.body, props: { modelValue: '23:55', minuteStep: 5 }, global: { plugins: [i18n] } })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    const minutes = panelButtons().map((b) => b.text())
    expect(minutes).toContain('55')
    expect(minutes).not.toContain('57')
  })

  it('does not open while disabled', async () => {
    const wrapper = mount(TimeWheelPicker, { attachTo: document.body, props: { modelValue: '10:00', disabled: true }, global: { plugins: [i18n] } })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(panelButtons()).toHaveLength(0)
  })
})
