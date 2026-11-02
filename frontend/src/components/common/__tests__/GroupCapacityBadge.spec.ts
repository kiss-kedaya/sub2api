import { describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import GroupCapacityBadge from '../GroupCapacityBadge.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const meters = (wrapper: VueWrapper) =>
  wrapper.findAll('[data-tone]').map((meter) => ({
    tone: meter.attributes('data-tone'),
    label: meter.attributes('aria-label'),
    text: meter.text().replace(/\s+/g, ' ').trim(),
  }))

describe('GroupCapacityBadge', () => {
  it('always shows concurrency and adds a users row once a session limit exists', () => {
    const withoutSessions = mount(GroupCapacityBadge, {
      props: { concurrencyUsed: 4, concurrencyMax: 24, sessionsUsed: 0, sessionsMax: 0, rpmUsed: 0, rpmMax: 0 },
    })
    expect(meters(withoutSessions)).toHaveLength(1)
    expect(meters(withoutSessions)[0].text).toContain('4')
    expect(meters(withoutSessions)[0].text).toContain('24')

    const withSessions = mount(GroupCapacityBadge, {
      props: { concurrencyUsed: 4, concurrencyMax: 24, sessionsUsed: 9, sessionsMax: 60, rpmUsed: 42, rpmMax: 180 },
    })
    expect(meters(withSessions)).toHaveLength(3)
    expect(meters(withSessions)[1].text).toContain('9')
    expect(meters(withSessions)[1].text).toContain('60')
  })

  it('explains each number instead of showing a bare badge', () => {
    const wrapper = mount(GroupCapacityBadge, {
      props: { concurrencyUsed: 4, concurrencyMax: 24, sessionsUsed: 9, sessionsMax: 60, rpmUsed: 42, rpmMax: 180 },
    })

    const labels = meters(wrapper).map((meter) => meter.label)
    expect(labels).toEqual([
      'admin.groups.capacityLabels.concurrency · admin.groups.capacityHints.concurrency',
      'admin.groups.capacityLabels.sessions · admin.groups.capacityHints.sessions',
      'admin.groups.capacityLabels.rpm · admin.groups.capacityHints.rpm',
    ])
    expect(wrapper.findAll('[data-tone][tabindex="0"]')).toHaveLength(3)
  })

  it('renders unknown counters as dashes and says so in the hint', () => {
    const wrapper = mount(GroupCapacityBadge, {
      props: {
        concurrencyUsed: 0,
        concurrencyMax: 24,
        sessionsUsed: 0,
        sessionsMax: 60,
        rpmUsed: 0,
        rpmMax: 0,
        runtimeKnown: false,
      },
    })

    const rows = meters(wrapper)
    expect(rows).toHaveLength(2)
    for (const row of rows) {
      expect(row.text).toContain('—')
      expect(row.tone).toBe('unknown')
      expect(row.label).toContain('admin.groups.capacityHints.unknown')
    }
  })

  it('marks a saturated group with the error tone', () => {
    const wrapper = mount(GroupCapacityBadge, {
      props: { concurrencyUsed: 24, concurrencyMax: 24, sessionsUsed: 0, sessionsMax: 0, rpmUsed: 0, rpmMax: 0 },
    })

    expect(meters(wrapper)[0].tone).toBe('full')
  })
})
