import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import Pagination from '../Pagination.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

function mountPagination() {
  return mount(Pagination, {
    props: { total: 200, page: 1, pageSize: 20, showJump: true, showPageSizeSelector: false },
    global: { stubs: { Icon: true } }
  })
}

describe('pagination jump input', () => {
  it.each(['click', 'enter'])('jumps to the entered numeric page using %s', async (action) => {
    const wrapper = mountPagination()
    const input = wrapper.get('input[type="number"]')
    await input.setValue('3')
    if (action === 'click') await wrapper.get('[data-test="pagination-jump"]').trigger('click')
    else await input.trigger('keyup', { key: 'Enter' })
    expect(wrapper.emitted('update:page')).toEqual([[3]])
    expect((input.element as HTMLInputElement).value).toBe('')
  })

  it('clamps an entered page to the last page', async () => {
    const wrapper = mountPagination()
    await wrapper.get('input').setValue('99')
    await wrapper.get('[data-test="pagination-jump"]').trigger('click')
    expect(wrapper.emitted('update:page')).toEqual([[10]])
  })

  it('ignores an empty jump input', async () => {
    const wrapper = mountPagination()
    await wrapper.get('[data-test="pagination-jump"]').trigger('click')
    expect(wrapper.emitted('update:page')).toBeUndefined()
  })
})

describe('pagination page buttons', () => {
  function mountAt(page: number) {
    return mount(Pagination, { props: { total: 200, page, pageSize: 20, showPageSizeSelector: false } })
  }

  it('marks the current page and emits the chosen page', async () => {
    const wrapper = mountAt(1)
    expect(wrapper.get('button[aria-current="page"]').text()).toBe('1')
    await wrapper.findAll('button[aria-label="pagination.goToPage"]').find(button => button.text() === '2')!.trigger('click')
    expect(wrapper.emitted('update:page')?.[0]).toEqual([2])
  })

  it('disables previous on the first page and next on the last page', async () => {
    const first = mountAt(1)
    expect(first.get('button[aria-label="pagination.previous"]').attributes('disabled')).toBeDefined()
    await first.get('button[aria-label="pagination.next"]').trigger('click')
    expect(first.emitted('update:page')).toEqual([[2]])

    const last = mountAt(10)
    expect(last.get('button[aria-label="pagination.next"]').attributes('disabled')).toBeDefined()
    await last.get('button[aria-label="pagination.previous"]').trigger('click')
    expect(last.emitted('update:page')).toEqual([[9]])
  })

  it('keeps the first and last pages reachable around an ellipsis', () => {
    const wrapper = mountAt(5)
    const labels = wrapper.findAll('nav button').map(button => button.text()).filter(Boolean)
    expect(labels[0]).toBe('1')
    expect(labels.at(-1)).toBe('10')
    expect(wrapper.text()).toContain('...')
  })
})
