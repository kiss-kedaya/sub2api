import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AmountInput from '../AmountInput.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('AmountInput interaction', () => {
  it('shows the payment currency, keeps limits and updates quick amounts', async () => {
    const wrapper = mount(AmountInput, { props: { modelValue: null, currency: 'CNY', amounts: [10, 50, 100], min: 20, max: 90 } })
    expect(wrapper.get('.payment-amount-input__currency').text()).toBe('CNY')
    expect(wrapper.findAll('button')).toHaveLength(1)
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([[50]])
    expect(wrapper.get('input').element.value).toBe('50')
    wrapper.unmount()
  })

  it.each(['50x', '1e5', '-5', '50.123'])('rejects %s visibly without retaining a misleading amount', async value => {
    const wrapper = mount(AmountInput, { props: { modelValue: 50 } })
    await wrapper.get('input').setValue(value)
    expect(wrapper.get('input').element.value).toBe('50')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    wrapper.unmount()
  })

  it('supports decimal editing and clearing with a controlled parent', async () => {
    const wrapper = mount(AmountInput, { props: { modelValue: 50,
      'onUpdate:modelValue': value => wrapper.setProps({ modelValue: value }),
    } })
    await wrapper.get('input').setValue('0')
    expect(wrapper.get('input').element.value).toBe('0')
    await wrapper.get('input').setValue('0.')
    expect(wrapper.get('input').element.value).toBe('0.')
    await wrapper.get('input').setValue('0.50')
    expect(wrapper.props('modelValue')).toBe(0.5)
    await wrapper.get('input').setValue('')
    expect(wrapper.props('modelValue')).toBeNull()
    expect(wrapper.get('input').element.value).toBe('')
    await wrapper.setProps({ modelValue: 100 })
    await wrapper.setProps({ modelValue: null })
    expect(wrapper.get('input').element.value).toBe('')
    wrapper.unmount()
  })
})
