import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import TotpLoginModal from '@/components/auth/TotpLoginModal.vue'

const { showErrorMock } = vi.hoisted(() => ({
  showErrorMock: vi.fn(),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError: (...args: any[]) => showErrorMock(...args),
  }),
}))

describe('TotpLoginModal', () => {
  beforeEach(() => {
    showErrorMock.mockReset()
  })

  it('sends verification errors to toast and does not render inline red text', async () => {
    const wrapper = mount(TotpLoginModal, {
      props: {
        tempToken: 'temp-token',
        userEmailMasked: 'u***@example.com',
      },
    })

    ;(wrapper.vm as unknown as { setError: (message: string) => void }).setError('Invalid code')
    await wrapper.vm.$nextTick()

    expect(showErrorMock).toHaveBeenCalledWith('Invalid code')
    expect(wrapper.text()).not.toContain('Invalid code')
    expect(wrapper.find('.bg-red-50').exists()).toBe(false)
  })

  it('keeps login auto-verification and clears the shared input after a rejection', async () => {
    const wrapper = mount(TotpLoginModal, { props: { tempToken: 'temp-token' } })
    const input = wrapper.get('input[autocomplete="one-time-code"]')
    await input.trigger('paste', { clipboardData: { getData: () => '012 345' } })
    expect(wrapper.emitted('verify')).toEqual([['012345']])
    expect(wrapper.findAll('.totp-code-input span').map(cell => cell.text())).toEqual(['0', '1', '2', '3', '4', '5'])
    ;(wrapper.vm as unknown as { setError: (message: string) => void }).setError('Expired code')
    await wrapper.vm.$nextTick()
    expect(input.element).toHaveProperty('value', '')
    await input.trigger('paste', { clipboardData: { getData: () => '654321' } })
    expect(wrapper.emitted('verify')).toEqual([['012345'], ['654321']])
    wrapper.unmount()
  })
})
