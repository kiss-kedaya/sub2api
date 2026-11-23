import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import UserApiKeysModal from '../UserApiKeysModal.vue'
import type { AdminUser } from '@/types'

const { getKeys, getAllGroups, updateGroup, showSuccess, showError, showInfo } = vi.hoisted(() => ({
  getKeys: vi.fn(),
  getAllGroups: vi.fn(),
  updateGroup: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
}))
vi.mock('@/api/admin', () => ({ adminAPI: {
  users: { getUserApiKeys: getKeys },
  groups: { getAll: getAllGroups },
  apiKeys: { updateApiKeyGroup: updateGroup },
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess, showInfo }) }))
vi.mock('@/utils/format', () => ({ formatDateTime: (value: string) => value }))
// step-up 弹窗会拉 @/api（依赖 i18n 实例），桩成两个按钮模拟「验证通过 / 取消」。
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({
  default: {
    props: ['controller'],
    template:
      '<div><button data-test="totp-verify" @click="controller.onVerified()">verify</button>' +
      '<button data-test="totp-cancel" @click="controller.onCancel()">cancel</button></div>',
  },
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
beforeEach(() => {
  localStorage.clear()
  delete window.__APP_CONFIG__
  getKeys.mockReset()
  getAllGroups.mockReset()
  getAllGroups.mockResolvedValue([])
  updateGroup.mockReset()
  showSuccess.mockReset()
  showError.mockReset()
  showInfo.mockReset()
  vi.spyOn(console, 'error').mockImplementation(() => {})
})
afterEach(() => vi.restoreAllMocks())
function deferred() {
  let resolve!: (value: unknown) => void
  let reject!: (error: Error) => void
  const promise = new Promise((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const user = (id: number) => ({ id, email: `user${id}@example.com`, username: `user${id}` }) as AdminUser
const keys = (id: number, name: string, page = 1, total = 1, pageSize = 20) => ({
  items: [{ id, name, key_prefix: 'sk-example', key_masked: 'sk-example****-tests', status: 'active', created_at: '2026-09-20', group_id: null }],
  total, page, page_size: pageSize, pages: Math.ceil(total / pageSize),
})
async function open() {
  const wrapper = mount(UserApiKeysModal, {
    props: { show: false, user: user(1) },
    global: { stubs: {
      BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
      GroupBadge: true, GroupOptionItem: true,
      // 分组下拉内容常驻渲染，便于直接点击选项验证 step-up 流程。
      UPopover: { template: '<div><slot /><slot name="content" /></div>' },
      Select: {
        props: ['modelValue', 'options'],
        template: `<select :value="modelValue" @change="$emit('update:model-value', $event.target.value)"><option v-for="option in options" :value="option.value">{{ option.label }}</option></select>`,
      },
    } },
  })
  await wrapper.setProps({ show: true })
  return wrapper
}
async function switchUser(wrapper: Awaited<ReturnType<typeof open>>) {
  await wrapper.setProps({ show: false })
  await wrapper.setProps({ show: true, user: user(2) })
}

describe('user API key masking', () => {
  it('renders the server key_masked value instead of slicing the key in the frontend', async () => {
    getKeys.mockResolvedValue(keys(1, 'masked-key'))
    const wrapper = await open(); await flushPromises()
    expect(wrapper.text()).toContain('sk-example****-tests')
    expect(wrapper.text()).not.toContain('sk-example-key-value-for-tests')
  })
})

describe('user API key loading', () => {
  it('does not display the previous user keys when the next load fails', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-user-key')).mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = await open(); await flushPromises()
    expect(wrapper.text()).toContain('first-user-key')
    await switchUser(wrapper); await flushPromises()
    expect(wrapper.text()).toContain('user2@example.com')
    expect(wrapper.text()).not.toContain('first-user-key')
  })

  it('does not replace current keys with a late previous response', async () => {
    const old = deferred()
    getKeys.mockReturnValueOnce(old.promise).mockResolvedValueOnce(keys(2, 'current-user-key'))
    const wrapper = await open()
    await switchUser(wrapper); await flushPromises()
    old.resolve(keys(1, 'old-user-key')); await flushPromises()
    expect(wrapper.text()).toContain('current-user-key')
    expect(wrapper.text()).not.toContain('old-user-key')
  })

  it('keeps the current request loading when an obsolete request fails', async () => {
    const old = deferred(); const current = deferred()
    getKeys.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = await open()
    await switchUser(wrapper)
    old.reject(new Error('obsolete')); await flushPromises()
    expect(wrapper.find('.animate-spin').exists()).toBe(true)
    current.resolve(keys(2, 'current-user-key')); await flushPromises()
    expect(wrapper.text()).toContain('current-user-key')
    expect(wrapper.find('.animate-spin').exists()).toBe(false)
  })

  it('loads keys when the selected user changes while the dialog is open', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-user-key')).mockResolvedValueOnce(keys(2, 'second-user-key'))
    const wrapper = await open(); await flushPromises()
    await wrapper.setProps({ user: user(2) }); await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(2, 1, 20)
    expect(wrapper.text()).toContain('second-user-key')
    expect(wrapper.text()).not.toContain('first-user-key')
  })
})


describe('user API key pagination', () => {
  it('loads another page only when requested and supports returning to the first page', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-page-key', 1, 21))
      .mockResolvedValueOnce(keys(21, 'second-page-key', 2, 21))
      .mockResolvedValueOnce(keys(1, 'first-page-key', 1, 21))
    const wrapper = await open()
    await flushPromises()
    expect(getKeys).toHaveBeenCalledTimes(1)
    expect(getKeys).toHaveBeenLastCalledWith(1, 1, 20)
    await wrapper.get('button[aria-label="pagination.next"]').trigger('click')
    await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(1, 2, 20)
    expect(wrapper.text()).toContain('second-page-key')
    expect(wrapper.text()).not.toContain('first-page-key')
    expect(wrapper.get('button[aria-label="pagination.next"]').attributes('disabled')).toBeDefined()
    await wrapper.get('button[aria-label="pagination.previous"]').trigger('click')
    await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(1, 1, 20)
    expect(wrapper.text()).toContain('first-page-key')
  })

  it('returns to page one when the page size changes', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-page-key', 1, 61))
      .mockResolvedValueOnce(keys(21, 'second-page-key', 2, 61))
      .mockResolvedValueOnce(keys(1, 'larger-page-key', 1, 61, 50))
    const wrapper = await open()
    await flushPromises()
    await wrapper.get('button[aria-label="pagination.next"]').trigger('click')
    await flushPromises()
    await wrapper.get('select').setValue('50')
    await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(1, 1, 50)
    expect(wrapper.text()).toContain('larger-page-key')
    expect(wrapper.get('button[aria-current="page"]').text()).toBe('1')
  })

  it('discards a previous user page response and starts the next user at page one', async () => {
    const stalePage = deferred()
    getKeys.mockResolvedValueOnce(keys(1, 'first-page-key', 1, 21))
      .mockReturnValueOnce(stalePage.promise)
      .mockResolvedValueOnce(keys(2, 'current-user-key'))
    const wrapper = await open()
    await flushPromises()
    await wrapper.get('button[aria-label="pagination.next"]').trigger('click')
    await wrapper.setProps({ user: user(2) })
    await flushPromises()
    stalePage.resolve(keys(21, 'stale-second-page-key', 2, 21))
    await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(2, 1, 20)
    expect(wrapper.text()).toContain('current-user-key')
    expect(wrapper.text()).not.toContain('stale-second-page-key')
    expect(wrapper.get('button[aria-current="page"]').text()).toBe('1')
  })

  it('uses the server effective page size for subsequent requests', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'server-capped-page', 1, 25, 10))
      .mockResolvedValueOnce(keys(11, 'next-capped-page', 2, 25, 10))
    const wrapper = await open()
    await flushPromises()
    await wrapper.get('button[aria-label="pagination.next"]').trigger('click')
    await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(1, 2, 10)
    expect(wrapper.text()).toContain('next-capped-page')
  })
})

// ── 管理端 Key 列表：智能路由 / 单分组徽章 ──────────────────────────────────
function keyRow(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    user_id: 1,
    name: 'key-one',
    key_prefix: 'sk-example',
    key_masked: 'sk-example****-test',
    status: 'active',
    created_at: '2026-09-20',
    group_id: null,
    ...overrides,
  }
}

function pageOf(items: unknown[]) {
  return { items, total: items.length, page: 1, page_size: 20, pages: 1 }
}

function stepUpError(code: string) {
  return Object.assign(new Error(code), { code })
}

describe('user API key route mode badge', () => {
  it('renders smart routing badge with the extra route count from the backend fields', async () => {
    getKeys.mockResolvedValue(pageOf([keyRow({
      group_id: 1,
      group: { id: 1, name: 'Group One', platform: 'anthropic' },
      group_ids: [1, 2, 3],
      route_mode: 'smart',
      route_group_count: 3,
      is_smart_routing: true,
    })]))
    const wrapper = await open(); await flushPromises()
    expect(wrapper.find('[data-test="key-route-mode-smart"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="key-route-mode-smart"]').text()).toContain('admin.users.routeModeSmart')
    expect(wrapper.find('[data-test="key-route-mode-single"]').exists()).toBe(false)
  })

  it('falls back to group_ids when an older server omits route_mode fields', async () => {
    getKeys.mockResolvedValue(pageOf([keyRow({
      group_id: 1,
      group: { id: 1, name: 'Group One', platform: 'anthropic' },
      group_ids: [1, 2],
    })]))
    const wrapper = await open(); await flushPromises()
    expect(wrapper.find('[data-test="key-route-mode-smart"]').exists()).toBe(true)
  })

  it('renders the single-group badge for a key bound to one group', async () => {
    getKeys.mockResolvedValue(pageOf([keyRow({
      group_id: 2,
      group: { id: 2, name: 'Group Two', platform: 'openai' },
      group_ids: [2],
      route_mode: 'single',
      route_group_count: 1,
      is_smart_routing: false,
    })]))
    const wrapper = await open(); await flushPromises()
    expect(wrapper.find('[data-test="key-route-mode-single"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="key-route-mode-smart"]').exists()).toBe(false)
  })

  it('renders no route badge when the key has no group', async () => {
    getKeys.mockResolvedValue(pageOf([keyRow()]))
    const wrapper = await open(); await flushPromises()
    expect(wrapper.find('[data-test="key-route-mode-smart"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="key-route-mode-single"]').exists()).toBe(false)
  })
})

// ── 管理端改 Key 分组：必须二次验证后才提交 ─────────────────────────────────
describe('user API key group change step-up', () => {
  beforeEach(() => {
    getAllGroups.mockResolvedValue([{ id: 2, name: 'Group Two', platform: 'openai', status: 'active' }])
  })

  it('prompts step-up before submitting and retries once verified', async () => {
    getKeys.mockResolvedValue(pageOf([keyRow()]))
    updateGroup
      .mockRejectedValueOnce(stepUpError('STEP_UP_REQUIRED'))
      .mockResolvedValueOnce({ api_key: keyRow({ group_id: 2 }), auto_granted_group_access: false })

    const wrapper = await open(); await flushPromises()
    // 不直接 await：changeGroup 会停在 step-up 对话框上，等操作者输入验证码。
    const pending = (wrapper.vm as any).changeGroup((wrapper.vm as any).apiKeys[0], 2)
    await flushPromises()

    // 第一次已被 step-up 拒绝，此时还没有提交成功。
    expect(updateGroup).toHaveBeenCalledTimes(1)
    expect(showSuccess).not.toHaveBeenCalled()

    await wrapper.get('[data-test="totp-verify"]').trigger('click')
    await pending
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledTimes(2)
    expect(updateGroup).toHaveBeenLastCalledWith(1, 2)
    expect(showSuccess).toHaveBeenCalledWith('admin.users.groupChangedSuccess')
    expect(showError).not.toHaveBeenCalled()
  })

  it('does not submit when the operator cancels the step-up dialog', async () => {
    getKeys.mockResolvedValue(pageOf([keyRow()]))
    updateGroup.mockRejectedValue(stepUpError('STEP_UP_REQUIRED'))

    const wrapper = await open(); await flushPromises()
    const pending = (wrapper.vm as any).changeGroup((wrapper.vm as any).apiKeys[0], 2)
    await flushPromises()
    await wrapper.get('[data-test="totp-cancel"]').trigger('click')
    await pending
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledTimes(1)
    expect(showInfo).toHaveBeenCalledWith('stepUp.cancelled')
    expect(showError).not.toHaveBeenCalled()
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('surfaces a clear error when the operator has no usable second factor', async () => {
    getKeys.mockResolvedValue(pageOf([keyRow()]))
    updateGroup.mockRejectedValue(stepUpError('STEP_UP_TOTP_NOT_ENABLED'))

    const wrapper = await open(); await flushPromises()
    await (wrapper.vm as any).changeGroup((wrapper.vm as any).apiKeys[0], 2)
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledTimes(1)
    expect(showError).toHaveBeenCalledWith('stepUp.notEnabled')
    expect(showSuccess).not.toHaveBeenCalled()
  })
})
