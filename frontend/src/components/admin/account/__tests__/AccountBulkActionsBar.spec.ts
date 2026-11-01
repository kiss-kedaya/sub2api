import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import AccountBulkActionsBar from '../AccountBulkActionsBar.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

let wrapper: VueWrapper | null = null
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
})

// Less frequent bulk actions sit in the "..." menu (UDropdownMenu, teleported to body).
async function openMoreMenu(w: VueWrapper) {
  await w.get('[data-test="bulk-more"]').trigger('keydown', { key: 'Enter' })
  await flushPromises()
}

const menuItem = (label: string) =>
  Array.from(document.body.querySelectorAll<HTMLElement>('[role="menuitem"]'))
    .find(item => item.textContent?.includes(label))

describe('AccountBulkActionsBar', () => {
  it('allows selecting all results before any row is selected', async () => {
    wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false,
        placement: 'footer'
      }
    })

    const button = wrapper.findAll('button').find(item =>
      item.text().includes('admin.accounts.bulkActions.selectAllResults')
    )

    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(wrapper.emitted('select-all-results')).toHaveLength(1)
  })

  it('preserves the upstream billing probe action from v0.1.166', async () => {
    wrapper = mount(AccountBulkActionsBar, {
      attachTo: document.body,
      props: {
        selectedIds: [1],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    await openMoreMenu(wrapper)
    const button = menuItem('admin.accounts.bulkActions.probeUpstreamBilling')

    expect(button).toBeDefined()
    button!.click()
    await flushPromises()
    expect(wrapper.emitted('probe-upstream-billing')).toHaveLength(1)
  })

  it('emits test-models from the batch detection button', async () => {
    wrapper = mount(AccountBulkActionsBar, {
      attachTo: document.body,
      props: {
        selectedIds: [1, 2],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    await openMoreMenu(wrapper)
    const button = menuItem('admin.accounts.bulkActions.testModels')

    expect(button).toBeDefined()
    button!.click()
    await flushPromises()
    expect(wrapper.emitted('test-models')).toHaveLength(1)
  })

  it('shows the selected count on the bulk edit button and keeps delete one click away', async () => {
    wrapper = mount(AccountBulkActionsBar, {
      props: { selectedIds: [1, 2, 3], totalResults: 45, selectingAll: false, allResultsSelected: false }
    })

    const edit = wrapper.findAll('button').find(item => item.text().includes('admin.accounts.bulkActions.edit'))!
    expect(edit.text()).toContain('3')
    await edit.trigger('click')
    expect(wrapper.emitted('edit-selected')).toHaveLength(1)

    await wrapper.findAll('button').find(item => item.text() === 'admin.accounts.bulkActions.delete')!.trigger('click')
    expect(wrapper.emitted('delete')).toHaveLength(1)
  })

  it('offers bulk update by filter when nothing is selected', async () => {
    wrapper = mount(AccountBulkActionsBar, {
      props: { selectedIds: [], totalResults: 45, selectingAll: false, allResultsSelected: false }
    })

    expect(wrapper.find('[data-test="bulk-more"]').exists()).toBe(false)
    await wrapper.findAll('button').find(item => item.text() === 'admin.accounts.bulkEdit.submit')!.trigger('click')
    expect(wrapper.emitted('edit-filtered')).toHaveLength(1)
  })
})
