import { readFileSync } from 'node:fs'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import StepUpSkipNotice from '../StepUpSkipNotice.vue'
import { recordStepUpSkip, resetStepUpSkipForTests } from '@/composables/useStepUpSkip'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: { time?: string }) => (params?.time ? `${key} ${params.time}` : key),
  }),
}))

enableAutoUnmount(afterEach)

function mountNotice() {
  return mount(StepUpSkipNotice, {
    global: { stubs: { UIcon: true } },
  })
}

describe('StepUpSkipNotice', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    resetStepUpSkipForTests()
  })

  afterEach(() => {
    resetStepUpSkipForTests()
    vi.useRealTimers()
  })

  it('is hidden when no sensitive request skipped 2FA', () => {
    const wrapper = mountNotice()
    expect(wrapper.find('[data-testid="step-up-skip-notice"]').exists()).toBe(false)
  })

  it('renders the localized copy with the remaining grace time', async () => {
    const wrapper = mountNotice()
    recordStepUpSkip(272)
    await flushPromises()

    const notice = wrapper.find('[data-testid="step-up-skip-notice"]')
    expect(notice.exists()).toBe(true)
    expect(notice.attributes('role')).toBe('status')
    expect(wrapper.find('[data-testid="step-up-skip-notice-text"]').text()).toContain(
      'stepUp.skipNotice 4:32',
    )
  })

  it('updates the countdown every second', async () => {
    const wrapper = mountNotice()
    recordStepUpSkip(272)
    await flushPromises()

    vi.advanceTimersByTime(1000)
    await flushPromises()

    expect(wrapper.find('[data-testid="step-up-skip-notice-text"]').text()).toContain('4:31')
  })

  it('uses the m:ss format for sub-minute windows', async () => {
    const wrapper = mountNotice()
    recordStepUpSkip(9)
    await flushPromises()
    expect(wrapper.find('[data-testid="step-up-skip-notice-text"]').text()).toContain('0:09')
  })

  it('uses GPU-only ease-out motion, reduced-motion guard, and safe-area bottom inset', async () => {
    const wrapper = mountNotice()
    recordStepUpSkip(30)
    await flushPromises()

    const notice = wrapper.get('[data-testid="step-up-skip-notice"]')
    expect(notice.classes()).toContain('bottom-[calc(1rem+env(safe-area-inset-bottom,0px))]')

    const sfc = readFileSync('src/components/auth/StepUpSkipNotice.vue', 'utf8')
    expect(sfc).toContain('enter-active-class="transition-[transform,opacity] duration-200 ease-out"')
    expect(sfc).toContain('leave-active-class="transition-[transform,opacity] duration-150 ease-out"')
    expect(sfc).not.toContain('ease-in')
    expect(sfc).toContain('motion-reduce:translate-y-0')
  })
})
