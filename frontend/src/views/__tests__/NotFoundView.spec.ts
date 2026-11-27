import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'
import NotFoundView from '../NotFoundView.vue'

const backMock = vi.fn()

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    back: backMock,
  }),
}))

describe('NotFoundView', () => {
  it('renders 404 heading, semantic buttons, and navigates back on click', async () => {
    backMock.mockReset()
    const wrapper = mount(NotFoundView, {
      global: {
        stubs: {
          RouterLink: RouterLinkStub,
          Icon: { template: '<span />' },
        },
      },
    })

    expect(wrapper.text()).toContain('404')
    expect(wrapper.text()).toContain('errors.pageNotFound')

    const goBackBtn = wrapper.get('button')
    expect(goBackBtn.attributes('type')).toBe('button')
    await goBackBtn.trigger('click')
    expect(backMock).toHaveBeenCalledOnce()

    const dashboardLink = wrapper.findComponent(RouterLinkStub)
    expect(dashboardLink.props('to')).toBe('/dashboard')
  })

  it('uses min-h-dvh, tabular-nums, and semantic tokens without gray/dark classes', () => {
    const source = readFileSync(resolve(__dirname, '../NotFoundView.vue'), 'utf8')
    expect(source).toContain('min-h-dvh')
    expect(source).not.toContain('min-h-screen')
    expect(source).toContain('tabular-nums')
    expect(source).toContain('bg-(--app-page-bg)')
    expect(source).not.toMatch(/\b(?:text|bg|border)-(?:gray|dark)-/)
  })
})
