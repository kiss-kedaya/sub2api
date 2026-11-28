import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import GroupWatchBellButton from '../GroupWatchBellButton.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params?.name ? `${key}:${String(params.name)}` : key,
  }),
}))

describe('GroupWatchBellButton', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('renders gray idle bell by default with platform tone class and emits toggle on click', async () => {
    const wrapper = mount(GroupWatchBellButton, {
      props: {
        platform: 'anthropic',
        subscribed: false,
        groupName: 'Claude 混合号池',
      },
    })

    const btn = wrapper.find('[data-testid="group-watch-bell"]')
    expect(btn.exists()).toBe(true)
    expect(btn.attributes('aria-pressed')).toBe('false')
    expect(btn.classes()).toContain('is-tone-anthropic')
    expect(btn.classes()).not.toContain('is-subscribed')
    expect(btn.find('svg.group-watch-bell__svg').exists()).toBe(true)

    await btn.trigger('click')
    expect(wrapper.emitted('toggle')).toHaveLength(1)
  })

  it('triggers ringing animation when subscribed becomes true and muting settle when toggled off', async () => {
    vi.useFakeTimers()
    const wrapper = mount(GroupWatchBellButton, {
      props: {
        platform: 'openai',
        subscribed: false,
        groupName: 'OpenAI Pool',
      },
    })

    const btn = wrapper.find('[data-testid="group-watch-bell"]')
    await wrapper.setProps({ subscribed: true })
    await nextTick()

    expect(btn.attributes('aria-pressed')).toBe('true')
    expect(btn.classes()).toContain('is-subscribed')
    expect(btn.classes()).toContain('is-tone-openai')
    expect(btn.classes()).toContain('is-ringing')

    vi.advanceTimersByTime(600)
    await nextTick()
    expect(btn.classes()).not.toContain('is-ringing')

    await wrapper.setProps({ subscribed: false })
    await nextTick()
    expect(btn.classes()).toContain('is-muting')
    expect(btn.classes()).not.toContain('is-subscribed')

    vi.advanceTimersByTime(250)
    await nextTick()
    expect(btn.classes()).not.toContain('is-muting')
  })

  it('prevents duplicate toggle while pending and maps unknown platform to default tone', async () => {
    const wrapper = mount(GroupWatchBellButton, {
      props: {
        platform: 'custom-provider',
        subscribed: false,
        pending: true,
      },
    })

    const btn = wrapper.find('[data-testid="group-watch-bell"]')
    expect(btn.classes()).toContain('is-tone-default')
    expect(btn.attributes('disabled')).toBeDefined()
    await btn.trigger('click')
    expect(wrapper.emitted('toggle')).toBeUndefined()
  })
})
