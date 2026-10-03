import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'

import DateRangePicker from '../DateRangePicker.vue'

const messages: Record<string, string> = {
  'dates.today': 'Today',
  'dates.yesterday': 'Yesterday',
  'dates.last24Hours': 'Last 24 Hours',
  'dates.last7Days': 'Last 7 Days',
  'dates.last14Days': 'Last 14 Days',
  'dates.last30Days': 'Last 30 Days',
  'dates.thisMonth': 'This Month',
  'dates.lastMonth': 'Last Month',
  'dates.startDate': 'Start Date',
  'dates.endDate': 'End Date',
  'dates.apply': 'Apply',
  'dates.selectDateRange': 'Select date range'
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => messages[key] ?? key,
    locale: ref('en')
  })
}))

const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

describe('DateRangePicker', () => {
  it('uses last 24 hours as the default recognized preset', () => {
    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)

    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: formatLocalDate(yesterday),
        endDate: formatLocalDate(now)
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    expect(wrapper.text()).toContain('Last 24 Hours')
  })

  it('emits range updates with last24Hours preset when applied', async () => {
    const now = new Date()
    const today = formatLocalDate(now)

    const wrapper = mount(DateRangePicker, {
      props: {
        startDate: today,
        endDate: today
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    await wrapper.find('.date-picker-trigger').trigger('click')
    const presetButton = wrapper.findAll('.date-picker-preset').find((node) =>
      node.text().includes('Last 24 Hours')
    )
    expect(presetButton).toBeDefined()

    await presetButton!.trigger('click')
    await wrapper.find('.date-picker-apply').trigger('click')

    const nowAfterClick = new Date()
    const yesterdayAfterClick = new Date(nowAfterClick.getTime() - 24 * 60 * 60 * 1000)
    const expectedStart = formatLocalDate(yesterdayAfterClick)
    const expectedEnd = formatLocalDate(nowAfterClick)

    expect(wrapper.emitted('update:startDate')?.[0]).toEqual([expectedStart])
    expect(wrapper.emitted('update:endDate')?.[0]).toEqual([expectedEnd])
    expect(wrapper.emitted('change')?.[0]).toEqual([
      {
        startDate: expectedStart,
        endDate: expectedEnd,
        preset: 'last24Hours'
      }
    ])
  })
})

describe('DateRangePicker second precision', () => {
  afterEach(() => vi.useRealTimers())

  const mountSeconds = () => mount(DateRangePicker, {
    props: { startDate: '2026-10-03T04:43:28', endDate: '2026-10-03T05:02:08', includeTime: true },
    global: { stubs: { Icon: true } },
  })

  it('shows and emits seconds without truncating to dates or minutes', async () => {
    const wrapper = mountSeconds()
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('04:43:28')
    expect(wrapper.text()).toContain('05:02:08')
    await wrapper.find('.date-picker-trigger').trigger('click')
    const inputs = wrapper.findAll('input[type="datetime-local"]')
    expect(inputs).toHaveLength(2)
    expect(inputs[0].attributes('step')).toBe('1')
    await inputs[1].setValue('2026-10-03T05:02:09')
    await wrapper.find('.date-picker-apply').trigger('click')
    expect(wrapper.emitted('change')?.[0]).toEqual([{
      startDate: '2026-10-03T04:43:28', endDate: '2026-10-03T05:02:09', preset: null,
    }])
    wrapper.unmount()
  })

  it('rejects reversed and empty ranges but allows a single second', async () => {
    const wrapper = mountSeconds()
    await wrapper.find('.date-picker-trigger').trigger('click')
    const inputs = wrapper.findAll('input')
    await inputs[1].setValue('2026-10-03T04:43:27')
    expect(wrapper.find('.date-picker-apply').attributes('disabled')).toBeDefined()
    await inputs[1].setValue('')
    expect(wrapper.find('.date-picker-apply').attributes('disabled')).toBeDefined()
    await inputs[1].setValue('2026-10-03T04:43:28')
    expect(wrapper.find('.date-picker-apply').attributes('disabled')).toBeUndefined()
    await wrapper.find('.date-picker-apply').trigger('click')
    expect(wrapper.emitted('change')?.[0]).toEqual([{
      startDate: '2026-10-03T04:43:28', endDate: '2026-10-03T04:43:28', preset: null,
    }])
    wrapper.unmount()
  })

  it('uses a real rolling 24-hour window including seconds', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 3, 15, 20, 37))
    const wrapper = mountSeconds()
    await wrapper.find('.date-picker-trigger').trigger('click')
    await wrapper.findAll('.date-picker-preset').find(node => node.text() === 'Last 24 Hours')!.trigger('click')
    await wrapper.find('.date-picker-apply').trigger('click')
    expect(wrapper.emitted('change')?.[0]).toEqual([{
      startDate: '2026-10-02T15:20:37', endDate: '2026-10-03T15:20:37', preset: 'last24Hours',
    }])
    wrapper.unmount()
  })
})
