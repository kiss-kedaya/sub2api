import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ref } from 'vue'
import { isRestrictedInSimpleMode } from '@/router/simpleMode'

const canUseBatchImage = ref(false)
const refreshBatchImageAccess = vi.fn()
const isSimpleMode = ref(false)

vi.mock('vue-i18n', async (original) => ({
  ...await original<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({ canUseBatchImage, refreshBatchImageAccess }),
}))
// `isSimpleMode` stays reactive so the component's filter follows the auth store.
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ get isSimpleMode() { return isSimpleMode.value } }),
}))

import UserDashboardQuickActions from '../UserDashboardQuickActions.vue'

const Page = { render: () => null }

async function mountActions() {
  const routes = ['/', '/keys', '/infinite-canvas', '/usage', '/batch-image', '/redeem']
    .map(path => ({ path, component: Page }))
  const router = createRouter({ history: createMemoryHistory(), routes })
  await router.push('/')
  const wrapper = mount(UserDashboardQuickActions, { global: { plugins: [router] } })
  await flushPromises()
  return { router, wrapper }
}

const hrefs = (wrapper: Awaited<ReturnType<typeof mountActions>>['wrapper']) =>
  wrapper.findAll('a[href]').map(link => link.attributes('href'))

beforeEach(() => {
  vi.clearAllMocks()
  canUseBatchImage.value = false
  isSimpleMode.value = false
})

describe('user dashboard quick actions', () => {
  it('navigates to every quick action and loads the batch-image gate on mount', async () => {
    const { router, wrapper } = await mountActions()
    expect(refreshBatchImageAccess).toHaveBeenCalledOnce()
    expect(hrefs(wrapper)).toEqual(['/keys', '/infinite-canvas', '/usage', '/redeem'])

    const links = wrapper.findAll('a[href]')
    for (const [index, link] of links.entries()) {
      await link.trigger('click')
      await flushPromises()
      expect(router.currentRoute.value.path).toBe(hrefs(wrapper)[index])
    }
    wrapper.unmount()
  })

  it('surfaces the batch-image entry only when the user holds a key that allows it', async () => {
    canUseBatchImage.value = true
    const { wrapper } = await mountActions()

    expect(hrefs(wrapper)).toEqual(['/keys', '/infinite-canvas', '/usage', '/batch-image', '/redeem'])
    wrapper.unmount()
  })

  it('hides destinations the simple-mode guard bounces back to the dashboard', async () => {
    isSimpleMode.value = true
    const { wrapper } = await mountActions()

    // The guard redirects /redeem to /dashboard in simple mode and the sidebar hides it, so a
    // card pointing there would be a dead click (#400).
    expect(refreshBatchImageAccess).toHaveBeenCalledOnce()
    expect(hrefs(wrapper)).toEqual(['/keys', '/infinite-canvas', '/usage'])
    for (const href of hrefs(wrapper)) expect(isRestrictedInSimpleMode(href ?? '')).toBe(false)
    wrapper.unmount()
  })

  it('keeps reachable entries in simple mode even when the sidebar trims them', async () => {
    isSimpleMode.value = true
    canUseBatchImage.value = true
    const { wrapper } = await mountActions()

    // /batch-image is absent from the simple sidebar but the guard still allows it, so the card
    // keeps working; only guard-blocked destinations are removed.
    expect(hrefs(wrapper)).toEqual(['/keys', '/infinite-canvas', '/usage', '/batch-image'])
    wrapper.unmount()
  })
})
