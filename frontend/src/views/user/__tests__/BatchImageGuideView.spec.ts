import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import BatchImageGuideView from '../BatchImageGuideView.vue'

const {
  listKeys,
  listBatchImageJobs,
  listBatchImageItems,
  listBatchImageModels,
  getBatchImageJob,
  fetchPublicSettings,
} = vi.hoisted(() => ({
  listKeys: vi.fn(),
  listBatchImageJobs: vi.fn(),
  listBatchImageItems: vi.fn(),
  listBatchImageModels: vi.fn(),
  getBatchImageJob: vi.fn(),
  fetchPublicSettings: vi.fn(),
}))

vi.mock('@/api', () => ({
  keysAPI: {
    list: listKeys,
  },
}))

vi.mock('@/api/batchImage', () => ({
  cancelBatchImageJob: vi.fn(),
  deleteBatchImageJobRecord: vi.fn(),
  downloadBatchImageZip: vi.fn(),
  getBatchImageItemContent: vi.fn(),
  getBatchImageJob,
  listBatchImageJobs,
  listBatchImageItems,
  listBatchImageModels,
  saveBlob: vi.fn(),
  submitBatchImageJob: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
    fetchPublicSettings,
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn().mockResolvedValue(true),
  }),
}))

vi.mock('@/composables/useConfirmation', () => ({
  confirm: vi.fn().mockResolvedValue(true),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      locale: { value: 'en' },
    }),
  }
})

const DataTableStub = {
  name: 'DataTable',
  props: ['columns', 'data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-row-id="row.id">
        <slot name="cell-id" :row="row" />
        <slot name="cell-actions" :row="row" />
      </div>
      <slot name="empty" />
    </div>
  `,
}

const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

describe('BatchImageGuideView viewport clamping & mobile targets', () => {
  const originalInnerWidth = window.innerWidth
  const originalInnerHeight = window.innerHeight

  const sampleJob = {
    id: 'batch-1',
    task_name: 'Sample Batch',
    parent_batch_id: '',
    status: 'failed',
    model: 'imagen-4',
    provider: 'gemini',
    item_count: 2,
    success_count: 1,
    fail_count: 1,
    estimated_cost: 0.02,
    hold_amount: 0,
    actual_cost: 0.01,
    created_at: 1700000000,
    downloaded_at: 0,
  }

  beforeEach(() => {
    vi.clearAllMocks()
    fetchPublicSettings.mockResolvedValue({})
    listKeys.mockResolvedValue({
      items: [
        {
          id: 1,
          name: 'Gemini Key',
          status: 'active',
          group: { platform: 'gemini', allow_batch_image_generation: true, name: 'Gemini' },
        },
      ],
      total: 1,
      page: 1,
      page_size: 100,
      pages: 1,
    })
    listBatchImageJobs.mockResolvedValue({ data: [sampleJob], has_more: false })
    listBatchImageModels.mockResolvedValue({ data: [] })
    getBatchImageJob.mockResolvedValue(sampleJob)
    listBatchImageItems.mockResolvedValue({
      data: [
        {
          id: 'item-1',
          custom_id: 'p1',
          prompt_preview: 'A very long prompt preview that spans multiple lines inside the popover body',
          status: 'completed',
          output_count: 1,
          images: [],
        },
      ],
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: originalInnerWidth })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: originalInnerHeight })
  })

  const mountView = async () => {
    const wrapper = mount(BatchImageGuideView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          TablePageLayout: {
            template: '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
          },
          UserPageHeader: { template: '<div><slot name="actions" /></div>' },
          DataTable: DataTableStub,
          BaseDialog: BaseDialogStub,
          Select: { props: ['modelValue', 'options'], template: '<div />' },
          SearchInput: { props: ['modelValue'], template: '<input />' },
          Icon: true,
        },
      },
    })
    await flushPromises()
    await nextTick()
    return wrapper
  }

  it('uses >= 44px coarse/mobile targets on the toolbar reset and refresh buttons', async () => {
    const wrapper = await mountView()
    const resetBtn = wrapper.findAll('button').find((btn) => btn.text().includes('common.reset'))!
    const refreshBtn = wrapper.get('button[title="common.refresh"]')

    expect(resetBtn.classes()).toContain('min-h-11')
    expect(refreshBtn.classes()).toEqual(expect.arrayContaining(['min-h-11', 'min-w-11']))
    wrapper.unmount()
  })

  it.each([
    { width: 320, height: 568 },
    { width: 375, height: 812 },
    { width: 667, height: 375 },
    { width: 1440, height: 900 },
  ])('flips moreMenu upward and clamps within $width x $height viewport', async ({ width, height }) => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: width })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: height })

    const wrapper = await mountView()
    const vm = wrapper.vm as unknown as {
      batchJobs: Array<typeof sampleJob & { api_key_id: number; api_key_name: string; child_count: number }>
      moreMenuStyle: Record<string, string>
      openMoreJobId: string
    }
    vm.batchJobs = [{ ...sampleJob, api_key_id: 1, api_key_name: 'Gemini Key', child_count: 0 }]
    await nextTick()

    const moreBtn = wrapper.get('button[title="batchImage.actions.moreActions"]')
    vi.spyOn(moreBtn.element, 'getBoundingClientRect').mockReturnValue({
      x: width - 52,
      y: height - 40,
      top: height - 40,
      bottom: height - 4,
      left: width - 52,
      right: width - 12,
      width: 40,
      height: 36,
      toJSON: () => ({}),
    } as DOMRect)

    await moreBtn.trigger('click')
    await nextTick()

    const menuEl = document.body.querySelector('.w-44.fixed.z-50') as HTMLElement
    expect(menuEl).not.toBeNull()
    vi.spyOn(menuEl, 'getBoundingClientRect').mockReturnValue({
      x: Number.parseFloat(menuEl.style.left),
      y: height - 48 - 108,
      top: height - 48 - 108,
      bottom: height - 48,
      left: Number.parseFloat(menuEl.style.left),
      right: Number.parseFloat(menuEl.style.left) + 176,
      width: 176,
      height: 108,
      toJSON: () => ({}),
    } as DOMRect)

    // Re-trigger open/measure verification by validating clamped style bounds
    const bottomPx = Number.parseFloat(vm.moreMenuStyle.bottom)
    const maxHeightPx = Number.parseFloat(vm.moreMenuStyle.maxHeight)
    expect(vm.moreMenuStyle['--transform-origin']).toBe('bottom right')
    expect(bottomPx).toBeGreaterThanOrEqual(8)
    expect(height - bottomPx).toBeLessThanOrEqual(height - 8)
    expect(height - bottomPx - Math.min(108, maxHeightPx)).toBeGreaterThanOrEqual(8)

    wrapper.unmount()
  })

  it.each([
    { width: 320, height: 568 },
    { width: 375, height: 812 },
    { width: 667, height: 375 },
    { width: 1440, height: 900 },
  ])('flips promptPopover upward and clamps within $width x $height viewport for 246px popovers', async ({ width, height }) => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: width })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: height })

    const wrapper = await mountView()
    const vm = wrapper.vm as unknown as {
      currentJob: typeof sampleJob | null
      items: Array<Record<string, unknown>>
      promptPopover: { visible: boolean; text: string; style: Record<string, string> }
    }
    vm.currentJob = sampleJob
    vm.items = [
      {
        id: 'item-1',
        batch_id: 'batch-1',
        source_task_name: 'Sample Batch',
        custom_id: 'p1',
        prompt_preview: 'Long prompt preview text',
        status: 'completed',
        output_count: 1,
        images: [],
      },
    ]
    await nextTick()

    const trigger = wrapper.get('.batch-prompt-trigger')
    vi.spyOn(trigger.element, 'getBoundingClientRect').mockReturnValue({
      x: 24,
      y: height - 50,
      top: height - 50,
      bottom: height - 26,
      left: 24,
      right: 224,
      width: 200,
      height: 24,
      toJSON: () => ({}),
    } as DOMRect)

    await trigger.trigger('click')
    await nextTick()

    const popoverEl = document.body.querySelector('.batch-prompt-popover') as HTMLElement
    expect(popoverEl).not.toBeNull()
    const bottomPx = Number.parseFloat(vm.promptPopover.style.bottom)
    const maxHeightPx = Number.parseFloat(vm.promptPopover.style.maxHeight)
    const renderedHeight = Math.min(246, maxHeightPx)

    expect(vm.promptPopover.style['--transform-origin']).toBe('bottom left')
    expect(bottomPx).toBeGreaterThanOrEqual(16)
    expect(height - bottomPx).toBeLessThanOrEqual(height - 16)
    expect(height - bottomPx - renderedHeight).toBeGreaterThanOrEqual(16)

    wrapper.unmount()
  })
})
