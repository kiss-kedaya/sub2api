import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import GroupRateSignals from '../GroupRateSignals.vue'
import { formatSignalTime, type GroupSignalSource } from '@/utils/groupRateSignals'

const state = vi.hoisted(() => ({ locale: 'en' as 'en' | 'zh' }))

// Interpolating t() over the real shipped copy, so the spec asserts real sentences.
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const en = (await import('@/i18n/locales/en/groupSignals')).default.groupSignals
  const zh = (await import('@/i18n/locales/zh/groupSignals')).default.groupSignals
  const bundles: Record<string, Record<string, string>> = { en, zh }
  return {
    ...actual,
    useI18n: () => ({
      locale: { value: state.locale },
      t: (key: string, params: Record<string, unknown> = {}) => {
        const template = bundles[state.locale][key.replace('groupSignals.', '')]
        if (template === undefined) throw new Error(`missing message ${key}`)
        return template.replace(/\{(\w+)\}/g, (_match, name: string) => String(params[name] ?? ''))
      },
    }),
  }
})

const CHANGED_AT = '2026-11-04T10:30:00Z'
const LAUNCHED_AT = '2026-11-05T01:00:00Z'

const rateUp: GroupSignalSource = {
  recent_rate_change: { direction: 'up', delta: 0.02, previous_rate: 0.06, changed_at: CHANGED_AT },
}
const rateDown: GroupSignalSource = {
  recent_rate_change: { direction: 'down', delta: 0.03, previous_rate: 0.11, changed_at: CHANGED_AT },
}
const newGroup: GroupSignalSource = { is_new: true, launched_at: LAUNCHED_AT, launch_rate: 0.06 }

type Props = { signals?: GroupSignalSource | null; showRateChange?: boolean; interactive?: boolean }
const mounted: VueWrapper[] = []
function mountSignals(props: Props = {}) {
  const wrapper = mount(GroupRateSignals, { attachTo: document.body, props })
  mounted.push(wrapper)
  return wrapper
}

function firePointer(target: Element, type: string, pointerType: 'mouse' | 'touch' | 'pen' = 'mouse') {
  const init = { bubbles: type !== 'pointerenter' && type !== 'pointerleave', cancelable: true, pointerType }
  const event = typeof PointerEvent === 'function' ? new PointerEvent(type, init) : new MouseEvent(type, init)
  if (!('pointerType' in event) || (event as PointerEvent).pointerType !== pointerType) {
    Object.defineProperty(event, 'pointerType', { value: pointerType })
  }
  target.dispatchEvent(event)
}

const detail = (testid: string) => document.body.querySelector(`[data-testid="${testid}-detail"]`)
async function settle(ms = 200) {
  await vi.advanceTimersByTimeAsync(ms)
  await flushPromises()
}

beforeEach(() => {
  state.locale = 'en'
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
})

afterEach(() => {
  while (mounted.length > 0) mounted.pop()?.unmount()
  document.body.innerHTML = ''
  vi.useRealTimers()
})

describe('GroupRateSignals states', () => {
  it('renders nothing without signals', () => {
    for (const signals of [null, undefined, {}, { is_new: false }, { recent_rate_change: null }]) {
      const wrapper = mountSignals({ signals })
      expect(wrapper.find('[data-testid="group-rate-signals"]').exists()).toBe(false)
      expect(wrapper.find('button').exists()).toBe(false)
    }
  })

  it('shows an up arrow with the size of the change in the warning tone', () => {
    const wrapper = mountSignals({ signals: rateUp })
    const chip = wrapper.get('[data-testid="group-rate-signal-rate"]')
    expect(chip.attributes('data-direction')).toBe('up')
    expect(chip.text()).toBe('0.02')
    expect(chip.classes()).toEqual(expect.arrayContaining(['bg-warning/15', 'text-warning-700', 'dark:text-warning']))
    expect(chip.classes()).not.toContain('text-success-700')
    expect(chip.attributes('aria-label')).toBe(`Rate up by 0.02, from 0.06x, changed ${formatSignalTime(CHANGED_AT, 'en')}`)
  })

  it('shows a down arrow in the success tone', () => {
    const wrapper = mountSignals({ signals: rateDown })
    const chip = wrapper.get('[data-testid="group-rate-signal-rate"]')
    expect(chip.attributes('data-direction')).toBe('down')
    expect(chip.text()).toBe('0.03')
    expect(chip.classes()).toEqual(expect.arrayContaining(['bg-success/15', 'text-success-700', 'dark:text-success']))
    expect(chip.classes()).not.toContain('text-warning-700')
    expect(chip.attributes('aria-label')).toBe(`Rate down by 0.03, from 0.11x, changed ${formatSignalTime(CHANGED_AT, 'en')}`)
  })

  it('shows a NEW pill with the launch moment and rate', () => {
    const wrapper = mountSignals({ signals: newGroup })
    const pill = wrapper.get('[data-testid="group-rate-signal-new"]')
    expect(pill.text()).toBe('NEW')
    expect(pill.classes()).toEqual(expect.arrayContaining(['bg-primary/10', 'text-primary-700', 'dark:text-primary', 'ring-1', 'ring-inset']))
    expect(pill.attributes('aria-label')).toBe(`New group, live since ${formatSignalTime(LAUNCHED_AT, 'en')} at 0.06x`)
  })

  it('leaves the launch rate out when the backend withheld it', () => {
    const wrapper = mountSignals({ signals: { is_new: true, launched_at: LAUNCHED_AT } })
    const label = wrapper.get('[data-testid="group-rate-signal-new"]').attributes('aria-label')
    expect(label).toBe(`New group, live since ${formatSignalTime(LAUNCHED_AT, 'en')}`)
    expect(label).not.toContain('x')
  })

  it('shows the rate change before the NEW pill when a new group was also repriced', () => {
    const wrapper = mountSignals({ signals: { ...newGroup, ...rateUp } })
    const order = wrapper.findAll('[data-testid]').map((node) => node.attributes('data-testid'))
    expect(order.filter((id) => id !== 'group-rate-signals')).toEqual(['group-rate-signal-rate', 'group-rate-signal-new'])
  })

  it('drops only the arrow when the host shows no per-token rate', () => {
    const wrapper = mountSignals({ signals: { ...newGroup, ...rateUp }, showRateChange: false })
    expect(wrapper.find('[data-testid="group-rate-signal-rate"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="group-rate-signal-new"]').exists()).toBe(true)

    const onlyArrow = mountSignals({ signals: rateUp, showRateChange: false })
    expect(onlyArrow.find('[data-testid="group-rate-signals"]').exists()).toBe(false)
  })

  it('formats deltas without float noise', () => {
    const signals: GroupSignalSource = {
      recent_rate_change: { direction: 'up', delta: 0.08 - 0.06, previous_rate: 0.06, changed_at: CHANGED_AT },
    }
    expect(mountSignals({ signals }).get('[data-testid="group-rate-signal-rate"]').text()).toBe('0.02')
  })

  it('uses theme tokens only, never raw palette colours', () => {
    const wrapper = mountSignals({ signals: { ...newGroup, ...rateUp } })
    expect(wrapper.html()).not.toMatch(/(?:text|bg|ring|border)-(?:red|green|emerald|amber|yellow|orange|lime|rose)-\d/)
    expect(wrapper.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/)
  })

  it('speaks Chinese when the locale is zh', () => {
    state.locale = 'zh'
    const wrapper = mountSignals({ signals: { ...newGroup, ...rateUp } })
    // The pill stays "NEW" in both languages, as the old channel-status badge did.
    expect(wrapper.get('[data-testid="group-rate-signal-new"]').text()).toBe('NEW')
    expect(wrapper.get('[data-testid="group-rate-signal-rate"]').attributes('aria-label'))
      .toBe(`倍率上调 0.02，原 0.06x，${formatSignalTime(CHANGED_AT, 'zh')} 调整`)
    expect(wrapper.get('[data-testid="group-rate-signal-new"]').attributes('aria-label'))
      .toBe(`新分组，${formatSignalTime(LAUNCHED_AT, 'zh')} 上线，初始倍率 0.06x`)
  })

  it('hides the decorative arrow and label from assistive tech and keeps the sentence on the control', () => {
    const wrapper = mountSignals({ signals: rateUp })
    const chip = wrapper.get('[data-testid="group-rate-signal-rate"]')
    expect(chip.element.tagName).toBe('BUTTON')
    expect(chip.attributes('type')).toBe('button')
    expect(chip.get('span').attributes('aria-hidden')).toBe('true')
  })
})

describe('GroupRateSignals inside another control', () => {
  it('renders passive labelled badges instead of nested buttons', () => {
    const wrapper = mountSignals({ signals: { ...newGroup, ...rateUp }, interactive: false })
    expect(wrapper.find('button').exists()).toBe(false)
    for (const id of ['group-rate-signal-rate', 'group-rate-signal-new']) {
      const badge = wrapper.get(`[data-testid="${id}"]`)
      expect(badge.element.tagName).toBe('SPAN')
      expect(badge.attributes('role')).toBe('img')
      expect(badge.attributes('aria-label')).toBeTruthy()
      expect(badge.attributes('tabindex')).toBeUndefined()
    }
  })

  it('keeps the details reachable through the native tooltip', () => {
    const wrapper = mountSignals({ signals: rateUp, interactive: false })
    const title = wrapper.get('[data-testid="group-rate-signal-rate"]').attributes('title')
    expect(title).toContain('Rate up from 0.06x')
    expect(title).toContain(`Changed ${formatSignalTime(CHANGED_AT, 'en')}`)
  })
})

describe('GroupRateSignals details', () => {
  it('opens on pointer hover after a short delay and shows the previous rate and time', async () => {
    const wrapper = mountSignals({ signals: rateUp })
    const chip = wrapper.get('[data-testid="group-rate-signal-rate"]')
    expect(detail('group-rate-signal-rate')).toBeNull()

    firePointer(chip.element, 'pointerenter', 'mouse')
    await settle(40)
    expect(detail('group-rate-signal-rate')).toBeNull()

    await settle(200)
    const bubble = detail('group-rate-signal-rate')
    expect(bubble).not.toBeNull()
    expect(bubble?.textContent).toContain('Rate up from 0.06x')
    expect(bubble?.textContent).toContain(`Changed ${formatSignalTime(CHANGED_AT, 'en')}`)
    expect(bubble?.getAttribute('aria-hidden')).toBe('true')
  })

  it('shows the launch time and rate for a new group', async () => {
    const wrapper = mountSignals({ signals: newGroup })
    firePointer(wrapper.get('[data-testid="group-rate-signal-new"]').element, 'pointerenter', 'mouse')
    await settle()
    const text = detail('group-rate-signal-new')?.textContent ?? ''
    expect(text).toContain('New group')
    expect(text).toContain(`Live since ${formatSignalTime(LAUNCHED_AT, 'en')} at 0.06x`)
  })

  it('opens for keyboard users on focus and closes on blur', async () => {
    const wrapper = mountSignals({ signals: rateUp })
    const chip = wrapper.get('[data-testid="group-rate-signal-rate"]')
    ;(chip.element as HTMLButtonElement).focus()
    await settle()
    expect(detail('group-rate-signal-rate')).not.toBeNull()

    ;(chip.element as HTMLButtonElement).blur()
    await settle()
    expect(detail('group-rate-signal-rate')).toBeNull()
  })

  it('closes on Escape', async () => {
    const wrapper = mountSignals({ signals: rateUp })
    ;(wrapper.get('[data-testid="group-rate-signal-rate"]').element as HTMLButtonElement).focus()
    await settle()
    expect(detail('group-rate-signal-rate')).not.toBeNull()

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await settle()
    expect(detail('group-rate-signal-rate')).toBeNull()
  })

  it('opens on tap without any hover and closes on the next tap', async () => {
    const wrapper = mountSignals({ signals: rateUp })
    const chip = wrapper.get('[data-testid="group-rate-signal-rate"]').element

    firePointer(chip, 'pointerenter', 'touch')
    await settle()
    expect(detail('group-rate-signal-rate')).toBeNull()

    firePointer(chip, 'pointerup', 'touch')
    await flushPromises()
    expect(detail('group-rate-signal-rate')).not.toBeNull()

    firePointer(chip, 'pointerup', 'touch')
    await flushPromises()
    expect(detail('group-rate-signal-rate')).toBeNull()
  })

  it('does not treat a mouse press as a tap', async () => {
    const wrapper = mountSignals({ signals: rateUp })
    firePointer(wrapper.get('[data-testid="group-rate-signal-rate"]').element, 'pointerup', 'mouse')
    await flushPromises()
    expect(detail('group-rate-signal-rate')).toBeNull()
  })

  it('switches off popover animation under reduced motion', async () => {
    const wrapper = mountSignals({ signals: rateUp })
    firePointer(wrapper.get('[data-testid="group-rate-signal-rate"]').element, 'pointerup', 'touch')
    await flushPromises()
    const content = document.body.querySelector('[data-slot="content"]')
    expect(content?.className).toContain('motion-reduce:data-[state=open]:animate-none')
    expect(content?.className).toContain('motion-reduce:data-[state=closed]:animate-none')
    expect(wrapper.get('[data-testid="group-rate-signal-rate"]').classes()).toContain('motion-reduce:transition-none')
  })

  it('gives the small chip a larger tap target and a visible keyboard focus ring', () => {
    const classes = mountSignals({ signals: rateUp }).get('[data-testid="group-rate-signal-rate"]').classes()
    expect(classes).toEqual(expect.arrayContaining(['before:-inset-1.5', 'touch-manipulation', 'focus-visible:outline-2']))
  })
})
