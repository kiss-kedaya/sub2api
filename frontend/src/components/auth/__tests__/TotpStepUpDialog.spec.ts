import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import UserCreateModal from '@/components/admin/user/UserCreateModal.vue'
import TotpStepUpDialog from '../TotpStepUpDialog.vue'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'
import { useStepUp } from '@/composables/useStepUp'

const mocks = vi.hoisted(() => ({ create: vi.fn(), stepUp: vi.fn(), showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin', () => ({ adminAPI: { users: { create: mocks.create } } }))
vi.mock('@/api', () => ({ totpAPI: { stepUp: mocks.stepUp } }))
vi.mock('@/stores', () => ({ useAppStore: () => mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
enableAutoUnmount(afterEach)

describe('step-up above the initiating user dialog', () => {
  afterEach(() => { vi.resetAllMocks() })

  it('opens above user creation, retains the form, and retries only after verification', async () => {
    mocks.create.mockRejectedValueOnce({ reason: 'STEP_UP_REQUIRED' }).mockResolvedValueOnce({ id: 11 })
    mocks.stepUp.mockResolvedValue({ verified: true })
    const wrapper = mount(UserCreateModal, { attachTo: document.body, props: { show: true } })
    await flushPromises()
    const form = document.querySelector<HTMLFormElement>('#create-user-form')!
    const email = form.querySelector<HTMLInputElement>('input[type="email"]')!
    const password = form.querySelector<HTMLInputElement>('input[type="text"]')!
    email.value = 'fixture@example.test'; email.dispatchEvent(new Event('input', { bubbles: true }))
    password.value = 'fixture-password'; password.dispatchEvent(new Event('input', { bubbles: true }))
    form.querySelector<HTMLSelectElement>('select')!.value = 'admin'
    form.querySelector<HTMLSelectElement>('select')!.dispatchEvent(new Event('change', { bubbles: true }))
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()
    const original = form.closest<HTMLElement>('.modal-overlay')!
    const input = document.querySelector<HTMLInputElement>('input[autocomplete="one-time-code"]')!
    const verification = input.closest<HTMLElement>('.modal-overlay')!
    expect(verification.parentElement).toBe(document.body)
    expect(Number(verification.style.zIndex)).toBeGreaterThan(Number(original.style.zIndex))
    expect(original.hasAttribute('inert')).toBe(true)
    expect(document.activeElement).toBe(input)
    expect(email.value).toBe('fixture@example.test')
    input.value = '012345'; input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    expect(mocks.stepUp).toHaveBeenCalledOnce()
    expect(mocks.stepUp).toHaveBeenCalledWith('012345')
    expect(mocks.create).toHaveBeenCalledTimes(2)
    expect(mocks.create.mock.calls[0][0]).toEqual(mocks.create.mock.calls[1][0])
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('auto-submits once, rejects concurrent completions, and retries after clearing a rejected code', async () => {
    let reject!: (reason: unknown) => void
    mocks.stepUp.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail }))
      .mockResolvedValueOnce({ verified: true })
    const controller = useStepUp()
    controller.visible.value = true
    const wrapper = mount(TotpStepUpDialog, { props: { controller }, global: { stubs: { teleport: true } } })
    await flushPromises()
    let input = wrapper.get('input[autocomplete="one-time-code"]')
    await input.setValue('01234')
    expect(mocks.stepUp).not.toHaveBeenCalled()
    await input.setValue('012345')
    expect(mocks.stepUp).toHaveBeenCalledOnce()
    expect(mocks.stepUp).toHaveBeenCalledWith('012345')
    await wrapper.getComponent(TotpCodeInput).vm.$emit('complete', '654321')
    expect(mocks.stepUp).toHaveBeenCalledOnce()
    reject({ message: 'Expired code' })
    await flushPromises()
    expect(mocks.showError).toHaveBeenCalledWith('Expired code')
    input = wrapper.get('input[autocomplete="one-time-code"]')
    expect(input.element).toHaveProperty('value', '')
    expect(controller.visible.value).toBe(true)
    await input.trigger('paste', { clipboardData: { getData: () => '012345' } })
    await flushPromises()
    expect(mocks.stepUp).toHaveBeenCalledTimes(2)
    expect(controller.visible.value).toBe(false)
  })
})
