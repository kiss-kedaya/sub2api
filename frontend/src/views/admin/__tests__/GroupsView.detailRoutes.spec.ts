import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineComponent } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AdminGroup } from '@/types'
import GroupsView from '@/views/admin/GroupsView.vue'

const api = vi.hoisted(() => ({
  list: vi.fn(),
  getById: vi.fn(),
  update: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))
const authState = vi.hoisted(() => ({ isSimpleMode: false }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: api.list,
      getById: api.getById,
      update: api.update,
      getModelAllowlistCandidates: api.getModelAllowlistCandidates,
      getUsageSummary: api.getUsageSummary,
      getCapacitySummary: api.getCapacitySummary,
      getLiveCapability: api.getLiveCapability,
      getAll: vi.fn(),
      create: vi.fn(),
      delete: vi.fn(),
      duplicate: vi.fn(),
      updateSortOrder: vi.fn(),
    },
    accounts: { list: vi.fn(), getById: vi.fn() },
  },
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.showSuccess, showError: api.showError }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => authState }))
vi.mock('@/stores/onboarding', () => ({ useOnboardingStore: () => ({ isCurrentStep: vi.fn(() => false), nextStep: vi.fn() }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

enableAutoUnmount(afterEach)

const group = {
  id: 42, name: 'Primary', description: null, platform: 'anthropic', rate_multiplier: 1, rpm_limit: 0,
  is_exclusive: false, status: 'active', subscription_type: 'standard',
  daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null,
  allow_image_generation: false, image_rate_independent: false, image_rate_multiplier: 1,
  image_price_1k: null, image_price_2k: null, image_price_4k: null,
  claude_code_only: false, fallback_group_id: null, fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false, default_mapped_model: '', require_oauth_only: false, require_privacy_set: false,
  created_at: '2026-07-16T00:00:00Z', updated_at: '2026-07-16T00:00:00Z',
  model_routing: null, model_routing_enabled: false, mcp_xml_inject: true, supported_model_scopes: [],
  account_count: 1, active_account_count: 1, rate_limited_account_count: 0, model_allowlist: undefined, sort_order: 10,
} as unknown as AdminGroup

const SECTIONS = ['basic', 'billing', 'models', 'pricing', 'dispatch', 'advanced']

const stubs = {
  AppLayout: defineComponent({ template: '<main><slot /></main>' }),
  TablePageLayout: defineComponent({ template: '<section data-test="list"><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' }),
  DataTable: defineComponent({
    props: { data: { type: Array, default: () => [] } },
    template: '<div><div v-for="row in data" :key="row.id" :data-row="row.id"><slot name="cell-name" :row="row" :value="row.name" /><slot name="cell-actions" :row="row" /></div></div>',
  }),
  BaseDialog: defineComponent({ props: { show: Boolean }, template: '<div v-if="show"><slot /><slot name="footer" /></div>' }),
  Pagination: true, ConfirmDialog: true, EmptyState: true, Select: true, PlatformIcon: true, Icon: true,
  GroupCapacityBadge: true, GroupRateMultipliersModal: true, GroupRPMOverridesModal: true, VueDraggable: true,
}

function makeRouter(): Router {
  const page = { template: '<div />' }
  // Mirrors the group routes in src/router/index.ts (checked by the last test).
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/admin/groups', name: 'AdminGroups', component: page },
      { path: '/admin/groups/:id(\\d+)', redirect: (to) => ({ name: 'AdminGroupDetail', params: { id: to.params.id, section: 'basic' } }) },
      { path: '/admin/groups/:id(\\d+)/:section(basic|billing|models|pricing|dispatch|advanced)', name: 'AdminGroupDetail', component: page },
    ],
  })
}

async function mountView(router?: Router) {
  const wrapper = mount(GroupsView, { global: { plugins: router ? [router] : [], stubs } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

const editButton = (wrapper: ReturnType<typeof mount>) =>
  wrapper.get('[data-row="42"]').findAll('button').find((button) => button.text() === 'common.edit')!
// v-show keeps every section mounted; only the active one is displayed.
const shown = (wrapper: ReturnType<typeof mount>, section: string) =>
  (wrapper.get(`[data-group-section="${section}"]`).element as HTMLElement).style.display !== 'none'
const listShown = (wrapper: ReturnType<typeof mount>) =>
  (wrapper.get('[data-test="list"]').element as HTMLElement).style.display !== 'none'

describe('GroupsView group detail sub-routes', () => {
  beforeEach(() => {
    authState.isSimpleMode = false
    localStorage.clear()
    for (const fn of Object.values(api)) fn.mockReset()
    api.list.mockResolvedValue({ items: [group], total: 1, page: 1, page_size: 20, pages: 1 })
    api.update.mockResolvedValue(group)
    api.getModelAllowlistCandidates.mockResolvedValue([])
    api.getUsageSummary.mockResolvedValue([])
    api.getCapacitySummary.mockResolvedValue([])
    api.getLiveCapability.mockResolvedValue({ supported: false })
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })
  afterEach(() => vi.restoreAllMocks())

  it('opens the detail on its basic sub-route and shows one section per sub-route', async () => {
    const router = makeRouter()
    await router.push('/admin/groups')
    const wrapper = await mountView(router)

    await editButton(wrapper).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/admin/groups/42/basic')
    expect(listShown(wrapper)).toBe(false)
    expect(SECTIONS.filter((section) => shown(wrapper, section))).toEqual(['basic'])
    const links = wrapper.get('[data-testid="group-detail-nav"]').findAll('[data-slot="link"]')
    expect(links.map((link) => link.attributes('href'))).toEqual(SECTIONS.map((section) => `/admin/groups/42/${section}`))

    await router.push('/admin/groups/42/pricing')
    await flushPromises()
    expect(SECTIONS.filter((section) => shown(wrapper, section))).toEqual(['pricing'])
    expect(api.getById).not.toHaveBeenCalled()
  })

  it('opens a linked group directly and keeps old /admin/groups/:id links working', async () => {
    const router = makeRouter()
    await router.push('/admin/groups/42')
    expect(router.currentRoute.value.fullPath).toBe('/admin/groups/42/basic')
    await router.push('/admin/groups/42/billing')
    const wrapper = await mountView(router)

    expect(wrapper.find('#edit-group-form').exists()).toBe(true)
    expect(SECTIONS.filter((section) => shown(wrapper, section))).toEqual(['billing'])
    expect(api.getById).not.toHaveBeenCalled()
  })

  it('loads a group from another list page, and returns to the list when the group is gone', async () => {
    api.getById.mockResolvedValueOnce({ ...group, id: 77, name: 'Far away' })
    const router = makeRouter()
    await router.push('/admin/groups/77/basic')
    const wrapper = await mountView(router)
    expect(api.getById).toHaveBeenCalledWith(77)
    expect(wrapper.text()).toContain('Far away')

    api.getById.mockRejectedValueOnce(new Error('not found'))
    await router.push('/admin/groups/88/basic')
    await flushPromises()
    await flushPromises()
    expect(api.showError).toHaveBeenCalledWith('admin.groups.detail.notFound')
    expect(router.currentRoute.value.name).toBe('AdminGroups')
    expect(wrapper.find('#edit-group-form').exists()).toBe(false)
  })

  it('goes back to the list from the detail, and after saving', async () => {
    const router = makeRouter()
    await router.push('/admin/groups')
    const wrapper = await mountView(router)

    await editButton(wrapper).trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="group-detail-back"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('AdminGroups')
    expect(wrapper.find('#edit-group-form').exists()).toBe(false)
    expect(listShown(wrapper)).toBe(true)

    await editButton(wrapper).trigger('click')
    await flushPromises()
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(api.update).toHaveBeenCalledWith(42, expect.objectContaining({ name: 'Primary' }))
    expect(router.currentRoute.value.name).toBe('AdminGroups')
    expect(listShown(wrapper)).toBe(true)
  })

  it('switches sections locally without a router, and an invalid field brings its section back', async () => {
    const wrapper = await mountView()
    await editButton(wrapper).trigger('click')
    await flushPromises()

    const links = wrapper.get('[data-testid="group-detail-nav"]').findAll('[data-slot="link"]')
    expect(links).toHaveLength(SECTIONS.length)
    await links[1].trigger('click')
    expect(SECTIONS.filter((section) => shown(wrapper, section))).toEqual(['billing'])

    await wrapper.get('#edit-group-form input[type="text"]').setValue('')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(api.update).not.toHaveBeenCalled()
    expect(SECTIONS.filter((section) => shown(wrapper, section))).toEqual(['basic'])
  })

  it('shows only the basics and no sub-nav in simple mode', async () => {
    authState.isSimpleMode = true
    const wrapper = await mountView()
    await editButton(wrapper).trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="group-detail-nav"]').exists()).toBe(false)
    expect(shown(wrapper, 'basic')).toBe(true)
    expect(wrapper.find('[data-group-section="billing"]').exists()).toBe(false)
  })

  it('registers the detail sub-routes, the old-link redirect and the breadcrumb parent', () => {
    const source = readFileSync(resolve('src/router/index.ts'), 'utf8')
    expect(source).toContain("path: '/admin/groups/:id(\\\\d+)/:section(basic|billing|models|pricing|dispatch|advanced)'")
    expect(source).toMatch(/path: '\/admin\/groups\/:id\(\\\\d\+\)',\s*redirect: /)
    expect(source).toContain("name: 'AdminGroupDetail'")
    expect(source).toContain("parent: 'AdminGroups'")
  })
})
