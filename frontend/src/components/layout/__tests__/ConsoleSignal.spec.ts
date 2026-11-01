import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import UApp from '@nuxt/ui/components/App.vue'
import { useAppStore, useAuthStore, useOnboardingStore, useSubscriptionStore } from '@/stores'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import AppLayout from '../AppLayout.vue'

vi.mock('@/composables/useOnboardingTour', () => ({
  useOnboardingTour: () => ({ replayTour: vi.fn() })
}))

vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({ canUseBatchImage: false, refreshBatchImageAccess: vi.fn() })
}))

const userPaths = ['/dashboard', '/keys', '/usage', '/monitor', '/available-channels', '/purchase',
  '/orders', '/subscriptions', '/profile', '/redeem', '/affiliate',
  '/payment/qrcode', '/payment/result', '/payment/stripe', '/payment/airwallex',
  '/model-plaza', '/batch-image', '/infinite-canvas', '/custom/demo']
const adminPaths = ['/admin/dashboard', '/admin/accounts', '/admin/groups', '/admin/users', '/admin/keys', '/admin/usage']
const leftoverPaths = ['/login', '/keys/other', '/usage-extra']
let wrapper: VueWrapper | undefined

// The page puts its title and one action in the frame through the shared header.
const PAGE = `
  <UserPageHeader title="Content title">
    <template #actions><button id="header-action">Header action</button></template>
  </UserPageHeader>
  <button id="page-action">Page action</button>`

async function renderShell(path: string, role: 'user' | 'admin' = 'user') {
  const pinia = createPinia()
  setActivePinia(pinia)
  const app = useAppStore()
  app.siteName = 'SIGNAL'
  app.publicSettingsLoaded = true
  app.docUrl = '/docs'
  app.cachedPublicSettings = { model_plaza_enabled: true } as NonNullable<typeof app.cachedPublicSettings>
  const auth = useAuthStore()
  auth.user = {
    id: 1,
    role,
    username: 'Local Test',
    email: 'test@example.test',
    balance: 20,
    frozen_balance: 3
  } as NonNullable<typeof auth.user>
  useAdminSettingsStore().fetch = vi.fn().mockResolvedValue(undefined)
  useSubscriptionStore().activeSubscriptions = [{ id: 7, group_id: 1 }] as never

  // Detail pages name their list page in meta.parent (route name or path) for the breadcrumb.
  const detailRoutes = [
    { path: '/admin/groups/:id', name: 'AdminGroupDetail', component: { template: '<div />' }, meta: { title: 'Group detail', parent: 'AdminGroupsList' } },
    { path: '/admin/groups/:id/pricing', name: 'AdminGroupPricing', component: { template: '<div />' }, meta: { title: 'Pricing', parent: 'AdminGroupDetail' } },
    { path: '/admin/groups-list', name: 'AdminGroupsList', component: { template: '<div />' }, meta: { title: 'Groups' } },
    { path: '/usage/detail', component: { template: '<div />' }, meta: { title: 'Usage detail', parent: '/usage' } }
  ]
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [...detailRoutes, ...[...new Set([...userPaths, ...adminPaths, ...leftoverPaths, '/:pathMatch(.*)*'])].map((routePath) => ({
      path: routePath,
      component: { template: '<div />' },
      meta: { title: routePath === '/keys' ? 'API Keys' : routePath }
    }))]
  })
  await router.push(path)
  await router.isReady()

  // UApp provides the tooltip / overlay context the sidebar rail and menus need.
  const Harness = defineComponent({
    components: { AppLayout, UserPageHeader },
    template: `<AppLayout>${PAGE}</AppLayout>`
  })
  wrapper = mount({ render: () => h(UApp, null, { default: () => h(Harness) }) }, {
    attachTo: document.body,
    global: {
      plugins: [pinia, router, createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
      stubs: { AnnouncementBell: true, SubscriptionProgressMini: true, VersionBadge: true }
    }
  })
  await flushPromises()
  return { router, app }
}

const frame = () => wrapper!.get('.console-frame')
const navbar = () => wrapper!.get('header.console-navbar')
// Top-level sidebar entries (groups and pages), in order.
const groupLabels = () => [...wrapper!.get('.sidebar-nav [data-slot="list"]').element.children]
  .filter((item) => item.tagName === 'LI')
  .map((item) => item.querySelector('[data-slot="linkLabel"]')?.textContent?.trim() ?? '')
  .filter(Boolean)

beforeEach(() => {
  localStorage.clear()
  document.documentElement.classList.remove('dark')
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
})

describe('console frame (Nuxt UI dashboard)', () => {
  it.each([...userPaths, '/keys?search=test#active', '/keys/'])('opts in %s and names the page once, in the navbar', async (path) => {
    await renderShell(path)
    expect(frame().classes()).toContain('console-signal')
    expect(wrapper!.find('.bg-mesh-gradient').exists()).toBe(false)
    // One heading for the page: the navbar title, filled by the page header.
    expect(document.querySelectorAll('h1')).toHaveLength(1)
    expect(navbar().get('h1').text()).toBe('Content title')
    // The header's action joins the navbar; the rest of the page stays in the panel body.
    expect(navbar().find('#header-action').exists()).toBe(true)
    expect(wrapper!.get('main.signal-main #page-action').text()).toBe('Page action')
    expect(wrapper!.get('.sidebar-brand-link').attributes('href')).toBe('/dashboard')
  })

  it.each(leftoverPaths)('still applies the shared theme on leftover AppLayout routes like %s', async (path) => {
    await renderShell(path)
    expect(frame().classes()).toContain('console-signal')
    expect(wrapper!.find('.bg-mesh-gradient').exists()).toBe(false)
    expect(wrapper!.find('.signal-header').exists()).toBe(true)
  })

  it.each(adminPaths)('applies the admin workspace on %s and names it in the navbar', async (path) => {
    await renderShell(path, 'admin')
    expect(frame().classes()).toContain('signal-admin')
    expect(navbar().get('h1').text()).toBe('Content title')
    expect(wrapper!.get('.sidebar-brand-link').attributes('href')).toBe('/admin/dashboard')
  })

  it('switches between admin and personal workspace without leaving theme state behind', async () => {
    const { router } = await renderShell('/keys', 'admin')
    expect(frame().classes()).toContain('console-signal')
    expect(wrapper!.find('.console-sidebar a[href="/admin/dashboard"]').exists()).toBe(true)
    // On a personal page the admin groups are there, closed.
    expect(groupLabels()).toContain('nav.groupAccountsChannels')
    await router.push('/admin/dashboard')
    expect(frame().classes()).toContain('console-signal')
    expect(frame().classes()).toContain('signal-admin')
    expect(document.body.classList.contains('console-signal')).toBe(false)
    await router.push('/usage')
    expect(frame().classes()).toContain('console-signal')
    expect(frame().classes()).not.toContain('signal-admin')
  })

  it.each(['/model-plaza?embedded=1', '/infinite-canvas', '/batch-image', '/custom/demo'])('returns administrators to their dashboard from %s', async (path) => {
    const { router } = await renderShell(path, 'admin')
    const home = wrapper!.get('.sidebar-brand-link')
    expect(home.attributes('href')).toBe('/admin/dashboard')
    await home.trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/admin/dashboard'))
  })

  it('keeps collapse, mobile navigation, theme and balance semantics', async () => {
    const { app } = await renderShell('/keys')
    expect(wrapper!.get('.header-balance-value').text()).toBe('$20.00')
    expect(wrapper!.get('.header-balance-frozen').text()).toContain('$3.00')
    expect(wrapper!.get('.header-balance-detail').text()).toContain('$23.00')
    expect(wrapper!.get('.header-balance').attributes('tabindex')).toBe('0')

    // UDashboardSidebarCollapse sits in the navbar and folds the sidebar to its rail.
    await navbar().get('[data-test="sidebar-collapse"]').trigger('click')
    await nextTick()
    expect(app.sidebarCollapsed).toBe(true)
    expect(wrapper!.get('.console-sidebar').attributes('data-collapsed')).toBe('true')
    // Phones: UDashboardSidebarToggle opens the drawer.
    await navbar().get('[data-test="sidebar-toggle"]').trigger('click')
    await nextTick()
    expect(app.mobileOpen).toBe(true)
    const wasDark = document.documentElement.classList.contains('dark')
    await wrapper!.get('[data-test="theme-toggle"]').trigger('click')
    expect(document.documentElement.classList.contains('dark')).toBe(!wasDark)
    // The drawer (teleported) lists the groups open, with their pages as links.
    await vi.waitFor(() => expect(document.body.querySelector('[role="dialog"] a[href="/keys"]')).not.toBeNull())
    expect(document.body.querySelector('[role="dialog"] a[href="/usage"]')).not.toBeNull()
  })

  it('keeps the light/dark toggle in step with the real dark class on <html>', async () => {
    await renderShell('/keys')
    // Icon (sun / moon) and label come from the same isDark, read from <html>.
    const label = () => wrapper!.get('[data-test="theme-toggle"]').attributes('aria-label')
    expect(label()).toBe('Switch to dark mode')
    // Something else (a preview, another page's toggle) switches the class directly.
    document.documentElement.classList.add('dark')
    await vi.waitFor(() => expect(label()).toBe('Switch to light mode'))
    await wrapper!.get('[data-test="theme-toggle"]').trigger('click')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
    await vi.waitFor(() => expect(label()).toBe('Switch to dark mode'))
  })

  it('moves docs, the model plaza and subscription status to the sidebar bottom group', async () => {
    await renderShell('/keys')
    const bottom = wrapper!.get('.sidebar-secondary')
    expect(bottom.find('a[href="/docs"]').exists()).toBe(true)
    expect(bottom.find('a[href="/model-plaza?embedded=1"]').exists()).toBe(true)
    expect(bottom.find('.sidebar-subscriptions').exists()).toBe(true)
    // The navbar keeps only notifications, theme and balance besides page actions.
    expect(navbar().find('a[href="/docs"]').exists()).toBe(false)
    expect(navbar().find('a[href="/tickets"]').exists()).toBe(false)
  })

  it('opens the user menu with account, preferences (theme, language) and logout', async () => {
    await renderShell('/keys')
    await wrapper!.get('.sidebar-footer [data-test="user-menu"]').trigger('keydown', { key: 'Enter' })
    await vi.waitFor(() => expect(document.body.textContent).toContain('nav.logout'))
    for (const key of ['nav.profile', 'nav.apiKeys', 'nav.language', 'test@example.test']) {
      expect(document.body.textContent).toContain(key)
    }
  })

  it('puts the user menu in the sidebar footer and keeps every collapsed group reachable', async () => {
    const { app } = await renderShell('/admin/channels/pricing', 'admin')
    expect(wrapper!.find('.sidebar-footer [data-test="user-menu"]').exists()).toBe(true)
    expect(wrapper!.find('.sidebar-footer button:last-child').attributes('data-test')).toBe('user-menu')
    // Expanded: the active group opens and lists its children as links.
    expect(wrapper!.find('.sidebar-nav [data-slot="childList"] a[href="/admin/channels/pricing"]').exists()).toBe(true)
    app.sidebarCollapsed = true
    await flushPromises()
    // Collapsed rail: no inline children, the group becomes a popover trigger with its label.
    expect(wrapper!.find('.sidebar-nav [data-slot="childList"]').exists()).toBe(false)
    const trigger = wrapper!.get('.sidebar-nav [aria-label="nav.groupAccountsChannels"]')
    expect(trigger.attributes('aria-haspopup')).toBe('dialog')
    // The rail button of the group holding the current page is drawn active (primary pill).
    expect(trigger.classes()).toContain('text-primary')
    await trigger.trigger('click')
    await vi.waitFor(() => expect(document.body.querySelector('.console-flyout-list a[href="/admin/channels/pricing"]')).not.toBeNull())
  })

  it('regroups the admin sidebar into closed groups; the current page opens its own', async () => {
    const { app } = await renderShell('/admin/account-ops', 'admin')
    expect(groupLabels()).toEqual(['nav.groupOverview', 'nav.groupAccountsChannels', 'nav.groupUsersBilling', 'nav.groupOperations', 'nav.groupMonitoring', 'nav.groupSystem', 'nav.myAccount'])
    // Only "运营" is open (it holds the page), with the former ops-center tiers as pages.
    const open = wrapper!.findAll('.sidebar-nav [data-slot="childList"] a').map((link) => link.attributes('href'))
    expect(open).toEqual(['/admin/ops-center', '/admin/account-ops', '/admin/ops-automation', '/admin/credential-guard', '/admin/channel-operations', '/admin/announcements', '/admin/tickets'])
    expect(wrapper!.find('.sidebar-nav a[href="/admin/account-ops"][aria-current="page"]').exists()).toBe(true)
    // Security appears with its feature flag.
    app.cachedPublicSettings = { ...app.cachedPublicSettings, risk_control_enabled: true } as NonNullable<typeof app.cachedPublicSettings>
    await nextTick()
    expect(groupLabels()).toContain('nav.groupSecurity')
  })

  it('shows section headers inside the admin "My account" group', async () => {
    await renderShell('/keys', 'admin')
    const headers = wrapper!.findAll('.sidebar-nav .sidebar-child-section').map((node) => node.text())
    expect(headers).toEqual(['nav.groupOverview', 'nav.groupApiUsage', 'nav.groupRecharge', 'nav.groupHelpAccount'])
    expect(wrapper!.find('.sidebar-nav a[href="/keys"][aria-current="page"]').exists()).toBe(true)
  })

  it('gives users four open groups', async () => {
    await renderShell('/keys')
    expect(groupLabels()).toEqual(['nav.groupOverview', 'nav.groupApiUsage', 'nav.groupRecharge', 'nav.groupHelpAccount'])
    for (const href of ['/dashboard', '/keys', '/usage', '/purchase', '/tickets', '/profile']) {
      expect(wrapper!.find(`.sidebar-nav a[href="${href}"]`).exists(), href).toBe(true)
    }
  })

  it('shows a breadcrumb back to the parent page on detail pages (meta.parent)', async () => {
    const { router } = await renderShell('/usage/detail')
    const crumbs = navbar().findAll('.console-crumbs a')
    expect(crumbs.map((crumb) => [crumb.text(), crumb.attributes('href')])).toEqual([['/usage', '/usage']])
    // Phones get a back button to the same page.
    expect(navbar().get('.console-back').attributes('href')).toBe('/usage')
    await crumbs[0].trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/usage'))
  })

  it('follows meta.parent by route name, keeping the route params, through two levels', async () => {
    await renderShell('/admin/groups/42/pricing', 'admin')
    const crumbs = navbar().findAll('.console-crumbs a').map((crumb) => [crumb.text(), crumb.attributes('href')])
    expect(crumbs).toEqual([['Groups', '/admin/groups-list'], ['Group detail', '/admin/groups/42']])
  })

  it('shows no breadcrumb on top-level pages', async () => {
    await renderShell('/keys')
    expect(navbar().find('.console-crumbs').exists()).toBe(false)
    expect(navbar().find('.console-back').exists()).toBe(false)
  })

  it('anchors the onboarding tour on the sidebar links (their groups open while it runs)', async () => {
    await renderShell('/admin/dashboard', 'admin')
    // Closed groups first: the tour's links are not rendered yet.
    expect(wrapper!.find('[data-tour="sidebar-channel-manage"]').exists()).toBe(false)
    useOnboardingStore().setDriverInstance({ isActive: () => true } as never)
    await vi.waitFor(() => expect(wrapper!.find('[data-tour="sidebar-channel-manage"]').exists()).toBe(true))
    expect(wrapper!.find('.sidebar-nav a[href="/admin/groups"][data-tour="sidebar-group-manage"]').exists()).toBe(true)
    expect(wrapper!.find('.sidebar-nav a[href="/admin/accounts"][data-tour="sidebar-channel-manage"]').exists()).toBe(true)
    expect(wrapper!.find('.sidebar-nav a[href="/keys"][data-tour="sidebar-my-keys"]').exists()).toBe(true)
  })
})
