import { nextTick } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { shallowMount } from '@vue/test-utils'

import Select from '@/components/common/Select.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import AccountTableFilters from '../AccountTableFilters.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const filters = () => ({
  search: 'retained search',
  platform: 'openai',
  type: 'oauth',
  status: 'active',
  privacy_mode: 'training_off',
  group: '12',
  page: 4,
  external_scope: 'retained'
})

const mountFilters = () => shallowMount(AccountTableFilters, {
  props: {
    searchQuery: 'retained search',
    filters: filters(),
    groups: [{ id: 12, name: 'Core pool' } as any]
  }
})

describe('AccountTableFilters', () => {
  it('leaves search reload to the parent model listener instead of refreshing twice', async () => {
    const wrapper = mountFilters()
    wrapper.getComponent(SearchInput).vm.$emit('update:modelValue', 'grok')
    wrapper.getComponent(SearchInput).vm.$emit('search', 'grok')
    await nextTick()
    expect(wrapper.emitted('update:searchQuery')).toEqual([['grok']])
    expect(wrapper.emitted('change')).toBeUndefined()
    wrapper.unmount()
  })

  it('keeps focus on a remaining tag or the filter toggle after removing conditions', async () => {
    const wrapper = shallowMount(AccountTableFilters, { attachTo: document.body, props: {
      searchQuery: '', filters: filters(),
      'onUpdate:filters': value => wrapper.setProps({ filters: value }),
    } })
    await wrapper.get('[data-filter-key="platform"]').trigger('click')
    await nextTick()
    expect(document.activeElement).toBe(wrapper.get('[data-filter-key="type"]').element)
    await wrapper.get('[data-test="reset-account-filters"]').trigger('click')
    await nextTick()
    expect(wrapper.findAll('.account-filter-tag')).toHaveLength(0)
    expect(document.activeElement).toBe(wrapper.get('.account-filter-toggle').element)
    wrapper.unmount()
  })

  it('collapses advanced filters on every viewport and exposes its state accessibly', async () => {
    const wrapper = mountFilters()
    const toggle = wrapper.get('.account-filter-toggle')

    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(toggle.attributes('aria-controls')).toBe('account-advanced-filters')
    expect(wrapper.get('#account-advanced-filters').attributes('style')).toContain('display: none')

    await toggle.trigger('click')

    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('#account-advanced-filters').attributes('style')).toBe('')
  })

  it('emits exactly one change when a Select updates a filter', async () => {
    const wrapper = mountFilters()
    const platformSelect = wrapper.findAllComponents(Select)[0]

    platformSelect.vm.$emit('update:modelValue', 'gemini')
    platformSelect.vm.$emit('change', 'gemini', { value: 'gemini', label: 'Gemini' })
    await nextTick()

    expect(wrapper.emitted('update:filters')).toHaveLength(1)
    expect(wrapper.emitted('update:filters')?.[0]?.[0]).toMatchObject({
      search: 'retained search',
      platform: 'gemini',
      page: 4,
      external_scope: 'retained'
    })
    expect(wrapper.emitted('change')).toHaveLength(1)
  })

  it('shows active conditions and refreshes once when one is cleared', async () => {
    const wrapper = mountFilters()

    expect(wrapper.findAll('.account-filter-tag')).toHaveLength(5)
    await wrapper.get('[data-filter-key="platform"]').trigger('click')

    expect(wrapper.emitted('update:filters')).toHaveLength(1)
    expect(wrapper.emitted('update:filters')?.[0]?.[0]).toMatchObject({
      search: 'retained search',
      platform: '',
      type: 'oauth',
      page: 4,
      external_scope: 'retained'
    })
    expect(wrapper.emitted('change')).toHaveLength(1)
  })

  it('resets only the five managed filters and preserves search and external params', async () => {
    const wrapper = mountFilters()

    await wrapper.get('[data-test="reset-account-filters"]').trigger('click')

    expect(wrapper.emitted('update:filters')).toHaveLength(1)
    expect(wrapper.emitted('update:filters')?.[0]?.[0]).toEqual({
      search: 'retained search',
      platform: '',
      type: '',
      status: '',
      privacy_mode: '',
      group: '',
      page: 4,
      external_scope: 'retained'
    })
    expect(wrapper.emitted('change')).toHaveLength(1)
  })
})
