import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TotpFactorPane from '@/components/auth/TotpFactorPane.vue'

const { showErrorMock } = vi.hoisted(() => ({ showErrorMock: vi.fn() }))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError: (...args: any[]) => showErrorMock(...args) })
}))

function setError(wrapper: ReturnType<typeof mount>, message: string, retryAfter = 0) {
  const vm = wrapper.vm as unknown as { setError: (m: string, r?: number) => void }
  vm.setError(message, retryAfter)
}

describe('TotpFactorPane account rate limit', () => {
  beforeEach(() => {
    sessionStorage.clear()
    showErrorMock.mockReset()
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-21T03:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('disables the input and shows a countdown after 5 submits in 60s (local limit)', async () => {
    const wrapper = mount(TotpFactorPane, {
      props: { tempToken: 'temp-token', userEmailMasked: 'u***@example.com', accountKey: 'user@example.com' }
    })
    const input = wrapper.get('input[autocomplete="one-time-code"]')

    for (let i = 1; i <= 5; i++) {
      await input.setValue('123456')
      expect(wrapper.emitted('verify')).toHaveLength(i)
      setError(wrapper, 'Invalid code', 0)
      await wrapper.vm.$nextTick()
    }

    // 第 6 次：本地计数已满，不再发请求，输入禁用并显示倒计时。
    await input.setValue('654321')
    expect(wrapper.emitted('verify')).toHaveLength(5)
    expect(input.attributes('disabled')).toBeDefined()
    const countdown = wrapper.get('[data-testid="totp-login-rate-limit"]')
    expect(countdown.text()).toContain('profile.totp.loginRateLimited')

    // 60 秒后恢复可输入。
    vi.advanceTimersByTime(61_000)
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-testid="totp-login-rate-limit"]').exists()).toBe(false)
    await input.setValue('123456')
    expect(wrapper.emitted('verify')).toHaveLength(6)
    wrapper.unmount()
  })

  it('honors server Retry-After on 429 and does not rely on local counting', async () => {
    const wrapper = mount(TotpFactorPane, {
      props: { tempToken: 'temp-token', accountKey: 'user@example.com' }
    })
    const input = wrapper.get('input[autocomplete="one-time-code"]')

    await input.setValue('123456')
    expect(wrapper.emitted('verify')).toHaveLength(1)

    // 服务端 429：Retry-After=30s → 即便本地只提交过 1 次也立刻禁用并倒计时。
    setError(wrapper, 'Too many attempts', 30)
    await wrapper.vm.$nextTick()

    expect(input.attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="totp-login-rate-limit"]').text()).toContain('profile.totp.loginRateLimited')

    await input.setValue('123456')
    expect(wrapper.emitted('verify')).toHaveLength(1)

    vi.advanceTimersByTime(31_000)
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-testid="totp-login-rate-limit"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
