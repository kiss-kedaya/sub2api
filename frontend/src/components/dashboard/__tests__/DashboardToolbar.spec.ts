import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'

vi.mock('vue-i18n', async (original) => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh' } }) }))
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => ({ path: '/', query: {}, hash: '' }),
  RouterLink: { name: 'RouterLink', props: ['to', 'custom'], template: '<a><slot /></a>' },
}))

import { h } from 'vue'
import DashboardToolbar from '../DashboardToolbar.vue'
import { statCorners } from '../dashboardUi'
import { FRAME_TARGETS, provideDashboardFrame } from '@/components/layout/dashboardFrame'

const RangeStub = defineComponent({
  name: 'DateRangePicker',
  props: ['startDate', 'endDate'],
  emits: ['update:startDate', 'update:endDate', 'change'],
  template: '<div class="range-stub" />',
})

const props = {
  startDate: '2026-10-31',
  endDate: '2026-11-01',
  granularity: 'hour',
  granularityItems: [{ value: 'day', label: 'day' }, { value: 'hour', label: 'hour' }],
  granularityLabel: 'granularity',
  refreshLabel: 'refresh',
}

describe('DashboardToolbar', () => {
  it('passes the range through and re-emits picker, granularity and refresh events', async () => {
    const wrapper = mount(DashboardToolbar, { props, global: { stubs: { DateRangePicker: RangeStub } } })
    const range = wrapper.getComponent(RangeStub)
    expect(range.props('startDate')).toBe('2026-10-31')
    expect(range.props('endDate')).toBe('2026-11-01')

    const change = { startDate: '2026-10-25', endDate: '2026-11-01', preset: '7days' }
    range.vm.$emit('update:startDate', '2026-10-25')
    range.vm.$emit('update:endDate', '2026-11-01')
    range.vm.$emit('change', change)
    expect(wrapper.emitted('update:startDate')).toEqual([['2026-10-25']])
    expect(wrapper.emitted('update:endDate')).toEqual([['2026-11-01']])
    expect(wrapper.emitted('dateRangeChange')).toEqual([[change]])

    const select = wrapper.getComponent({ name: 'Select' })
    expect(select.props('modelValue')).toBe('hour')
    select.vm.$emit('update:modelValue', 'day')
    expect(wrapper.emitted('update:granularity')).toEqual([['day']])
    expect(wrapper.emitted('granularityChange')).toEqual([['day']])

    await wrapper.get('button[aria-label="refresh"]').trigger('click')
    expect(wrapper.emitted('refresh')).toHaveLength(1)
    wrapper.unmount()
  })

  it('puts the filters on the left of the frame toolbar row and refresh on the right', async () => {
    // A minimal frame: the provider plus the two toolbar targets AppHeader renders.
    const Frame = defineComponent({
      setup() {
        provideDashboardFrame()
        return () => h('div', [
          h('div', { id: FRAME_TARGETS.toolbarStart }),
          h('div', { id: FRAME_TARGETS.toolbarEnd }),
          h(DashboardToolbar, props),
        ])
      },
    })
    const wrapper = mount(Frame, { attachTo: document.body, global: { stubs: { DateRangePicker: RangeStub } } })
    await nextTick()
    const start = wrapper.get(`#${FRAME_TARGETS.toolbarStart}`)
    const end = wrapper.get(`#${FRAME_TARGETS.toolbarEnd}`)
    expect(start.find('.range-stub').exists()).toBe(true)
    expect(start.find('button[aria-label="refresh"]').exists()).toBe(false)
    expect(end.find('button[aria-label="refresh"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('disables refresh while loading', async () => {
    const wrapper = mount(DashboardToolbar, { props: { ...props, refreshing: true }, global: { stubs: { DateRangePicker: RangeStub } } })
    expect(wrapper.get('button[aria-label="refresh"]').attributes('disabled')).toBeDefined()
    await wrapper.get('button[aria-label="refresh"]').trigger('click')
    expect(wrapper.emitted('refresh')).toBeUndefined()
    wrapper.unmount()
  })
})

describe('statCorners', () => {
  it('rounds only the outer corners of a full two-row grid', () => {
    const corners = Array.from({ length: 8 }, (_, index) => statCorners(index, 8))
    expect(corners[0]).toContain('lg:rounded-tl-lg')
    expect(corners[3]).toContain('lg:rounded-tr-lg')
    expect(corners[4]).toContain('lg:rounded-bl-lg')
    expect(corners[7]).toContain('lg:rounded-br-lg')
    for (const index of [1, 2, 5, 6]) expect(corners[index]).toBe('lg:rounded-none')
  })

  it('keeps a short last row one block (seven tiles)', () => {
    const corners = Array.from({ length: 7 }, (_, index) => statCorners(index, 7))
    // Tile 4 (top right) has nothing below it, so its bottom-right corner is outside.
    expect(corners[3]).toContain('lg:rounded-tr-lg')
    expect(corners[3]).toContain('lg:rounded-br-lg')
    expect(corners[6]).toContain('lg:rounded-br-lg')
    expect(corners[6]).not.toContain('lg:rounded-tr-lg')
    expect(corners[4]).toContain('lg:rounded-bl-lg')
  })
})
