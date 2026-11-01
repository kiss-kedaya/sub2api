import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import Select from '../Select.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

let unmountWrapper: (() => void) | undefined

const openSelect = async (props: Record<string, unknown> = {}, slots: Record<string, string> = {}) => {
  const wrapper = mount(Select, {
    props: {
      modelValue: null,
      options: [
        {
          value: 'example',
          label: 'very-long-unbroken-option-value-that-must-not-overflow',
        },
      ],
      ...props,
    },
    slots,
  })
  unmountWrapper = () => wrapper.unmount()

  await wrapper.get('button').trigger('click')
  await nextTick()

  return { wrapper, dropdown: document.body.querySelector<HTMLElement>('.select-dropdown-portal') }
}

const optionLabels = (dropdown: HTMLElement | null) =>
  [...(dropdown?.querySelectorAll('[role="option"]') ?? [])].map((el) => el.textContent?.trim())

afterEach(() => {
  unmountWrapper?.()
  unmountWrapper = undefined
  document.body.innerHTML = ''
  vi.useRealTimers()
  vi.restoreAllMocks()
})

// The menu is a Nuxt UI SelectMenu positioned by floating-ui (jsdom has no layout), so these check
// the constraints it is given rather than computed pixel positions.
describe('Select dropdown viewport constraints', () => {
  it('keeps the 200px (or trigger-width) minimum width and grows to fit long options', async () => {
    const { dropdown } = await openSelect()

    expect(dropdown).not.toBeNull()
    expect(dropdown!.classList).toContain('w-max')
    expect(dropdown!.classList).toContain('min-w-[min(max(200px,var(--reka-combobox-trigger-width)),calc(100vw-16px))]')
  })

  it('never grows wider than the viewport minus the 8px safe padding on each side', async () => {
    const { dropdown } = await openSelect()

    expect(dropdown!.classList).toContain('max-w-[calc(100vw-16px)]')
  })

  it('keeps 8px from the viewport edges when the trigger sits near or past them', async () => {
    const { wrapper } = await openSelect()

    expect(wrapper.findComponent({ name: 'ComboboxContent' }).props('collisionPadding')).toBe(8)
  })

  it('opens below the trigger and stacks above page chrome', async () => {
    const { wrapper, dropdown } = await openSelect()

    expect(wrapper.findComponent({ name: 'ComboboxContent' }).props('side')).toBe('bottom')
    expect(dropdown!.classList).toContain('z-[60]')
  })
})

describe('Select values and options', () => {
  it('keeps empty-string, null, zero and false values distinct', async () => {
    const options = [
      { value: '', label: 'All' },
      { value: null, label: 'None' },
      { value: 0, label: 'Zero' },
      { value: false, label: 'Off' },
    ]
    const { wrapper, dropdown } = await openSelect({ modelValue: '', options })

    expect(optionLabels(dropdown)).toEqual(['All', 'None', 'Zero', 'Off'])
    const all = dropdown!.querySelectorAll<HTMLElement>('[role="option"]')
    expect([...all].map((el) => el.getAttribute('aria-selected'))).toEqual(['true', 'false', 'false', 'false'])

    all[2].click()
    await nextTick()
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([0])
    expect(wrapper.emitted('change')?.at(-1)).toEqual([0, { value: 0, label: 'Zero' }])
  })

  it('shows group headers as labels and does not select disabled options', async () => {
    const options = [
      { kind: 'group', value: 'g', label: 'Anthropic' },
      { value: 'a', label: 'Claude', disabled: true },
      { value: 'b', label: 'Opus' },
    ]
    const { wrapper, dropdown } = await openSelect({ options })

    expect(dropdown!.textContent).toContain('Anthropic')
    expect(optionLabels(dropdown)).toEqual(['Claude', 'Opus'])
    dropdown!.querySelector<HTMLElement>('[role="option"]')!.click()
    await nextTick()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('shows the placeholder until a matching option is selected', () => {
    const wrapper = mount(Select, { props: { modelValue: 'missing', options: [{ value: 'a', label: 'Alpha' }], placeholder: 'Pick one' } })
    unmountWrapper = () => wrapper.unmount()

    expect(wrapper.get('button').text()).toContain('Pick one')
  })

  it('clears to null from the trigger without opening the menu', async () => {
    const wrapper = mount(Select, { props: { modelValue: 'a', clearable: true, options: [{ value: 'a', label: 'Alpha' }] } })
    unmountWrapper = () => wrapper.unmount()

    await wrapper.get('[aria-label="Clear selection"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([[null]])
    expect(wrapper.emitted('change')).toEqual([[null, null]])
    expect(document.body.querySelector('.select-dropdown-portal')).toBeNull()
  })

  it('offers the typed query first in creatable mode', async () => {
    const options = [{ value: 'claude-opus', label: 'claude-opus' }]
    const { wrapper, dropdown } = await openSelect({ options, searchable: true, creatable: true, creatablePrefix: 'Use' })

    const input = dropdown!.querySelector<HTMLInputElement>('.select-search-input')!
    input.value = '  gpt-6 '
    input.dispatchEvent(new Event('input'))
    await nextTick()

    expect(optionLabels(dropdown)[0]).toBe('Use "gpt-6"')
    dropdown!.querySelector<HTMLElement>('[role="option"]')!.click()
    await nextTick()
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['gpt-6'])
  })

  it('renders custom option and selected slots', async () => {
    const { wrapper, dropdown } = await openSelect(
      { modelValue: 'example' },
      {
        option: '<template #option="{ option, selected }"><span class="custom-row">{{ option.label }}{{ selected ? " *" : "" }}</span></template>',
        selected: '<template #selected="{ option }"><b class="custom-value">{{ option?.label }}</b></template>',
      }
    )

    expect(wrapper.get('.custom-value').text()).toBe('very-long-unbroken-option-value-that-must-not-overflow')
    expect(dropdown!.querySelector('.custom-row')?.textContent).toBe('very-long-unbroken-option-value-that-must-not-overflow *')
  })

  it('shows a search box only for more than five options in auto mode', async () => {
    const few = await openSelect({ options: Array.from({ length: 5 }, (_, i) => ({ value: i, label: `Option ${i}` })) })
    expect(few.dropdown!.querySelector('.select-search-input')).toBeNull()
    unmountWrapper?.()
    document.body.innerHTML = ''

    const many = await openSelect({ options: Array.from({ length: 6 }, (_, i) => ({ value: i, label: `Option ${i}` })) })
    expect(many.dropdown!.querySelector('.select-search-input')).not.toBeNull()
  })

  it('opens without any network request', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    await openSelect({ clearable: true, modelValue: 'example', searchable: true })
    await new Promise((resolve) => setTimeout(resolve, 0))

    expect(fetchSpy).not.toHaveBeenCalled()
  })
})

describe('Select remote search', () => {
  const mountRemoteSelect = (props: Record<string, unknown> = {}) => {
    const wrapper = mount(Select, {
      props: {
        modelValue: null,
        remote: true,
        options: [
          { value: 'alpha', label: 'Alpha account' },
          { value: 'beta', label: 'Beta account' },
        ],
        ...props,
      },
    })
    unmountWrapper = () => wrapper.unmount()
    return wrapper
  }

  const openDropdown = async () => {
    const dropdown = document.body.querySelector<HTMLElement>('.select-dropdown-portal')
    expect(dropdown).not.toBeNull()
    return dropdown as HTMLElement
  }

  const typeSearchQuery = async (query: string) => {
    const dropdown = await openDropdown()
    const input = dropdown.querySelector<HTMLInputElement>('.select-search-input')
    expect(input).not.toBeNull()
    input!.value = query
    input!.dispatchEvent(new Event('input'))
    await nextTick()
  }

  it('emits debounced search events and skips local filtering in remote mode', async () => {
    vi.useFakeTimers()
    const wrapper = mountRemoteSelect()
    await wrapper.get('button').trigger('click')
    await nextTick()

    await typeSearchQuery('zzz')

    // 防抖窗口内不触发。
    expect(wrapper.emitted('search')).toBeUndefined()
    await vi.advanceTimersByTimeAsync(300)

    expect(wrapper.emitted('search')).toEqual([['zzz']])
    // 远程模式不做本地过滤：无命中的 query 下选项仍完整展示（由父组件更新 options）。
    const dropdown = await openDropdown()
    const labels = [...dropdown.querySelectorAll('.select-option-label')].map((el) => el.textContent)
    expect(labels).toContain('Alpha account')
    expect(labels).toContain('Beta account')
  })

  it('does not emit search when the dropdown closes and the query resets', async () => {
    vi.useFakeTimers()
    const wrapper = mountRemoteSelect()
    await wrapper.get('button').trigger('click')
    await nextTick()

    await typeSearchQuery('hidden')

    // 关闭下拉：排队中的防抖定时器应被取消，也不应因 query 重置而尾随 emit。
    await wrapper.get('button').trigger('click')
    await nextTick()
    await vi.advanceTimersByTimeAsync(300)

    expect(wrapper.emitted('search')).toBeUndefined()
  })

  it('shows the loading text instead of empty text while loading with no options', async () => {
    const wrapper = mountRemoteSelect({ options: [], loading: true })
    await wrapper.get('button').trigger('click')
    await nextTick()

    const dropdown = await openDropdown()
    expect(dropdown.querySelector('.select-empty')?.textContent).toContain('common.loading')
  })

  it('keeps local filtering and emits nothing when remote is not set', async () => {
    vi.useFakeTimers()
    const wrapper = mount(Select, {
      props: {
        modelValue: null,
        searchable: true,
        options: [
          { value: 'alpha', label: 'Alpha account' },
          { value: 'beta', label: 'Beta account' },
        ],
      },
    })
    unmountWrapper = () => wrapper.unmount()
    await wrapper.get('button').trigger('click')
    await nextTick()

    await typeSearchQuery('alpha')
    await vi.advanceTimersByTimeAsync(300)

    expect(wrapper.emitted('search')).toBeUndefined()
    const dropdown = await openDropdown()
    const labels = [...dropdown.querySelectorAll('.select-option-label')].map((el) => el.textContent)
    expect(labels).toEqual(['Alpha account'])
  })
})
