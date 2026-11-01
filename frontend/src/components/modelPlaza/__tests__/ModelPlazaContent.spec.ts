import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import ModelPlazaContent from '../ModelPlazaContent.vue'
import PlazaGroupCard from '../PlazaGroupCard.vue'
import PlazaGroupSection from '../PlazaGroupSection.vue'
import type { ModelPlazaGroup, ModelPlazaResponse, PlazaModel } from '@/api/modelPlaza'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAuthenticated: true }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ cachedPublicSettings: null }) }))

const model = (name: string): PlazaModel => ({
  name,
  platform: 'anthropic',
  pricing: { billing_mode: 'token', input_price: 3e-6, output_price: 1.5e-5, cache_write_price: null, cache_read_price: null, image_input_price: null, image_output_price: null, per_request_price: null, intervals: [] },
  official_pricing: null
})

const group = (id: number, name: string, models: PlazaModel[], rate = 1): ModelPlazaGroup => ({
  id, name, description: '', platform: 'anthropic', subscription_type: 'standard', rate_multiplier: rate,
  peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1, is_exclusive: false,
  image_rate_independent: false, image_rate_multiplier: 1, long_context_pricing_enabled: true, models
})

const response: ModelPlazaResponse = {
  description: '',
  groups: [group(1, 'Claude', [model('claude-sonnet'), model('claude-haiku')], 0.8), group(2, 'Fast', [model('claude-haiku')], 0.5)]
} as ModelPlazaResponse

async function mountPlaza(path = '/model-plaza') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/model-plaza', component: { template: '<div />' } }]
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(ModelPlazaContent, {
    props: { response, loading: false },
    global: {
      plugins: [router],
      stubs: {
        teleport: true,
        UserPageHeader: true,
        PlazaFilterBar: true,
        GroupBadge: { props: ['name'], template: '<span class="group-badge">{{ name }}</span>' },
        PlazaModelPricingTable: { props: ['models'], template: '<div class="pricing-table">{{ models.map((m) => m.name).join(",") }}</div>' }
      }
    }
  })
  await flushPromises()
  return { wrapper, router }
}

describe('ModelPlazaContent grid and detail', () => {
  it('shows one card per group, cheapest first, linking to the group detail', async () => {
    const { wrapper } = await mountPlaza('/model-plaza?embedded=1')
    const cards = wrapper.findAllComponents(PlazaGroupCard)
    expect(cards.map((card) => card.props('group').name)).toEqual(['Fast', 'Claude'])
    // The card link keeps the page's own query (embedded=1) and adds the group.
    expect(cards[1].props('to')).toEqual({ path: '/model-plaza', query: { embedded: '1', group: '1' } })
    expect(cards[1].text()).toContain('claude-sonnet')
    expect(cards[1].text()).toContain('modelPlaza.detail.modelCount')
    // No price table until a group is opened.
    expect(wrapper.find('.pricing-table').exists()).toBe(false)
  })

  it('opens the group from ?group= and closes it back to the list', async () => {
    const { wrapper, router } = await mountPlaza('/model-plaza?group=1')
    const detail = wrapper.findComponent(PlazaGroupSection)
    expect(detail.exists()).toBe(true)
    expect(detail.props('plain')).toBe(true)
    expect(detail.props('group').name).toBe('Claude')
    expect(wrapper.get('.pricing-table').text()).toBe('claude-sonnet,claude-haiku')

    wrapper.findComponent({ name: 'Slideover' }).vm.$emit('update:open', false)
    await flushPromises()
    expect(router.currentRoute.value.query.group).toBeUndefined()
    expect(wrapper.findComponent(PlazaGroupSection).exists()).toBe(false)
  })

  it('ignores an unknown group id', async () => {
    const { wrapper } = await mountPlaza('/model-plaza?group=99')
    expect(wrapper.findComponent(PlazaGroupSection).exists()).toBe(false)
    expect(wrapper.findAllComponents(PlazaGroupCard)).toHaveLength(2)
  })
})
