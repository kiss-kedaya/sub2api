import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ProfileBalanceNotifyCard from '../ProfileBalanceNotifyCard.vue'

const { sendNotifyEmailCode, verifyNotifyEmail, getProfile, updateProfile, toggleNotifyEmail } = vi.hoisted(() => ({
  sendNotifyEmailCode: vi.fn(),
  verifyNotifyEmail: vi.fn(),
  getProfile: vi.fn(),
  updateProfile: vi.fn(),
  toggleNotifyEmail: vi.fn()
}))

vi.mock('@/api', () => ({
  userAPI: { sendNotifyEmailCode, verifyNotifyEmail, getProfile, updateProfile, toggleNotifyEmail }
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: null }) }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() })
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

enableAutoUnmount(afterEach)

const deferred = () => {
  let resolve!: () => void
  const promise = new Promise<void>((done) => { resolve = done })
  return { promise, resolve }
}

const pendingRows = (wrapper: VueWrapper) => wrapper.findAll('.bg-yellow-50')
const pendingEmails = (wrapper: VueWrapper) => pendingRows(wrapper).map(row => row.get('span').text())

const button = (wrapper: VueWrapper, text: string) =>
  wrapper.findAll('button').find(item => item.text() === text)!

describe('ProfileBalanceNotifyCard', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.resetAllMocks()
    sendNotifyEmailCode.mockResolvedValue({})
    getProfile.mockResolvedValue({ balance_notify_extra_emails: [] })
    updateProfile.mockResolvedValue({ balance_notify_enabled: false })
  })

  afterEach(() => {
    vi.clearAllTimers()
    vi.useRealTimers()
  })

  it('renders both switch surfaces as Nuxt UI switches, not hand-rolled checkboxes', async () => {
    const wrapper = mount(ProfileBalanceNotifyCard, {
      props: { enabled: true, threshold: null, extraEmails: [{ email: 'a@example.com', disabled: false, verified: true, primary: false }], systemDefaultThreshold: 5, userEmail: '' }
    })
    const switches = wrapper.findAll('[role="switch"]')
    expect(switches).toHaveLength(2)
    expect(switches.map(item => item.attributes('aria-checked'))).toEqual(['true', 'true'])
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    expect(switches[0]!.classes().join(' ')).toContain('data-[state=checked]:bg-primary')
  })

  it('persists the enable switch through updateProfile and reverts the switch on failure', async () => {
    updateProfile.mockRejectedValueOnce(new Error('boom'))
    const wrapper = mount(ProfileBalanceNotifyCard, {
      props: { enabled: true, threshold: null, extraEmails: [], systemDefaultThreshold: 5, userEmail: '' }
    })
    await wrapper.findAll('[role="switch"]')[0]!.trigger('click')
    await flushPromises()
    expect(updateProfile).toHaveBeenCalledWith({ balance_notify_enabled: false })
    expect(wrapper.findAll('[role="switch"]')[0]!.attributes('aria-checked')).toBe('true')
  })

  it('disables an extra email through the switch model and stores the returned list', async () => {
    toggleNotifyEmail.mockResolvedValue({ balance_notify_extra_emails: [{ email: 'a@example.com', disabled: true, verified: true, primary: false }] })
    const wrapper = mount(ProfileBalanceNotifyCard, {
      props: { enabled: true, threshold: null, extraEmails: [{ email: 'a@example.com', disabled: false, verified: true, primary: false }], systemDefaultThreshold: 5, userEmail: '' }
    })
    expect(wrapper.text()).toContain('a@example.com')
    await wrapper.findAll('[role="switch"]')[1]!.trigger('click')
    await flushPromises()
    expect(toggleNotifyEmail).toHaveBeenCalledWith('a@example.com', true)
  })

  it.each([0, 1])('removes only verified emails when request %i finishes first', async (first) => {
    const emails = ['first@example.com', 'second@example.com', 'third@example.com']
    const requests = [deferred(), deferred()]
    verifyNotifyEmail.mockImplementation((email: string) => requests[emails.indexOf(email)]!.promise)
    const wrapper = mount(ProfileBalanceNotifyCard, {
      props: { enabled: true, threshold: null, extraEmails: [], systemDefaultThreshold: 5, userEmail: '' }
    })

    for (const email of emails) {
      await wrapper.get('input[type="email"]').setValue(email)
      await button(wrapper, 'common.add').trigger('click')
    }
    for (const row of pendingRows(wrapper)) {
      await row.findAll('button').find(item => item.text() === 'profile.balanceNotify.sendCode')!.trigger('click')
    }
    await flushPromises()
    for (const row of pendingRows(wrapper)) {
      await row.get('input').setValue('123456')
    }
    for (const row of pendingRows(wrapper).slice(0, 2)) {
      await row.findAll('button').find(item => item.text() === 'profile.balanceNotify.verify')!.trigger('click')
    }
    expect(verifyNotifyEmail.mock.calls).toEqual(emails.slice(0, 2).map(email => [email, '123456']))

    requests[first]!.resolve()
    await flushPromises()
    expect(pendingEmails(wrapper)).toEqual(emails.filter((_, index) => index !== first))

    requests[1 - first]!.resolve()
    await flushPromises()
    expect(pendingEmails(wrapper)).toEqual([emails[2]])
    expect((pendingRows(wrapper)[0]!.get('input').element as HTMLInputElement).value).toBe('123456')
    expect(getProfile).toHaveBeenCalledTimes(2)
  })
})
