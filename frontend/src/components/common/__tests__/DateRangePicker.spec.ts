import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'

import DateRangePicker from '../DateRangePicker.vue'
import { bodyFindAll, bodyGet } from './overlay-helpers'

// The range panel is a Nuxt UI popover teleported to <body>.
// after-hooks run last-registered-first: unmount the wrappers, then clear what is left in <body>.
afterEach(() => {
  document.body.innerHTML = ''
})
enableAutoUnmount(afterEach)

const presetButton = (label: string) => bodyFindAll('[data-test="date-range-preset"]').find((node) => node.text() === label)
const applyButton = () => bodyGet('[data-test="date-range-apply"]')

async function openPicker(wrapper: { find: (selector: string) => { trigger: (event: string) => Promise<void> } }) {
  await wrapper.find('[data-test="date-range-trigger"]').trigger('click')
  await flushPromises()
}

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
      attachTo: document.body,
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

    await openPicker(wrapper)
    const last24 = bodyFindAll('[data-test="date-range-preset"]').find((node) =>
      node.text().includes('Last 24 Hours')
    )
    expect(last24).toBeDefined()
    // The calendar grid renders next to the presets.
    expect(document.body.querySelector('[data-test="date-range-panel"] [data-slot="grid"]')).not.toBeNull()

    await last24!.trigger('click')
    expect(last24!.attributes('aria-pressed')).toBe('true')
    await applyButton().trigger('click')

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
    attachTo: document.body,
    props: { startDate: '2026-10-03T04:43:28', endDate: '2026-10-03T05:02:08', includeTime: true },
    global: { stubs: { Icon: true } },
  })

  it('shows and emits seconds without truncating to dates or minutes', async () => {
    const wrapper = mountSeconds()
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('04:43:28')
    expect(wrapper.text()).toContain('05:02:08')
    await openPicker(wrapper)
    const inputs = bodyFindAll<HTMLInputElement>('input[type="datetime-local"]')
    expect(inputs).toHaveLength(2)
    expect(inputs[0].attributes('step')).toBe('1')
    await inputs[1].setValue('2026-10-03T05:02:09')
    await applyButton().trigger('click')
    expect(wrapper.emitted('change')?.[0]).toEqual([{
      startDate: '2026-10-03T04:43:28', endDate: '2026-10-03T05:02:09', preset: null,
    }])
    wrapper.unmount()
  })

  it('rejects reversed and empty ranges but allows a single second', async () => {
    const wrapper = mountSeconds()
    await openPicker(wrapper)
    const inputs = bodyFindAll<HTMLInputElement>('[data-test="date-range-panel"] input')
    await inputs[1].setValue('2026-10-03T04:43:27')
    expect(applyButton().attributes('disabled')).toBeDefined()
    await inputs[1].setValue('')
    expect(applyButton().attributes('disabled')).toBeDefined()
    await inputs[1].setValue('2026-10-03T04:43:28')
    expect(applyButton().attributes('disabled')).toBeUndefined()
    await applyButton().trigger('click')
    expect(wrapper.emitted('change')?.[0]).toEqual([{
      startDate: '2026-10-03T04:43:28', endDate: '2026-10-03T04:43:28', preset: null,
    }])
    wrapper.unmount()
  })

  it('uses a real rolling 24-hour window including seconds', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 3, 15, 20, 37))
    const wrapper = mountSeconds()
    await openPicker(wrapper)
    await presetButton('Last 24 Hours')!.trigger('click')
    await applyButton().trigger('click')
    expect(wrapper.emitted('change')?.[0]).toEqual([{
      startDate: '2026-10-02T15:20:37', endDate: '2026-10-03T15:20:37', preset: 'last24Hours',
    }])
    wrapper.unmount()
  })
})

describe('DateRangePicker refreshActivePreset', () => {
  afterEach(() => vi.useRealTimers())

  it('slides a relative preset window to the current time', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 17, 10, 38, 14))
    const wrapper = mount(DateRangePicker, {
      props: { startDate: '2026-10-16T10:38:14', endDate: '2026-10-17T10:38:14', includeTime: true },
      global: { stubs: { Icon: true } },
    })
    await wrapper.vm.$nextTick()

    vi.setSystemTime(new Date(2026, 9, 17, 10, 44, 0))
    const range = (wrapper.vm as any).refreshActivePreset()
    expect(range).toEqual({ startDate: '2026-10-16T10:44:00', endDate: '2026-10-17T10:44:00' })
    // 组件只回传新范围；重新加载由调用方负责，不能额外触发一次 change 事件。
    expect(wrapper.emitted('change')).toBeUndefined()
    wrapper.unmount()
  })

  it('returns null for a custom range without touching it', async () => {
    const wrapper = mount(DateRangePicker, {
      props: { startDate: '2026-10-01T00:00:00', endDate: '2026-10-02T00:00:00', includeTime: true },
      global: { stubs: { Icon: true } },
    })
    await wrapper.vm.$nextTick()

    expect((wrapper.vm as any).refreshActivePreset()).toBeNull()
    expect(wrapper.emitted('change')).toBeUndefined()
    wrapper.unmount()
  })
})
