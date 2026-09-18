import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import ConsoleNavigationSearch from '../ConsoleNavigationSearch.vue'

async function render(role: 'user' | 'admin') {
  const pinia = createPinia()
  setActivePinia(pinia)
  const auth = useAuthStore()
  auth.user = { id: 900001, role } as NonNullable<typeof auth.user>
  const app = useAppStore()
  app.cachedPublicSettings = { payment_enabled: false, channel_monitor_enabled: false } as NonNullable<typeof app.cachedPublicSettings>
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })
  await router.push('/dashboard')
  const push = vi.spyOn(router, 'push')
  const wrapper = mount(ConsoleNavigationSearch, {
    global: {
      plugins: [pinia, router, createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
      stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' } }
    }
  })
  await wrapper.get('.console-search-trigger').trigger('click')
  return { wrapper, push }
}

describe('console navigation search', () => {
  it('does not expose admin or disabled feature destinations to a user', async () => {
    const { wrapper } = await render('user')
    expect(wrapper.text()).not.toContain('admin.accounts.title')
    expect(wrapper.text()).not.toContain('nav.buySubscription')
    expect(wrapper.text()).not.toContain('nav.channelStatus')
    expect(wrapper.text()).toContain('nav.apiKeys')
    wrapper.unmount()
  })

  it('filters admin destinations and navigates on Enter without triggering a business action', async () => {
    const { wrapper, push } = await render('admin')
    await wrapper.get('input').setValue('/admin/accounts')
    expect(wrapper.findAll('.console-search-result')).toHaveLength(1)
    await wrapper.get('input').trigger('keydown.enter')
    await flushPromises()
    expect(push).toHaveBeenCalledWith('/admin/accounts')
    expect(wrapper.find('input').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps an empty query result open and ignores Enter', async () => {
    const { wrapper, push } = await render('user')
    await wrapper.get('input').setValue('missing-destination')
    await wrapper.get('input').trigger('keydown.enter')
    expect(wrapper.find('input').exists()).toBe(true)
    expect(push).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
