import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

import RedeemView from '../RedeemView.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'

const {
  listRedeemCodes,
  generateRedeemCodes,
  copyToClipboard,
  getAllGroups,
  showSuccess,
  showError,
  showInfo
} = vi.hoisted(() => ({
  listRedeemCodes: vi.fn(),
  generateRedeemCodes: vi.fn(),
  copyToClipboard: vi.fn(),
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
      batchUpdate: vi.fn(),
      exportCodes: vi.fn()
    },
    groups: {
      getAll: getAllGroups
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError, showInfo })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const DataTableStub = {
  props: ['columns', 'data'],
  template: `
    <table>
      <thead><tr><th v-for="column in columns" :key="column.key">{{ column.label }}</th></tr></thead>
      <tbody>
        <tr v-for="row in data" :key="row.id" :data-test="'code-row-' + row.id">
          <td v-for="column in columns" :key="column.key">
            <slot :name="'cell-' + column.key" :row="row" :value="row[column.key]">{{ row[column.key] }}</slot>
          </td>
        </tr>
      </tbody>
    </table>
  `
}

const TotpStepUpDialogStub = defineComponent({
  name: 'TotpStepUpDialog',
  props: { controller: { type: Object, required: true } },
  template: '<div data-test="totp-step-up-stub" />'
})

const PLAINTEXT = 'AAAABBBBCCCCDDDDEEEEFFFF00001111'

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
        GroupBadge: true,
        GroupOptionItem: true,
        Icon: true,
        Teleport: true,
        TotpStepUpDialog: TotpStepUpDialogStub
      }
    }
  })
}

function resultDialog(wrapper: ReturnType<typeof mountRedeemView>) {
  return wrapper
    .findAllComponents(BaseDialog)
    .find((dialog) => dialog.props('title') === 'admin.redeem.generatedSuccessfully')
}

async function generateOnce(wrapper: ReturnType<typeof mountRedeemView>) {
  await flushPromises()
  await wrapper.get('[data-test="generate-open"]').trigger('click')
  await flushPromises()
  await wrapper.get('[data-test="generate-form"]').trigger('submit')
  await flushPromises()
}

// 强制展示窗口是真实的 setTimeout（1s），这里用真实等待验证，避免假定时器与
// flushPromises 的微任务刷新互相打架。
const waitForUnlock = () => new Promise((resolve) => setTimeout(resolve, 1100))

describe('admin RedeemView plaintext-once contract', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    document.body.innerHTML = ''

    listRedeemCodes.mockReset()
    generateRedeemCodes.mockReset()
    copyToClipboard.mockReset()
    getAllGroups.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    showInfo.mockReset()

    copyToClipboard.mockResolvedValue(true)
    listRedeemCodes.mockResolvedValue({
      items: [
        {
          id: 1,
          // The API only ever returns a mask for stored codes.
          code: 'AAAA****',
          code_prefix: 'AAAABBBB',
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
    generateRedeemCodes.mockResolvedValue([
      {
        id: 2,
        code: PLAINTEXT,
        code_prefix: PLAINTEXT.slice(0, 8),
        type: 'balance',
        value: 10,
        status: 'unused',
        created_at: '2026-01-01T00:00:00Z',
        expires_at: null,
        validity_days: 30
      }
    ])
    getAllGroups.mockResolvedValue([])
  })

  it('offers no copy action for stored (masked) codes in the list', async () => {
    const wrapper = mountRedeemView()
    await flushPromises()

    const row = wrapper.get('[data-test="code-row-1"]')
    expect(row.text()).toContain('AAAA****')
    const copyButtons = Array.from(row.element.querySelectorAll('button')).filter((button) =>
      Boolean(button.querySelector('.i-lucide-copy'))
    )
    expect(copyButtons).toHaveLength(0)
    wrapper.unmount()
  })

  it('shows the plaintext once, blocks closing for the minimum display window and copies at most once', async () => {
    const wrapper = mountRedeemView()
    await generateOnce(wrapper)

    const dialog = resultDialog(wrapper)
    expect(dialog).toBeTruthy()
    // The only plaintext entry point: the freshly generated code, rendered in the
    // read-only textarea of the generation receipt.
    const receiptTextarea = () => dialog!.find('textarea').element as HTMLTextAreaElement
    expect(receiptTextarea().value).toBe(PLAINTEXT)
    // 1 秒内不允许关闭（连蒙带猜点了关闭就会永久丢卡密）。
    expect(dialog!.props('closeButtonDisabled')).toBe(true)

    const copyButton = () =>
      wrapper
        .findAll('button')
        .find(
          (button) =>
            button.text().includes('admin.redeem.copyAll') || button.text().includes('admin.redeem.copied')
        )
    expect(copyButton()).toBeTruthy()
    expect(copyButton()!.attributes('disabled')).toBeDefined()

    await waitForUnlock()
    await flushPromises()
    expect(resultDialog(wrapper)!.props('closeButtonDisabled')).toBe(false)
    expect(copyButton()!.attributes('disabled')).toBeUndefined()

    await copyButton()!.trigger('click')
    await flushPromises()
    expect(copyToClipboard).toHaveBeenCalledTimes(1)
    expect(copyToClipboard).toHaveBeenCalledWith(PLAINTEXT, 'admin.redeem.copied')

    // 复制一次之后按钮锁死：不允许二次复制。
    await copyButton()!.trigger('click')
    await flushPromises()
    expect(copyToClipboard).toHaveBeenCalledTimes(1)
    expect(copyButton()!.attributes('disabled')).toBeDefined()

    // 不再提供「下载」这种第二条明文外带通道。
    expect(wrapper.html()).not.toContain('i-lucide-download')
    wrapper.unmount()
  })

  it('discards the plaintext when the dialog is closed', async () => {
    const wrapper = mountRedeemView()
    await generateOnce(wrapper)

    await waitForUnlock()
    await flushPromises()

    const closeButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('common.close'))
    expect(closeButton).toBeTruthy()
    await closeButton!.trigger('click')
    await flushPromises()

    expect(resultDialog(wrapper)!.props('show')).toBe(false)
    expect(wrapper.text()).not.toContain(PLAINTEXT)
    wrapper.unmount()
  })
})
