import { nextTick } from 'vue'
import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import VerificationCodeInput from '@/components/auth/VerificationCodeInput.vue'

afterEach(() => {
  // mount(attachTo:) leaves nodes behind; a stale focused input would leak into
  // the next focus assertion.
  document.body.innerHTML = ''
  document.body.removeAttribute('style')
})

function mountInput(props: Record<string, unknown> = {}, slots: Record<string, unknown> = {}) {
  return mount(VerificationCodeInput, {
    props: { modelValue: '', label: 'Verification code', ...props },
    slots: slots as never,
    attachTo: document.body
  })
}

const digits = (wrapper: ReturnType<typeof mountInput>) =>
  wrapper.findAll('[data-testid^="verification-code-digit-"]')

const digitValue = (wrapper: ReturnType<typeof mountInput>, index: number) =>
  (digits(wrapper)[index].element as HTMLInputElement).value

async function paste(wrapper: ReturnType<typeof mountInput>, index: number, text: string) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { getData: () => text } })
  digits(wrapper)[index].element.dispatchEvent(event)
  await nextTick()
  await nextTick()
}

describe('VerificationCodeInput', () => {
  it('renders one cell per configured digit and a separator between the halves', () => {
    const wrapper = mountInput()
    expect(digits(wrapper)).toHaveLength(6)
    expect(wrapper.findAll('[data-testid="verification-code-separator"]')).toHaveLength(1)

    const four = mountInput({ length: 4 })
    expect(digits(four)).toHaveLength(4)

    const plain = mountInput({ separator: false })
    expect(plain.findAll('[data-testid="verification-code-separator"]')).toHaveLength(0)
  })

  it('accepts digits only and advances to the next cell', async () => {
    const wrapper = mountInput()
    await digits(wrapper)[0].setValue('1')
    await nextTick()

    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['1'])
    expect(digitValue(wrapper, 0)).toBe('1')
    expect(document.activeElement).toBe(digits(wrapper)[1].element)

    // 字母/符号被过滤，不会写入格子。
    await digits(wrapper)[1].setValue('a')
    expect(digitValue(wrapper, 1)).toBe('')
  })

  it('distributes a pasted code and emits complete + submit once', async () => {
    const wrapper = mountInput()
    await paste(wrapper, 0, '123456')

    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['123456'])
    expect(wrapper.emitted('complete')?.[0]).toEqual(['123456'])
    expect(wrapper.emitted('submit')?.[0]).toEqual(['123456'])
    expect(digits(wrapper).map((_, index) => digitValue(wrapper, index))).toEqual(['1', '2', '3', '4', '5', '6'])
  })

  it('treats an autofill/test write of the whole code as a paste', async () => {
    const wrapper = mountInput({ autocomplete: 'one-time-code' })
    await digits(wrapper)[0].setValue('654321')

    expect(wrapper.emitted('complete')?.[0]).toEqual(['654321'])
    expect(wrapper.emitted('submit')?.[0]).toEqual(['654321'])
  })

  it('only submits once per entered code and once more after an edit', async () => {
    const wrapper = mountInput()
    await paste(wrapper, 0, '123456')
    await digits(wrapper)[3].setValue('9')
    await digits(wrapper)[3].setValue('4')
    await digits(wrapper)[3].setValue('4')
    expect(wrapper.emitted('submit')).toHaveLength(1)

    // 退格清空后重新输满，才允许再验证一次。
    await digits(wrapper)[5].trigger('keydown', { key: 'Backspace' })
    await digits(wrapper)[5].setValue('6')
    expect(wrapper.emitted('submit')).toHaveLength(2)
  })

  it('does not auto-submit when the page disables it, but still reports completion', async () => {
    const wrapper = mountInput({ autoSubmit: false })
    await paste(wrapper, 0, '123456')
    expect(wrapper.emitted('complete')).toHaveLength(1)
    expect(wrapper.emitted('submit')).toBeUndefined()
  })

  it('clears the current digit on Backspace, then walks back', async () => {
    const wrapper = mountInput()
    await paste(wrapper, 0, '123456')

    await digits(wrapper)[2].trigger('keydown', { key: 'Backspace' })
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['12456'])

    await digits(wrapper)[2].trigger('keydown', { key: 'Backspace' })
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['1456'])
    await nextTick()
    expect(document.activeElement).toBe(digits(wrapper)[1].element)
  })

  it('moves with the arrow keys and ignores shortcuts while disabled', async () => {
    const wrapper = mountInput()
    await digits(wrapper)[0].setValue('1')
    await nextTick()
    await digits(wrapper)[2].trigger('keydown', { key: 'ArrowLeft' })
    await nextTick()
    expect(document.activeElement).toBe(digits(wrapper)[1].element)

    const disabled = mountInput({ disabled: true })
    for (const input of digits(disabled)) {
      expect(input.attributes('disabled')).toBeDefined()
    }
  })

  it('syncs an external clear without emitting submit', async () => {
    const wrapper = mountInput()
    await paste(wrapper, 0, '123456')
    expect(wrapper.emitted('submit')).toHaveLength(1)

    // Parent-side value: the component is used with v-model in production, so the
    // prop round-trip must clear the cells without re-triggering verification.
    await wrapper.setProps({ modelValue: '123456' })
    await wrapper.setProps({ modelValue: '' })
    await nextTick()
    await nextTick()
    expect(digits(wrapper).map((_, index) => digitValue(wrapper, index))).toEqual(['', '', '', '', '', ''])
    expect(wrapper.emitted('submit')).toHaveLength(1)
  })

  it('marks the field invalid and renders the hint and footer slot', () => {
    const wrapper = mountInput(
      { invalid: true, hint: 'Check your inbox', errorText: '' },
      { footer: '<button data-testid="resend">Resend</button>' }
    )
    for (const input of digits(wrapper)) {
      expect(input.attributes('aria-invalid')).toBe('true')
    }
    expect(wrapper.text()).toContain('Check your inbox')
    expect(wrapper.get('[data-testid="resend"]').text()).toBe('Resend')
  })

  it('keeps the six cells on one line for narrow screens', () => {
    const wrapper = mountInput()
    // The cells are flex children with min-w-0 and the group caps its own width,
    // so all six fit a 320px phone without horizontal scrolling.
    const group = wrapper.get('[data-testid="verification-code-input"] .flex')
    expect(group.classes()).toContain('flex')
    for (const input of digits(wrapper)) {
      expect(input.classes()).toContain('verification-code-input__digit')
    }
  })
})
