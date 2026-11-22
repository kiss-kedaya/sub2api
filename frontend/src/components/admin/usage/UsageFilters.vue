<template>
  <div :class="flat ? 'p-4 sm:p-6' : 'card p-6'">
    <!-- 筛选字段：桌面 3 列 / 平板 2 列 / 手机 1 列。
         编辑只写本地草稿（v-model 对象），不触发查询；点「查询」才把草稿交给父级生效。 -->
    <div class="usage-filter-grid grid grid-cols-1 gap-x-4 gap-y-4 sm:grid-cols-2 xl:grid-cols-3" data-testid="usage-filter-grid">
      <div class="usage-filter-field flex min-w-0 flex-col text-left" data-testid="usage-date-range-filter">
        <label class="input-label">{{ t('admin.dashboard.timeRange') }}</label>
        <DateRangePicker
          ref="dateRangeRef"
          include-time
          class="w-full"
          :start-date="startDate"
          :end-date="endDate"
          @change="onDateRangeChange"
        />
      </div>
      <div v-if="!observer" class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('admin.usage.requestId') }}</label>
        <input
          v-model="filters.request_id"
          type="text"
          class="input"
          data-testid="usage-request-id"
          :placeholder="t('usage.requestIdPlaceholder')"
          @keyup.enter="emitQuery"
        />
      </div>
      <template v-if="observer">
        <div v-for="filter in observerIdFilters" :key="filter.key" class="usage-filter-field flex min-w-0 flex-col">
          <label :for="`observer-${filter.key}`" class="input-label">{{ t(filter.label) }} ID</label>
          <input
            :id="`observer-${filter.key}`"
            v-model.number="filters[filter.key]"
            type="number"
            min="1"
            step="1"
            class="input"
            @keyup.enter="emitQuery"
          />
        </div>
      </template>
      <!-- User Search -->
      <div v-if="!observer" ref="userSearchRef" class="usage-filter-field usage-filter-dropdown flex min-w-0 flex-col">
        <label class="input-label">{{ t('admin.usage.userFilter') }}</label>
        <div class="relative min-w-0">
          <input
            v-model="userKeyword"
            type="text"
            class="input pr-8"
            :placeholder="t('admin.usage.searchUserPlaceholder')"
            @input="debounceUserSearch"
            @focus="showUserDropdown = true"
            @keyup.enter="emitQuery"
          />
          <button
            v-if="filters.user_id"
            type="button"
            @click="clearUser"
            class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400"
            aria-label="Clear user filter"
          >
            ✕
          </button>
          <div
            v-if="showUserDropdown && (userResults.length > 0 || userKeyword)"
            class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-lg border bg-white shadow-lg dark:bg-dark-800"
          >
            <button
              v-for="u in userResults"
              :key="u.id"
              type="button"
              @click="selectUser(u)"
              class="w-full px-4 py-2 text-left hover:bg-gray-100 dark:hover:bg-dark-700"
            >
              <span>{{ u.email }}<span v-if="u.deleted" class="ml-1 text-xs text-gray-400">（{{ t('admin.usage.userDeletedBadge') }}）</span></span>
              <span class="ml-2 text-xs text-gray-400">#{{ u.id }}</span>
            </button>
          </div>
        </div>
      </div>

      <!-- API Key Search -->
      <div v-if="!observer" ref="apiKeySearchRef" class="usage-filter-field usage-filter-dropdown flex min-w-0 flex-col">
        <label class="input-label">{{ t('usage.apiKeyFilter') }}</label>
        <div class="relative min-w-0">
          <input
            v-model="apiKeyKeyword"
            type="text"
            class="input pr-8"
            :placeholder="t('admin.usage.searchApiKeyPlaceholder')"
            @input="debounceApiKeySearch"
            @focus="onApiKeyFocus"
            @keyup.enter="emitQuery"
          />
          <button
            v-if="filters.api_key_id"
            type="button"
            @click="clearApiKey"
            class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400"
            aria-label="Clear API key filter"
          >
            ✕
          </button>
          <div
            v-if="showApiKeyDropdown && apiKeyResults.length > 0"
            class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-lg border bg-white shadow-lg dark:bg-dark-800"
          >
            <button
              v-for="k in apiKeyResults"
              :key="k.id"
              type="button"
              @click="selectApiKey(k)"
              class="w-full px-4 py-2 text-left hover:bg-gray-100 dark:hover:bg-dark-700"
            >
              <span class="truncate">{{ k.name || `#${k.id}` }}</span>
              <span class="ml-2 text-xs text-gray-400">#{{ k.id }}</span>
            </button>
          </div>
        </div>
      </div>

      <!-- Model Filter -->
      <div class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('usage.model') }}</label>
        <Select v-model="filters.model" :options="modelOptions" searchable class="w-full" />
      </div>

      <!-- Account Filter -->
      <div v-if="!observer" ref="accountSearchRef" class="usage-filter-field usage-filter-dropdown flex min-w-0 flex-col">
        <label class="input-label">{{ t('admin.usage.account') }}</label>
        <div class="relative min-w-0">
          <input
            v-model="accountKeyword"
            type="text"
            class="input pr-8"
            :placeholder="t('admin.usage.searchAccountPlaceholder')"
            @input="debounceAccountSearch"
            @focus="showAccountDropdown = true"
            @keyup.enter="emitQuery"
          />
          <button
            v-if="filters.account_id"
            type="button"
            @click="clearAccount"
            class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400"
            aria-label="Clear account filter"
          >
            ✕
          </button>
          <div
            v-if="showAccountDropdown && (accountResults.length > 0 || accountKeyword)"
            class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-lg border bg-white shadow-lg dark:bg-dark-800"
          >
            <button
              v-for="a in accountResults"
              :key="a.id"
              type="button"
              @click="selectAccount(a)"
              class="w-full px-4 py-2 text-left hover:bg-gray-100 dark:hover:bg-dark-700"
            >
              <span class="truncate">{{ a.name }}</span>
              <span class="ml-2 text-xs text-gray-400">#{{ a.id }}</span>
            </button>
          </div>
        </div>
      </div>

      <!-- Request Type Filter (usage only) -->
      <div v-if="mode !== 'errors'" class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('usage.type') }}</label>
        <Select v-model="filters.request_type" :options="requestTypeOptions" class="w-full" />
      </div>

      <!-- Native compaction is independent of the transport request type. -->
      <div v-if="mode !== 'errors'" class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('usage.compactionFilter') }}</label>
        <Select v-model="filters.native_compaction_v2" :options="compactionOptions" class="w-full" />
      </div>

      <!-- Billing Type Filter (usage only) -->
      <div v-if="mode !== 'errors'" class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('admin.usage.billingType') }}</label>
        <Select v-model="filters.billing_type" :options="billingTypeOptions" class="w-full" />
      </div>

      <!-- Billing Mode Filter (usage only；用户排行的 user-breakdown 接口不支持该维度) -->
      <div v-if="mode === 'usage'" class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('admin.usage.billingMode') }}</label>
        <Select v-model="filters.billing_mode" :options="billingModeOptions" class="w-full" />
      </div>

      <div v-if="mode === 'usage'" class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('admin.usage.upstreamModelAudit') }}</label>
        <Select v-model="filters.upstream_model_mismatch" :options="upstreamModelMismatchOptions" class="w-full" />
      </div>

      <!-- Error Phase Filter (errors only) -->
      <div v-if="mode === 'errors'" class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('admin.ops.errorLog.type') }}</label>
        <Select v-model="filters.error_phase" :options="errorPhaseOptions" class="w-full" />
      </div>

      <!-- Error Category Filter (errors only) -->
      <div v-if="mode === 'errors'" class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('usage.errors.category') }}</label>
        <Select v-model="filters.error_category" :options="errorCategoryOptions" class="w-full" />
      </div>

      <!-- Status Code Filter (errors only) -->
      <div v-if="mode === 'errors'" class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('admin.ops.errorLog.status') }}</label>
        <Select v-model="filters.status_code" :options="statusCodeOptions" class="w-full" />
      </div>

      <!-- Group Filter -->
      <div v-if="!observer" class="usage-filter-field flex min-w-0 flex-col">
        <label class="input-label">{{ t('admin.usage.group') }}</label>
        <Select v-model="filters.group_id" :options="groupOptions" searchable class="w-full" />
      </div>
    </div>

    <!-- 操作栏独立一行，靠右；查询为主按钮，导出/刷新等为次按钮 -->
    <div
      v-if="showActions"
      class="usage-filter-actions mt-4 flex flex-wrap items-center justify-end gap-2 border-t border-gray-200 pt-4 dark:border-dark-700"
      data-testid="usage-filter-actions"
    >
      <button type="button" data-testid="usage-filter-query" class="btn btn-primary" @click="emitQuery">
        {{ t('common.query') }}
      </button>
      <button type="button" data-testid="usage-filter-reset" class="btn btn-secondary" @click="$emit('reset')">
        {{ t('common.reset') }}
      </button>
      <button type="button" data-testid="usage-filter-refresh" class="btn btn-secondary" @click="$emit('refresh')">
        {{ t('common.refresh') }}
      </button>
      <slot name="after-reset" />
      <template v-if="mode === 'usage' && !observer">
        <button type="button" data-testid="usage-filter-cleanup" @click="$emit('cleanup')" class="btn btn-danger">
          {{ t('admin.usage.cleanup.button') }}
        </button>
        <button type="button" data-testid="usage-filter-export-excel" @click="$emit('export')" :disabled="exporting" class="btn btn-secondary">
          {{ t('usage.exportExcel') }}
        </button>
        <button type="button" data-testid="usage-filter-export-csv" @click="$emit('export-csv')" :disabled="exporting" class="btn btn-secondary">
          {{ t('usage.exportCsv') }}
        </button>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, toRef, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { fetchPaginatedItems } from '@/utils/paginatedItems'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import { COMMON_ERROR_STATUS_CODES } from '@/utils/errorBadges'
import type { SimpleApiKey, SimpleUser } from '@/api/admin/usage'

type ModelValue = Record<string, any>

interface Props {
  modelValue: ModelValue
  exporting: boolean
  startDate: string
  endDate: string
  showActions?: boolean
  modelOptions?: string[]
  /**
   * errors 模式:隐藏用量专属字段/按钮,显示错误类型+状态码(错误请求 tab 用)
   * ranking 模式:同 usage 但隐藏计费模式筛选与清理/导出按钮(用户排行 tab 用)
   */
  mode?: 'usage' | 'errors' | 'ranking'
  /** 嵌入统一卡片内使用：去掉自身卡片外观 */
  flat?: boolean
  observer?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  showActions: true,
  mode: 'usage',
  flat: false,
  observer: false
})
// 手动查询：编辑只改草稿（modelValue），不发请求；'query' 由父级应用草稿并查询。
const emit = defineEmits([
  'update:modelValue',
  'update:startDate',
  'update:endDate',
  'date-range-change',
  'query',
  'refresh',
  'reset',
  'export',
  'export-csv',
  'cleanup'
])

const observerIdFilters = [
  { key: 'user_id', label: 'admin.usage.user' },
  { key: 'api_key_id', label: 'usage.apiKeyFilter' },
  { key: 'account_id', label: 'admin.usage.account' },
  { key: 'group_id', label: 'admin.usage.group' },
]

const { t } = useI18n()
const filters = toRef(props, 'modelValue')
const onDateRangeChange = (range: { startDate: string; endDate: string; preset: string | null }) => {
  emit('update:startDate', range.startDate)
  emit('update:endDate', range.endDate)
  emit('date-range-change', range)
}

const userSearchRef = ref<HTMLElement | null>(null)
const apiKeySearchRef = ref<HTMLElement | null>(null)
const accountSearchRef = ref<HTMLElement | null>(null)
const dateRangeRef = ref<InstanceType<typeof DateRangePicker> | null>(null)

const userKeyword = ref('')
const userResults = ref<SimpleUser[]>([])
const showUserDropdown = ref(false)
let userSearchTimeout: ReturnType<typeof setTimeout> | null = null
let userSearchSequence = 0

const apiKeyKeyword = ref('')
const apiKeyResults = ref<SimpleApiKey[]>([])
const showApiKeyDropdown = ref(false)
let apiKeySearchTimeout: ReturnType<typeof setTimeout> | null = null

interface SimpleAccount {
  id: number
  name: string
}
const accountKeyword = ref('')
const accountResults = ref<SimpleAccount[]>([])
const showAccountDropdown = ref(false)
let accountSearchTimeout: ReturnType<typeof setTimeout> | null = null

const modelOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allModels') },
  ...(props.modelOptions ?? []).map((m) => ({ value: m, label: m })),
])
const groupOptions = ref<SelectOption[]>([{ value: null, label: t('admin.usage.allGroups') }])

const requestTypeOptions = ref<SelectOption[]>([
  { value: null, label: t('admin.usage.allTypes') },
  { value: 'ws_v2', label: t('usage.ws') },
  { value: 'live', label: t('usage.live') },
  { value: 'stream', label: t('usage.stream') },
  { value: 'sync', label: t('usage.sync') },
  { value: 'cyber', label: t('usage.cyber') }
])

const compactionOptions = ref<SelectOption[]>([
  { value: null, label: t('usage.allCompactionTypes') },
  { value: true, label: t('usage.compactionOnly') }
])

const billingTypeOptions = ref<SelectOption[]>([
  { value: null, label: t('admin.usage.allBillingTypes') },
  { value: 0, label: t('admin.usage.billingTypeBalance') },
  { value: 1, label: t('admin.usage.billingTypeSubscription') }
])

// 错误类型对应后端 phase 参数(与错误表"类型"徽章同语义)
const errorPhaseOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allTypes') },
  { value: 'upstream', label: t('admin.ops.errorLog.typeUpstream') },
  { value: 'account_auth', label: t('admin.ops.errorLog.typeAccountAuth') },
  { value: 'request', label: t('admin.ops.errorLog.typeRequest') },
  { value: 'auth', label: t('admin.ops.errorLog.typeAuth') },
  { value: 'routing', label: t('admin.ops.errorLog.typeRouting') },
  { value: 'internal', label: t('admin.ops.errorLog.typeInternal') },
])

// 分类码同用户端 /usage 错误筛选;"other" 无法反查为过滤条件,刻意不列
const errorCategoryCodes = ['auth', 'rate_limit', 'quota', 'invalid_request', 'model_not_found', 'service_unavailable', 'upstream', 'internal', 'cyber']

const errorCategoryOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.errors.allCategories') },
  ...errorCategoryCodes.map((c) => ({ value: c, label: t('usage.errors.categories.' + c) })),
])

const statusCodeOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.errors.allStatuses') },
  ...COMMON_ERROR_STATUS_CODES.map((c) => ({ value: c, label: String(c) })),
])

const billingModeOptions = ref<SelectOption[]>([
  { value: null, label: t('admin.usage.allBillingModes') },
  { value: 'token', label: t('admin.usage.billingModeToken') },
  { value: 'per_request', label: t('admin.usage.billingModePerRequest') },
  { value: 'image', label: t('admin.usage.billingModeImage') },
  { value: 'video', label: t('admin.usage.billingModeVideo') }
])

const upstreamModelMismatchOptions = ref<SelectOption[]>([
  { value: null, label: t('admin.usage.allUpstreamModelAudit') },
  { value: true, label: t('admin.usage.upstreamModelMismatchOnly') },
  { value: false, label: t('admin.usage.upstreamModelMatchedOnly') }
])

// 点「查询」或在筛选输入框内回车：把草稿交给父级应用（父级负责重置分页并只发一次请求）。
const emitQuery = () => emit('query')

const clearPendingUserSearch = () => {
  if (userSearchTimeout) {
    clearTimeout(userSearchTimeout)
    userSearchTimeout = null
  }
  userSearchSequence += 1
}

const debounceUserSearch = () => {
  if (props.observer) return
  clearPendingUserSearch()
  const query = userKeyword.value.trim()
  if (!query) {
    userResults.value = []
    return
  }

  const sequence = userSearchSequence
  userSearchTimeout = setTimeout(async () => {
    userSearchTimeout = null
    try {
      const results = await adminAPI.usage.searchUsers(query)
      if (sequence === userSearchSequence) {
        userResults.value = results.sort((a, b) => Number(a.deleted) - Number(b.deleted))
      }
    } catch {
      if (sequence === userSearchSequence) {
        userResults.value = []
      }
    }
  }, 300)
}

const debounceApiKeySearch = () => {
  if (props.observer) return
  if (apiKeySearchTimeout) clearTimeout(apiKeySearchTimeout)
  apiKeySearchTimeout = setTimeout(async () => {
    try {
      apiKeyResults.value = await adminAPI.usage.searchApiKeys(
        filters.value.user_id,
        apiKeyKeyword.value || ''
      )
    } catch {
      apiKeyResults.value = []
    }
  }, 300)
}

const selectUser = async (u: SimpleUser) => {
  clearPendingUserSearch()
  userKeyword.value = u.email
  showUserDropdown.value = false
  filters.value.user_id = u.id
  clearApiKey()

  // Auto-load API keys for this user
  try {
    apiKeyResults.value = await adminAPI.usage.searchApiKeys(u.id, '')
  } catch {
    apiKeyResults.value = []
  }
}

const clearUser = () => {
  clearPendingUserSearch()
  userKeyword.value = ''
  userResults.value = []
  showUserDropdown.value = false
  filters.value.user_id = undefined
  clearApiKey()
}

const selectApiKey = (k: SimpleApiKey) => {
  apiKeyKeyword.value = k.name || String(k.id)
  showApiKeyDropdown.value = false
  filters.value.api_key_id = k.id
}

const clearApiKey = () => {
  apiKeyKeyword.value = ''
  apiKeyResults.value = []
  showApiKeyDropdown.value = false
  filters.value.api_key_id = undefined
}

const debounceAccountSearch = () => {
  if (props.observer) return
  if (accountSearchTimeout) clearTimeout(accountSearchTimeout)
  accountSearchTimeout = setTimeout(async () => {
    if (!accountKeyword.value) {
      accountResults.value = []
      return
    }
    try {
      const res = await adminAPI.accounts.list(1, 20, { search: accountKeyword.value })
      accountResults.value = res.items.map((a) => ({ id: a.id, name: a.name }))
    } catch {
      accountResults.value = []
    }
  }, 300)
}

const selectAccount = (a: SimpleAccount) => {
  accountKeyword.value = a.name
  showAccountDropdown.value = false
  filters.value.account_id = a.id
}

const clearAccount = () => {
  accountKeyword.value = ''
  accountResults.value = []
  showAccountDropdown.value = false
  filters.value.account_id = undefined
}

const onApiKeyFocus = () => {
  showApiKeyDropdown.value = true
  // Trigger search if no results yet
  if (apiKeyResults.value.length === 0) {
    debounceApiKeySearch()
  }
}

const onDocumentClick = (e: MouseEvent) => {
  const target = e.target as Node | null
  if (!target) return

  const clickedInsideUser = userSearchRef.value?.contains(target) ?? false
  const clickedInsideApiKey = apiKeySearchRef.value?.contains(target) ?? false
  const clickedInsideAccount = accountSearchRef.value?.contains(target) ?? false

  if (!clickedInsideUser) showUserDropdown.value = false
  if (!clickedInsideApiKey) showApiKeyDropdown.value = false
  if (!clickedInsideAccount) showAccountDropdown.value = false
}

watch(
  () => props.startDate,
  (value) => {
    filters.value.start_date = value
  },
  { immediate: true }
)

watch(
  () => props.endDate,
  (value) => {
    filters.value.end_date = value
  },
  { immediate: true }
)

watch(
  () => filters.value.user_id,
  (userId) => {
    if (!userId) {
      clearPendingUserSearch()
      userKeyword.value = ''
      userResults.value = []
    }
  }
)

watch(
  () => filters.value.api_key_id,
  (apiKeyId) => {
    if (!apiKeyId) {
      apiKeyKeyword.value = ''
      apiKeyResults.value = []
    }
  }
)

watch(
  () => filters.value.account_id,
  (accountId) => {
    if (!accountId) {
      accountKeyword.value = ''
      accountResults.value = []
    }
  }
)

onMounted(async () => {
  document.addEventListener('click', onDocumentClick)
  if (props.observer) return
  try {
    const groups = await fetchPaginatedItems(
      (page, size) => adminAPI.groups.list(page, size), 1000, 1000
    )
    groupOptions.value.push(...groups.map((g) => ({ value: g.id, label: g.name })))
  } catch {
    // Ignore filter option loading errors (page still usable)
  }
})

onUnmounted(() => {
  clearPendingUserSearch()
  document.removeEventListener('click', onDocumentClick)
})

// 供外部(如用户排行下钻)在程序化设置 user_id 后回显选中的用户邮箱
const setUserKeyword = (email: string) => {
  clearPendingUserSearch()
  userKeyword.value = email
  userResults.value = []
  showUserDropdown.value = false
}

const getUserSearchRevision = () => userSearchSequence

// refreshActivePreset re-applies the active quick time-range preset (近24小时
// etc.) so the refresh button slides a relative window to "now". Returns null
// for a custom range — custom windows stay put.
const refreshActivePreset = (): { startDate: string; endDate: string } | null =>
  dateRangeRef.value?.refreshActivePreset?.() ?? null

defineExpose({ getUserSearchRevision, setUserKeyword, refreshActivePreset })
</script>

<style scoped>
/* 同一行的输入框 / 下拉框 / 日期范围触发器统一高度，避免控件参差。 */
.usage-filter-grid {
  --usage-filter-control-height: 2.625rem;
}

.usage-filter-field :deep(.input),
.usage-filter-field :deep([data-slot='base']),
.usage-filter-field :deep([data-test='date-range-trigger']) {
  min-height: var(--usage-filter-control-height);
}

@media (max-width: 639px) {
  .usage-filter-actions {
    justify-content: stretch;
  }

  .usage-filter-actions > * {
    flex: 1 1 auto;
  }
}
</style>
