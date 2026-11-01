import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import DataTable from '../DataTable.vue'
import Select from '../Select.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const stubDesktopMatchMedia = () => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn()
    }))
  })
}

const stubMobileMatchMedia = () => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn()
    }))
  })
}

describe('DataTable', () => {
  beforeEach(() => {
    stubDesktopMatchMedia()
    localStorage.clear()
  })

  it('renders paired sort arrows and highlights the active direction', async () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [
          { key: 'name', label: 'Name', sortable: true },
          { key: 'created_at', label: 'Created', sortable: true }
        ],
        data: [
          { id: 1, name: 'Beta', created_at: '2026-01-02T00:00:00Z' },
          { id: 2, name: 'Alpha', created_at: '2026-01-01T00:00:00Z' }
        ],
        defaultSortKey: 'name',
        defaultSortOrder: 'asc'
      },
      slots: {
        'header-name': '<span data-test="custom-name-header">Name</span>'
      }
    })

    await wrapper.vm.$nextTick()

    const [nameHeader, createdHeader] = wrapper.findAll('th')
    expect(nameHeader.find('[data-test="custom-name-header"]').exists()).toBe(true)
    expect(nameHeader.attributes('aria-sort')).toBe('ascending')
    // Sortable headers are ghost buttons with one arrow icon showing the active direction.
    const nameSort = nameHeader.get('button.data-table-sort')
    expect(nameSort.attributes('data-sort')).toBe('ascending')
    expect(nameHeader.findComponent({ name: 'Button' }).props('icon')).toBe('i-lucide-arrow-up-narrow-wide')
    expect(createdHeader.attributes('aria-sort')).toBe('none')
    expect(createdHeader.findComponent({ name: 'Button' }).props('icon')).toBe('i-lucide-arrow-up-down')

    await nameSort.trigger('click')
    await wrapper.vm.$nextTick()

    expect(nameHeader.attributes('aria-sort')).toBe('descending')
    expect(nameSort.attributes('data-sort')).toBe('descending')
    expect(nameHeader.findComponent({ name: 'Button' }).props('icon')).toBe('i-lucide-arrow-down-wide-narrow')
    expect(createdHeader.findComponent({ name: 'Button' }).props('icon')).toBe('i-lucide-arrow-up-down')
  })

  it('renders every row with no virtual padding spacer for small datasets (virtualization off)', async () => {
    const data = Array.from({ length: 8 }, (_, i) => ({ id: i + 1, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data
      }
    })

    await wrapper.vm.$nextTick()

    // Virtualization is OFF for a small list…
    expect((wrapper.vm as any).shouldVirtualize).toBe(false)
    // …every row is in the DOM…
    expect(wrapper.findAll('tbody tr[data-index]')).toHaveLength(data.length)
    // …and there are no aria-hidden virtual padding spacer rows.
    expect(wrapper.findAll('tbody tr[aria-hidden="true"]')).toHaveLength(0)
  })

  it('switches to windowed rendering once row count exceeds virtualizeThreshold', async () => {
    const data = Array.from({ length: 12 }, (_, i) => ({ id: i + 1, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        virtualizeThreshold: 3
      }
    })

    await wrapper.vm.$nextTick()

    // Virtualization is ON: the mode-switch decision flipped…
    expect((wrapper.vm as any).shouldVirtualize).toBe(true)
    // …and the virtualizer drives off the full row count.
    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    expect(instance.options.count).toBe(data.length)
  })

  it('keys the virtualizer size cache by row identity, not index (avoids stale heights on sort/filter)', async () => {
    const data = Array.from({ length: 12 }, (_, i) => ({ id: 100 + i, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        rowKey: 'id',
        virtualizeThreshold: 3
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    // getItemKey must resolve to the row's stable key (id), not the positional index.
    expect(instance.options.getItemKey(0)).toBe(100)
    expect(instance.options.getItemKey(5)).toBe(105)
  })

  it('clears stale row and element caches when pagination replaces the row ID set', async () => {
    const firstPage = Array.from({ length: 100 }, (_, i) => ({ id: i + 1, name: `First ${i + 1}` }))
    const secondPage = Array.from({ length: 100 }, (_, i) => ({ id: i + 101, name: `Second ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: firstPage,
        rowKey: 'id',
        virtualizeThreshold: 1
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const firstPageIDs = firstPage.map(row => row.id)
    ;(instance as any).itemSizeCache = new Map(firstPageIDs.map(id => [id, 156]))
    instance.elementsCache.clear()
    for (const id of firstPageIDs) {
      instance.elementsCache.set(id, document.createElement('tr'))
    }
    const measureElementSpy = vi.spyOn(instance, 'measureElement')

    await wrapper.setProps({ data: secondPage })
    await wrapper.vm.$nextTick()

    const sizeCache = (instance as any).itemSizeCache as Map<number, number>
    expect(sizeCache.size).toBeLessThanOrEqual(secondPage.length)
    expect(instance.elementsCache.size).toBeLessThanOrEqual(secondPage.length)
    expect(firstPageIDs.some(id => sizeCache.has(id))).toBe(false)
    expect(firstPageIDs.some(id => instance.elementsCache.has(id))).toBe(false)
    expect(measureElementSpy.mock.calls.some(([node]) => node === null)).toBe(true)
  })

  it('clears stale caches when equal-length pages replace rows without stable keys', async () => {
    const firstPage = Array.from({ length: 12 }, (_, i) => ({ name: `First ${i + 1}` }))
    const secondPage = Array.from({ length: 12 }, (_, i) => ({ name: `Second ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: firstPage,
        virtualizeThreshold: 1
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const measureElementSpy = vi.spyOn(instance, 'measureElement')

    await wrapper.setProps({ data: secondPage })
    await wrapper.vm.$nextTick()

    expect(measureElementSpy.mock.calls.some(([node]) => node === null)).toBe(true)
  })

  it('conservatively clears caches when duplicate row-key multiplicity changes', async () => {
    const firstPage = [
      { id: 1, name: 'First A' },
      { id: 1, name: 'First B' },
      { id: 2, name: 'First C' }
    ]
    const secondPage = [
      { id: 1, name: 'Second A' },
      { id: 2, name: 'Second B' },
      { id: 2, name: 'Second C' }
    ]
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: firstPage,
        rowKey: 'id',
        virtualizeThreshold: 1
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const measureElementSpy = vi.spyOn(instance, 'measureElement')

    await wrapper.setProps({ data: secondPage })
    await wrapper.vm.$nextTick()

    expect(measureElementSpy.mock.calls.some(([node]) => node === null)).toBe(true)
  })

  it('preserves cache when rows without stable keys only reorder the same objects', async () => {
    const data = Array.from({ length: 12 }, (_, i) => ({ name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        virtualizeThreshold: 1
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const measureSpy = vi.spyOn(instance, 'measure')

    await wrapper.setProps({ data: [...data].reverse() })
    await wrapper.vm.$nextTick()

    expect(measureSpy).not.toHaveBeenCalled()
  })

  it('preserves stable row height cache when the same row IDs are only reordered', async () => {
    const data = Array.from({ length: 100 }, (_, i) => ({ id: i + 1, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        rowKey: 'id',
        virtualizeThreshold: 1
      }
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    ;(instance as any).itemSizeCache = new Map(data.map(row => [row.id, 156]))
    const measureSpy = vi.spyOn(instance, 'measure')

    await wrapper.setProps({ data: [...data].reverse() })
    await wrapper.vm.$nextTick()

    const sizeCache = (instance as any).itemSizeCache as Map<number, number>
    expect(measureSpy).not.toHaveBeenCalled()
    expect(sizeCache.size).toBe(100)
  })

  it('emits controlled current-page selection while preserving off-page keys', async () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: [
          { id: 1, name: 'One' },
          { id: 2, name: 'Two' }
        ],
        rowKey: 'id',
        selectable: true,
        selectedKeys: [99]
      }
    })

    // Nuxt UI checkboxes are buttons with role="checkbox"; they toggle on click.
    await wrapper.get('[data-test="select-all"]').trigger('click')

    const selectedAll = wrapper.emitted('update:selectedKeys')?.at(-1)?.[0]
    expect(selectedAll).toEqual([99, 1, 2])

    await wrapper.setProps({ selectedKeys: selectedAll as number[] })
    const rowCheckboxes = wrapper.findAll('[data-test="select-row"]')
    expect(rowCheckboxes.every((checkbox) => checkbox.attributes('aria-checked') === 'true')).toBe(true)
    // Selected rows carry the table's selected state (tinted row).
    expect(wrapper.findAll('tbody tr[data-selected="true"]')).toHaveLength(2)

    await rowCheckboxes[0].trigger('click')

    expect(wrapper.emitted('update:selectedKeys')?.at(-1)?.[0]).toEqual([99, 2])
    expect(wrapper.emitted('selectionChange')?.at(-1)?.[0]).toEqual([99, 2])
  })

  it('keeps the single usage field shrinkable in a 320px mobile card', () => {
    stubMobileMatchMedia()
    const viewport = document.createElement('div')
    viewport.style.width = '320px'
    document.body.appendChild(viewport)
    const wrapper = mount(DataTable, {
      attachTo: viewport,
      props: {
        columns: [{ key: 'usage', label: 'Usage' }],
        data: [{ id: 1, usage: 'snapshot' }],
        rowKey: 'id'
      },
      slots: {
        'cell-usage': '<div data-test="usage-cell">snapshot</div>'
      }
    })

    expect(viewport.style.width).toBe('320px')
    expect(wrapper.findAll('[data-field="usage"]')).toHaveLength(1)
    expect(wrapper.find('[data-field="ollama_cloud_usage"]').exists()).toBe(false)
    const field = wrapper.get('[data-field="usage"]')
    expect(field.classes()).toContain('min-w-0')
    expect(field.get('div').classes()).toEqual(expect.arrayContaining(['min-w-0', 'max-w-full']))
    expect(wrapper.findAll('[data-test="usage-cell"]')).toHaveLength(1)

    wrapper.unmount()
    viewport.remove()
  })

  it('offers current-page select all in the mobile card layout', async () => {
    stubMobileMatchMedia()
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: [
          { id: 1, name: 'One' },
          { id: 2, name: 'Two' }
        ],
        rowKey: 'id',
        selectable: true,
        selectedKeys: [99]
      }
    })

    await wrapper.get('[data-test="select-all-mobile"]').trigger('click')

    expect(wrapper.emitted('update:selectedKeys')?.at(-1)?.[0]).toEqual([99, 1, 2])
  })

  it('sorts from the mobile card toolbar with server-side sort enabled', async () => {
    stubMobileMatchMedia()
    const wrapper = mount(DataTable, {
      props: {
        columns: [
          { key: 'name', label: 'Name', sortable: true },
          { key: 'notes', label: 'Notes' },
          { key: 'balance', label: 'Balance', sortable: true }
        ],
        data: [
          { id: 1, name: 'One', balance: 5 },
          { id: 2, name: 'Two', balance: 90 }
        ],
        rowKey: 'id',
        serverSideSort: true
      }
    })

    const bar = wrapper.find('[data-test="mobile-sort-bar"]')
    expect(bar.exists()).toBe(true)

    const select = wrapper.findComponent(Select)
    expect(select.props('options')).toEqual([
      { value: 'name', label: 'Name' },
      { value: 'balance', label: 'Balance' }
    ])

    select.vm.$emit('change', 'balance')
    await wrapper.vm.$nextTick()
    expect(wrapper.emitted('sort')?.at(-1)).toEqual(['balance', 'asc'])

    await wrapper.get('[data-test="mobile-sort-direction"]').trigger('click')
    expect(wrapper.emitted('sort')?.at(-1)).toEqual(['balance', 'desc'])
  })

  it('sorts the mobile cards locally when the table is not server-side sorted', async () => {
    stubMobileMatchMedia()
    const wrapper = mount(DataTable, {
      props: {
        columns: [
          { key: 'name', label: 'Name', sortable: true },
          { key: 'balance', label: 'Balance', sortable: true }
        ],
        data: [
          { id: 1, name: 'One', balance: 5 },
          { id: 2, name: 'Two', balance: 90 }
        ],
        rowKey: 'id'
      }
    })

    wrapper.findComponent(Select).vm.$emit('change', 'balance')
    await wrapper.vm.$nextTick()

    const names = wrapper.findAll('[data-field="name"]').map((cell) => cell.text())
    expect(names[0]).toContain('One')
    expect(names[1]).toContain('Two')

    await wrapper.get('[data-test="mobile-sort-direction"]').trigger('click')
    const namesDesc = wrapper.findAll('[data-field="name"]').map((cell) => cell.text())
    expect(namesDesc[0]).toContain('Two')
    expect(namesDesc[1]).toContain('One')
  })

  it('stamps each rendered row with its key and sorted index', async () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name', sortable: true }],
        data: [
          { id: 7, name: 'Gamma' },
          { id: 3, name: 'Alpha' }
        ],
        defaultSortKey: 'name'
      }
    })
    await wrapper.vm.$nextTick()

    const rows = wrapper.findAll('tbody tr[data-row-id]')
    expect(rows.map((row) => row.attributes('data-row-id'))).toEqual(['3', '7'])
    expect(rows.map((row) => row.attributes('data-index'))).toEqual(['0', '1'])
  })

  it('emits rowClick with the source row, but not from the selection checkbox', async () => {
    const data = [{ id: 1, name: 'One' }, { id: 2, name: 'Two' }]
    const wrapper = mount(DataTable, {
      props: { columns: [{ key: 'name', label: 'Name' }], data, clickableRows: true, selectable: true }
    })
    await wrapper.vm.$nextTick()

    await wrapper.findAll('tbody tr[data-row-id]')[1].find('td:last-child').trigger('click')
    expect(wrapper.emitted('rowClick')).toEqual([[data[1]]])

    await wrapper.findAll('[data-test="select-row"]')[0].trigger('click')
    expect(wrapper.emitted('rowClick')).toHaveLength(1)
    expect(wrapper.emitted('update:selectedKeys')?.at(-1)?.[0]).toEqual([1])
  })

  it('shows skeleton rows while loading and the empty slot when there is no data', async () => {
    const wrapper = mount(DataTable, {
      props: { columns: [{ key: 'name', label: 'Name' }], data: [{ id: 1, name: 'One' }], loading: true },
      slots: { empty: '<p data-test="custom-empty">Nothing yet</p>' }
    })
    await wrapper.vm.$nextTick()

    expect(wrapper.findAll('tbody tr')).toHaveLength(5)
    expect(wrapper.findAll('tbody tr[data-row-id]')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('One')

    await wrapper.setProps({ loading: false, data: [] })
    expect(wrapper.find('[data-test="custom-empty"]').exists()).toBe(true)
  })

  it('pins the first column and the actions column, and the checkbox column with the first', async () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }, { key: 'notes', label: 'Notes' }, { key: 'actions', label: 'Actions' }],
        data: [{ id: 1, name: 'One', notes: 'n' }]
      }
    })
    await wrapper.vm.$nextTick()
    const cells = wrapper.find('tbody tr').findAll('td')
    expect(cells[0].classes()).toEqual(expect.arrayContaining(['data-table-pin', 'sticky', 'left-0']))
    expect(cells[1].classes()).not.toContain('sticky')
    expect(cells[2].classes()).toEqual(expect.arrayContaining(['data-table-pin', 'sticky', 'right-0']))

    await wrapper.setProps({ selectable: true })
    const selectableCells = wrapper.find('tbody tr').findAll('td')
    expect(selectableCells[0].classes()).toEqual(expect.arrayContaining(['sticky', 'left-0']))
    expect(selectableCells[1].classes()).toEqual(expect.arrayContaining(['sticky', 'left-(--data-table-pin-offset)']))
  })

  it('sorts from a click anywhere in a sortable header cell, once', async () => {
    const wrapper = mount(DataTable, {
      props: { columns: [{ key: 'name', label: 'Name', sortable: true }, { key: 'notes', label: 'Notes' }], data: [], serverSideSort: true }
    })
    const [nameHeader, notesHeader] = wrapper.findAll('th')

    await nameHeader.trigger('click')
    expect(wrapper.emitted('sort')).toEqual([['name', 'asc']])
    await nameHeader.get('button.data-table-sort').trigger('click')
    expect(wrapper.emitted('sort')).toEqual([['name', 'asc'], ['name', 'desc']])
    await notesHeader.trigger('click')
    expect(wrapper.emitted('sort')).toHaveLength(2)
  })

  it('hides the mobile sort bar when no column is sortable', () => {
    stubMobileMatchMedia()
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: [{ id: 1, name: 'One' }],
        rowKey: 'id'
      }
    })

    expect(wrapper.find('[data-test="mobile-sort-bar"]').exists()).toBe(false)
  })
})
