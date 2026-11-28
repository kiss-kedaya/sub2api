import { readFileSync } from 'node:fs'
import { describe, expect, it, vi } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'
import LegalFooterLinks from '../LegalFooterLinks.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('@/i18n', () => ({
  getLocale: () => 'en',
}))

describe('LegalFooterLinks', () => {
  it('renders navigation links for bundled legal documents', () => {
    const wrapper = mount(LegalFooterLinks, {
      global: {
        stubs: {
          RouterLink: RouterLinkStub,
        },
      },
    })

    const nav = wrapper.get('nav.legal-footer-links')
    expect(nav.attributes('aria-label')).toBe('legal.navLabel')

    const links = wrapper.findAllComponents(RouterLinkStub)
    expect(links.length).toBeGreaterThan(0)
    expect(links.some((link) => link.props('to') === '/legal/privacy')).toBe(true)
    expect(links.some((link) => link.props('to') === '/legal/terms')).toBe(true)
    wrapper.unmount()
  })

  it('uses var(--ui-primary), capability-gated hover, and focus-visible styling', () => {
    const sfc = readFileSync('src/components/legal/LegalFooterLinks.vue', 'utf8')
    expect(sfc).toContain('var(--ui-primary)')
    expect(sfc).not.toContain('rgb(59 130 246)')
    expect(sfc).toMatch(/@media\s*\(hover:\s*hover\)\s*and\s*\(pointer:\s*fine\)\s*\{[^}]*\.legal-footer-link:hover/s)
    expect(sfc).toContain('.legal-footer-link:focus-visible')
    expect(sfc).toContain('outline: 2px solid var(--ui-primary)')
  })
})
