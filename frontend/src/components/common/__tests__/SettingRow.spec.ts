import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import SettingRow from '@/components/common/SettingRow.vue'
import SettingPanel from '@/components/common/SettingPanel.vue'
import SettingGroup from '@/components/common/SettingGroup.vue'

// The summary bar translates its own count line; return the key so the assertions read the contract.
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

// A row whose control is a plain checkbox bound to `on`, with or without a fields slot.
function mountRow(options: { on?: boolean; fields?: boolean; summary?: string; hint?: string | string[] } = {}) {
  const on = ref(options.on ?? false)
  const Host = defineComponent({
    setup: () => () =>
      h(
        SettingRow,
        { label: 'Window cost', hint: options.hint ?? 'Limits spend in the window', summary: options.summary, on: on.value },
        {
          control: () =>
            h('input', {
              type: 'checkbox',
              'data-test': 'control',
              checked: on.value,
              onClick: () => { on.value = !on.value },
            }),
          ...(options.fields ? { fields: () => h('input', { 'data-test': 'field' }) } : {}),
        },
      ),
  })
  return { wrapper: mount(Host, { attachTo: document.body }), on }
}

const header = (w: ReturnType<typeof mountRow>['wrapper']) => w.get('.setting-row__button')
const isOpen = (w: ReturnType<typeof mountRow>['wrapper']) => header(w).attributes('aria-expanded') === 'true'

afterEach(() => {
  document.body.innerHTML = ''
  vi.useRealTimers()
})

describe('SettingRow', () => {
  it('expands and collapses from the row header, wiring aria-expanded to the body it controls', async () => {
    const { wrapper } = mountRow()
    const button = header(wrapper)

    expect(button.element.tagName).toBe('BUTTON')
    expect(isOpen(wrapper)).toBe(false)
    const body = wrapper.get(`#${button.attributes('aria-controls')}`)
    expect(body.attributes('data-open')).toBe('false')
    // Collapsed content is inert, so Tab does not land inside a closed row.
    expect(body.get('.setting-row__body-inner').attributes('inert')).toBeDefined()

    await button.trigger('click')
    expect(isOpen(wrapper)).toBe(true)
    expect(body.attributes('data-open')).toBe('true')
    expect(body.get('.setting-row__body-inner').attributes('inert')).toBeUndefined()
    expect(body.text()).toContain('Limits spend in the window')

    await button.trigger('click')
    expect(isOpen(wrapper)).toBe(false)
  })

  it('does not expand when the control on the right is clicked', async () => {
    const { wrapper, on } = mountRow()

    await wrapper.get('[data-test="control"]').trigger('click')

    expect(on.value).toBe(true)
    expect(isOpen(wrapper)).toBe(false)
  })

  it('opens by itself when the user turns on a setting that has fields to fill in', async () => {
    const { wrapper, on } = mountRow({ fields: true })

    await wrapper.get('[data-test="control"]').trigger('click')
    await nextTick()

    expect(on.value).toBe(true)
    expect(isOpen(wrapper)).toBe(true)
    expect(wrapper.find('[data-test="field"]').exists()).toBe(true)
  })

  it('does not auto-expand a setting without fields, or one that was already on when the form loaded', async () => {
    const noFields = mountRow()
    await noFields.wrapper.get('[data-test="control"]').trigger('click')
    expect(isOpen(noFields.wrapper)).toBe(false)

    // A form that fetches its values after mount flips `on` without the user touching the control.
    const loaded = mountRow({ fields: true })
    loaded.on.value = true
    await nextTick()
    expect(isOpen(loaded.wrapper)).toBe(false)
  })

  it('shows no inline summary while the row is off, and the value once it is on', async () => {
    const { wrapper, on } = mountRow({ summary: 'Limit 50$ · Reserve 5$' })

    expect(wrapper.find('.setting-row__summary').exists()).toBe(false)
    expect(wrapper.get('.setting-row').attributes('data-on')).toBeUndefined()

    on.value = true
    await nextTick()

    expect(wrapper.get('.setting-row__summary').text()).toBe('Limit 50$ · Reserve 5$')
    expect(wrapper.get('.setting-row').attributes('data-on')).toBe('true')
  })

  it('keeps a value summary for select-style rows even when they sit at their default', () => {
    const wrapper = mount(SettingRow, { props: { label: 'Mode', summary: 'Auto', on: false, persistentSummary: true } })
    expect(wrapper.get('.setting-row__summary').text()).toBe('Auto')
  })

  it('renders several hint paragraphs in order', async () => {
    const { wrapper } = mountRow({ hint: ['First paragraph', 'Second paragraph'] })
    await header(wrapper).trigger('click')
    expect(wrapper.findAll('.setting-row__hint').map((p) => p.text())).toEqual(['First paragraph', 'Second paragraph'])
  })

  it('expands to show a rich hint slot even when the row has no fields', async () => {
    const wrapper = mount(SettingRow, {
      props: { label: 'Linked', on: false },
      slots: { hint: () => h('a', { href: '/docs', class: 'doc-link' }, 'Read the guide') },
    })
    const button = wrapper.get('.setting-row__button')
    expect(button.attributes('disabled')).toBeUndefined()
    // The body stays in the DOM (so it can animate) but is inert while closed.
    expect(wrapper.get('.setting-row__body-inner').attributes('inert')).toBeDefined()
    await button.trigger('click')
    expect(button.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('.setting-row__body-inner').attributes('inert')).toBeUndefined()
    expect(wrapper.get('.setting-row__body-inner .doc-link').text()).toBe('Read the guide')
  })

  it('has nothing to expand when there is neither a hint nor fields', async () => {
    const wrapper = mount(SettingRow, { props: { label: 'Bare', hint: '' } })
    const button = wrapper.get('.setting-row__button')
    expect(button.attributes('aria-expanded')).toBeUndefined()
    expect(button.attributes('disabled')).toBeDefined()
  })

  it('opens on the value when a leading "change this field" checkbox is ticked, without the click expanding by itself', async () => {
    const ticked = ref(false)
    const Host = defineComponent({
      setup: () => () =>
        h(
          SettingRow,
          { label: 'Base URL', on: ticked.value },
          {
            leading: () =>
              h('input', {
                type: 'checkbox',
                'data-test': 'tick',
                checked: ticked.value,
                onClick: () => { ticked.value = !ticked.value },
              }),
            fields: () => h('input', { 'data-test': 'value' }),
          },
        ),
    })
    const wrapper = mount(Host, { attachTo: document.body })
    const leading = wrapper.get('.setting-row__leading')
    // The checkbox sits before the name, outside the expand button.
    expect(leading.element.compareDocumentPosition(wrapper.get('.setting-row__button').element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(wrapper.get('.setting-row__button').find('[data-test="tick"]').exists()).toBe(false)

    await wrapper.get('[data-test="tick"]').trigger('click')
    await nextTick()
    expect(ticked.value).toBe(true)
    expect(wrapper.get('.setting-row__button').attributes('aria-expanded')).toBe('true')

    // Unticking does not toggle the row closed through the header.
    await wrapper.get('[data-test="tick"]').trigger('click')
    await nextTick()
    expect(ticked.value).toBe(false)
    expect(wrapper.get('.setting-row__button').attributes('aria-expanded')).toBe('true')
  })
})

describe('SettingPanel summary', () => {
  function mountPanel() {
    const state = ref({ a: false, b: true, c: false, cShown: true })
    const Host = defineComponent({
      setup: () => () =>
        h(SettingPanel, null, () => [
          h(SettingGroup, { title: 'Scheduling' }, () => [
            h(SettingRow, { label: 'Alpha', on: state.value.a }),
            h(SettingRow, { label: 'Beta', on: state.value.b }),
          ]),
          h(SettingGroup, { title: 'Limits' }, () => [
            state.value.cShown ? h(SettingRow, { label: 'Gamma', on: state.value.c }) : null,
            h(SettingRow, { label: 'Threshold', on: true, inSummary: false }),
          ]),
        ]),
    })
    return { wrapper: mount(Host, { attachTo: document.body }), state }
  }

  const count = (w: ReturnType<typeof mountPanel>['wrapper']) => w.get('[data-testid="setting-summary-count"]').text()
  const chips = (w: ReturnType<typeof mountPanel>['wrapper']) => w.findAll('.setting-summary__chip').map((c) => c.text())

  it('lists the enabled rows by name, in page order, with their count', async () => {
    const { wrapper, state } = mountPanel()
    await nextTick()
    expect(count(wrapper)).toBe('common.settingsSummary.enabled')
    expect(chips(wrapper)).toEqual(['Beta'])

    state.value.c = true
    state.value.a = true
    await nextTick()
    expect(chips(wrapper)).toEqual(['Alpha', 'Beta', 'Gamma'])
  })

  it('drops a row that unmounts and ignores rows that opt out of the summary', async () => {
    const { wrapper, state } = mountPanel()
    state.value.c = true
    await nextTick()
    expect(chips(wrapper)).toEqual(['Beta', 'Gamma'])

    state.value.cShown = false
    await nextTick()
    expect(chips(wrapper)).toEqual(['Beta'])
    expect(chips(wrapper)).not.toContain('Threshold')
  })

  it('says nothing is on when nothing is on, without claiming anything is at its default', async () => {
    const { wrapper, state } = mountPanel()
    state.value.b = false
    await nextTick()
    expect(chips(wrapper)).toEqual([])
    // One fact, said once: no "0 enabled" in front of it, and no verdict about defaults.
    expect(wrapper.get('[data-testid="setting-summary"]').text()).toBe('common.settingsSummary.noneOn')
    expect(wrapper.get('.setting-summary__none').text()).toBe('common.settingsSummary.noneOn')

    state.value.b = true
    await nextTick()
    expect(wrapper.find('.setting-summary__none').exists()).toBe(false)
    expect(count(wrapper)).toContain('common.settingsSummary.enabled')
  })

  it('renders the group titles above their rows', () => {
    const { wrapper } = mountPanel()
    expect(wrapper.findAll('.setting-group__title').map((t) => t.text())).toEqual(['Scheduling', 'Limits'])
  })
})

describe('SettingPanel with custom summary wording', () => {
  it('uses the caller\'s count and empty keys, e.g. a bulk edit counting fields to change', async () => {
    const ticked = ref(false)
    const Host = defineComponent({
      setup: () => () =>
        h(
          SettingPanel,
          { summaryCountKey: 'common.settingsSummary.willChange', summaryEmptyKey: 'common.settingsSummary.noneSelected' },
          () => [h(SettingRow, { label: 'Proxy', on: ticked.value })],
        ),
    })
    const wrapper = mount(Host, { attachTo: document.body })
    await nextTick()
    // Nothing ticked: only the empty line, in the caller's words (no "0 to change" in front of it).
    expect(wrapper.get('[data-testid="setting-summary"]').text()).toBe('common.settingsSummary.noneSelected')
    expect(wrapper.get('.setting-summary__none').text()).toBe('common.settingsSummary.noneSelected')

    ticked.value = true
    await nextTick()
    expect(wrapper.get('[data-testid="setting-summary-count"]').text()).toBe('common.settingsSummary.willChange')
    expect(wrapper.findAll('.setting-summary__chip').map((c) => c.text())).toEqual(['Proxy'])
    expect(wrapper.find('.setting-summary__none').exists()).toBe(false)
  })
})

describe('SettingPanel with no rows', () => {
  it('hides the summary bar entirely when every row is hidden by its own condition', () => {
    const Host = defineComponent({
      setup: () => () => h(SettingPanel, null, () => [h(SettingGroup, { title: 'Empty' }, () => [])]),
    })
    const wrapper = mount(Host, { attachTo: document.body })
    expect(wrapper.find('[data-testid="setting-summary"]').exists()).toBe(false)
  })
})
