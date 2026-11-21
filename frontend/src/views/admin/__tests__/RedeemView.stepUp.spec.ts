import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

import RedeemView from '../RedeemView.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { StepUpController } from '@/composables/useStepUp'

const { listRedeemCodes, batchUpdateRedeemCodes, generateRedeemCodes, getAllGroups, showSuccess, showError, showInfo } =
  vi.hoisted(() => ({
    listRedeemCodes: vi.fn(),
    batchUpdateRedeemCodes: vi.fn(),
    generateRedeemCodes: vi.fn(),
    getAllGroups: vi.fn(),
    showSuccess: vi.fn(),
    showError: vi.fn(),
    showInfo: vi.fn()
  }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    redeem: {
      list: listRedeemCodes,
      generate: generateRedeemCodes,
      delete: vi.fn(),
      batchDelete: vi.fn(),
      batchUpdate: batchUpdateRedeemCodes,
      exportCodes: vi.fn()
    },
    groups: {
      getAll: getAllGroups
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess,
    showError,
    showInfo
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const DataTableStub = {
  props: ['columns', 'data'],
  template: `
    <table>
      <thead>
        <tr>
          <th v-for="column in columns" :key="column.key">
            <slot :name="'header-' + column.key" :column="column">{{ column.label }}</slot>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in data" :key="row.id">
          <td v-for="column in columns" :key="column.key">
            <slot :name="'cell-' + column.key" :row="row" :value="row[column.key]">
              {{ row[column.key] }}
            </slot>
          </td>
        </tr>
      </tbody>
    </table>
  `
}

const SelectStub = {
  props: ['modelValue', 'options'],
  emits: ['update:modelValue', 'change'],
  setup(
    props: { options: Array<{ value: unknown; label: string }> },
    { emit }: { emit: (event: string, ...args: unknown[]) => void }
  ) {
    const onChange = (event: Event) => {
      const raw = (event.target as HTMLSelectElement).value
      const option = props.options.find((item) => String(item.value ?? '') === raw)
      const value = option ? option.value : raw
      emit('update:modelValue', value)
      emit('change', value, option ?? null)
    }
    return { onChange }
  },
  template: `
    <select v-bind="$attrs" :value="modelValue ?? ''" @change="onChange">
      <option v-for="option in options" :key="String(option.value ?? '')" :value="option.value ?? ''">
        {{ option.label }}
      </option>
    </select>
  `
}

// The real dialog owns the TOTP form; for the retry contract we only need the controller.
const TotpStepUpDialogStub = defineComponent({
  name: 'TotpStepUpDialog',
  props: { controller: { type: Object, required: true } },
  template: '<div data-test="totp-step-up-stub" />'
})

function mountRedeemView() {
  return mount(RedeemView, {
    attachTo: document.body,
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
        },
        DataTable: DataTableStub,
        Pagination: true,
        ConfirmDialog: true,
        Select: SelectStub,
        GroupBadge: true,
        GroupOptionItem: true,
        Icon: true,
        Teleport: true,
        TotpStepUpDialog: TotpStepUpDialogStub
      }
    }
  })
}

function stepUpController(wrapper: ReturnType<typeof mountRedeemView>): StepUpController {
  const dialog = wrapper.findComponent(TotpStepUpDialogStub)
  return dialog.props('controller') as StepUpController
}

async function submitBatchDisable(wrapper: ReturnType<typeof mountRedeemView>) {
  await flushPromises()
  await wrapper.findAll('[data-test="select-code"]')[0].trigger('click')
  await wrapper.get('[data-test="batch-update-open"]').trigger('click')
  await flushPromises()
  await wrapper.get('[data-test="batch-field-status"]').trigger('click')
  await flushPromises()
  await wrapper.get('[data-test="batch-status-select"]').setValue('disabled')
  await wrapper.get('[data-test="batch-update-form"]').trigger('submit')
  await flushPromises()
}

const stepUpRequired = { code: 'STEP_UP_REQUIRED', status: 403 }

describe('admin RedeemView step-up 2FA', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    document.body.innerHTML = ''

    listRedeemCodes.mockReset()
    batchUpdateRedeemCodes.mockReset()
    generateRedeemCodes.mockReset()
    getAllGroups.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    showInfo.mockReset()

    listRedeemCodes.mockResolvedValue({
      items: [
        {
          id: 1,
          code: 'CODE-1',
          type: 'balance',
          value: 10,
          status: 'unused',
          used_by: null,
          used_at: null,
          created_at: '2026-01-01T00:00:00Z',
          expires_at: null
        }
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    generateRedeemCodes.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
  })

  it('prompts for TOTP on STEP_UP_REQUIRED list read and retries it', async () => {
    listRedeemCodes.mockReset()
    listRedeemCodes.mockRejectedValueOnce(stepUpRequired).mockResolvedValueOnce({
      items: [
        {
          id: 1,
          code: 'CODE-1',
          type: 'balance',
          value: 10,
          status: 'unused',
          used_by: null,
          used_at: null,
          created_at: '2026-01-01T00:00:00Z',
          expires_at: null
        }
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })

    const wrapper = mountRedeemView()
    await flushPromises()

    // 卡密列表是敏感读：第一次被 403 STEP_UP_REQUIRED 拦住，弹 TOTP 对话框。
    expect(listRedeemCodes).toHaveBeenCalledTimes(1)
    expect(stepUpController(wrapper).visible.value).toBe(true)

    stepUpController(wrapper).onVerified()
    await flushPromises()

    // 验证通过后自动重试同一次列表读取，并把明文 code 渲染出来。
    expect(listRedeemCodes).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('CODE-1')
    expect(stepUpController(wrapper).visible.value).toBe(false)
    wrapper.unmount()
  })

  it('shows the machine-credential message when the read is STEP_UP_ADMIN_API_KEY_FORBIDDEN', async () => {
    listRedeemCodes.mockReset()
    listRedeemCodes.mockRejectedValue({ code: 'STEP_UP_ADMIN_API_KEY_FORBIDDEN', status: 403 })

    const wrapper = mountRedeemView()
    await flushPromises()

    expect(stepUpController(wrapper).visible.value).toBe(false)
    expect(showError).toHaveBeenCalledWith('stepUp.adminApiKeyForbidden')
    wrapper.unmount()
  })

  it('prompts for TOTP on STEP_UP_REQUIRED and retries the write once verified', async () => {
    batchUpdateRedeemCodes
      .mockRejectedValueOnce(stepUpRequired)
      .mockResolvedValueOnce({ updated: 1, message: 'ok' })

    const wrapper = mountRedeemView()
    await submitBatchDisable(wrapper)

    // First attempt blocked: dialog opened, no success toast, no extra call yet.
    expect(batchUpdateRedeemCodes).toHaveBeenCalledTimes(1)
    expect(stepUpController(wrapper).visible.value).toBe(true)
    expect(showSuccess).not.toHaveBeenCalled()

    // Entering a valid TOTP code resolves the prompt; the original write is replayed.
    stepUpController(wrapper).onVerified()
    await flushPromises()

    expect(batchUpdateRedeemCodes).toHaveBeenCalledTimes(2)
    expect(batchUpdateRedeemCodes).toHaveBeenLastCalledWith([1], { status: 'disabled' })
    expect(showSuccess).toHaveBeenCalledWith('admin.redeem.batchUpdateSuccess')
    expect(stepUpController(wrapper).visible.value).toBe(false)
    wrapper.unmount()
  })

  it('reports the operation as not executed when the TOTP prompt is cancelled', async () => {
    batchUpdateRedeemCodes.mockRejectedValue(stepUpRequired)

    const wrapper = mountRedeemView()
    await submitBatchDisable(wrapper)

    expect(stepUpController(wrapper).visible.value).toBe(true)
    stepUpController(wrapper).onCancel()
    await flushPromises()

    // Cancelling must not replay the write and must tell the user nothing ran.
    expect(batchUpdateRedeemCodes).toHaveBeenCalledTimes(1)
    expect(showInfo).toHaveBeenCalledWith('stepUp.cancelled')
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

// BaseDialog is imported for parity with the production component tree; keep the import used.
void BaseDialog
