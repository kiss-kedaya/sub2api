import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SocialLoginButtons from '@/components/common/SocialLoginButtons.vue'
import type { SocialLoginProvider } from '@/components/common/socialLogin'

const providers: SocialLoginProvider[] = [
  { id: 'github', label: 'Continue with GitHub' },
  { id: 'google', label: 'Continue with Google' }
]

describe('SocialLoginButtons', () => {
  it('renders one full-width outlined button per provider, in order', () => {
    const wrapper = mount(SocialLoginButtons, { props: { providers } })
    const buttons = wrapper.findAll('button')

    expect(buttons).toHaveLength(2)
    expect(buttons[0].text()).toBe('Continue with GitHub')
    expect(buttons[1].text()).toBe('Continue with Google')
    // 认证参考稿：一列全宽、按钮之间统一 10px 间距、共用一个圆角。
    const grid = wrapper.get('.grid')
    expect(grid.classes()).toContain('grid-cols-1')
    expect(grid.classes()).toContain('gap-2.5')
    expect(wrapper.get('[data-testid="social-login-github"]').classes()).toContain('w-full')
  })

  it('emits the provider id when a button is clicked', async () => {
    const wrapper = mount(SocialLoginButtons, { props: { providers } })
    await wrapper.get('[data-testid="social-login-google"]').trigger('click')
    expect(wrapper.emitted('select')).toEqual([['google']])
  })

  it('disables every button when the form is busy, and single providers on demand', () => {
    const wrapper = mount(SocialLoginButtons, {
      props: {
        disabled: true,
        providers: [...providers, { id: 'oidc', label: 'SSO', disabled: true }]
      }
    })
    for (const button of wrapper.findAll('button')) {
      expect(button.attributes('disabled')).toBeDefined()
    }
  })

  it('lets the page supply a multi-colour brand mark through the icon slot', () => {
    const wrapper = mount(SocialLoginButtons, {
      props: { providers },
      slots: {
        'icon-google': '<span data-testid="google-mark" class="brand-mark">G</span>'
      }
    })
    expect(wrapper.get('[data-testid="google-mark"]').text()).toBe('G')
    // No slot for GitHub: the label still renders, the icon area stays empty.
    expect(wrapper.find('[data-testid="social-login-github"]').exists()).toBe(true)
  })

  it('falls back to a UIcon name when a provider carries one', () => {
    const wrapper = mount(SocialLoginButtons, {
      props: { providers: [{ id: 'passkey', label: 'Passkey', icon: 'i-lucide-key-round' }] }
    })
    expect(wrapper.find('[data-testid="social-login-passkey"] [data-slot="leading"]').exists()).toBe(false)
    expect(wrapper.find('svg, span.iconify').exists()).toBe(true)
  })

  it('draws the "or continue with" divider with the caller copy', () => {
    const wrapper = mount(SocialLoginButtons, {
      props: { providers, divider: true, dividerLabel: 'or continue with' }
    })
    expect(wrapper.get('.social-login-buttons__divider').text()).toBe('or continue with')
  })

  it('supports two columns only when the page opts in', () => {
    const wrapper = mount(SocialLoginButtons, { props: { providers, columns: 2 } })
    expect(wrapper.get('.grid').classes()).toContain('sm:grid-cols-2')
  })

  it('renders the footer slot under the buttons', () => {
    const wrapper = mount(SocialLoginButtons, {
      props: { providers },
      slots: { footer: '<p data-testid="login-hint">hint</p>' }
    })
    expect(wrapper.get('[data-testid="login-hint"]').text()).toBe('hint')
  })
})
