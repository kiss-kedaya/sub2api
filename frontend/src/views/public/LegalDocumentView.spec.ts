import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LegalDocumentView from './LegalDocumentView.vue'

const route = { params: { documentId: 'privacy' } }

const { appStore } = vi.hoisted(() => ({
  appStore: {
    cachedPublicSettings: {
      site_name: 'Kedaya',
      site_logo: '',
      login_agreement_documents: [] as Array<{ id: string; title: string; content_md: string }>,
      login_agreement_updated_at: '',
    },
    fetchPublicSettings: vi.fn().mockResolvedValue(null),
  },
}))

vi.mock('vue-router', () => ({
  useRoute: () => route,
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, values?: { date?: string }) =>
      values?.date ? `${key}:${values.date}` : key,
  }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('@/i18n', () => ({
  getLocale: () => 'en',
}))

function mountLegal(documentId = 'privacy') {
  route.params.documentId = documentId
  return mount(LegalDocumentView, {
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        LocaleSwitcher: { template: '<div data-testid="locale-switcher" />' },
      },
    },
  })
}

describe('LegalDocumentView', () => {
  beforeEach(() => {
    route.params.documentId = 'privacy'
    appStore.cachedPublicSettings.login_agreement_documents = []
    appStore.fetchPublicSettings.mockClear()
    appStore.fetchPublicSettings.mockResolvedValue(null)
  })

  it('renders bundled privacy terms when admin content is empty', async () => {
    const wrapper = mountLegal('privacy')
    await flushPromises()
    expect(wrapper.text()).toContain('Privacy Policy')
    expect(wrapper.text()).toContain('API relay')
    expect(wrapper.findAllComponents(RouterLinkStub).some((link) => link.props('to') === '/legal/disclaimer')).toBe(true)
  })

  it('maps usage-policy to the bundled acceptable use policy', async () => {
    const wrapper = mountLegal('usage-policy')
    await flushPromises()
    expect(wrapper.text()).toContain('Acceptable Use Policy')
    expect(wrapper.text()).toContain('child sexual abuse')
  })

  it('uses admin markdown when the body is not empty', async () => {
    appStore.cachedPublicSettings.login_agreement_documents = [
      { id: 'privacy', title: 'Override title', content_md: 'Custom privacy body from admin' },
    ]
    const wrapper = mountLegal('privacy')
    await flushPromises()
    expect(wrapper.text()).toContain('Override title')
    expect(wrapper.text()).toContain('Custom privacy body from admin')
  })

  it('still shows bundled terms if public settings fail to load', async () => {
    appStore.fetchPublicSettings.mockRejectedValueOnce(new Error('network'))
    const wrapper = mountLegal('disclaimer')
    await flushPromises()
    expect(wrapper.text()).toContain('Disclaimer')
    expect(wrapper.text()).toContain('AS IS')
  })

  it('shows not found for unknown documents', async () => {
    const wrapper = mountLegal('not-a-real-doc')
    await flushPromises()
    expect(wrapper.text()).toContain('legal.notFound')
  })
})
