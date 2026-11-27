import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useEphemeralKeyStore } from '@/stores/ephemeralKeys'

import type { ApiKey } from '@/types'
import KeysView from '../KeysView.vue'
import { useAppUpdate } from '@/composables/useAppUpdate'

const {
  listKeys,
  createKey,
  updateKey,
  deleteKey,
  deleteMany,
  rotateKey,
  batchRotateKeys,
  getOverview,
  putSecretSeal,
  sealApiKey,
  getVerificationFactors,
  authorizeReveal,
  getSecretSeal,
  beginKeyPasskeyVerification,
  finishKeyPasskeyVerification,
  getKeyRotationGateStatus,
  getPublicSettings,
  getDashboardApiKeysUsage,
  getAvailableGroups,
  getUserGroupRates,
  showError,
  showSuccess,
  copyToClipboard,
  isCurrentStep,
  nextStep,
  authUser,
} = vi.hoisted(() => ({
  listKeys: vi.fn(),
  createKey: vi.fn(),
  updateKey: vi.fn(),
  deleteKey: vi.fn(),
  deleteMany: vi.fn(),
  rotateKey: vi.fn(),
  batchRotateKeys: vi.fn(),
  getOverview: vi.fn(),
  putSecretSeal: vi.fn(),
  sealApiKey: vi.fn(),
  getVerificationFactors: vi.fn(),
  authorizeReveal: vi.fn(),
  getSecretSeal: vi.fn(),
  beginKeyPasskeyVerification: vi.fn(),
  finishKeyPasskeyVerification: vi.fn(),
  getKeyRotationGateStatus: vi.fn(),
  getPublicSettings: vi.fn(),
  getDashboardApiKeysUsage: vi.fn(),
  getAvailableGroups: vi.fn(),
  getUserGroupRates: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  copyToClipboard: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
  authUser: { value: null as null | { id: number; email: string } },
}))

const messages: Record<string, string> = {
  'common.actions': 'Actions',
  'common.name': 'Name',
  'common.refresh': 'Refresh',
  'common.status': 'Status',
  'keys.apiKey': 'API Key',
  'keys.title': 'API Keys',
  'keys.allGroups': 'All Groups',
  'keys.allStatus': 'All Status',
  'keys.columnSettings': 'Column Settings',
  'keys.createKey': 'Create API Key',
  'auth.stepProgress': 'Step {current} of {total}',
  'keys.createStep.detailsHint': 'Name the key and pick a route to create it.',
  'keys.createStep.moreSettings': 'More settings',
  'keys.createStep.moreSettingsHint': 'Custom key, IP rules, quota, rate limits, expiry',
  'keys.createStep.details': 'Details',
  'keys.createStep.verify': 'Verification',
  'keys.createStep.next': 'Next',
  'keys.createStep.back': 'Back',
  'keys.createStep.verifyHint': 'Verify your identity once before creating a key.',
  'keys.factor.description': 'Verify your identity once.',
  'keys.factor.methodLabel': 'Verification method',
  'keys.factor.methodPassword': 'Login password',
  'keys.factor.methodTotp': 'Dynamic code',
  'keys.factor.methodPasskey': 'Passkey',
  'keys.factor.preferred': 'Recommended',
  'keys.factor.passwordLabel': 'Login password',
  'keys.factor.passwordPlaceholder': 'Enter your login password',
  'keys.factor.passwordHint': 'Only used for this verification.',
  'keys.factor.decryptPasswordLabel': 'Login password (for local decryption)',
  'keys.factor.decryptPasswordHint': 'Decryption happens in this browser.',
  'keys.factor.totpLabel': '6-digit dynamic code',
  'keys.factor.totpPlaceholder': 'Enter the 6-digit code',
  'keys.factor.passkeyHint': 'Verify with a passkey bound to this device.',
  'keys.factor.passkeyVerify': 'Verify with Passkey',
  'keys.factor.passkeyReverify': 'Verify Passkey again',
  'keys.factor.passkeyVerified': 'Verified, valid for {seconds}s',
  'keys.factor.passkeyUnsupported': 'This browser or device does not support passkeys.',
  'keys.factor.passkeyFailed': 'Passkey verification failed. Please retry.',
  'keys.factor.unavailable': 'No verification method is available for this account.',
  'keys.factor.requiredAll': 'Complete identity verification first.',
  'keys.factor.passwordRequired': 'Enter your login password.',
  'keys.factor.totpRequired': 'Enter the 6-digit dynamic code.',
  'keys.factor.passkeyRequired': 'Verify with Passkey first.',
  'keys.factor.factorUnavailable': 'This verification method is currently unavailable.',
  'keys.factor.passwordInvalid': 'The password is incorrect. Enter your current login password.',
  'keys.factor.passwordRateLimited': 'Too many incorrect password attempts. Please wait a minute and try again.',
  'keys.seal.wrongPassword': 'The login password is incorrect, so "view later" cannot be saved.',
  'keys.failedToSave': 'Failed to save',
  'keys.created': 'Created',
  'keys.expiresAt': 'Expires',
  'keys.group': 'Group',
  'keys.smartRouting': 'Smart routing',
  'keys.smartRoutingMore': '+{count}',
  'keys.smartRoutingPrimaryRoute': 'Primary route',
  'keys.smartRoutingOtherRoutes': 'Other routes ({count})',
  'keys.smartRoutingMoveUp': 'Move up',
  'keys.smartRoutingMoveDown': 'Move down',
  'keys.smartRoutingRemove': 'Remove',
  'keys.smartRoutingRoute': 'Route',
  'keys.smartRoutingEmpty': 'No groups added yet',
  'keys.smartRoutingChangeInEditor': 'Edit smart-routing keys in the dialog',
  'admin.groups.rateLabel': 'Rate',
  'keys.clickToChangeGroup': 'Click to change group',
  'keys.id': 'ID',
  'keys.currentConcurrency': 'Current Concurrency',
  'keys.lastUsedAt': 'Last Used',
  'keys.lastUsedIP': 'Last Used IP',
  'keys.rateLimitColumn': 'Rate Limit',
  'keys.searchPlaceholder': 'Search name or key...',
  'keys.status.active': 'Active',
  'keys.status.expired': 'Expired',
  'keys.status.inactive': 'Inactive',
  'keys.status.quota_exhausted': 'Quota exhausted',
  'keys.usage': 'Usage',
  'keys.deleteKey': 'Delete key',
  'common.selectedCount': '({count} selected)',
  // Key-rotation gate remediation hints.
  'keys.rotationBadge': 'Needs rotation',
  'keys.rotationReasonCreatedBeforeCutoff': 'Created before the cutoff',
  'keys.rotationReasonNoIPAllowlist': 'No IP allowlist',
  'keys.rotationFixByRotate': 'Rotate',
  'keys.rotationFixByWhitelist': 'Set allowlist',
  'keys.rotationPendingSummary': '{count} keys need rotation',
  'keys.rotationPendingAction': 'Fix all',
  'keys.rotationFilterHint': 'Showing {count} keys that need rotation',
  'keys.rotationShowAll': 'Show all',
  'keys.rotationNoneTitle': 'No keys need rotation',
  'keys.rotationNoneDescription': 'Badges clear as soon as a key is rotated or allowlisted.',
  'keys.rotationAllClear': 'All keys are remediated',
  'keys.copyKey': 'Copy key',
  'keys.ephemeralHint': 'Created in this tab; copy the full key now.',
  'keys.sealedHint': 'Password-encrypted copy saved.',
  'keys.maskedOnlyHint': 'Server-side masked value.',
  'keys.ephemeralBar.title': 'New key created — copy it again within 5 minutes',
  'keys.ephemeralBar.hint': 'Plaintext lives in this tab only for {minutes} minutes.',
  'keys.ephemeralBar.copy': 'Copy new key',
  'keys.overview.column': 'Overview',
  'keys.overview.calls24h': 'Last 24h',
  'keys.overview.ipWhitelist': 'IP allowlist · {count}',
  'keys.overview.ipBlacklist': 'IP blocklist · {count}',
  'keys.overview.ipBoth': 'Allow {whitelist} / Block {blacklist}',
  'keys.overview.ipOpen': 'No IP restriction',
  'keys.overview.expiresAt': 'Expires {date}',
  'keys.overview.neverExpires': 'Never expires',
  'keys.batchRotate.entry': 'Bulk rotate',
  'keys.batchRotate.title': 'Bulk rotate keys',
  'keys.batchRotate.confirm': 'Rotate {count} keys? Old keys stop working immediately.',
  'keys.batchRotate.confirmAction': 'Rotate now',
  'keys.batchRotate.resultTitle': 'Bulk rotation result',
  'keys.batchRotate.summary': '{succeeded} rotated, {failed} failed',
  'keys.batchRotate.copyAll': 'Copy all',
  'keys.batchRotate.itemOk': 'Rotated',
  'keys.batchRotate.itemFailed': 'Failed',
  'keys.batchRotate.itemSealed': 'Re-encrypted and saved',
  'keys.batchRotate.itemSealFailed': 'Re-encryption failed',
  'keys.batchRotate.sealOptIn': 'Re-save with my login password',
  'keys.batchRotate.sealHint': 'Rotation deletes the old ciphertext.',
  'keys.batchRotate.sealAction': 'Re-save ({count})',
  'keys.batchRotate.sealSummary': 'Saved {sealed}, failed {failed}',
  'keys.batchRotate.success': 'Rotated {count} keys; old keys are invalid',
  'keys.batchRotate.partial': '{failed} keys failed to rotate',
  'keys.batchRotate.failed': 'Bulk rotation failed',
  'keys.batchRotate.sealSuccess': 'Re-encrypted and saved {count} keys',
  'keys.batchRotate.sealPartial': 'Re-save partially failed: {sealed} saved, {failed} failed',
  'keys.noSeal.title': 'This key cannot be recovered',
  'keys.noSeal.message': 'This key was created without the opt-in; rotate a new key.',
  'keys.noSeal.rotate': 'Rotate a new key',
  'keys.seal.optIn': 'Allow viewing this key later with my login password',
  'keys.seal.passwordLabel': 'Login password',
}

vi.mock('@/api', () => ({
  keysAPI: {
    list: listKeys,
    create: createKey,
    update: updateKey,
    delete: deleteKey,
    deleteMany,
    toggleStatus: vi.fn(),
    rotateKey,
    batchRotateKeys,
    getOverview,
    putSecretSeal,
    getVerificationFactors,
    authorizeReveal,
    getSecretSeal,
    beginKeyPasskeyVerification,
    finishKeyPasskeyVerification,
  },
  authAPI: {
    getPublicSettings,
  },
  usageAPI: {
    getDashboardApiKeysUsage,
  },
  userGroupsAPI: {
    getAvailable: getAvailableGroups,
    getUserGroupRates,
  },
  endpointsAPI: {
    getCustomEndpoints: vi.fn().mockResolvedValue([]),
  },
  keyRotationGateAPI: {
    getStatus: getKeyRotationGateStatus,
  },
}))

vi.mock('@/utils/apikeySeal', async () => {
  const actual = await vi.importActual<typeof import('@/utils/apikeySeal')>('@/utils/apikeySeal')
  return {
    ...actual,
    sealApiKey,
  }
})

vi.mock('@/features/channel-monitor-v2/groupStatusDot', () => ({
  loadGroupStatusClasses: vi.fn().mockResolvedValue({}),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep,
    nextStep,
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: authUser.value,
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        const template = messages[key] ?? key
        if (!params) return template
        return Object.entries(params).reduce(
          (text, [name, value]) => text.replace(`{${name}}`, String(value)),
          template
        )
      },
    }),
  }
})

const createApiKey = (): ApiKey => ({
  id: 1,
  user_id: 1,
  key_prefix: 'sk-test-',
  key_masked: 'sk-test-****-key',
  name: 'test-key',
  group_id: null,
  status: 'active',
  ip_whitelist: [],
  ip_blacklist: [],
  last_used_at: null,
  last_used_ip: null,
  quota: 0,
  quota_used: 0,
  expires_at: null,
  created_at: '2026-06-27T00:00:00Z',
  updated_at: '2026-06-27T00:00:00Z',
  current_concurrency: 3,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  usage_5h: 0,
  usage_1d: 0,
  usage_7d: 0,
  window_5h_start: null,
  window_1d_start: null,
  window_7d_start: null,
  reset_5h_at: null,
  reset_1d_at: null,
  reset_7d_at: null,
})

const AppLayoutStub = {
  template: '<div><slot /></div>',
}

const TablePageLayoutStub = {
  template: `
    <div>
      <slot name="filters" />
      <slot name="actions" />
      <slot name="table" />
      <slot name="pagination" />
    </div>
  `,
}

const DataTableStub = {
  name: 'DataTable',
  props: ['columns', 'data', 'selectable', 'selectedKeys', 'rowKey'],
  emits: ['sort', 'update:selectedKeys', 'selectionChange'],
  template: `
    <div>
      <div data-test="columns">{{ columns.map((col) => col.key).join(',') }}</div>
      <button
        v-if="selectable"
        data-test="select-all"
        @click="$emit('update:selectedKeys', data.map((row) => row.id))"
      >
        select all
      </button>
      <div data-test="columns-meta">{{ JSON.stringify(columns.map((col) => ({ key: col.key, sortable: !!col.sortable }))) }}</div>
      <button data-test="sort-current-concurrency" @click="$emit('sort', 'current_concurrency', 'asc')">
        Sort Current Concurrency
      </button>
      <div v-for="row in data" :key="row.id" :data-row-id="row.id">
        <div
          v-if="columns.some((col) => col.key === 'id')"
          data-test="key-id"
        >
          <slot name="cell-id" :value="row.id" :row="row" />
        </div>
        <div data-test="key-cell"><slot name="cell-key" :value="row.key_masked" :row="row" /></div>
        <slot name="cell-name" :value="row.name" :row="row" />
        <div data-test="key-status"><slot name="cell-status" :value="row.status" :row="row" /></div>
        <div data-test="key-groups">
          <slot name="cell-group" :row="row" />
        </div>
        <slot name="cell-actions" :row="row" />
        <div data-test="current-concurrency">
          <slot name="cell-current_concurrency" :value="row.current_concurrency" :row="row" />
        </div>
        <div data-test="key-overview-cell"><slot name="cell-overview" :row="row" /></div>
        <div
          v-if="columns.some((col) => col.key === 'last_used_ip')"
          data-test="last-used-ip"
        >
          <slot name="cell-last_used_ip" :value="row.last_used_ip" :row="row" />
        </div>
      </div>
      <slot name="empty" />
    </div>
  `,
}

const SelectStub = {
  name: 'Select',
  // `error` mirrors the real Select's prop (red colour + highlight ring); the stub only needs to accept it.
  props: ['modelValue', 'options', 'error'],
  emits: ['update:modelValue'],
  template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"></select>',
}

const SearchInputStub = {
  name: 'SearchInput',
  props: ['modelValue'],
  emits: ['update:modelValue', 'search'],
  template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
}

const PaginationStub = {
  name: 'Pagination',
  props: ['page', 'total', 'pageSize'],
  emits: ['update:page', 'update:pageSize'],
  template: `
    <div>
      <button data-test="page-size-50" @click="$emit('update:pageSize', 50)">50</button>
    </div>
  `,
}

const IconStub = {
  props: ['name'],
  template: '<span data-test="icon">{{ name }}</span>',
}

const ConfirmDialogStub = {
  name: 'ConfirmDialog',
  props: ['show', 'title', 'message', 'confirmText', 'cancelText', 'danger'],
  emits: ['confirm', 'cancel'],
  template: `
    <div v-if="show" data-test="confirm-dialog">
      <p data-test="confirm-message">{{ message }}</p>
      <button data-test="confirm-ok" @click="$emit('confirm')">ok</button>
      <button data-test="confirm-cancel" @click="$emit('cancel')">cancel</button>
    </div>
  `,
}

const UseKeyModalStub = {
  name: 'UseKeyModal',
  props: ['show', 'apiKey', 'baseUrl', 'platform', 'allowMessagesDispatch'],
  emits: ['close', 'assign-group'],
  template: `
    <div v-if="show" data-test="use-key-modal" :data-platform="platform || 'none'">
      <span data-test="use-key-modal-api-key">{{ apiKey }}</span>
      <span data-test="use-key-modal-no-group">{{ platform ? '' : 'keys.useKeyModal.noGroupTitle' }}</span>
      <button data-test="use-key-modal-close" @click="$emit('close')">close</button>
      <button data-test="use-key-modal-assign" @click="$emit('assign-group')">assign</button>
    </div>
  `,
}

// The ephemeral-plaintext store needs an active Pinia; the other stores are mocked.
beforeEach(() => {
  setActivePinia(createPinia())
  // New batch/overview calls are secondary: default them to empty so every
  // existing test keeps its original assertion surface.
  authUser.value = null
  batchRotateKeys.mockReset()
  batchRotateKeys.mockResolvedValue({ results: [], requested_count: 0, succeeded_count: 0, failed_count: 0 })
  getOverview.mockReset()
  getOverview.mockResolvedValue({ items: [], window_hours: 24 })
  putSecretSeal.mockReset()
  putSecretSeal.mockResolvedValue(undefined)
  sealApiKey.mockReset()
  getVerificationFactors.mockReset()
  getVerificationFactors.mockResolvedValue({
    password_available: false,
    totp_enabled: false,
    passkey_count: 0,
    preferred: '',
    available_methods: [],
    seal_available: false,
  })
  authorizeReveal.mockReset()
  getSecretSeal.mockReset()
  beginKeyPasskeyVerification.mockReset()
  finishKeyPasskeyVerification.mockReset()
  createKey.mockReset()
  showError.mockReset()
  showSuccess.mockReset()
})

// Default stubs keep every existing assertion's surface unchanged. Rich-row tests
// below mount the same view without the Select/GroupOptionItem/GroupBadge stubs.
const baseStubs = {
  AppLayout: AppLayoutStub,
  TablePageLayout: TablePageLayoutStub,
  DataTable: DataTableStub,
  Pagination: PaginationStub,
  BaseDialog: {
    props: ['show'],
    template: '<div v-if="show"><slot /><slot name="footer" /></div>',
  },
  ConfirmDialog: ConfirmDialogStub,
  EmptyState: true,
  Select: SelectStub,
  SearchInput: SearchInputStub,
  Icon: IconStub,
  UseKeyModal: UseKeyModalStub,
  EndpointPopover: true,
  GroupBadge: { props: ['name'], template: '<span data-test="group-badge">{{ name }}</span>' },
  GroupOptionItem: true,
  Teleport: true,
}

const mountViewWithStubs = async (stubs: Record<string, any>) => {
  const wrapper = mount(KeysView, {
    global: {
      stubs,
    },
  })
  await flushPromises()
  await nextTick()
  return wrapper
}

const mountView = () => mountViewWithStubs(baseStubs)

// 智能路由「添加分组」的富样式行只能靠真实的 Select + GroupOptionItem + GroupBadge
// 渲染验证；其余组件继续用默认 stub，保持挂载成本与断言面不变。Teleport 也必须用真的，
// 否则下拉内容会被渲染在原地而不是 body 里。
const richGroupRowStubs = () => {
  const {
    Select: _Select,
    GroupOptionItem: _GroupOptionItem,
    GroupBadge: _GroupBadge,
    Teleport: _Teleport,
    ...stubs
  } = baseStubs
  return stubs
}

const visibleColumnKeys = (wrapper: VueWrapper) =>
  wrapper.get('[data-test="columns"]').text().split(',').filter(Boolean)

const visibleColumnMeta = (wrapper: VueWrapper): Array<{ key: string; sortable: boolean }> =>
  JSON.parse(wrapper.get('[data-test="columns-meta"]').text())

const getButtonByText = (wrapper: VueWrapper, text: string) => {
  const button = wrapper.findAll('button').find((item) => item.text().includes(text))
  if (!button) {
    throw new Error(`Button not found: ${text}`)
  }
  return button
}

const readBrowserStorage = () => {
  const read = (storage: Storage) =>
    Array.from({ length: storage.length }, (_, index) => {
      const key = storage.key(index)
      return key === null ? '' : `${key}=${storage.getItem(key) ?? ''}`
    })
  return [...read(localStorage), ...read(sessionStorage)].join('\n')
}

describe('user KeysView column settings', () => {
  beforeEach(() => {
    localStorage.clear()

    listKeys.mockReset()
    createKey.mockReset()
    updateKey.mockReset()
    deleteKey.mockReset()
    deleteMany.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
    isCurrentStep.mockReturnValue(false)
  })

  it.each([
    { baseUrl: 'https://api.example.com/prefix', app: 'codex', endpoint: 'https://api.example.com/prefix' },
    { baseUrl: 'https://api.example.com/prefix/v1', app: 'codex', endpoint: 'https://api.example.com/prefix/v1' },
    { baseUrl: 'https://api.example.com/prefix/v1///', app: 'codex', endpoint: 'https://api.example.com/prefix/v1///' },
    { baseUrl: 'https://api.example.com/prefix', app: 'grokbuild', endpoint: 'https://api.example.com/prefix/v1' }
  ])('imports a balance script with exactly one /v1 for $app at $baseUrl', async ({ baseUrl, app, endpoint }) => {
    getPublicSettings.mockResolvedValue({ api_base_url: baseUrl })
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'Claude', platform: 'anthropic' }])
    // 创建/轮换响应是唯一带明文的回执，CCS 导入只能从这次回执进入。
    createKey.mockResolvedValue({ ...createApiKey(), key: 'sk-test-key' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: [] }) }))
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const wrapper = await mountView()
    try {
      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      await wrapper.get('[data-tour="key-form-name"]').setValue('ccs key')
      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()
      await getButtonByText(wrapper, 'keys.secretDialog.importCcs').trigger('click')
      await flushPromises()
      wrapper.getComponent('#ccs-import-platform').vm.$emit('update:modelValue', app)
      await nextTick()
      const launchAnchor = wrapper.get('[data-test="ccs-import-launch"]')
      expect(launchAnchor.element.tagName).toBe('A')
      const href = launchAnchor.attributes('href') ?? ''
      expect(href.startsWith('ccswitch://')).toBe(true)
      const params = new URL(href).searchParams
      expect(params.get('endpoint')).toBe(endpoint)
      expect(params.get('usageAutoInterval')).toBe('30')
      const script = atob(params.get('usageScript')!).replaceAll('{{baseUrl}}', endpoint)
      const request = new Function(`return (${script})`)().request
      expect(request.url).toBe('https://api.example.com/prefix/v1/usage')
      expect(request.method).toBe('GET')
      expect(open).not.toHaveBeenCalled()
    } finally {
      wrapper.unmount()
      open.mockRestore()
      vi.unstubAllGlobals()
    }
  })

  it('shows only real models returned by the key /v1/models in Import to CCS and auto-matches on platform switch', async () => {
    getPublicSettings.mockResolvedValue({ api_base_url: 'https://api.example.com' })
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'Claude', platform: 'anthropic' }])
    createKey.mockResolvedValue({ ...createApiKey(), id: 91, group_id: 3, group_ids: [3], key: 'sk-real-models-key' })
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        object: 'list',
        data: [{ id: 'claude-sonnet-4-5-real' }, { id: 'gpt-5.4-real' }],
      }),
    })
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = await mountView()
    try {
      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      await wrapper.get('[data-tour="key-form-name"]').setValue('real models key')
      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()

      await getButtonByText(wrapper, 'keys.secretDialog.importCcs').trigger('click')
      await flushPromises()

      expect(fetchMock).toHaveBeenCalledWith(
        'https://api.example.com/v1/models',
        expect.objectContaining({ headers: { Authorization: 'Bearer sk-real-models-key' } }),
      )
      const modelSelect = wrapper.getComponent('#ccs-import-model')
      const optionValues = (modelSelect.props('options') as Array<{ value: string }>).map((o) => o.value)
      expect(optionValues).toEqual(['claude-sonnet-4-5-real', 'gpt-5.4-real'])
      expect(optionValues).not.toContain('claude-sonnet-4-6')
      expect(modelSelect.props('modelValue')).toBe('claude-sonnet-4-5-real')

      // Switching platform to Codex picks this key's real GPT model instead of a hardcoded fake model.
      wrapper.getComponent('#ccs-import-platform').vm.$emit('update:modelValue', 'codex')
      await nextTick()
      expect(modelSelect.props('modelValue')).toBe('gpt-5.4-real')

      const href = wrapper.get('[data-test="ccs-import-launch"]').attributes('href') ?? ''
      const params = new URL(href).searchParams
      expect(params.get('app')).toBe('codex')
      expect(params.get('model')).toBe('gpt-5.4-real')
    } finally {
      wrapper.unmount()
      vi.unstubAllGlobals()
    }
  })

  it('launches ccswitch:// without preventDefault and shows calm fallback after 1300ms only when focus never leaves', async () => {
    vi.useFakeTimers()
    getPublicSettings.mockResolvedValue({ api_base_url: 'https://api.example.com' })
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'Claude', platform: 'anthropic' }])
    createKey.mockResolvedValue({ ...createApiKey(), id: 93, group_id: 3, group_ids: [3], key: 'sk-deeplink-fallback' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: [] }) }))
    copyToClipboard.mockResolvedValue(true)
    const wrapper = await mountView()
    try {
      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      await wrapper.get('[data-tour="key-form-name"]').setValue('fallback key')
      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()

      await getButtonByText(wrapper, 'keys.secretDialog.importCcs').trigger('click')
      await flushPromises()

      const launchEl = wrapper.get('[data-test="ccs-import-launch"]').element
      const clickEvent = new MouseEvent('click', { bubbles: true, cancelable: true })
      launchEl.dispatchEvent(clickEvent)
      expect(clickEvent.defaultPrevented).toBe(false)

      // Blur suppresses fallback.
      window.dispatchEvent(new Event('blur'))
      await vi.advanceTimersByTimeAsync(1300)
      await nextTick()
      expect(wrapper.find('[data-test="ccs-import-fallback"]').exists()).toBe(false)
      expect(showError).not.toHaveBeenCalled()

      // Clicking again without blur/visibilitychange reveals the calm inline fallback after 1300ms.
      await wrapper.get('[data-test="ccs-import-launch"]').trigger('click')
      await vi.advanceTimersByTimeAsync(1299)
      await nextTick()
      expect(wrapper.find('[data-test="ccs-import-fallback"]').exists()).toBe(false)

      await vi.advanceTimersByTimeAsync(1)
      await nextTick()
      const fallback = wrapper.get('[data-test="ccs-import-fallback"]')
      expect(fallback.text()).toContain('keys.ccSwitchNotInstalled')
      expect(fallback.get('[data-test="ccs-import-retry"]').attributes('href')).toMatch(/^ccswitch:\/\//)
      expect(fallback.get('[data-test="ccs-import-download"]').attributes('href')).toContain('ccswitch.io')
      expect(showError).not.toHaveBeenCalled()

      await fallback.get('[data-test="ccs-import-copy-deeplink"]').trigger('click')
      await flushPromises()
      expect(copyToClipboard).toHaveBeenCalledWith(expect.stringMatching(/^ccswitch:\/\//), 'common.copied')
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
      vi.unstubAllGlobals()
    }
  })

  it('never shows hardcoded fake models when /v1/models returns empty or fails', async () => {
    getPublicSettings.mockResolvedValue({ api_base_url: 'https://api.example.com' })
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'Claude', platform: 'anthropic' }])
    createKey.mockResolvedValue({ ...createApiKey(), id: 92, group_id: 3, group_ids: [3], key: 'sk-empty-models-key' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: [] }) }))
    const wrapper = await mountView()
    try {
      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      await wrapper.get('[data-tour="key-form-name"]').setValue('empty models key')
      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()

      await getButtonByText(wrapper, 'keys.secretDialog.importCcs').trigger('click')
      await flushPromises()

      const modelSelect = wrapper.getComponent('#ccs-import-model')
      expect(modelSelect.props('options')).toEqual([])
      expect(modelSelect.props('modelValue')).toBe('')
      expect(wrapper.get('[data-test="ccs-import-model-hint"]').text()).toContain('keys.ccsImport.modelEmpty')
    } finally {
      wrapper.unmount()
      vi.unstubAllGlobals()
    }
  })

  it('renders the server-masked key and never builds the mask in the frontend', async () => {
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="key-value"]').text()).toBe('sk-test-****-key')
    expect(wrapper.text()).not.toContain('sk-test-key')
    wrapper.unmount()
  })

  it('offers a copy action next to the masked key and explains when the key cannot be recovered', async () => {
    const wrapper = await mountView()
    const keyCell = wrapper.get('[data-test="key-cell"]')
    // The cell is the server-masked value; the copy button never reveals more than the row knows.
    expect(keyCell.get('[data-test="key-value"]').text()).toBe('sk-test-****-key')
    const copyButton = keyCell.get('[data-test="key-copy"]')
    expect(copyButton.attributes('aria-label')).toBe('Copy key')

    expect(copyToClipboard).not.toHaveBeenCalled()
    // No ciphertext was saved for this key: copying first explains that rotation is the only way.
    await copyButton.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="confirm-dialog"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="confirm-message"]').text()).toBe('This key was created without the opt-in; rotate a new key.')
    expect(copyToClipboard).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps the one-time receipt on screen for a second and clears the plaintext on close', async () => {
    const secret = 'sk-live-abc123'
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'DeepSeek', platform: 'deepseek' }])
    createKey.mockResolvedValue({ ...createApiKey(), id: 77, name: 'once key', group_id: 3, group_ids: [3], key: secret })
    const wrapper = await mountView()

    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('once key')
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
    await nextTick()
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    // The receipt shows the full plaintext exactly once.
    expect(wrapper.text()).toContain(secret)
    expect(wrapper.get('[data-test="api-key-secret-wait-hint"]').exists()).toBe(true)
    const done = wrapper.get('[data-test="api-key-secret-done"]')
    expect(done.attributes('disabled')).toBeDefined()

    // During the forced window even the footer close action is inert.
    ;(done.element as HTMLButtonElement).click()
    await nextTick()
    expect(wrapper.text()).toContain(secret)

    await new Promise((resolve) => setTimeout(resolve, 1100))
    await nextTick()
    await flushPromises()

    expect(wrapper.get('[data-test="api-key-secret-done"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-test="api-key-secret-done"]').trigger('click')
    await flushPromises()

    // Closing drops the plaintext from the dialog...
    expect(wrapper.find('[data-test="api-key-secret-dialog"]').exists()).toBe(false)
    // ...and the row only ever shows the mask: the in-memory plaintext never enters the table.
    const cells = wrapper.findAll('[data-test="key-cell"]').map((cell) => cell.text())
    expect(cells.length).toBeGreaterThan(0)
    expect(cells.some((text) => text.includes(secret))).toBe(false)
    expect(cells.some((text) => text.includes('sk-test-****-key'))).toBe(true)
    // The full value is still copyable from the row while this tab stays open.
    await wrapper.get('[data-row-id="77"]').get('[data-test="key-copy"]').trigger('click')
    await flushPromises()
    expect(copyToClipboard).toHaveBeenCalledWith(secret)
    // The plaintext never reaches browser storage.
    expect(readBrowserStorage()).not.toContain(secret)
    wrapper.unmount()
  })

  it('keeps named icon tools and the key count in the workspace heading', async () => {
    const wrapper = await mountView()
    expect(wrapper.get('h1').text()).toContain('API Keys')
    expect(wrapper.get('[data-test="key-count"]').text()).toBe('1')
    const columns = wrapper.get('button[aria-label="Column Settings"]')
    expect(columns.attributes('title')).toBe('Column Settings')
    expect(columns.attributes('aria-expanded')).toBe('false')
    await columns.trigger('click')
    expect(columns.attributes('aria-expanded')).toBe('true')
    listKeys.mockClear()
    await wrapper.get('button[aria-label="Refresh"]').trigger('click')
    await flushPromises()
    expect(listKeys).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('explains how to recover from a predictable custom key rejection', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'DeepSeek', platform: 'deepseek' }])
    createKey.mockRejectedValueOnce({ reason: 'API_KEY_TOO_PREDICTABLE' })
    const wrapper = await mountView()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('test key')
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
    await nextTick()
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('keys.customKeyTooPredictable')
    expect(wrapper.find('#key-form').exists()).toBe(true)
  })

  it('filters groups by provider and never submits a stale group after switching', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 1, name: 'Claude', platform: 'anthropic' },
      { id: 2, name: 'GPT', platform: 'openai' },
      { id: 3, name: 'Grok', platform: 'grok' },
      { id: 4, name: 'Combined', platform: 'composite' },
    ])
    const wrapper = await mountView()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('Provider key')
    const groupSelect = wrapper.getComponent('[data-tour="key-form-group"]')
    expect(groupSelect.props('options').map((option: { value: number }) => option.value)).toEqual([1])
    expect(wrapper.get('input[name="key-provider"][value="cn"]').attributes('disabled')).toBeDefined()
    groupSelect.vm.$emit('update:modelValue', 1)
    await nextTick()
    await wrapper.get('input[name="key-provider"][value="openai"]').setValue()
    expect(groupSelect.props('modelValue')).toBeNull()
    expect(groupSelect.props('options').map((option: { value: number }) => option.value)).toEqual([2])
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(createKey).not.toHaveBeenCalled()
    // 必填提示改为就地标红，不再弹吐司。
    expect(wrapper.get('[data-test="key-field-group"]').text()).toContain('keys.groupRequired')
    await wrapper.get('input[name="key-provider"][value="other"]').setValue()
    expect(groupSelect.props('options').map((option: { value: number }) => option.value)).toEqual([3, 4])
    groupSelect.vm.$emit('update:modelValue', 3)
    await nextTick()
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(createKey).toHaveBeenCalledWith('Provider key', 3, undefined, [], [], 0, undefined,
      { rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0 }, undefined, 'inherit',
      { deepseekToolInjectionEnabled: true, autoBlockDatacenterIPs: false })
    wrapper.unmount()
  })

  it.each([true, false])('creates a key with DeepSeek tool injection set to %s', async (enabled) => {
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'DeepSeek', platform: 'deepseek' }])
    const wrapper = await mountView()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('Web tools key')
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
    await nextTick()
    const toggle = wrapper.get('[role="switch"][aria-labelledby="key-deepseek-tool-injection-label"]')
    expect(toggle.attributes('aria-checked')).toBe('true')
    if (!enabled) await toggle.trigger('click')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(createKey).toHaveBeenCalledWith('Web tools key', 3, undefined, [], [], 0, undefined,
      { rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0 }, undefined, 'inherit',
      { deepseekToolInjectionEnabled: enabled, autoBlockDatacenterIPs: false })
    wrapper.unmount()
  })

  it('hides DeepSeek tool injection for non-DeepSeek groups', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 4, name: 'OpenAI', platform: 'openai' }])
    const wrapper = await mountView()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 4)
    await nextTick()
    expect(wrapper.find('[role="switch"][aria-labelledby="key-deepseek-tool-injection-label"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each([
    { saved: undefined, expected: true },
    { saved: true, expected: true },
    { saved: false, expected: false },
  ])('loads the saved DeepSeek switch $saved and keeps it on save', async ({ saved, expected }) => {
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'DeepSeek', platform: 'deepseek' }])
    listKeys.mockResolvedValue({
      items: [{ ...createApiKey(), group_id: 3, deepseek_tool_injection_enabled: saved }],
      total: 1,
    })
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'common.edit').trigger('click')
    const toggle = wrapper.get('[role="switch"][aria-labelledby="key-deepseek-tool-injection-label"]')
    expect(toggle.attributes('aria-checked')).toBe(String(expected))
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(updateKey).toHaveBeenCalledWith(1, expect.objectContaining({
      deepseek_tool_injection_enabled: expected,
    }))
    wrapper.unmount()
  })

  it('saves an explicit off value and resets the next new key to on', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'DeepSeek', platform: 'deepseek' }])
    listKeys.mockResolvedValue({ items: [{ ...createApiKey(), group_id: 3 }], total: 1 })
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'common.edit').trigger('click')
    await wrapper.get('[role="switch"][aria-labelledby="key-deepseek-tool-injection-label"]').trigger('click')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(updateKey).toHaveBeenCalledWith(1, expect.objectContaining({
      deepseek_tool_injection_enabled: false,
    }))
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    // The switch only renders once a DeepSeek group is selected.
    expect(wrapper.find('[role="switch"][aria-labelledby="key-deepseek-tool-injection-label"]').exists()).toBe(false)
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
    await nextTick()
    expect(wrapper.get('[role="switch"][aria-labelledby="key-deepseek-tool-injection-label"]')
      .attributes('aria-checked')).toBe('true')
    wrapper.unmount()
  })

  it('can turn injection back on for a smart-routing key that includes DeepSeek', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 2, name: 'OpenAI', platform: 'openai' },
      { id: 3, name: 'DeepSeek', platform: 'deepseek' },
    ])
    listKeys.mockResolvedValue({
      items: [{ ...createApiKey(), group_id: 2, group_ids: [2, 3], deepseek_tool_injection_enabled: false }],
      total: 1,
    })
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'common.edit').trigger('click')
    const toggle = wrapper.get('[role="switch"][aria-labelledby="key-deepseek-tool-injection-label"]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await toggle.trigger('click')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(updateKey).toHaveBeenCalledWith(1, expect.objectContaining({
      group_id: 2, group_ids: [2, 3], deepseek_tool_injection_enabled: true,
    }))
    wrapper.unmount()
  })

  it('selects an available provider when groups arrive after the dialog opens', async () => {
    let resolveGroups!: (groups: unknown[]) => void
    getAvailableGroups.mockReturnValue(new Promise((resolve) => { resolveGroups = resolve }))
    const wrapper = await mountView()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    resolveGroups([{ id: 2, name: 'GPT', platform: 'openai' }])
    await flushPromises()
    expect((wrapper.get('input[name="key-provider"][value="openai"]').element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.getComponent('[data-tour="key-form-group"]').props('options')).toMatchObject([{ value: 2 }])
    wrapper.unmount()
  })

  it('opens an existing key in its own provider and includes all Chinese platforms', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 1, name: 'GPT', platform: 'openai' },
      { id: 2, name: 'Kimi', platform: 'kimi' },
      { id: 3, name: 'DeepSeek', platform: 'deepseek' },
      { id: 4, name: 'GLM', platform: 'zhipu' },
      { id: 5, name: 'MiniMax', platform: 'minimax' },
    ])
    listKeys.mockResolvedValue({ items: [{ ...createApiKey(), group_id: 3 }], total: 1 })
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'common.edit').trigger('click')
    expect((wrapper.get('input[name="key-provider"][value="cn"]').element as HTMLInputElement).checked).toBe(true)
    const groupSelect = wrapper.getComponent('[data-tour="key-form-group"]')
    expect(groupSelect.props('modelValue')).toBe(3)
    expect(groupSelect.props('options').map((option: { value: number }) => option.value)).toEqual([2, 3, 4, 5])
    wrapper.unmount()
  })

  it('keeps ordered cross-provider smart routes while changing the provider filter', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 1, name: 'Claude', platform: 'anthropic' },
      { id: 2, name: 'GPT', platform: 'openai' },
      { id: 3, name: 'Grok', platform: 'grok' },
    ])
    listKeys.mockResolvedValue({ items: [{ ...createApiKey(), group_id: 2, group_ids: [2, 1] }], total: 1 })
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'common.edit').trigger('click')
    await wrapper.get('input[name="key-provider"][value="other"]').setValue()
    const addSelect = wrapper.findAllComponents({ name: 'Select' }).find((select) => select.attributes('placeholder') === 'keys.smartRoutingAdd')!
    expect(addSelect.props('options')).toMatchObject([{ value: 3 }])
    addSelect.vm.$emit('update:modelValue', 3)
    await nextTick()
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(updateKey).toHaveBeenCalledWith(1, expect.objectContaining({ group_id: 2, group_ids: [2, 1, 3] }))
    wrapper.unmount()
  })

  it.each([
    { initialStatus: 'quota_exhausted', status: 'active', formStatus: 'active' },
    { initialStatus: 'inactive', status: 'inactive', formStatus: 'inactive' },
    { initialStatus: 'active', status: 'active', formStatus: 'inactive' },
  ] as const)('syncs quota reset from $initialStatus to $status with form status $formStatus', async ({ initialStatus, status, formStatus }) => {
    const key: ApiKey = {
      ...createApiKey(), group_id: 1, quota: 10, quota_used: 10,
      status: initialStatus,
    }
    listKeys.mockResolvedValueOnce({ items: [key], total: 1, page: 1, page_size: 20, pages: 1 })
    updateKey.mockResolvedValue({ ...key, status, quota_used: 0 })
    const wrapper = await mountView()
    await getButtonByText(wrapper, 'common.edit').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('Unsaved name')
    const statusSelect = wrapper.findAllComponents({ name: 'Select' })
      .find((select) => select.props('options').length === 2 &&
        select.props('options')[0].value === 'active')!
    statusSelect.vm.$emit('update:modelValue', 'inactive')
    await wrapper.get('button[title="keys.resetQuotaUsed"]').trigger('click')
    const confirmation = wrapper.findAllComponents({ name: 'ConfirmDialog' })
      .find((dialog) => dialog.props('title') === 'keys.resetQuotaTitle')!
    confirmation.vm.$emit('confirm')
    await flushPromises()

    expect(updateKey).toHaveBeenNthCalledWith(1, key.id, { reset_quota: true })
    expect(wrapper.findComponent({ name: 'DataTable' }).props('data')[0])
      .toMatchObject({ status, quota_used: 0 })
    expect(statusSelect.props('modelValue')).toBe(formStatus)
    expect((wrapper.get('[data-tour="key-form-name"]').element as HTMLInputElement).value)
      .toBe('Unsaved name')

    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(updateKey).toHaveBeenNthCalledWith(2, key.id, expect.objectContaining({ name: 'Unsaved name', status: formStatus }))
    wrapper.unmount()
  })

  it('uses the default API key columns with low-frequency columns hidden', async () => {
    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).toEqual([
      'name',
      'key',
      'group',
      'current_concurrency',
      'usage',
      'overview',
      'expires_at',
      'status',
      'created_at',
      'actions',
    ])
    expect(visibleColumnKeys(wrapper)).not.toContain('rate_limit')
    expect(visibleColumnKeys(wrapper)).not.toContain('last_used_at')
    expect(visibleColumnKeys(wrapper)).not.toContain('last_used_ip')
    expect(visibleColumnKeys(wrapper)).not.toContain('id')
  })

  it('shows a hidden column when toggled and persists the preference', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Rate Limit').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('rate_limit')
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['id', 'last_used_at', 'last_used_ip'])
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  it('shows the API key ID column when toggled', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'ID').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('id')
    expect(wrapper.get('[data-test="key-id"]').text()).toBe('#1')
    expect(visibleColumnMeta(wrapper).find((column) => column.key === 'id')?.sortable).toBe(true)
  })

  it('shows the last used IP column when toggled', async () => {
    listKeys.mockResolvedValueOnce({
      items: [{ ...createApiKey(), last_used_ip: '203.0.113.10' }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await getButtonByText(wrapper, 'Last Used IP').trigger('click')
    await nextTick()

    expect(visibleColumnKeys(wrapper)).toContain('last_used_ip')
    expect(wrapper.get('[data-test="last-used-ip"]').text()).toBe('203.0.113.10')
  })

  it('restores column preferences from localStorage on mount', async () => {
    localStorage.setItem('api-key-hidden-columns', JSON.stringify(['group', 'created_at']))
    localStorage.setItem('api-key-column-settings-version', '1')

    const wrapper = await mountView()

    expect(visibleColumnKeys(wrapper)).toEqual([
      'name',
      'key',
      'current_concurrency',
      'usage',
      'overview',
      'rate_limit',
      'expires_at',
      'status',
      'last_used_at',
      'actions',
    ])
    expect(localStorage.getItem('api-key-hidden-columns')).toBe(
      JSON.stringify(['group', 'created_at', 'last_used_ip', 'id'])
    )
    expect(localStorage.getItem('api-key-column-settings-version')).toBe('3')
  })

  it('does not include always-visible columns in the toggleable menu', async () => {
    const wrapper = await mountView()

    await wrapper.get('button[title="Column Settings"]').trigger('click')
    await nextTick()

    const columnMenuText = wrapper.text()
    expect(columnMenuText).toContain('API Key')
    expect(columnMenuText).toContain('ID')
    expect(columnMenuText).toContain('Current Concurrency')
    expect(columnMenuText).toContain('Rate Limit')
    expect(columnMenuText).toContain('Last Used IP')
    expect(columnMenuText).not.toContain('Name')
    expect(columnMenuText).not.toContain('Actions')
  })

  it('renders the current concurrency value', async () => {
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="current-concurrency"]').text()).toBe('3')
  })

  it('marks current concurrency as sortable', async () => {
    const wrapper = await mountView()

    const currentConcurrencyColumn = visibleColumnMeta(wrapper).find(
      (column) => column.key === 'current_concurrency'
    )
    expect(currentConcurrencyColumn?.sortable).toBe(true)
  })

  it('keeps filters and selected page size when sorting by current concurrency', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 42, name: 'OpenAI' }])
    const wrapper = await mountView()

    await wrapper.get('[data-test="page-size-50"]').trigger('click')
    await flushPromises()

    await wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('update:modelValue', 'target')
    await wrapper.findComponent({ name: 'SearchInput' }).vm.$emit('search')
    await flushPromises()

    const selects = wrapper.findAllComponents({ name: 'Select' })
    await selects[0].vm.$emit('update:modelValue', 42)
    await flushPromises()
    await selects[1].vm.$emit('update:modelValue', 'active')
    await flushPromises()

    listKeys.mockClear()

    await wrapper.get('[data-test="sort-current-concurrency"]').trigger('click')
    await flushPromises()

    expect(listKeys).toHaveBeenLastCalledWith(
      1,
      50,
      {
        search: 'target',
        status: 'active',
        group_id: 42,
        sort_by: 'current_concurrency',
        sort_order: 'asc',
      },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
  })
})

describe('user KeysView smart-routing add dropdown rich rows', () => {
  // 与截图一致：倍率 0.05x/0.8x/1.2x + 灰色小字描述；最后一项缺倍率/描述用于降级验证。
  const richGroups = [
    { id: 1, name: 'Claude 官方', platform: 'anthropic', rate_multiplier: 1.2, description: 'Claude 官方号池 酒馆用' },
    { id: 2, name: 'GPT free', platform: 'openai', rate_multiplier: 0.8, description: '弱智 free号 酒馆用 不建议vibecoding' },
    { id: 3, name: 'GPT 小号', platform: 'openai', rate_multiplier: 0.05, description: '免费号池 仅供测试' },
    { id: 4, name: 'Plain OpenAI', platform: 'openai' },
  ]

  beforeEach(() => {
    localStorage.clear()

    listKeys.mockReset()
    createKey.mockReset()
    updateKey.mockReset()
    deleteKey.mockReset()
    deleteMany.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()

    listKeys.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    createKey.mockResolvedValue({ ...createApiKey(), key: 'rich-row-fixture-secret' })
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue(richGroups)
    getUserGroupRates.mockResolvedValue({})
    isCurrentStep.mockReturnValue(false)
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  const openCreateForm = async (wrapper: VueWrapper) => {
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-test="key-smart-routing"]').trigger('click')
    await nextTick()
    await wrapper.get('input[name="key-provider"][value="openai"]').setValue()
    await nextTick()
    return wrapper
  }

  const openSmartRouteAdd = async (wrapper: VueWrapper) => {
    await wrapper.get('[data-test="smart-route-add"] button').trigger('click')
    await nextTick()
    const dropdown = document.body.querySelector<HTMLElement>('.select-dropdown-portal')
    if (!dropdown) throw new Error('smart-routing add dropdown did not open')
    return dropdown
  }

  const optionRows = (dropdown: HTMLElement) =>
    [...dropdown.querySelectorAll<HTMLElement>('[role="option"]')]

  it('renders platform icon, rate pill and description like the normal group picker', async () => {
    const wrapper = await mountViewWithStubs(richGroupRowStubs())
    await openCreateForm(wrapper)

    const dropdown = await openSmartRouteAdd(wrapper)
    const rows = optionRows(dropdown)
    expect(rows).toHaveLength(3)

    const gptRow = rows.find((row) => row.textContent?.includes('GPT free'))
    expect(gptRow).toBeDefined()
    // 平台图标：复用 GroupBadge 里的 PlatformIcon
    expect(gptRow!.querySelector('svg.platform-icon--openai')).not.toBeNull()
    // 右侧倍率徽标与灰色小字描述
    expect(gptRow!.textContent).toContain('0.8x')
    expect(gptRow!.textContent).toContain('Rate')
    expect(gptRow!.querySelector('.line-clamp-3')?.textContent?.trim())
      .toBe('弱智 free号 酒馆用 不建议vibecoding')
    // 候选行都是未选中态
    expect(gptRow!.getAttribute('aria-selected')).toBe('false')

    wrapper.unmount()
  })

  it('keeps the normal group picker rich and marks the picked row as selected', async () => {
    const wrapper = await mountViewWithStubs(richGroupRowStubs())
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('input[name="key-provider"][value="openai"]').setValue()
    await nextTick()

    await wrapper.get('[data-tour="key-form-group"] button').trigger('click')
    await nextTick()
    const dropdown = document.body.querySelector<HTMLElement>('.select-dropdown-portal')
    if (!dropdown) throw new Error('normal group dropdown did not open')
    const gptRow = [...dropdown.querySelectorAll<HTMLElement>('[role="option"]')]
      .find((row) => row.textContent?.includes('GPT free'))
    expect(gptRow).toBeDefined()
    expect(gptRow!.querySelector('svg.platform-icon--openai')).not.toBeNull()
    expect(gptRow!.textContent).toContain('0.8x')
    expect(gptRow!.querySelector('.line-clamp-3')?.textContent?.trim())
      .toBe('弱智 free号 酒馆用 不建议vibecoding')
    gptRow!.click()
    await nextTick()
    await flushPromises()
    expect(wrapper.get('[data-tour="key-form-group"]').text()).toContain('GPT free')

    // 重新打开：已选中行是选中态并带勾选标记，未选中行没有。
    await wrapper.get('[data-tour="key-form-group"] button').trigger('click')
    await nextTick()
    const reopened = document.body.querySelector<HTMLElement>('.select-dropdown-portal')
    if (!reopened) throw new Error('normal group dropdown did not reopen')
    const rows = [...reopened.querySelectorAll<HTMLElement>('[role="option"]')]
    const pickedRow = rows.find((row) => row.textContent?.includes('GPT free'))!
    expect(pickedRow.getAttribute('aria-selected')).toBe('true')
    expect(pickedRow.querySelector('svg.h-4.w-4')).not.toBeNull()
    expect(rows.find((row) => row.textContent?.includes('GPT 小号'))!
      .querySelector('svg.h-4.w-4')).toBeNull()

    wrapper.unmount()
  })

  it('keeps search filtering over names and descriptions of the rich rows', async () => {
    const wrapper = await mountViewWithStubs(richGroupRowStubs())
    await openCreateForm(wrapper)
    const dropdown = await openSmartRouteAdd(wrapper)

    const search = dropdown.querySelector<HTMLInputElement>('.select-search-input')
    expect(search).not.toBeNull()
    const typeQuery = async (query: string) => {
      search!.value = query
      search!.dispatchEvent(new Event('input'))
      await nextTick()
    }

    // 命中描述里的关键词
    await typeQuery('vibecoding')
    let rows = optionRows(dropdown)
    expect(rows).toHaveLength(1)
    expect(rows[0].textContent).toContain('GPT free')
    expect(rows[0].querySelector('svg.platform-icon--openai')).not.toBeNull()

    // 命中分组名
    await typeQuery('小号')
    rows = optionRows(dropdown)
    expect(rows).toHaveLength(1)
    expect(rows[0].textContent).toContain('GPT 小号')

    wrapper.unmount()
  })

  it('adds the clicked rich row, hides it from candidates and keeps the added order', async () => {
    const wrapper = await mountViewWithStubs(richGroupRowStubs())
    await openCreateForm(wrapper)
    await wrapper.get('[data-tour="key-form-name"]').setValue('Rich routed key')

    let dropdown = await openSmartRouteAdd(wrapper)
    const gptRow = optionRows(dropdown).find((row) => row.textContent?.includes('GPT free'))
    expect(gptRow).toBeDefined()
    gptRow!.click()
    await nextTick()
    await flushPromises()
    expect(wrapper.get('[data-test="smart-route-primary"]').text()).toContain('GPT free')

    // 已加入的分组不再出现在「添加分组」候选里；再次点击的新分组排在后面。
    dropdown = await openSmartRouteAdd(wrapper)
    const remaining = optionRows(dropdown)
    expect(remaining.map((row) => row.textContent).join('|')).not.toContain('GPT free')
    const smallRow = remaining.find((row) => row.textContent?.includes('GPT 小号'))
    expect(smallRow).toBeDefined()
    smallRow!.click()
    await nextTick()
    await flushPromises()
    expect(wrapper.get('[data-test="smart-routes-toggle"]').text()).toContain('Other routes (1)')

    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    expect(createKey).toHaveBeenCalled()
    const [name, groupId, , , , , , , groupIds] = createKey.mock.calls[0]
    expect(name).toBe('Rich routed key')
    expect(groupId).toBe(2)
    expect(groupIds).toEqual([2, 3])

    wrapper.unmount()
  })

  it('degrades safely when a group has no rate multiplier or description', async () => {
    const wrapper = await mountViewWithStubs(richGroupRowStubs())
    await openCreateForm(wrapper)

    const dropdown = await openSmartRouteAdd(wrapper)
    const plainRow = optionRows(dropdown).find((row) => row.textContent?.includes('Plain OpenAI'))
    expect(plainRow).toBeDefined()
    expect(plainRow!.querySelector('svg.platform-icon--openai')).not.toBeNull()
    // 没有倍率就不画徽标，没有描述就不画第二行
    expect(plainRow!.textContent).not.toMatch(/\d+x/)
    expect(plainRow!.querySelector('.line-clamp-3')).toBeNull()

    wrapper.unmount()
  })
})

describe('user KeysView mutations', () => {
  beforeEach(() => {
    localStorage.clear()

    listKeys.mockReset()
    createKey.mockReset()
    updateKey.mockReset()
    deleteKey.mockReset()
    deleteMany.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
    isCurrentStep.mockReturnValue(false)
  })

  const twoKeys = () => ({
    items: [createApiKey(), { ...createApiKey(), id: 2, name: 'second-key' }],
    total: 2,
    page: 1,
    page_size: 20,
    pages: 1,
  })

  const clickRowAction = async (wrapper: VueWrapper, text: string, index = 0) => {
    const buttons = wrapper.findAll('button').filter((button) => button.text().includes(text))
    if (!buttons[index]) throw new Error(`Row action not found: ${text}`)
    await buttons[index].trigger('click')
    await nextTick()
  }

  it('removes a deleted key immediately and never refetches the list', async () => {
    listKeys.mockResolvedValue(twoKeys())
    deleteKey.mockResolvedValue({ message: 'deleted' })
    const wrapper = await mountView()
    expect(wrapper.findAll('[data-test="current-concurrency"]')).toHaveLength(2)

    listKeys.mockClear()
    await clickRowAction(wrapper, 'common.delete')
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(deleteKey).toHaveBeenCalledWith(1)
    expect(listKeys).not.toHaveBeenCalled()
    expect(wrapper.findAll('[data-test="current-concurrency"]')).toHaveLength(1)
    expect(showSuccess).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('restores the row and reports the failure when a delete request fails', async () => {
    listKeys.mockResolvedValue(twoKeys())
    deleteKey.mockRejectedValueOnce({ response: { data: { detail: 'permission denied' } } })
    const wrapper = await mountView()

    await clickRowAction(wrapper, 'common.delete')
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(wrapper.findAll('[data-test="current-concurrency"]')).toHaveLength(2)
    expect(showError).toHaveBeenCalledWith('permission denied')
    wrapper.unmount()
  })

  it('bulk deletes the selected keys with one batch call and no refetch', async () => {
    listKeys.mockResolvedValue(twoKeys())
    deleteMany.mockResolvedValue({ succeeded: [1, 2], failed: [] })
    const wrapper = await mountView()

    expect(wrapper.find('[data-test="keys-bulk-delete"]').exists()).toBe(false)

    listKeys.mockClear()
    await wrapper.get('[data-test="select-all"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="keys-bulk-delete"]').trigger('click')
    await nextTick()
    expect(wrapper.get('[data-test="confirm-message"]').text()).toContain('(2 selected)')

    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(deleteMany).toHaveBeenCalledWith([1, 2])
    expect(listKeys).not.toHaveBeenCalled()
    expect(wrapper.findAll('[data-test="current-concurrency"]')).toHaveLength(0)
    wrapper.unmount()
  })

  it('puts only the failed rows back after a partial bulk delete', async () => {
    listKeys.mockResolvedValue(twoKeys())
    deleteMany.mockResolvedValue({ succeeded: [1], failed: [2] })
    const wrapper = await mountView()

    await wrapper.get('[data-test="select-all"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="keys-bulk-delete"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(wrapper.findAll('[data-test="current-concurrency"]')).toHaveLength(1)
    expect(showError).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows the first creation secret and inserts the key without refetching', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'DeepSeek', platform: 'deepseek' }])
    const secret = 'fixture-only-creation-secret'
    createKey.mockResolvedValue({ ...createApiKey(), id: 99, name: 'new key', group_id: 3, group_ids: [3], key: secret })
    const wrapper = await mountView()

    listKeys.mockClear()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('new key')
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
    await nextTick()
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).toHaveBeenCalled()
    expect(listKeys).not.toHaveBeenCalled()
    expect(wrapper.findAll('[data-test="current-concurrency"]')).toHaveLength(2)
    expect(wrapper.get('[data-test="key-bound-group"]').attributes('data-group-id')).toBe('3')
    expect(wrapper.text()).toContain('DeepSeek')
    expect(wrapper.find('#key-form').exists()).toBe(false)
    expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe(secret)
    // 列表行只保留掩码；明文只在回执弹窗与内存 store 里（可复制），绝不进表格。
    const createdRowCells = wrapper.findAll('[data-test="key-cell"]')
    expect(createdRowCells.length).toBe(2)
    expect(createdRowCells.some((cell) => cell.text().includes(secret))).toBe(false)
    expect(createdRowCells.every((cell) => cell.text().includes('sk-test-****-key'))).toBe(true)
    expect(showSuccess).toHaveBeenCalledTimes(1)
    expect(showSuccess).toHaveBeenCalledWith('keys.keyCreatedSuccess')
    await getButtonByText(wrapper, 'keys.secretDialog.copy').trigger('click')
    expect(copyToClipboard).toHaveBeenCalledWith(secret, 'keys.secretDialog.copied')
    wrapper.unmount()
  })

  it.each([
    { state: 'already listed', id: 1 },
    { state: 'absent from the list', id: 2 },
  ])('refreshes after a redacted create replay without applying the snapshot when its key is $state', async ({ id }) => {
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'DeepSeek', platform: 'deepseek' }])
    const replayedKey = { ...createApiKey(), name: 'stale receipt name', key_masked: 'fixture****old', group_id: 3, group_ids: [3] }
    const listedKey: ApiKey = { ...createApiKey(), id, name: 'listed name', status: 'inactive', key_masked: 'fixture****seen' }
    const latestKey = { ...listedKey, name: 'latest name', key_masked: 'fixture****new' }
    const page = (row: ApiKey) => ({ items: [row], total: 1, page: 1, page_size: 20, pages: 1 })
    listKeys.mockResolvedValueOnce(page(listedKey))
    createKey.mockResolvedValue(replayedKey)
    const wrapper = await mountView()
    try {
      listKeys.mockClear()
      let finishRefresh!: (value: ReturnType<typeof page>) => void
      listKeys.mockImplementationOnce(() => new Promise((resolve) => { finishRefresh = resolve }))

      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      await wrapper.get('[data-tour="key-form-name"]').setValue(replayedKey.name)
      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()

      expect(wrapper.text()).toContain(listedKey.name)
      expect(wrapper.text()).not.toContain(replayedKey.name)
      expect(wrapper.get('[data-test="key-status"]').text()).toBe('Inactive')
      expect(wrapper.get('[data-test="key-value"]').text()).toBe(listedKey.key_masked)
      expect(wrapper.get('[data-test="key-count"]').text()).toBe('1')
      expect(listKeys).toHaveBeenCalledTimes(1)
      expect(wrapper.find('#key-form').exists()).toBe(false)
      expect(wrapper.find('[data-test="api-key-secret-dialog"]').exists()).toBe(false)
      expect(showSuccess).toHaveBeenCalledTimes(1)
      expect(showSuccess).toHaveBeenCalledWith('keys.keyCreatedReplaySuccess')
      expect(copyToClipboard).not.toHaveBeenCalled()

      finishRefresh(page(latestKey))
      await flushPromises()

      expect(showError).not.toHaveBeenCalled()
      expect(wrapper.findAll('[data-test="key-cell"]')).toHaveLength(1)
      expect(wrapper.text()).toContain(latestKey.name)
      expect(wrapper.get('[data-test="key-status"]').text()).toBe('Inactive')
      expect(wrapper.get('[data-test="key-value"]').text()).toBe(latestKey.key_masked)
      expect(wrapper.get('[data-test="key-count"]').text()).toBe('1')
    } finally {
      wrapper.unmount()
    }
  })

  it('shows a newly created smart-routing key when filtering by a secondary route group', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 2, name: 'OpenAI', platform: 'openai' },
      { id: 3, name: 'Grok', platform: 'grok' },
    ])
    createKey.mockResolvedValue({
      ...createApiKey(), id: 99, name: 'filtered route key', group_id: 2, group_ids: [2, 3], key: 'fixture-only-creation-secret',
    })
    const wrapper = await mountView()

    const groupFilter = wrapper.findAllComponents({ name: 'Select' })[0]
    groupFilter.vm.$emit('update:modelValue', 3)
    await flushPromises()
    listKeys.mockClear()

    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('filtered route key')
    await wrapper.get('input[name="key-provider"][value="openai"]').setValue()
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 2)
    await nextTick()
    const smartRouting = wrapper.findAll('[role="switch"]').find(
      (node) => node.attributes('data-test') === 'key-smart-routing' || node.text().includes('keys.smartRouting')
    )
    if (!smartRouting) throw new Error('smart routing switch not found')
    await smartRouting.trigger('click')
    await nextTick()
    await wrapper.get('input[name="key-provider"][value="other"]').setValue()
    const addRoute = wrapper.findAllComponents({ name: 'Select' })
      .find((select) => select.attributes('placeholder') === 'keys.smartRoutingAdd')!
    addRoute.vm.$emit('update:modelValue', 3)
    await nextTick()

    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(listKeys).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('filtered route key')
    // The table condenses the routes, so the secondary group only proves the row is still listed.
    expect(wrapper.findAll('[data-test="key-bound-group"]').map((node) => node.attributes('data-group-id')))
      .toEqual(['2'])
    wrapper.unmount()
  })

  it('shows the server-returned smart-routing first group and count immediately without refetching', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 1, name: 'Claude', platform: 'anthropic' },
      { id: 2, name: 'OpenAI', platform: 'openai' },
      { id: 3, name: 'Grok', platform: 'grok' },
    ])
    createKey.mockResolvedValue({
      ...createApiKey(), id: 99, name: 'mapped key', group_id: 2, group_ids: [2, 3], key: 'fixture-only-creation-secret',
    })
    const wrapper = await mountView()

    listKeys.mockClear()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('mapped key')
    await wrapper.get('input[name="key-provider"][value="openai"]').setValue()
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 2)
    await nextTick()
    const smartRouting = wrapper.findAll('[role="switch"]').find(
      (node) => node.attributes('data-test') === 'key-smart-routing' || node.text().includes('keys.smartRouting')
    )
    if (!smartRouting) throw new Error('smart routing switch not found')
    await smartRouting.trigger('click')
    await nextTick()
    await wrapper.get('input[name="key-provider"][value="other"]').setValue()
    const addRoute = wrapper.findAllComponents({ name: 'Select' })
      .find((select) => select.attributes('placeholder') === 'keys.smartRoutingAdd')!
    addRoute.vm.$emit('update:modelValue', 3)
    await nextTick()

    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).toHaveBeenCalledWith('mapped key', 2, undefined, [], [], 0, undefined,
      { rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0 }, [2, 3], 'inherit',
      { deepseekToolInjectionEnabled: true, autoBlockDatacenterIPs: false })
    expect(listKeys).not.toHaveBeenCalled()
    // The first returned route renders immediately; the rest collapse into the count.
    expect(wrapper.findAll('[data-test="key-bound-group"]').map((node) => node.attributes('data-group-id'))).toEqual(['2'])
    expect(wrapper.get('[data-test="key-smart-routing-count"]').text()).toBe('Smart routing +1')
    expect(wrapper.text()).toContain('OpenAI')
    expect(wrapper.text()).not.toContain('Grok')
    wrapper.unmount()
  })

  it('condenses many smart-routing groups into the first group and the remaining count', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 1, name: 'Claude', platform: 'anthropic' },
      { id: 2, name: 'OpenAI', platform: 'openai' },
      { id: 3, name: 'Grok', platform: 'grok' },
      { id: 4, name: 'DeepSeek', platform: 'deepseek' },
      { id: 5, name: 'Kimi', platform: 'kimi' },
    ])
    listKeys.mockResolvedValue({
      items: [{ ...createApiKey(), group_id: 1, group_ids: [1, 2, 3, 4, 5] }],
      total: 1,
    })
    const wrapper = await mountView()

    expect(wrapper.findAll('[data-test="key-bound-group"]').map((node) => node.attributes('data-group-id'))).toEqual(['1'])
    expect(wrapper.get('[data-test="key-smart-routing-count"]').text()).toBe('Smart routing +4')
    expect(wrapper.text()).toContain('Claude')
    expect(wrapper.text()).not.toContain('OpenAI')
    expect(wrapper.text()).not.toContain('Kimi')
    wrapper.unmount()
  })

  it('patches the edited row in place without refetching the list', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 1, name: 'Claude', platform: 'anthropic' }])
    listKeys.mockResolvedValue({
      items: [{ ...createApiKey(), group_id: 1 }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    updateKey.mockResolvedValue({ ...createApiKey(), group_id: 1, name: 'renamed' })
    const wrapper = await mountView()

    listKeys.mockClear()
    await getButtonByText(wrapper, 'common.edit').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('renamed')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(updateKey).toHaveBeenCalled()
    expect(listKeys).not.toHaveBeenCalled()
    expect(wrapper.getComponent({ name: 'DataTable' }).props('data')[0].name).toBe('renamed')
    wrapper.unmount()
  })


  it('collapses secondary smart-routing groups behind a disclosure and keeps ordering actions', async () => {
    getAvailableGroups.mockResolvedValue([
      { id: 1, name: 'Claude', platform: 'anthropic' },
      { id: 2, name: 'GPT', platform: 'openai' },
      { id: 3, name: 'Grok', platform: 'grok' },
    ])
    const wrapper = await mountView()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('Routed key')

    await wrapper.get('[data-test="key-smart-routing"]').trigger('click')
    await nextTick()
    expect(wrapper.find('[data-test="smart-routing-editor"]').exists()).toBe(true)

    const addRoute = wrapper.getComponent('[data-test="smart-route-add"]')
    addRoute.vm.$emit('update:modelValue', 1)
    await nextTick()
    addRoute.vm.$emit('update:modelValue', 2)
    await nextTick()
    addRoute.vm.$emit('update:modelValue', 3)
    await nextTick()

    // Only the primary route is on screen until the disclosure is opened.
    expect(wrapper.get('[data-test="smart-route-primary"]').text()).toContain('Claude')
    expect(wrapper.findAll('[data-test="smart-route-item"]')).toHaveLength(0)
    expect(wrapper.get('[data-test="smart-routes-toggle"]').text()).toContain('Other routes (2)')

    await wrapper.get('[data-test="smart-routes-toggle"]').trigger('click')
    await flushPromises()
    const items = wrapper.findAll('[data-test="smart-route-item"]')
    expect(items).toHaveLength(2)
    expect(items[0].text()).toContain('Route 2')
    expect(items[0].text()).toContain('GPT')
    expect(items[1].text()).toContain('Route 3')
    expect(items[1].text()).toContain('Grok')

    // Moving the first secondary route up promotes it to the primary slot.
    await items[0].get('[data-test="smart-route-up"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="smart-route-primary"]').text()).toContain('GPT')
    expect(wrapper.findAll('[data-test="smart-route-item"]')[0].text()).toContain('Claude')

    // Removing a secondary route keeps the panel open and updates the count.
    await wrapper.findAll('[data-test="smart-route-item"]')[0].get('[data-test="smart-route-remove"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-test="smart-route-item"]')).toHaveLength(1)
    expect(wrapper.get('[data-test="smart-routes-toggle"]').text()).toContain('Other routes (1)')

    // Adding a route back appends it to the ordered list without collapsing the panel.
    wrapper.getComponent('[data-test="smart-route-add"]').vm.$emit('update:modelValue', 4)
    await nextTick()
    await flushPromises()
    expect(wrapper.findAll('[data-test="smart-route-item"]')).toHaveLength(2)
    expect(wrapper.find('[data-test="smart-routes-list"]').exists()).toBe(true)
    wrapper.unmount()
  })

  describe('rate hints in the group cell', () => {
    const rateUp = { direction: 'up', delta: 0.02, previous_rate: 0.06, changed_at: '2026-11-04T10:30:00Z' }
    const hintedGroup = {
      id: 1, name: 'Claude', platform: 'anthropic', subscription_type: 'standard',
      recent_rate_change: rateUp, is_new: true, launched_at: '2026-11-05T01:00:00Z', launch_rate: 0.06,
    }
    const selectorSearch = 'input[placeholder="keys.searchGroup"]'

    const mountWithHints = async (groups: Array<Record<string, unknown>>, key: Partial<ApiKey> = {}) => {
      getAvailableGroups.mockResolvedValue(groups)
      listKeys.mockResolvedValue({
        items: [{ ...createApiKey(), group_id: 1, ...key }],
        total: 1,
        page: 1,
        page_size: 20,
        pages: 1,
      })
      return mountView()
    }

    it('puts the hints right after the trigger button instead of inside it', async () => {
      const wrapper = await mountWithHints([hintedGroup])
      const cell = wrapper.get('[data-group-cell]').element
      const signals = cell.querySelector('[data-group-signals]')

      expect(signals).not.toBeNull()
      expect(signals?.parentElement).toBe(cell)
      expect(cell.querySelector('button [data-group-signals]')).toBeNull()
      const children = Array.from(cell.children)
      expect(children[0].tagName).toBe('BUTTON')
      expect(children[1]).toBe(signals)
      expect(signals?.querySelector('[data-testid="group-rate-signal-rate"]')?.tagName).toBe('BUTTON')
      expect(signals?.querySelector('[data-testid="group-rate-signal-new"]')).not.toBeNull()
      wrapper.unmount()
    })

    it('reads the hints from the available groups, not from the group embedded in the key', async () => {
      const wrapper = await mountWithHints([hintedGroup], {
        group: { id: 1, name: 'Claude', platform: 'anthropic' } as ApiKey['group'],
      })
      expect(wrapper.find('[data-testid="group-rate-signal-rate"]').exists()).toBe(true)
      wrapper.unmount()
    })

    it('shows nothing for a group without hints', async () => {
      const wrapper = await mountWithHints([{ id: 1, name: 'Claude', platform: 'anthropic' }])
      expect(wrapper.find('[data-group-signals]').exists()).toBe(false)
      wrapper.unmount()
    })

    it('keeps a click on a hint from opening the group selector, while the rest of the cell still does', async () => {
      const wrapper = await mountWithHints([hintedGroup])
      expect(wrapper.find(selectorSearch).exists()).toBe(false)

      // Assert after each click: the selector toggles, so two clicks in a row would hide a bug.
      await wrapper.get('[data-testid="group-rate-signal-rate"]').trigger('click')
      expect(wrapper.find(selectorSearch).exists()).toBe(false)
      await wrapper.get('[data-testid="group-rate-signal-new"]').trigger('click')
      expect(wrapper.find(selectorSearch).exists()).toBe(false)

      await wrapper.get('[data-group-cell] > button').trigger('click')
      expect(wrapper.find(selectorSearch).exists()).toBe(true)
      wrapper.unmount()
    })

    it('opens the group selector from the empty end of the cell too', async () => {
      const wrapper = await mountWithHints([hintedGroup])
      await wrapper.get('[data-group-cell]').trigger('click')
      expect(wrapper.find(selectorSearch).exists()).toBe(true)
      wrapper.unmount()
    })

    it('drops only the arrow for a subscription group, which shows a label instead of a rate', async () => {
      const wrapper = await mountWithHints([{ ...hintedGroup, subscription_type: 'subscription' }])
      expect(wrapper.find('[data-testid="group-rate-signal-rate"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="group-rate-signal-new"]').exists()).toBe(true)
      wrapper.unmount()
    })

    it('shows the hints of the primary route only for a smart-routing key', async () => {
      const wrapper = await mountWithHints(
        [
          { id: 1, name: 'Claude', platform: 'anthropic', recent_rate_change: rateUp },
          { id: 2, name: 'GPT', platform: 'openai', is_new: true, launched_at: '2026-11-05T01:00:00Z' },
        ],
        { group_id: 1, group_ids: [1, 2] },
      )
      expect(wrapper.findAll('[data-group-signals]')).toHaveLength(1)
      expect(wrapper.find('[data-testid="group-rate-signal-rate"]').exists()).toBe(true)
      expect(wrapper.find('[data-testid="group-rate-signal-new"]').exists()).toBe(false)
      wrapper.unmount()
    })
  })

  describe('one-time receipt with stacked child dialogs', () => {
    const CREATED_SECRET = 'fixture-only-created-secret'
    const deepSeekGroup = { id: 3, name: 'DeepSeek', platform: 'deepseek' }

    const createKeyThroughForm = async (overrides: Partial<ApiKey> = {}, selectGroup = true) => {
      createKey.mockResolvedValue({
        ...createApiKey(), id: 99, name: 'used key', group_id: 3, group_ids: [3],
        key: CREATED_SECRET, ...overrides,
      })
      const wrapper = await mountView()
      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      await wrapper.get('[data-tour="key-form-name"]').setValue('used key')
      const groupSelect = wrapper.findComponent('[data-tour="key-form-group"]')
      if (selectGroup && groupSelect.exists()) {
        groupSelect.vm.$emit('update:modelValue', 3)
        await nextTick()
      }
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()
      return wrapper
    }

    it('opens the use-key guide without a no-group warning and returns to the receipt', async () => {
      getAvailableGroups.mockResolvedValue([deepSeekGroup])
      const wrapper = await createKeyThroughForm()
      try {
        await getButtonByText(wrapper, 'keys.secretDialog.use').trigger('click')
        await flushPromises()

        // 子弹窗叠在回执之上，拿到的分组来自刚创建的 key。
        const guide = wrapper.get('[data-test="use-key-modal"]')
        expect(guide.attributes('data-platform')).toBe('deepseek')
        expect(wrapper.get('[data-test="use-key-modal-no-group"]').text()).toBe('')
        // 回执本身没被销毁，明文还在。
        expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe(CREATED_SECRET)

        await wrapper.get('[data-test="use-key-modal-close"]').trigger('click')
        await flushPromises()

        expect(wrapper.find('[data-test="use-key-modal"]').exists()).toBe(false)
        expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe(CREATED_SECRET)
        await getButtonByText(wrapper, 'keys.secretDialog.copy').trigger('click')
        expect(copyToClipboard).toHaveBeenCalledWith(CREATED_SECRET, 'keys.secretDialog.copied')
      } finally {
        wrapper.unmount()
      }
    })

    it('returns to the receipt after closing the CCS import dialog', async () => {
      getAvailableGroups.mockResolvedValue([deepSeekGroup])
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: [] }) }))
      const wrapper = await createKeyThroughForm()
      try {
        await getButtonByText(wrapper, 'keys.secretDialog.importCcs').trigger('click')
        await flushPromises()
        expect(getButtonByText(wrapper, 'common.cancel')).toBeDefined()

        await getButtonByText(wrapper, 'common.cancel').trigger('click')
        await flushPromises()

        // 关掉 CCS 导入后回到回执：明文还在，还能继续选「使用密钥」。
        expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe(CREATED_SECRET)
        await getButtonByText(wrapper, 'keys.secretDialog.use').trigger('click')
        await flushPromises()
        expect(wrapper.get('[data-test="use-key-modal"]').attributes('data-platform')).toBe('deepseek')
      } finally {
        wrapper.unmount()
        vi.unstubAllGlobals()
      }
    })

    it('jumps from the no-group warning to the key row group selector', async () => {
      // 账号没有任何可绑定分组时允许建无分组 key（后端同样只在实例没有分组时放行）。
      getAvailableGroups.mockResolvedValue([])
      const wrapper = await createKeyThroughForm({ group_id: null, group_ids: undefined, name: 'orphan key' }, false)
      try {
        await getButtonByText(wrapper, 'keys.secretDialog.use').trigger('click')
        await flushPromises()
        expect(wrapper.get('[data-test="use-key-modal-no-group"]').text()).toBe('keys.useKeyModal.noGroupTitle')
        await wrapper.get('[data-test="use-key-modal-assign"]').trigger('click')
        await flushPromises()
        await nextTick()

        // 子弹窗和回执都关掉，并直接把这一行的分组选择器打开。
        expect(wrapper.find('[data-test="use-key-modal"]').exists()).toBe(false)
        expect(wrapper.find('[data-test="api-key-secret-value"]').exists()).toBe(false)
        expect(wrapper.find('[data-test="group-selector-dropdown"]').exists()).toBe(true)
      } finally {
        wrapper.unmount()
      }
    })

    it('allows creating a key without a group only when the account has no available groups', async () => {
      getAvailableGroups.mockResolvedValue([])
      createKey.mockResolvedValue({
        ...createApiKey(), id: 88, name: 'bootstrap key', group_id: null, key: CREATED_SECRET,
      })
      const wrapper = await mountView()
      try {
        await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
        expect(wrapper.get('[data-test="key-no-available-groups-hint"]').exists()).toBe(true)
        await wrapper.get('[data-tour="key-form-name"]').setValue('bootstrap key')
        await wrapper.get('#key-form').trigger('submit')
        await flushPromises()

        expect(createKey).toHaveBeenCalledTimes(1)
        expect(createKey.mock.calls[0][1]).toBeNull()
        expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe(CREATED_SECRET)
      } finally {
        wrapper.unmount()
      }
    })

    it('maps the server group-required rejection to a clear message', async () => {
      getAvailableGroups.mockResolvedValue([])
      createKey.mockRejectedValueOnce({ reason: 'API_KEY_GROUP_REQUIRED' })
      const wrapper = await mountView()
      try {
        await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
        await wrapper.get('[data-tour="key-form-name"]').setValue('needs group')
        await wrapper.get('#key-form').trigger('submit')
        await flushPromises()

        expect(showError).toHaveBeenCalledWith('keys.groupRequiredByServer')
        expect(wrapper.find('#key-form').exists()).toBe(true)
      } finally {
        wrapper.unmount()
      }
    })
  })
})

describe('user KeysView key-rotation remediation', () => {
  const neededKey = (overrides: Partial<ApiKey> = {}): ApiKey => ({
    ...createApiKey(),
    needs_rotation: true,
    rotation_reasons: ['created_before_cutoff', 'no_ip_allowlist'],
    ...overrides,
  })

  const pageOf = (items: ApiKey[]) => ({
    items,
    total: items.length,
    page: 1,
    page_size: 20,
    pages: 1,
  })

  beforeEach(() => {
    localStorage.clear()

    listKeys.mockReset()
    createKey.mockReset()
    updateKey.mockReset()
    deleteKey.mockReset()
    deleteMany.mockReset()
    rotateKey.mockReset()
    getKeyRotationGateStatus.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()

    getKeyRotationGateStatus.mockResolvedValue(undefined)
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
    isCurrentStep.mockReturnValue(false)
  })

  it('badges only the pending keys and shows the header summary with the one-click action', async () => {
    listKeys.mockResolvedValue(pageOf([neededKey(), { ...createApiKey(), id: 2, name: 'already-ok' }]))
    const wrapper = await mountView()

    // Exactly the pending row is badged; the remediated row is untouched.
    const badges = wrapper.findAll('[data-test="key-needs-rotation-badge"]')
    expect(badges).toHaveLength(1)
    expect(badges[0].text()).toContain('Needs rotation')
    expect(wrapper.get('[data-test="key-needs-rotation-reason"]').text()).toContain('Created before the cutoff')
    expect(wrapper.get('[data-test="key-needs-rotation-reason"]').text()).toContain('No IP allowlist')

    // Header summary counts the pending keys and offers the one-click flow.
    expect(wrapper.get('[data-test="keys-rotation-count"]').text()).toContain('1 keys need rotation')
    expect(wrapper.get('[data-test="keys-rotation-process"]').text()).toContain('Fix all')
    wrapper.unmount()
  })

  it('prefers the gate count and clears both the badge and the summary after setting an allowlist', async () => {
    getKeyRotationGateStatus.mockResolvedValue({ pending_key_count: 2 })
    getAvailableGroups.mockResolvedValue([{ id: 1, name: 'Claude', platform: 'anthropic' }])
    listKeys.mockResolvedValue(
      pageOf([neededKey({ group_id: 1 }), { ...createApiKey(), id: 2, name: 'other', group_id: 1 }]),
    )
    updateKey.mockResolvedValue({
      ...neededKey({ group_id: 1 }),
      needs_rotation: false,
      rotation_reasons: [],
      ip_whitelist: ['203.0.113.7'],
    })
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="keys-rotation-count"]').text()).toContain('2 keys need rotation')

    // A successful edit response carries the new needs_rotation=false, so the row
    // badge must disappear without a list refetch.
    listKeys.mockClear()
    getKeyRotationGateStatus.mockResolvedValue({ pending_key_count: 1 })
    await getButtonByText(wrapper, 'common.edit').trigger('click')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(updateKey).toHaveBeenCalled()
    expect(listKeys).not.toHaveBeenCalled()
    expect(wrapper.findAll('[data-test="key-needs-rotation-badge"]')).toHaveLength(0)
    wrapper.unmount()
  })

  it('removes the badge immediately after rotating the key', async () => {
    // 轮换会下发新明文：账号有可用因子时必须先过身份校验，这里给出无因子账号
    // 直接轮换；「有因子必须先校验」由下面的专项用例覆盖。
    listKeys.mockResolvedValue(pageOf([neededKey()]))
    rotateKey.mockResolvedValue({
      ...neededKey(),
      needs_rotation: false,
      rotation_reasons: [],
      key: 'sk-rotated-receipt',
    })
    getKeyRotationGateStatus
      .mockResolvedValueOnce({ pending_key_count: 1 })
      .mockResolvedValue({ pending_key_count: 0 })
    const wrapper = await mountView()

    expect(wrapper.find('[data-test="key-needs-rotation-badge"]').exists()).toBe(true)
    await wrapper.get('[data-test="key-needs-rotation-rotate"]').trigger('click')
    await flushPromises()

    expect(rotateKey).toHaveBeenCalledWith(1, undefined)
    expect(wrapper.find('[data-test="key-needs-rotation-badge"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="keys-rotation-count"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('账号有可用因子时，单条轮换必须先过身份校验再带 verification 轮换', async () => {
    listKeys.mockResolvedValue(pageOf([neededKey()]))
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
    })
    rotateKey.mockResolvedValue({
      ...neededKey(),
      needs_rotation: false,
      rotation_reasons: [],
      key: 'sk-rotated-verified',
    })
    getKeyRotationGateStatus.mockResolvedValue({ pending_key_count: 0 })
    const wrapper = await mountView()

    await wrapper.get('[data-test="key-needs-rotation-rotate"]').trigger('click')
    await flushPromises()

    // 还没校验：一次都不能轮换（旧 Key 不能被悄悄换掉）。
    expect(rotateKey).not.toHaveBeenCalled()
    const dialog = wrapper.get('[data-test="rotate-verification"]')
    expect(dialog.exists()).toBe(true)

    await dialog.get('[data-test="key-factor-password-input"]').setValue('correct horse')
    await wrapper.get('[data-test="rotate-verify-submit"]').trigger('click')
    await flushPromises()

    expect(rotateKey).toHaveBeenCalledWith(1, { method: 'password', password: 'correct horse' })
    wrapper.unmount()
  })

  it('轮换时输过登录密码：回执里「以后可查看」直接用它，不再要第二遍', async () => {
    listKeys.mockResolvedValue(pageOf([neededKey()]))
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
      create_verification_required: true,
    })
    rotateKey.mockResolvedValue({ ...neededKey(), needs_rotation: false, rotation_reasons: [], key: 'sk-rotated-reuse' })
    getKeyRotationGateStatus.mockResolvedValue({ pending_key_count: 0 })
    const seal = { version: 1, kdf: { algo: 'PBKDF2-SHA256', iterations: 310000, salt: 'c2FsdA==' }, sealed: 'c2VhbGVk' }
    sealApiKey.mockResolvedValue(seal)
    putSecretSeal.mockResolvedValue(undefined)
    const wrapper = await mountView()

    await wrapper.get('[data-test="key-needs-rotation-rotate"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="rotate-verification"] [data-test="key-factor-password-input"]').setValue('typed-for-rotation')
    await wrapper.get('[data-test="rotate-verify-submit"]').trigger('click')
    await flushPromises()

    // The receipt is up; the switch defaults ON, and the password field is not shown because password is remembered.
    expect(wrapper.find('[data-test="api-key-secret-dialog"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="api-key-seal-checkbox"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.find('[data-test="api-key-seal-password"]').exists()).toBe(false)

    await new Promise((resolve) => setTimeout(resolve, 1100))
    await nextTick()
    await wrapper.get('[data-test="api-key-secret-done"]').trigger('click')
    await flushPromises()

    expect(sealApiKey).toHaveBeenCalledWith('sk-rotated-reuse', 'typed-for-rotation')
    expect(putSecretSeal).toHaveBeenCalledWith(1, seal, { password: 'typed-for-rotation', authFactor: 'password' })
    expect(wrapper.find('[data-test="api-key-secret-dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('只有登录密码的账号，单条轮换不弹校验、直接带空 verification 轮换', async () => {
    listKeys.mockResolvedValue(pageOf([neededKey()]))
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
      create_verification_required: false,
    })
    rotateKey.mockResolvedValue({
      ...neededKey(),
      needs_rotation: false,
      rotation_reasons: [],
      key: 'sk-rotated-no-password',
    })
    getKeyRotationGateStatus.mockResolvedValue({ pending_key_count: 0 })
    const wrapper = await mountView()

    await wrapper.get('[data-test="key-needs-rotation-rotate"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="rotate-verification"]').exists()).toBe(false)
    expect(rotateKey).toHaveBeenCalledWith(1, undefined)
    wrapper.unmount()
  })

  it('开了 TOTP、但用户关掉了「创建时需要验证身份」：单条轮换不弹校验、直接带空 verification 轮换', async () => {
    listKeys.mockResolvedValue(pageOf([neededKey()]))
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: true,
      passkey_count: 0,
      preferred: 'totp',
      available_methods: ['totp', 'password'],
      seal_available: true,
      create_verification_required: false,
      create_verification_mode: 'off',
      create_verification_configurable: true,
    })
    rotateKey.mockResolvedValue({
      ...neededKey(),
      needs_rotation: false,
      rotation_reasons: [],
      key: 'sk-rotated-2fa-off',
    })
    getKeyRotationGateStatus.mockResolvedValue({ pending_key_count: 0 })
    const wrapper = await mountView()

    await wrapper.get('[data-test="key-needs-rotation-rotate"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="rotate-verification"]').exists()).toBe(false)
    expect(rotateKey).toHaveBeenCalledWith(1, undefined)
    wrapper.unmount()
  })

  it('开了 TOTP 的账号，单条轮换仍然先过身份校验', async () => {
    listKeys.mockResolvedValue(pageOf([neededKey()]))
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: true,
      passkey_count: 0,
      preferred: 'totp',
      available_methods: ['totp', 'password'],
      seal_available: true,
      create_verification_required: true,
    })
    getKeyRotationGateStatus.mockResolvedValue({ pending_key_count: 0 })
    const wrapper = await mountView()

    await wrapper.get('[data-test="key-needs-rotation-rotate"]').trigger('click')
    await flushPromises()

    expect(rotateKey).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="rotate-verification"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('单条轮换的校验被服务端拒绝时给出具体原因', async () => {
    listKeys.mockResolvedValue(pageOf([neededKey()]))
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
    })
    rotateKey.mockRejectedValue({ reason: 'VERIFY_PASSWORD_INVALID' })
    getKeyRotationGateStatus.mockResolvedValue({ pending_key_count: 0 })
    const wrapper = await mountView()

    await wrapper.get('[data-test="key-needs-rotation-rotate"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="rotate-verification"] [data-test="key-factor-password-input"]').setValue('wrong')
    await wrapper.get('[data-test="rotate-verify-submit"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith(
      'The password is incorrect. Enter your current login password.',
    )
    wrapper.unmount()
  })

  it('one-click processing shows only the pending keys and can return to the full list', async () => {
    listKeys
      .mockResolvedValueOnce(pageOf([neededKey(), { ...createApiKey(), id: 2, name: 'other' }]))
      .mockResolvedValueOnce(pageOf([neededKey(), neededKey({ id: 3, name: 'old-key-2' })]))
      .mockResolvedValue(pageOf([neededKey(), { ...createApiKey(), id: 2, name: 'other' }]))
    const wrapper = await mountView()

    await wrapper.get('[data-test="keys-rotation-process"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="keys-rotation-filter"]').exists()).toBe(true)
    const filteredRows = wrapper.getComponent({ name: 'DataTable' }).props('data') as ApiKey[]
    expect(filteredRows.map((row) => row.id)).toEqual([1, 3])

    await wrapper.get('[data-test="keys-rotation-show-all"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="keys-rotation-filter"]').exists()).toBe(false)
    const fullRows = wrapper.getComponent({ name: 'DataTable' }).props('data') as ApiKey[]
    expect(fullRows.map((row) => row.id)).toEqual([1, 2])
    wrapper.unmount()
  })
})

describe('user KeysView ephemeral and sealed keys', () => {
  const pageOf = (items: ApiKey[]) => ({
    items,
    total: items.length,
    page: 1,
    page_size: 20,
    pages: 1,
  })

  beforeEach(() => {
    localStorage.clear()

    listKeys.mockReset()
    createKey.mockReset()
    updateKey.mockReset()
    deleteKey.mockReset()
    deleteMany.mockReset()
    rotateKey.mockReset()
    getKeyRotationGateStatus.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()

    getKeyRotationGateStatus.mockResolvedValue(undefined)
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
    isCurrentStep.mockReturnValue(false)
    copyToClipboard.mockResolvedValue(true)
  })

  it('keeps the server mask in the row when the tab holds the plaintext, and still copies it', async () => {
    listKeys.mockResolvedValue(pageOf([createApiKey()]))
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="key-value"]').text()).toBe('sk-test-****-key')

    useEphemeralKeyStore().remember(1, 'sk-fixture-full')
    await nextTick()

    // 内存里的明文绝不进表格：行内始终是后端掩码，超长 Key 不会把行撑宽。
    expect(wrapper.get('[data-test="key-value"]').text()).toBe('sk-test-****-key')
    expect(wrapper.text()).not.toContain('sk-fixture-full')
    expect(wrapper.get('[data-test="key-value"]').attributes('title')).toBe(
      'Created in this tab; copy the full key now.'
    )

    // 复制按钮仍从内存 store 取完整明文，并切到「已复制」的勾选态。
    await wrapper.get('[data-test="key-copy"]').trigger('click')
    await flushPromises()
    expect(copyToClipboard).toHaveBeenCalledWith('sk-fixture-full')
    expect(wrapper.get('[data-test="key-copy"]').html()).toContain('i-lucide-check')
    wrapper.unmount()
  })

  it('asks for the login password instead of copying when only the ciphertext exists', async () => {
    listKeys.mockResolvedValue(pageOf([{ ...createApiKey(), has_sealed_secret: true }]))
    const wrapper = await mountView()

    await wrapper.get('[data-test="key-copy"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="api-key-reveal-dialog"]').exists()).toBe(true)
    expect(copyToClipboard).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps the mask after rotating in this tab and copies the rotated plaintext', async () => {
    listKeys.mockResolvedValue(pageOf([{ ...createApiKey(), needs_rotation: true }]))
    rotateKey.mockResolvedValue({
      ...createApiKey(),
      needs_rotation: false,
      rotation_reasons: [],
      key: 'sk-rotated-fresh',
    })
    getKeyRotationGateStatus.mockResolvedValue({ pending_key_count: 0 })
    const wrapper = await mountView()

    await wrapper.get('[data-test="key-needs-rotation-rotate"]').trigger('click')
    await flushPromises()

    // 回执弹窗里才有明文；列表行仍只渲染后端口径的掩码。
    expect(wrapper.get('[data-test="key-value"]').text()).toBe('sk-test-****-key')
    expect(wrapper.get('[data-test="key-cell"]').text()).not.toContain('sk-rotated-fresh')
    await wrapper.get('[data-test="key-copy"]').trigger('click')
    await flushPromises()
    expect(copyToClipboard).toHaveBeenCalledWith('sk-rotated-fresh')
    wrapper.unmount()
  })

  it('always renders a uniform mask instead of the full plaintext, including after a refresh', async () => {
    listKeys.mockResolvedValue(pageOf([createApiKey()]))
    const first = await mountView()
    useEphemeralKeyStore().remember(1, 'sk-refresh-fixture')
    await nextTick()

    // 本标签页内：行内仍是掩码，明文只在内存 store / 复制按钮里。
    expect(first.get('[data-test="key-value"]').text()).toBe('sk-test-****-key')
    expect(first.text()).not.toContain('sk-refresh-fixture')
    first.unmount()

    // 刷新页面 = 新的 Pinia 实例：明文不落任何浏览器存储，列表仍显示同一掩码。
    setActivePinia(createPinia())
    const second = await mountView()
    expect(second.get('[data-test="key-value"]').text()).toBe('sk-test-****-key')
    expect(second.get('[data-test="key-value"]').text()).toMatch(/^sk-.+\*{4}/)
    expect(second.text()).not.toContain('sk-refresh-fixture')
    expect(readBrowserStorage()).not.toContain('sk-refresh-fixture')
    second.unmount()
  })

  it('falls back to prefix + **** for legacy rows without a server mask', async () => {
    listKeys.mockResolvedValue(pageOf([{ ...createApiKey(), key_masked: '', key_prefix: 'sk-legacy-' }]))
    const wrapper = await mountView()

    expect(wrapper.get('[data-test="key-value"]').text()).toBe('sk-legacy-****')
    wrapper.unmount()
  })

  it('keeps long mask values on one line without widening the column', async () => {
    const longMask = `sk-${'a'.repeat(60)}****${'b'.repeat(4)}`
    listKeys.mockResolvedValue(pageOf([{ ...createApiKey(), key_masked: longMask }]))
    const wrapper = await mountView()

    const value = wrapper.get('[data-test="key-value"]')
    expect(value.text()).toBe(longMask)
    // 等宽 + 单行截断：超长内容不换行，也不会把列撑宽。
    expect(value.classes()).toContain('font-mono')
    expect(value.classes()).toContain('truncate')
    expect(value.classes()).not.toContain('break-all')
    expect(wrapper.get('[data-test="key-cell-content"]').classes()).toContain('max-w-[12.5rem]')
    expect(wrapper.get('[data-test="key-copy"]').classes()).toContain('shrink-0')
    wrapper.unmount()
  })
})

describe('user KeysView batch rotation, overview and ephemeral bar', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()

    listKeys.mockReset()
    createKey.mockReset()
    putSecretSeal.mockReset()
    putSecretSeal.mockResolvedValue(undefined)
    sealApiKey.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    copyToClipboard.mockResolvedValue(true)
    isCurrentStep.mockReset()
    nextStep.mockReset()
    batchRotateKeys.mockReset()
    batchRotateKeys.mockResolvedValue({ results: [], requested_count: 0, succeeded_count: 0, failed_count: 0 })
    getOverview.mockReset()
    getOverview.mockResolvedValue({ items: [], window_hours: 24 })

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
    isCurrentStep.mockReturnValue(false)
  })

  it('renders the 24h calls/cost, expiry and IP allowlist status from one batched cover', async () => {
    listKeys.mockResolvedValue({
      items: [
        {
          ...createApiKey(),
          ip_whitelist: ['1.2.3.4', '5.6.7.8'],
          expires_at: '2027-01-01T00:00:00Z',
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getOverview.mockResolvedValue({
      items: [{ id: 1, requests_24h: 42, cost_24h: 0.1234 }],
      window_hours: 24,
    })

    const wrapper = await mountView()
    await flushPromises()

    expect(getOverview).toHaveBeenCalledWith([1])
    const cell = wrapper.get('[data-test="key-overview-cell"]')
    expect(cell.get('[data-test="key-overview-calls"]').text()).toBe('42')
    expect(cell.get('[data-test="key-overview-cost"]').text()).toBe('$0.1234')
    expect(cell.get('[data-test="key-overview-ip"]').text()).toContain('IP allowlist · 2')
    expect(cell.get('[data-test="key-overview-expiry"]').text()).toContain('Expires')
    wrapper.unmount()
  })

  it('rotates selected keys in bulk, stores each plaintext in memory and copies all', async () => {
    listKeys.mockResolvedValue({
      items: [createApiKey(), { ...createApiKey(), id: 2, name: 'second-key' }],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    batchRotateKeys.mockResolvedValue({
      results: [
        { id: 1, status: 'ok', key: 'rotated-one-fixture-secret' },
        { id: 2, status: 'failed', error_code: 'API_KEY_ROTATE_FAILED' },
      ],
      requested_count: 2,
      succeeded_count: 1,
      failed_count: 1,
    })

    const wrapper = await mountView()
    await wrapper.get('[data-test="select-all"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="keys-bulk-rotate"]').trigger('click')
    await nextTick()

    // 二次确认必须说明旧 Key 立即失效。
    const confirm = wrapper.get('[data-test="confirm-dialog"]')
    expect(confirm.text()).toContain('Rotate 2 keys?')
    expect(confirm.text()).toContain('Old keys stop working immediately')

    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    // accounts without any factor: the backend explicitly allows it and the
    // plaintext is still shown exactly once.
    expect(batchRotateKeys).toHaveBeenCalledWith([1, 2], undefined)
    expect(useEphemeralKeyStore().recall(1)).toBe('rotated-one-fixture-secret')

    const result = wrapper.get('[data-test="batch-rotate-result"]')
    expect(result.text()).toContain('1 rotated, 1 failed')
    expect(result.text()).toContain('rotated-one-fixture-secret')
    expect(wrapper.get('[data-test="batch-rotate-result-2"]').text()).toContain('API_KEY_ROTATE_FAILED')

    await wrapper.get('[data-test="batch-rotate-copy-all"]').trigger('click')
    await flushPromises()
    expect(copyToClipboard).toHaveBeenCalledWith('test-key: rotated-one-fixture-secret')
    wrapper.unmount()
  })

  it('只有登录密码的账号，批量轮换确认后直接轮换，不弹身份校验', async () => {
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
      create_verification_required: false,
    })
    listKeys.mockResolvedValue({
      items: [createApiKey(), { ...createApiKey(), id: 2, name: 'second-key' }],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    batchRotateKeys.mockResolvedValue({
      results: [{ id: 1, status: 'ok', key: 'rotated-no-password' }],
      requested_count: 2,
      succeeded_count: 1,
      failed_count: 1,
    })

    const wrapper = await mountView()
    await wrapper.get('[data-test="select-all"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="keys-bulk-rotate"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="batch-rotate-verification"]').exists()).toBe(false)
    expect(batchRotateKeys).toHaveBeenCalledWith([1, 2], undefined)
    wrapper.unmount()
  })

  it('开了 TOTP、但用户关掉了「创建时需要验证身份」：批量轮换确认后直接轮换，不弹身份校验', async () => {
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: true,
      passkey_count: 0,
      preferred: 'totp',
      available_methods: ['totp', 'password'],
      seal_available: true,
      create_verification_required: false,
      create_verification_mode: 'off',
      create_verification_configurable: true,
    })
    listKeys.mockResolvedValue({
      items: [createApiKey(), { ...createApiKey(), id: 2, name: 'second-key' }],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    batchRotateKeys.mockResolvedValue({
      results: [{ id: 1, status: 'ok', key: 'rotated-two-factor-off' }],
      requested_count: 2,
      succeeded_count: 1,
      failed_count: 1,
    })

    const wrapper = await mountView()
    await wrapper.get('[data-test="select-all"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="keys-bulk-rotate"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="batch-rotate-verification"]').exists()).toBe(false)
    expect(batchRotateKeys).toHaveBeenCalledWith([1, 2], undefined)
    wrapper.unmount()
  })

  it('账号有可用因子时，批量轮换必须先过身份校验再带 verification 提交', async () => {
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
    })
    listKeys.mockResolvedValue({
      items: [createApiKey(), { ...createApiKey(), id: 2, name: 'second-key' }],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    batchRotateKeys.mockResolvedValue({
      results: [{ id: 1, status: 'ok', key: 'rotated-verified-secret' }],
      requested_count: 2,
      succeeded_count: 1,
      failed_count: 1,
    })

    const wrapper = await mountView()
    await wrapper.get('[data-test="select-all"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="keys-bulk-rotate"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    // 确认后不能立刻轮换：先出身份校验步，旧 Key 一把都不能被换掉。
    expect(batchRotateKeys).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="batch-rotate-verification"]').exists()).toBe(true)

    await wrapper.get('[data-test="batch-rotate-verification"] [data-test="key-factor-password-input"]')
      .setValue('correct horse')
    await wrapper.get('[data-test="batch-rotate-verify-submit"]').trigger('click')
    await flushPromises()

    expect(batchRotateKeys).toHaveBeenCalledWith([1, 2], { method: 'password', password: 'correct horse' })
    expect(useEphemeralKeyStore().recall(1)).toBe('rotated-verified-secret')
    wrapper.unmount()
  })

  it('批量轮换校验被拒绝时提示具体原因，且一次都不轮换', async () => {
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
    })
    listKeys.mockResolvedValue({ items: [createApiKey()], total: 1, page: 1, page_size: 20, pages: 1 })
    batchRotateKeys.mockRejectedValue({ reason: 'VERIFY_REQUIRED' })

    const wrapper = await mountView()
    await wrapper.get('[data-test="select-all"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="keys-bulk-rotate"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="batch-rotate-verification"] [data-test="key-factor-password-input"]')
      .setValue('correct horse')
    await wrapper.get('[data-test="batch-rotate-verify-submit"]').trigger('click')
    await flushPromises()

    expect(batchRotateKeys).toHaveBeenCalledTimes(1)
    expect(showError).toHaveBeenCalledWith('Complete identity verification first.')
    wrapper.unmount()
  })

  it('re-seals rotated keys one by one and reports per-item seal status', async () => {
    authUser.value = { id: 1, email: 'user@example.com' }
    listKeys.mockResolvedValue({
      items: [{ ...createApiKey(), has_sealed_secret: true }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    batchRotateKeys.mockResolvedValue({
      results: [{ id: 1, status: 'ok', key: 'rotated-sealed-fixture-secret' }],
      requested_count: 1,
      succeeded_count: 1,
      failed_count: 0,
    })
    const seal = {
      version: 1 as const,
      kdf: { algo: 'PBKDF2-SHA256' as const, iterations: 310000, salt: 'c2FsdA==' },
      sealed: 'c2VhbGVk',
    }
    sealApiKey.mockResolvedValue(seal)
    putSecretSeal.mockResolvedValue(undefined)

    const wrapper = await mountView()
    await wrapper.get('[data-test="select-all"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="keys-bulk-rotate"]').trigger('click')
    await nextTick()
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="batch-rotate-seal"]').exists()).toBe(true)
    await wrapper.get('[data-test="batch-rotate-seal-password"]').setValue('hunter2')
    await wrapper.get('[data-test="batch-rotate-seal-submit"]').trigger('click')
    await flushPromises()

    expect(sealApiKey).toHaveBeenCalledWith('rotated-sealed-fixture-secret', 'hunter2')
    // 重新封存同样要让服务端校验登录密码，并记录授权因子。
    expect(putSecretSeal).toHaveBeenCalledWith(1, seal, {
      password: 'hunter2',
      authFactor: 'password',
    })
    expect(wrapper.get('[data-test="batch-rotate-seal-summary"]').text()).toContain('Saved 1')
    wrapper.unmount()
  })

  // 服务端按用户限制登录密码输错的次数：整批用同一个错密码把剩下的 Key 全打一遍，只会把账号锁一分钟。
  describe('batch re-seal stops at the first password failure', () => {
    const seal = {
      version: 1 as const,
      kdf: { algo: 'PBKDF2-SHA256' as const, iterations: 310000, salt: 'c2FsdA==' },
      sealed: 'c2VhbGVk',
    }
    const rotateThreeSealedKeysAndSubmit = async (password: string) => {
      authUser.value = { id: 1, email: 'user@example.com' }
      listKeys.mockResolvedValue({
        items: [1, 2, 3].map((id) => ({ ...createApiKey(), id, name: `key-${id}`, has_sealed_secret: true })),
        total: 3,
        page: 1,
        page_size: 20,
        pages: 1,
      })
      batchRotateKeys.mockResolvedValue({
        results: [1, 2, 3].map((id) => ({ id, status: 'ok', key: `rotated-fixture-secret-${id}` })),
        requested_count: 3,
        succeeded_count: 3,
        failed_count: 0,
      })
      sealApiKey.mockResolvedValue(seal)
      const wrapper = await mountView()
      await wrapper.get('[data-test="select-all"]').trigger('click')
      await nextTick()
      await wrapper.get('[data-test="keys-bulk-rotate"]').trigger('click')
      await nextTick()
      await wrapper.get('[data-test="confirm-ok"]').trigger('click')
      await flushPromises()
      await wrapper.get('[data-test="batch-rotate-seal-password"]').setValue(password)
      await wrapper.get('[data-test="batch-rotate-seal-submit"]').trigger('click')
      await flushPromises()
      return wrapper
    }

    it('wrong login password: one request, a specific message, nothing else attempted', async () => {
      putSecretSeal.mockRejectedValue({ status: 400, reason: 'VERIFY_PASSWORD_INVALID' })
      const wrapper = await rotateThreeSealedKeysAndSubmit('wrong')

      expect(putSecretSeal).toHaveBeenCalledTimes(1)
      expect(wrapper.get('[data-test="batch-rotate-seal-error"]').text()).toBe(
        'The login password is incorrect, so "view later" cannot be saved.',
      )
      wrapper.unmount()
    })

    it('password budget used up part-way: keeps what was saved, stops, and says to wait', async () => {
      putSecretSeal
        .mockResolvedValueOnce(undefined)
        .mockRejectedValue({ status: 429, reason: 'VERIFY_PASSWORD_RATE_LIMITED' })
      const wrapper = await rotateThreeSealedKeysAndSubmit('hunter2')

      expect(putSecretSeal).toHaveBeenCalledTimes(2)
      expect(wrapper.get('[data-test="batch-rotate-seal-error"]').text()).toBe(
        'Too many incorrect password attempts. Please wait a minute and try again.',
      )
      expect(wrapper.get('[data-test="batch-rotate-seal-summary"]').text()).toContain('Saved 1')
      wrapper.unmount()
    })

    it('other failures keep the old behaviour: every key is still tried', async () => {
      putSecretSeal.mockRejectedValue({ status: 500, message: 'boom' })
      const wrapper = await rotateThreeSealedKeysAndSubmit('hunter2')

      expect(putSecretSeal).toHaveBeenCalledTimes(3)
      expect(wrapper.find('[data-test="batch-rotate-seal-error"]').exists()).toBe(false)
      wrapper.unmount()
    })
  })

  it('shows the five-minute re-copy bar after creation and copies from the in-memory store', async () => {
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'DeepSeek', platform: 'deepseek' }])
    createKey.mockResolvedValue({
      ...createApiKey(),
      id: 77,
      name: 'once key',
      group_id: 3,
      group_ids: [3],
      key: 'live-bar-fixture-secret',
    })

    const wrapper = await mountView()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await wrapper.get('[data-tour="key-form-name"]').setValue('once key')
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
    await nextTick()
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    // 浮条不是 modal：创建成功后就出现在页面右下角，明文只从内存 store 取。
    const bar = wrapper.get('[data-test="ephemeral-key-bar"]')
    expect(bar.text()).toContain('New key created — copy it again within 5 minutes')
    expect(useEphemeralKeyStore().recall(77)).toBe('live-bar-fixture-secret')
    expect(readBrowserStorage()).not.toContain('live-bar-fixture-secret')

    await wrapper.get('[data-test="ephemeral-key-copy"]').trigger('click')
    await flushPromises()
    expect(copyToClipboard).toHaveBeenCalledWith('live-bar-fixture-secret')

    await wrapper.get('[data-test="ephemeral-key-close"]').trigger('click')
    await nextTick()
    expect(wrapper.find('[data-test="ephemeral-key-bar"]').exists()).toBe(false)
    wrapper.unmount()
  })
})

describe('user KeysView 创建身份校验（三选一）', () => {
  const openCreateAndFill = async (name = 'verified key') => {
    const wrapper = await mountView()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-tour="key-form-name"]').setValue(name)
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
    await nextTick()
    return wrapper
  }

  it('第一步只推进到身份校验，不创建 Key', async () => {
    authUser.value = { id: 1, email: 'user@example.com' }
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
    })
    const wrapper = await openCreateAndFill()

    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="key-create-verification"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="key-create-steps"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="key-create-next"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('第二步密码校验通过后带 verification 创建 Key', async () => {
    authUser.value = { id: 1, email: 'user@example.com' }
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
    })
    createKey.mockResolvedValue({ ...createApiKey(), key: 'sk-verified-fixture' })
    const wrapper = await openCreateAndFill()

    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    await wrapper.get('[data-test="key-factor-password-input"]').setValue('correct horse')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).toHaveBeenCalledTimes(1)
    const options = createKey.mock.calls[0][10] as Record<string, any>
    expect(options.verification).toEqual({ method: 'password', password: 'correct horse' })
    expect(showSuccess).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('密码错误时直接提示「密码不正确」，不显示笼统保存失败', async () => {
    authUser.value = { id: 1, email: 'user@example.com' }
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
    })
    createKey.mockRejectedValue({ reason: 'VERIFY_PASSWORD_INVALID' })
    const wrapper = await openCreateAndFill()

    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    await wrapper.get('[data-test="key-factor-password-input"]').setValue('wrong')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith(
      'The password is incorrect. Enter your current login password.',
    )
    wrapper.unmount()
  })

  it('密码输错次数用尽（429）时提示稍后再试，不显示笼统保存失败', async () => {
    authUser.value = { id: 1, email: 'user@example.com' }
    getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
    })
    createKey.mockRejectedValue({ status: 429, reason: 'VERIFY_PASSWORD_RATE_LIMITED' })
    const wrapper = await openCreateAndFill()

    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()
    await wrapper.get('[data-test="key-factor-password-input"]').setValue('whatever')
    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith(
      'Too many incorrect password attempts. Please wait a minute and try again.',
    )
    expect(showError).not.toHaveBeenCalledWith('Failed to save')
    wrapper.unmount()
  })

  it('账号没有任何可用因子时跳过校验，直接创建（仅一次性显示）', async () => {
    authUser.value = { id: 1, email: 'user@example.com' }
    createKey.mockResolvedValue({ ...createApiKey(), key: 'sk-one-time-fixture' })
    const wrapper = await openCreateAndFill('one time key')

    await wrapper.get('#key-form').trigger('submit')
    await flushPromises()

    expect(createKey).toHaveBeenCalledTimes(1)
    const options = createKey.mock.calls[0][10] as Record<string, any>
    expect(options.verification).toBeUndefined()
    wrapper.unmount()
  })

  it('Key 名称输入框使用非凭据 name 与 autocomplete=off，避免自动填充串扰', async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await flushPromises()
    const name = wrapper.get('[data-tour="key-form-name"]')
    expect(name.attributes('name')).toBe('api-key-name')
    expect(name.attributes('autocomplete')).toBe('off')
    expect(name.attributes('data-lpignore')).toBe('true')
    expect(name.attributes('data-1p-ignore')).toBe('true')
    wrapper.unmount()
  })
})

describe('user KeysView 创建弹窗（Quiet 流程）与页面过期守卫', () => {
  // 老服务端的口径（响应里没有 create_verification_required）：有任何可用因子都强制校验。
  // 新服务端「只有开了 TOTP / Passkey 才强制」的行为见下面的「创建不再强制登录密码」。
  const passwordFactors = {
    password_available: true,
    totp_enabled: false,
    passkey_count: 0,
    preferred: 'password',
    available_methods: ['password'],
    seal_available: true,
  }
  const appUpdate = useAppUpdate()
  // 只还原这里自己建的 spy：vi.restoreAllMocks() 会把前面用例遗留在其它 mock 上的返回值一并清掉。
  let ensureFreshSpy: { mockRestore: () => void } | null = null
  const resetUpdateState = () => {
    appUpdate.updateAvailable.value = false
    appUpdate.blockingOpen.value = false
  }

  beforeEach(() => {
    window.localStorage.clear()
    resetUpdateState()
    authUser.value = { id: 1, email: 'user@example.com' }
    getVerificationFactors.mockResolvedValue(passwordFactors)
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'DeepSeek', platform: 'deepseek' }])
  })

  afterEach(() => {
    ensureFreshSpy?.mockRestore()
    ensureFreshSpy = null
    resetUpdateState()
  })

  const openCreate = async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await flushPromises()
    return wrapper
  }

  const dialogDescription = (wrapper: VueWrapper) =>
    wrapper.get('#key-form').element.closest('[description]')?.getAttribute('description') ?? null

  const advancedHidden = (wrapper: VueWrapper) =>
    (wrapper.get('[data-test="key-advanced-settings"]').element as HTMLElement).style.display === 'none'

  it('第一步：Quiet 步骤头、一句话说明，只露出名称 / 提供商 / 分组，其余收进「更多设置」', async () => {
    const wrapper = await openCreate()

    expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 1 of 2')
    expect(dialogDescription(wrapper)).toBe('Name the key and pick a route to create it.')

    expect(advancedHidden(wrapper)).toBe(true)
    const toggle = wrapper.get('[data-test="key-advanced-toggle"]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(toggle.attributes('aria-controls')).toBe('key-advanced-settings')
    expect(toggle.text()).toContain('More settings')

    // 页脚：左边取消，右边下一步；第一步没有「上一步」。
    expect(wrapper.find('[data-test="key-create-cancel"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="key-create-next"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="key-create-back"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('「更多设置」可以展开 / 收起，字段始终挂载（不影响校验和已有选择器）', async () => {
    const wrapper = await openCreate()
    const toggle = wrapper.get('[data-test="key-advanced-toggle"]')
    expect(wrapper.find('#key-expiration-label').exists()).toBe(true)

    await toggle.trigger('click')
    expect(advancedHidden(wrapper)).toBe(false)
    expect(toggle.attributes('aria-expanded')).toBe('true')

    await toggle.trigger('click')
    expect(advancedHidden(wrapper)).toBe(true)
    expect(toggle.attributes('aria-expanded')).toBe('false')
    wrapper.unmount()
  })

  it('每次重新打开创建弹窗，「更多设置」都回到收起', async () => {
    const wrapper = await openCreate()
    await wrapper.get('[data-test="key-advanced-toggle"]').trigger('click')
    expect(advancedHidden(wrapper)).toBe(false)

    await wrapper.get('[data-test="key-create-cancel"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
    await flushPromises()
    expect(advancedHidden(wrapper)).toBe(true)
    wrapper.unmount()
  })

  describe('必填项：名称和分组没填就不能进下一步，并高亮未完成项', () => {
    const nameField = (wrapper: VueWrapper) => wrapper.get('[data-test="key-field-name"]')
    const groupField = (wrapper: VueWrapper) => wrapper.get('[data-test="key-field-group"]')

    it('刚打开的空表单不标红；点「下一步」后名称和分组同时标红并留在第一步', async () => {
      const wrapper = await openCreate()
      expect(nameField(wrapper).text()).not.toContain('keys.nameRequired')
      expect(groupField(wrapper).text()).not.toContain('keys.groupRequired')

      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()

      expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 1 of 2')
      expect(nameField(wrapper).text()).toContain('keys.nameRequired')
      expect(groupField(wrapper).text()).toContain('keys.groupRequired')
      expect(wrapper.get('[data-tour="key-form-name"]').attributes('aria-invalid')).toBe('true')
      expect(createKey).not.toHaveBeenCalled()
      wrapper.unmount()
    })

    it('名称只有空白也算没填；填好名称后名称的红色立刻消失，分组仍标红', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-tour="key-form-name"]').setValue('   ')
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()
      expect(nameField(wrapper).text()).toContain('keys.nameRequired')

      await wrapper.get('[data-tour="key-form-name"]').setValue('my key')
      await flushPromises()
      expect(nameField(wrapper).text()).not.toContain('keys.nameRequired')
      expect(groupField(wrapper).text()).toContain('keys.groupRequired')
      wrapper.unmount()
    })

    it('名称填了、分组没选：只标红分组，仍不能进下一步', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-tour="key-form-name"]').setValue('only name')
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()

      expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 1 of 2')
      expect(nameField(wrapper).text()).not.toContain('keys.nameRequired')
      expect(groupField(wrapper).text()).toContain('keys.groupRequired')
      wrapper.unmount()
    })

    it('分组选好之后红色立刻消失，再点「下一步」才进第二步', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-tour="key-form-name"]').setValue('both')
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()
      expect(groupField(wrapper).text()).toContain('keys.groupRequired')

      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      expect(groupField(wrapper).text()).not.toContain('keys.groupRequired')

      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()
      expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 2 of 2')
      wrapper.unmount()
    })

    it('每次被拦下都会重放一次抖动（连续点两次也再提示一次）', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()
      expect(nameField(wrapper).classes()).toContain('key-field-nudge')

      // 动画靠「先摘后挂」重放：第二次点击期间类名必须真的被摘掉过一次，浏览器才会重新播放。
      const seen: boolean[] = []
      const el = nameField(wrapper).element
      const observer = new MutationObserver(() => seen.push(el.classList.contains('key-field-nudge')))
      observer.observe(el, { attributes: true, attributeFilter: ['class'] })
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()
      observer.disconnect()
      expect(seen).toContain(false)
      expect(nameField(wrapper).classes()).toContain('key-field-nudge')
      wrapper.unmount()
    })

    it('分组已选、名称没填：只标红名称，留在第一步（名称检查不能被分组掩盖）', async () => {
      const wrapper = await openCreate()
      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()

      expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 1 of 2')
      expect(nameField(wrapper).text()).toContain('keys.nameRequired')
      expect(groupField(wrapper).text()).not.toContain('keys.groupRequired')
      wrapper.unmount()
    })

    it('智能路由开着、一条路由都没加：同样算没选分组', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-tour="key-form-name"]').setValue('smart')
      await wrapper.get('[data-test="key-smart-routing"]').trigger('click')
      await nextTick()
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()

      expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 1 of 2')
      expect(groupField(wrapper).text()).toContain('keys.groupRequired')
      expect(createKey).not.toHaveBeenCalled()
      wrapper.unmount()
    })

    it('智能路由下以「路由列表」为准：加了一条再删掉，列表空了就算没选分组', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-tour="key-form-name"]').setValue('smart')
      await wrapper.get('[data-test="key-smart-routing"]').trigger('click')
      await nextTick()
      wrapper.getComponent('[data-test="smart-route-add"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      // 删掉唯一的主路由（真实按钮）：路由列表回到空。
      await wrapper.get('[data-test="smart-route-primary-remove"]').trigger('click')
      await nextTick()
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()

      expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 1 of 2')
      expect(groupField(wrapper).text()).toContain('keys.groupRequired')
      expect(createKey).not.toHaveBeenCalled()
      wrapper.unmount()
    })

    it('不经过 closeModals 直接再点「创建密钥」：旧的红色提示同样被清掉', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()
      expect(nameField(wrapper).text()).toContain('keys.nameRequired')

      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      await flushPromises()
      expect(nameField(wrapper).text()).not.toContain('keys.nameRequired')
      expect(groupField(wrapper).text()).not.toContain('keys.groupRequired')
      wrapper.unmount()
    })

    it('分组选择框带上红色错误状态（不只是下面的文字）', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()
      expect(wrapper.getComponent('[data-tour="key-form-group"]').props('error')).toBe(true)

      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      expect(wrapper.getComponent('[data-tour="key-form-group"]').props('error')).toBe(false)
      wrapper.unmount()
    })

    it('关掉再重新打开：上一次的红色提示不会留着', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()
      expect(nameField(wrapper).text()).toContain('keys.nameRequired')

      // 关闭后（弹窗已卸掉内容之前的那一刻）状态就必须已清空：不依赖「下次打开时再清」。
      await wrapper.get('[data-test="key-create-cancel"]').trigger('click')
      const afterClose = wrapper.vm as unknown as { attemptedSubmit?: boolean }
      expect(afterClose.attemptedSubmit).toBeFalsy()

      await flushPromises()
      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      await flushPromises()
      expect(nameField(wrapper).text()).not.toContain('keys.nameRequired')
      expect(groupField(wrapper).text()).not.toContain('keys.groupRequired')
      wrapper.unmount()
    })

    it('不需要二次校验的账号：直接点「创建」同样被拦下并标红，不会发请求', async () => {
      getVerificationFactors.mockResolvedValue({
        password_available: true, totp_enabled: false, passkey_count: 0, preferred: 'password',
        available_methods: ['password'], seal_available: true, create_verification_required: false,
      })
      const wrapper = await openCreate()
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()
      expect(createKey).not.toHaveBeenCalled()
      expect(nameField(wrapper).text()).toContain('keys.nameRequired')
      expect(groupField(wrapper).text()).toContain('keys.groupRequired')
      wrapper.unmount()
    })
  })

  it('第二步：步骤头走到 2/2，说明换成身份校验，页脚变成「上一步 + 创建」，且能退回第一步', async () => {
    const wrapper = await openCreate()
    await wrapper.get('[data-tour="key-form-name"]').setValue('quiet key')
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
    await nextTick()

    await wrapper.get('[data-test="key-create-next"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 2 of 2')
    expect(dialogDescription(wrapper)).toBe('Verify your identity once before creating a key.')
    expect(wrapper.find('[data-test="key-create-verification"]').exists()).toBe(true)
    // 说明在标题栏，组件自己的那一句不再重复。
    expect(wrapper.get('[data-test="key-create-verification"]').text()).not.toContain('Verify your identity once.')
    expect(wrapper.find('[data-test="key-create-back"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="key-create-cancel"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="key-create-next"]').exists()).toBe(false)
    expect(wrapper.find('[data-tour="key-form-submit"]').exists()).toBe(true)

    await wrapper.get('[data-test="key-create-back"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 1 of 2')
    expect(wrapper.find('[data-test="key-create-next"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('密码框交给浏览器密码管理器：current-password + 隐藏的用户名', async () => {
    const wrapper = await openCreate()
    await wrapper.get('[data-tour="key-form-name"]').setValue('autofill key')
    wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
    await nextTick()
    await wrapper.get('[data-test="key-create-next"]').trigger('click')
    await flushPromises()

    const password = wrapper.get('[data-test="key-factor-password-input"]')
    expect(password.attributes('autocomplete')).toBe('current-password')
    expect(password.attributes('data-lpignore')).toBeUndefined()
    const username = wrapper.get('[data-test="key-factor-username"]')
    expect((username.element as HTMLInputElement).value).toBe('user@example.com')
    wrapper.unmount()
  })

  it('账号没有任何可用因子：不显示步骤头，也只有一个「创建」按钮', async () => {
    getVerificationFactors.mockResolvedValue({
      password_available: false,
      totp_enabled: false,
      passkey_count: 0,
      preferred: '',
      available_methods: [],
      seal_available: false,
    })
    const wrapper = await openCreate()

    expect(wrapper.find('[data-test="key-create-steps"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="key-create-next"]').exists()).toBe(false)
    expect(wrapper.find('[data-tour="key-form-submit"]').exists()).toBe(true)
    expect(dialogDescription(wrapper)).toBe('Name the key and pick a route to create it.')
    wrapper.unmount()
  })

  it('编辑保持原样：所有设置展开、没有「更多设置」、没有步骤头', async () => {
    const wrapper = await mountView()
    const vm = wrapper.vm as unknown as { showEditModal: boolean }
    vm.showEditModal = true
    await nextTick()

    expect(wrapper.find('[data-test="key-advanced-toggle"]').exists()).toBe(false)
    expect(advancedHidden(wrapper)).toBe(false)
    expect(wrapper.find('[data-test="key-create-steps"]').exists()).toBe(false)
    expect(dialogDescription(wrapper)).toBeNull()
    expect(wrapper.find('[data-test="key-create-footer"]').exists()).toBe(false)
    expect(wrapper.find('[data-tour="key-form-submit"]').exists()).toBe(true)
    wrapper.unmount()
  })

  describe('创建不再强制登录密码', () => {
    // 新服务端：只有登录密码（含用 Google / GitHub 登录的用户）→ 不强制；开了 TOTP / Passkey → 强制。
    const passwordOnly = { ...passwordFactors, create_verification_required: false }
    const totpAccount = {
      password_available: true,
      totp_enabled: true,
      passkey_count: 0,
      preferred: 'totp',
      available_methods: ['totp', 'password'],
      seal_available: true,
      create_verification_required: true,
    }

    const fillAndSubmit = async (wrapper: VueWrapper) => {
      await wrapper.get('[data-tour="key-form-name"]').setValue('no password key')
      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()
    }
    const lastCreateOptions = () => (createKey.mock.calls.at(-1)?.at(-1) ?? {}) as Record<string, unknown>

    it('只有登录密码的账号：没有步骤头、没有「下一步」、没有密码框，填完直接创建且不带 verification', async () => {
      getVerificationFactors.mockResolvedValue(passwordOnly)
      createKey.mockResolvedValue({ ...createApiKey(), id: 77, group_id: 3, group_ids: [3], key: 'sk-direct' })
      const wrapper = await openCreate()

      expect(wrapper.find('[data-test="key-create-steps"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="key-create-next"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="key-factor-password-input"]').exists()).toBe(false)
      expect(dialogDescription(wrapper)).toBe('Name the key and pick a route to create it.')

      await fillAndSubmit(wrapper)

      expect(createKey).toHaveBeenCalledTimes(1)
      expect(lastCreateOptions()).not.toHaveProperty('verification')
      expect(wrapper.find('[data-test="key-factor-password-input"]').exists()).toBe(false)
      wrapper.unmount()
    })

    it('开了 TOTP 的账号：仍然分两步，第一步不会直接创建，第二步默认走 TOTP 而不是强制密码', async () => {
      getVerificationFactors.mockResolvedValue(totpAccount)
      const wrapper = await openCreate()
      expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 1 of 2')

      await wrapper.get('[data-tour="key-form-name"]').setValue('2fa key')
      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      await wrapper.get('[data-test="key-create-next"]').trigger('click')
      await flushPromises()

      expect(createKey).not.toHaveBeenCalled()
      expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 2 of 2')
      expect(wrapper.find('[data-test="key-factor-totp-input"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="key-factor-password-input"]').exists()).toBe(false)
      wrapper.unmount()
    })

    it('因子接口失败：不把人拦在输密码这一步，直接单步创建', async () => {
      getVerificationFactors.mockRejectedValue(new Error('network'))
      createKey.mockResolvedValue({ ...createApiKey(), id: 78, group_id: 3, group_ids: [3], key: 'sk-fallback' })
      const wrapper = await openCreate()

      expect(wrapper.find('[data-test="key-create-steps"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="key-create-next"]').exists()).toBe(false)
      await fillAndSubmit(wrapper)

      expect(createKey).toHaveBeenCalledTimes(1)
      expect(lastCreateOptions()).not.toHaveProperty('verification')
      wrapper.unmount()
    })

    it('因子还在加载时也不闪出步骤头（绝大多数账号根本没有校验这一步）', async () => {
      getVerificationFactors.mockReturnValue(new Promise(() => {}))
      const wrapper = await openCreate()

      expect(wrapper.find('[data-test="key-create-steps"]').exists()).toBe(false)
      wrapper.unmount()
    })

    describe('创建后回执里的「以后可查看」', () => {
      const seal = { version: 1, kdf: { algo: 'PBKDF2-SHA256', iterations: 310000, salt: 'c2FsdA==' }, sealed: 'c2VhbGVk' }

      beforeEach(() => {
        sealApiKey.mockReset()
        sealApiKey.mockResolvedValue(seal)
        putSecretSeal.mockReset()
        putSecretSeal.mockResolvedValue(undefined)
      })

      const fillName = async (wrapper: VueWrapper, name: string) => {
        await wrapper.get('[data-tour="key-form-name"]').setValue(name)
        wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
        await nextTick()
      }
      const finishReceipt = async (wrapper: VueWrapper) => {
        await new Promise((resolve) => setTimeout(resolve, 1100))
        await nextTick()
        await wrapper.get('[data-test="api-key-secret-done"]').trigger('click')
        await flushPromises()
      }

      it('创建时输过登录密码（要校验的账号选了密码）：开关一点即可，不再要第二遍密码', async () => {
        getVerificationFactors.mockResolvedValue({ ...passwordFactors, create_verification_required: true })
        createKey.mockResolvedValue({ ...createApiKey(), id: 77, group_id: 3, group_ids: [3], key: 'sk-typed-once-key' })
        const wrapper = await openCreate()
        await fillName(wrapper, 'typed once')
        await wrapper.get('[data-test="key-create-next"]').trigger('click')
        await flushPromises()
        await wrapper.get('[data-test="key-factor-password-input"]').setValue('typed-once')
        await wrapper.get('#key-form').trigger('submit')
        await flushPromises()

        expect(createKey).toHaveBeenCalledTimes(1)
        expect(lastCreateOptions()).toMatchObject({ verification: { method: 'password', password: 'typed-once' } })

        expect(wrapper.get('[data-test="api-key-seal-checkbox"]').attributes('aria-checked')).toBe('true')
        expect(wrapper.find('[data-test="api-key-seal-password"]').exists()).toBe(false)

        await finishReceipt(wrapper)
        expect(sealApiKey).toHaveBeenCalledWith('sk-typed-once-key', 'typed-once')
        expect(putSecretSeal).toHaveBeenCalledWith(77, seal, { password: 'typed-once', authFactor: 'password' })
        // Memory only: not in the page, not in the browser's storage.
        expect(wrapper.html()).not.toContain('typed-once')
        expect(readBrowserStorage()).not.toContain('typed-once')
        wrapper.unmount()
      })

      it('创建时没输过密码（只有登录密码的账号）：回执默认开启「以后可查看」并展示密码框，也可关闭跳过', async () => {
        getVerificationFactors.mockResolvedValue({ ...passwordFactors, create_verification_required: false })
        createKey.mockResolvedValue({ ...createApiKey(), id: 78, group_id: 3, group_ids: [3], key: 'sk-no-password-key' })
        const wrapper = await openCreate()
        await fillName(wrapper, 'no password')
        await wrapper.get('#key-form').trigger('submit')
        await flushPromises()
        expect(lastCreateOptions()).not.toHaveProperty('verification')

        // Default ON: switch is checked and password field is immediately ready.
        expect(wrapper.get('[data-test="api-key-seal-checkbox"]').attributes('aria-checked')).toBe('true')
        const field = wrapper.get('[data-test="api-key-seal-password"]')
        await field.setValue('typed-here')

        await finishReceipt(wrapper)
        expect(sealApiKey).toHaveBeenCalledWith('sk-no-password-key', 'typed-here')
        expect(putSecretSeal).toHaveBeenCalledWith(78, seal, { password: 'typed-here', authFactor: 'password' })
        wrapper.unmount()
      })

      it('回执一关，记住的登录密码就从内存里清掉（不等下一把 Key 来覆盖）', async () => {
        getVerificationFactors.mockResolvedValue({ ...passwordFactors, create_verification_required: true })
        createKey.mockResolvedValue({ ...createApiKey(), id: 81, group_id: 3, group_ids: [3], key: 'sk-clear-key' })
        const wrapper = await openCreate()
        await fillName(wrapper, 'clear me')
        await wrapper.get('[data-test="key-create-next"]').trigger('click')
        await flushPromises()
        await wrapper.get('[data-test="key-factor-password-input"]').setValue('typed-once')
        await wrapper.get('#key-form').trigger('submit')
        await flushPromises()

        const held = () => (wrapper.vm as unknown as { receiptVerifiedPassword: string }).receiptVerifiedPassword
        expect(held()).toBe('typed-once')

        await finishReceipt(wrapper)
        expect(held()).toBe('')
        wrapper.unmount()
      })

      it('上一把 Key 回执里记住的密码不会带到下一把 Key', async () => {
        getVerificationFactors.mockResolvedValue({ ...passwordFactors, create_verification_required: true })
        createKey.mockResolvedValueOnce({ ...createApiKey(), id: 79, group_id: 3, group_ids: [3], key: 'sk-first-key' })
        const wrapper = await openCreate()
        await fillName(wrapper, 'first')
        await wrapper.get('[data-test="key-create-next"]').trigger('click')
        await flushPromises()
        await wrapper.get('[data-test="key-factor-password-input"]').setValue('typed-once')
        await wrapper.get('#key-form').trigger('submit')
        await flushPromises()
        await finishReceipt(wrapper)

        // Second key, this time created by an account that is not asked (server says so): nothing is remembered.
        getVerificationFactors.mockResolvedValue({ ...passwordFactors, create_verification_required: false })
        createKey.mockResolvedValueOnce({ ...createApiKey(), id: 80, group_id: 3, group_ids: [3], key: 'sk-second-key' })
        await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
        await flushPromises()
        await fillName(wrapper, 'second')
        await wrapper.get('#key-form').trigger('submit')
        await flushPromises()

        expect(wrapper.get('[data-test="api-key-seal-checkbox"]').attributes('aria-checked')).toBe('true')
        expect(wrapper.find('[data-test="api-key-seal-password"]').exists()).toBe(true)
        wrapper.unmount()
      })
    })

    it('开了 TOTP、但用户在个人资料里把「创建时需要验证身份」关掉：没有步骤头、不弹校验，也不带 verification 字段', async () => {
      // 服务端算好的有效值是 false（模式 off），前端只看它；available_methods 里仍然有 totp。
      getVerificationFactors.mockResolvedValue({
        ...totpAccount,
        create_verification_required: false,
        create_verification_mode: 'off',
        create_verification_configurable: true,
      })
      createKey.mockResolvedValue({ ...createApiKey(), id: 78, group_id: 3, group_ids: [3], key: 'sk-two-factor-off' })
      const wrapper = await openCreate()

      expect(wrapper.find('[data-test="key-create-steps"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="key-create-next"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="key-factor-totp-input"]').exists()).toBe(false)

      await fillAndSubmit(wrapper)

      expect(createKey).toHaveBeenCalledTimes(1)
      expect(lastCreateOptions()).not.toHaveProperty('verification')
      expect(wrapper.find('[data-test="key-factor-totp-input"]').exists()).toBe(false)
      wrapper.unmount()
    })

    it('老服务端（响应里没有这个字段）：仍按「有可用因子就强制」，密码账号继续走两步', async () => {
      getVerificationFactors.mockResolvedValue(passwordFactors)
      const wrapper = await openCreate()
      expect(wrapper.get('[data-test="key-create-steps"]').text()).toContain('Step 1 of 2')
      expect(wrapper.find('[data-test="key-create-next"]').exists()).toBe(true)
      wrapper.unmount()
    })
  })

  describe('页面是旧版本', () => {
    it('已确认过期：点「创建」不再打开弹窗，改弹必须刷新的对话框', async () => {
      appUpdate.updateAvailable.value = true
      const wrapper = await mountView()
      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      await flushPromises()

      expect(wrapper.find('#key-form').exists()).toBe(false)
      expect(appUpdate.blockingOpen.value).toBe(true)
      expect(getVerificationFactors).not.toHaveBeenCalled()
      wrapper.unmount()
    })

    it('弹窗已经打开时后台实时检查发现过期：收起创建弹窗，换成必须刷新的对话框', async () => {
      ensureFreshSpy = vi.spyOn(appUpdate, 'ensureFresh').mockImplementation(async () => {
        appUpdate.blockingOpen.value = true
        return false
      })
      const wrapper = await openCreate()

      expect(wrapper.find('#key-form').exists()).toBe(false)
      expect(appUpdate.blockingOpen.value).toBe(true)
      wrapper.unmount()
    })

    it('页面是最新的：照常打开，不弹刷新对话框', async () => {
      const ensureFresh = vi.spyOn(appUpdate, 'ensureFresh').mockResolvedValue(true)
      ensureFreshSpy = ensureFresh
      const wrapper = await openCreate()

      expect(ensureFresh).toHaveBeenCalledTimes(1)
      expect(wrapper.find('#key-form').exists()).toBe(true)
      expect(appUpdate.blockingOpen.value).toBe(false)
      wrapper.unmount()
    })

    it('填表期间页面被判定过期：提交不再发请求，改弹必须刷新的对话框', async () => {
      const wrapper = await openCreate()
      await wrapper.get('[data-tour="key-form-name"]').setValue('stale key')
      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()

      appUpdate.updateAvailable.value = true
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()

      expect(createKey).not.toHaveBeenCalled()
      expect(appUpdate.blockingOpen.value).toBe(true)
      wrapper.unmount()
    })
  })

  describe('quick create query entry (?action=create)', () => {
    const mountWithRouter = async (initialPath: string) => {
      const router = createRouter({
        history: createMemoryHistory(),
        routes: [{ path: '/keys', component: KeysView }],
      })
      await router.push(initialPath)
      await router.isReady()
      const wrapper = mount(KeysView, {
        global: {
          plugins: [router],
          stubs: baseStubs,
        },
      })
      await flushPromises()
      await nextTick()
      return { wrapper, router }
    }

    it('opens the create modal automatically when entering /keys?action=create and cleans only the action param', async () => {
      const { wrapper, router } = await mountWithRouter('/keys?action=create&source=docs')
      expect(wrapper.find('#key-form').exists()).toBe(true)
      expect(router.currentRoute.value.query.action).toBeUndefined()
      expect(router.currentRoute.value.query.source).toBe('docs')
      wrapper.unmount()
    })

    it('opens the create modal when navigating to /keys?action=create while already mounted', async () => {
      const { wrapper, router } = await mountWithRouter('/keys')
      expect(wrapper.find('#key-form').exists()).toBe(false)

      await router.push('/keys?action=create')
      await flushPromises()
      await nextTick()

      expect(wrapper.find('#key-form').exists()).toBe(true)
      expect(router.currentRoute.value.query.action).toBeUndefined()
      wrapper.unmount()
    })

    it('ignores unrelated action query values', async () => {
      const { wrapper, router } = await mountWithRouter('/keys?action=inspect')
      expect(wrapper.find('#key-form').exists()).toBe(false)
      expect(router.currentRoute.value.query.action).toBe('inspect')
      wrapper.unmount()
    })
  })
})

describe('floating dropdown clamping, scroll tracking, and reduced-motion polish', () => {
  beforeEach(() => {
    localStorage.clear()
    listKeys.mockReset()
    createKey.mockReset()
    rotateKey.mockReset()
    getPublicSettings.mockReset()
    getDashboardApiKeysUsage.mockReset()
    getAvailableGroups.mockReset()
    getUserGroupRates.mockReset()
    getKeyRotationGateStatus.mockReset()
    getVerificationFactors.mockReset()

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getPublicSettings.mockResolvedValue({})
    getDashboardApiKeysUsage.mockResolvedValue({ stats: {} })
    getAvailableGroups.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
    getKeyRotationGateStatus.mockResolvedValue({ required: false, items: [] })
    getVerificationFactors.mockResolvedValue({
      password_available: false,
      totp_enabled: false,
      passkey_count: 0,
      preferred: '',
      available_methods: [],
      seal_available: false,
    })
  })

  it('clamps column dropdown height, flips origin-bottom near viewport bottom, and tracks scroll', async () => {
    const originalInnerWidth = window.innerWidth
    const originalInnerHeight = window.innerHeight
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 667 })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 375 })

    const { Teleport: _Teleport, ...stubs } = baseStubs
    const wrapper = await mountViewWithStubs(stubs)
    try {
      const triggerBtn = wrapper.get('button[title="Column Settings"]')
      const triggerWrap = triggerBtn.element.parentElement!
      let topPos = 315
      vi.spyOn(triggerWrap, 'getBoundingClientRect').mockImplementation(() => ({
        x: 580,
        y: topPos,
        top: topPos,
        bottom: topPos + 40,
        left: 580,
        right: 624,
        width: 44,
        height: 40,
        toJSON: () => ({}),
      }))

      await triggerBtn.trigger('click')
      await nextTick()

      const el = document.body.querySelector('[data-test="keys-column-dropdown"]') as HTMLElement
      expect(el).not.toBeNull()
      expect(el.classList.contains('origin-bottom')).toBe(true)
      expect(el.classList.contains('origin-top')).toBe(false)
      expect(Number.parseFloat(el.style.bottom)).toBe(64)
      expect(Number.parseFloat(el.style.maxHeight)).toBe(303)

      topPos = 40
      window.dispatchEvent(new Event('scroll'))
      await nextTick()

      expect(el.classList.contains('origin-top')).toBe(true)
      expect(el.classList.contains('origin-bottom')).toBe(false)
      expect(Number.parseFloat(el.style.top)).toBe(84)
      expect(Number.parseFloat(el.style.maxHeight)).toBe(283)
    } finally {
      wrapper.unmount()
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: originalInnerWidth })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: originalInnerHeight })
    }
  })

  it('clamps group selector height and flips origin-bottom on short viewports', async () => {
    const originalInnerWidth = window.innerWidth
    const originalInnerHeight = window.innerHeight
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 320 })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 568 })

    const wrapper = await mountView()
    try {
      const cell = wrapper.get('[data-group-cell]')
      vi.spyOn(cell.element, 'getBoundingClientRect').mockReturnValue({
        x: 24,
        y: 500,
        top: 500,
        bottom: 536,
        left: 24,
        right: 160,
        width: 136,
        height: 36,
        toJSON: () => ({}),
      } as DOMRect)

      await cell.trigger('click')
      await nextTick()

      const dropdown = wrapper.get('[data-test="group-selector-dropdown"]')
      expect(dropdown.classes()).toContain('origin-bottom')
      expect(dropdown.classes()).not.toContain('origin-top')
      const el = dropdown.element as HTMLElement
      expect(Number.parseFloat(el.style.bottom)).toBe(72)
      expect(Number.parseFloat(el.style.maxHeight)).toBe(488)
    } finally {
      wrapper.unmount()
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: originalInnerWidth })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: originalInnerHeight })
    }
  })

  it('gates rotation spinners with motion-reduce:animate-none and adds coarse target + focus ring to CCS model reload', async () => {
    let resolveRotate!: (value: ApiKey) => void
    rotateKey.mockImplementation(() => new Promise<ApiKey>((resolve) => { resolveRotate = resolve }))
    getAvailableGroups.mockResolvedValue([{ id: 3, name: 'Claude', platform: 'anthropic' }])
    createKey.mockResolvedValue({ ...createApiKey(), id: 92, group_id: 3, group_ids: [3], key: 'test-key-must-not-leak' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: [] }) }))
    listKeys.mockResolvedValue({
      items: [{ ...createApiKey(), id: 11, needs_rotation: true, rotation_reason: 'shared_ip' }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const wrapper = await mountView()
    try {
      const rotateBtn = wrapper.get('[data-test="key-needs-rotation-rotate"]')
      await rotateBtn.trigger('click')
      await nextTick()
      expect(rotateBtn.html()).toContain('animate-spin')
      expect(rotateBtn.html()).toContain('motion-reduce:animate-none')
      resolveRotate({ ...createApiKey(), id: 11 })
      await flushPromises()

      await wrapper.get('[data-tour="keys-create-btn"]').trigger('click')
      await wrapper.get('[data-tour="key-form-name"]').setValue('ccs reload key')
      wrapper.getComponent('[data-tour="key-form-group"]').vm.$emit('update:modelValue', 3)
      await nextTick()
      await wrapper.get('#key-form').trigger('submit')
      await flushPromises()
      await getButtonByText(wrapper, 'keys.secretDialog.importCcs').trigger('click')
      await flushPromises()

      const reloadBtn = wrapper.get('[data-test="ccs-import-model-reload"]')
      expect(reloadBtn.classes()).toEqual(expect.arrayContaining([
        'min-h-11',
        'px-1',
        'rounded',
        '[@media(pointer:fine)]:min-h-0',
        'focus-visible:outline-2',
        'focus-visible:outline-offset-2',
        'focus-visible:outline-primary',
      ]))
    } finally {
      wrapper.unmount()
      vi.unstubAllGlobals()
    }
  })
})
