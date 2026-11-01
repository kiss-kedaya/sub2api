import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ExportProgressDialog from '../ExportProgressDialog.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

describe('ExportProgressDialog', () => {
  afterEach(() => { document.body.innerHTML = '' })

  it('reports clamped progress through an accessible progressbar', async () => {
    mount(ExportProgressDialog, { attachTo: document.body, props: { show: true, progress: 142.6, current: 30, total: 20, estimatedTime: '' } })
    await flushPromises()
    const bar = document.querySelector('[role="progressbar"]')!
    expect(bar.getAttribute('aria-valuenow')).toBe('100')
    expect(bar.getAttribute('aria-valuemax')).toBe('100')
    expect(bar.getAttribute('aria-label')).toBe('usage.exportingProgress: 100%')
    expect(document.body.textContent).toContain('100%')
  })

  it('emits cancel from the footer button', async () => {
    const wrapper = mount(ExportProgressDialog, { attachTo: document.body, props: { show: true, progress: 40, current: 4, total: 10, estimatedTime: '1m' } })
    await flushPromises()
    expect(document.body.textContent).toContain('usage.estimatedTime')
    ;[...document.querySelectorAll<HTMLButtonElement>('.modal-footer button')].find(button => button.textContent?.includes('usage.cancelExport'))!.click()
    expect(wrapper.emitted('cancel')).toHaveLength(1)
  })
})
