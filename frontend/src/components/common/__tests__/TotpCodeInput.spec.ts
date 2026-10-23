import { defineComponent, ref } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import TotpCodeInput from '../TotpCodeInput.vue'

enableAutoUnmount(afterEach)

function render() {
  return mount(defineComponent({
    components: { TotpCodeInput },
    setup: () => ({ code: ref(''), disabled: ref(false) }),
    template: '<TotpCodeInput v-model="code" label="Authenticator code" :disabled="disabled" id="test-otp" />',
  }), { attachTo: document.body })
}

describe('shared six-cell authenticator input', () => {
  it('provides one labelled autofill field and focuses it when opened', async () => {
    const wrapper = render()
    await flushPromises()
    const input = wrapper.get('input')
    expect(wrapper.findAll('input')).toHaveLength(1)
    expect(wrapper.findAll('span[aria-hidden="true"]')).toHaveLength(6)
    expect(input.attributes('aria-label')).toBe('Authenticator code')
    expect(input.attributes('autocomplete')).toBe('one-time-code')
    expect(input.attributes('inputmode')).toBe('numeric')
    expect(input.attributes('id')).toBe('test-otp')
    expect(document.activeElement).toBe(input.element)
  })

  it('pastes the full code with separators and preserves a leading zero', async () => {
    const wrapper = render()
    const input = wrapper.get('input')
    await input.trigger('paste', { clipboardData: { getData: () => '01 23-45' } })
    await flushPromises()
    expect(wrapper.vm.code).toBe('012345')
    expect(input.element).toHaveProperty('value', '012345')
    expect(wrapper.findAll('span').map(cell => cell.text())).toEqual(['0', '1', '2', '3', '4', '5'])
  })

  it('normalizes full-width digits and refuses to submit a truncated overlong code', async () => {
    const wrapper = render()
    const input = wrapper.get('input')
    await input.setValue('０１２３４５')
    expect(wrapper.vm.code).toBe('012345')
    await input.trigger('paste', { clipboardData: { getData: () => '1234567' } })
    expect(wrapper.vm.code).toBe('')
    expect(input.element).toHaveProperty('value', '')
    await input.setValue('12a45')
    expect(wrapper.vm.code).toBe('1245')
  })

  it('keeps external resets in sync with the displayed cells', async () => {
    const wrapper = render()
    await wrapper.get('input').setValue('123456')
    wrapper.vm.code = ''
    await flushPromises()
    expect(wrapper.get('input').element).toHaveProperty('value', '')
    expect(wrapper.findAll('span').every(cell => !cell.text().replace(/\u200b/g, ''))).toBe(true)
  })

  it('disables editing during verification and restores focus when ready', async () => {
    const wrapper = render()
    wrapper.vm.disabled = true
    await flushPromises()
    expect(wrapper.get('input').attributes('disabled')).toBeDefined()
    await wrapper.get('input').trigger('paste', { clipboardData: { getData: () => '123456' } })
    expect(wrapper.vm.code).toBe('')
    wrapper.vm.disabled = false
    await flushPromises()
    expect(document.activeElement).toBe(wrapper.get('input').element)
  })

  it('emits completion only for a full new entry, including autofill and paste', async () => {
    const wrapper = render()
    const control = wrapper.getComponent(TotpCodeInput)
    const input = wrapper.get('input')
    for (const code of ['0', '01', '012', '0123', '01234']) await input.setValue(code)
    expect(control.emitted('complete')).toBeUndefined()
    await input.setValue('012345')
    expect(control.emitted('complete')).toEqual([['012345']])
    await input.trigger('input')
    expect(control.emitted('complete')).toHaveLength(1)
    wrapper.vm.code = ''
    await flushPromises()
    await input.trigger('paste', { clipboardData: { getData: () => '０１２ ３４５' } })
    expect(control.emitted('complete')).toEqual([['012345'], ['012345']])
    wrapper.vm.code = '654321'
    await flushPromises()
    expect(control.emitted('complete')).toHaveLength(2)
    wrapper.vm.disabled = true
    await flushPromises()
    await input.trigger('paste', { clipboardData: { getData: () => '111111' } })
    expect(control.emitted('complete')).toHaveLength(2)
  })
})
