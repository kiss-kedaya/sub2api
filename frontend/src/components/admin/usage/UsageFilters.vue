<template>
  <div
    class="usage-filters"
    :class="flat ? 'px-4 py-4 sm:px-6 sm:py-5' : 'card p-4 sm:p-5'"
    data-testid="usage-filters"
  >
    <UCard
      :ui="{ root: 'ring-0 bg-transparent', body: showActions ? 'p-0' : '' }"
      data-testid="usage-filters-card"
    >
      <!-- 标题栏：标题 + 已生效筛选数 + 高级筛选开关（Nuxt UI 折叠） -->
      <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div class="flex min-w-0 items-center gap-2">
          <h3 class="text-sm font-semibold text-highlighted">{{ t('admin.usage.filters.title') }}</h3>
          <UBadge
            v-if="activeFilterCount > 0"
            :label="t('admin.usage.filters.activeCount', { count: activeFilterCount })"
            color="primary"
            variant="soft"
            size="sm"
            data-testid="usage-filters-active-count"
          />
        </div>
        <UButton
          :label="t('admin.usage.filters.advanced')"
          color="neutral"
          variant="ghost"
          size="sm"
          trailing-icon="i-lucide-chevron-down"
          :aria-expanded="advancedOpen"
          data-testid="usage-filters-advanced-toggle"
          @click="toggleAdvanced"
        >
          <UBadge
            v-if="advancedFilterCount > 0"
            :label="String(advancedFilterCount)"
            color="neutral"
            variant="subtle"
            size="sm"
            data-testid="usage-filters-advanced-count"
          />
          <template #trailing>
            <UIcon
              name="i-lucide-chevron-down"
              :class="['size-4', ICON_TRANSITION, advancedOpen ? 'rotate-180' : '']"
            />
          </template>
        </UButton>
      </div>

      <!-- 常用筛选：时间范围 / 用户 / API 密钥 / 模型（桌面 4 列、平板 2 列、手机 1 列） -->
      <div class="mt-3 grid gap-x-3 gap-y-3 sm:grid-cols-2 xl:grid-cols-4" data-testid="usage-filters-common">
        <UFormField
          v-bind="{ 'data-testid': 'usage-filters-field', 'data-field': 'date' }"
          :label="t('admin.dashboard.timeRange')"
          class="min-w-0"
        >
          <DateRangePicker
            ref="dateRangeRef"
            class="w-full min-w-0"
            include-time
            :start-date="startDate"
            :end-date="endDate"
            @change="onDateRangeChange"
          />
        </UFormField>

        <!-- 观察者只能按匿名 ID 过滤，不加载用户/密钥名单 -->
        <template v-if="observer">
          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
            v-for="filter in observerIdFilters"
            :key="filter.key"
            :label="t(filter.label)"
            class="min-w-0"
          >
            <UInput
              :id="`observer-${filter.key}`"
              v-model.number="filters[filter.key]"
              type="number"
              min="1"
              step="1"
              @keyup.enter="emitQuery"
              class="w-full"
              :placeholder="t('admin.usage.filters.anonymousIdPlaceholder')"
            />
          </UFormField>
        </template>
        <template v-else>
          <!-- 用户：远程搜索 + 清除 -->
          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
            :label="t('admin.usage.userFilter')"
            class="min-w-0"
            :ui="{ container: 'relative' }"
          >
            <UInput
              v-model="userKeyword"
              icon="i-lucide-user"
              :loading="userSearching"
              :placeholder="t('admin.usage.searchUserPlaceholder')"
              class="w-full"
              :aria-label="t('admin.usage.userFilter')"
              @change="debounceUserSearch"
              @focus="showUserDropdown = true"
            >
              <template v-if="filters.user_id" #trailing>
                <UButton
                  icon="i-lucide-x"
                  color="neutral"
                  variant="link"
                  size="xs"
                  :padded="false"
                  :aria-label="t('admin.usage.filters.clearUser')"
                  data-testid="usage-filter-clear-user"
                  @click="clearUser"
                />
              </template>
            </UInput>
            <div
              v-if="showUserDropdown && userResults.length > 0"
              class="absolute inset-x-0 top-full z-50 mt-1 max-h-60 overflow-auto rounded-md border border-default bg-default py-1 shadow-lg"
              data-testid="usage-filter-user-options"
            >
              <button
                v-for="u in userResults"
                :key="u.id"
                type="button"
                class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm text-default hover:bg-elevated"
                @click="selectUser(u)"
              >
                <span class="min-w-0 truncate">{{ u.email }}</span>
                <UBadge
                  v-if="u.deleted"
                  :label="t('admin.usage.userDeletedBadge')"
                  color="neutral"
                  variant="subtle"
                  size="sm"
                />
                <span class="ml-auto text-xs text-dimmed">#{{ u.id }}</span>
              </button>
            </div>
          </UFormField>

          <!-- API 密钥：选中用户后只列出该用户的密钥 -->
          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
            :label="t('usage.apiKeyFilter')"
            class="min-w-0"
            :ui="{ container: 'relative' }"
          >
            <UInput
              v-model="apiKeyKeyword"
              icon="i-lucide-key-round"
              :loading="apiKeySearching"
              :placeholder="t('admin.usage.searchApiKeyPlaceholder')"
              class="w-full"
              :aria-label="t('usage.apiKeyFilter')"
              @change="debounceApiKeySearch"
              @focus="onApiKeyFocus"
            >
              <template v-if="filters.api_key_id" #trailing>
                <UButton
                  icon="i-lucide-x"
                  color="neutral"
                  variant="link"
                  size="xs"
                  :padded="false"
                  :aria-label="t('admin.usage.filters.clearApiKey')"
                  data-testid="usage-filter-clear-api-key"
                  @click="onClearApiKey"
                />
              </template>
            </UInput>
            <div
              v-if="showApiKeyDropdown && apiKeyResults.length > 0"
              class="absolute inset-x-0 top-full z-50 mt-1 max-h-60 overflow-auto rounded-md border border-default bg-default py-1 shadow-lg"
              data-testid="usage-filter-api-key-options"
            >
              <button
                v-for="k in apiKeyResults"
                :key="k.id"
                type="button"
                class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm text-default hover:bg-elevated"
                @click="selectApiKey(k)"
              >
                <span class="min-w-0 truncate">{{ k.name || `#${k.id}` }}</span>
                <span class="ml-auto text-xs text-dimmed">#{{ k.id }}</span>
              </button>
            </div>
          </UFormField>
        </template>

        <!-- 模型 -->
        <UFormField
          v-bind="{ 'data-testid': FIELD_TESTID }"
          :label="t('usage.model')"
          class="min-w-0"
        >
          <Select v-model="filters.model" :options="modelOptions" searchable @change="emitQuery" />
        </UFormField>
      </div>

      <!-- 高级筛选：按 请求 / 计费 / 范围 分组，桌面 3 列、平板 2 列、手机 1 列 -->
      <UCollapsible v-model:open="advancedOpen" :unmount-on-hide="false">
        <!-- 折叠区内容：桌面/平板展开，手机默认收起（外层只是一个容器，内层才是可折叠区） -->
        <div data-testid="usage-filters-advanced-collapsible">
        <div v-show="advancedOpen">
          <div
            class="mt-4 grid gap-x-5 gap-y-4 border-t border-default pt-4 sm:grid-cols-2 xl:grid-cols-3"
            data-testid="usage-filters-advanced"
          >
            <!-- 请求 -->
            <fieldset class="min-w-0 space-y-3" data-testid="usage-filters-group-request">
              <legend class="text-xs font-semibold uppercase tracking-wide text-dimmed">
                {{ t('admin.usage.filters.groupRequest') }}
              </legend>

          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
                v-if="!observer"
                :label="t('admin.usage.requestId')"
                :description="t('admin.usage.filters.requestIdHint')"
                class="min-w-0"
              >
                <UInput
                  v-model="filters.request_id"
                  icon="i-lucide-hash"
                  class="w-full"
                  :placeholder="t('usage.requestIdPlaceholder')"
                  @keyup.enter="emitQuery"
                />
              </UFormField>

          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
                v-if="mode !== 'errors'"
                :label="t('usage.type')"
                class="min-w-0"
              >
                <Select v-model="filters.request_type" :options="requestTypeOptions" @change="emitQuery" />
              </UFormField>

          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
                v-if="mode !== 'errors'"
                :label="t('usage.compactionFilter')"
                :description="t('admin.usage.filters.compactionHint')"
                class="min-w-0"
              >
                <Select v-model="filters.native_compaction_v2" :options="compactionOptions" @change="emitQuery" />
              </UFormField>

              <template v-if="mode === 'errors'">
          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
            :label="t('admin.ops.errorLog.type')"
            class="min-w-0"
          >
            <Select v-model="filters.error_phase" :options="errorPhaseOptions" @change="emitQuery" />
                </UFormField>
          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
            :label="t('usage.errors.category')"
            class="min-w-0"
          >
            <Select v-model="filters.error_category" :options="errorCategoryOptions" @change="emitQuery" />
                </UFormField>
          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
            :label="t('admin.ops.errorLog.status')"
            class="min-w-0"
          >
            <Select v-model="filters.status_code" :options="statusCodeOptions" @change="emitQuery" />
                </UFormField>
              </template>
            </fieldset>

            <!-- 计费 -->
            <fieldset
              v-if="mode !== 'errors'"
              class="min-w-0 space-y-3"
              data-testid="usage-filters-group-billing"
            >
              <legend class="text-xs font-semibold uppercase tracking-wide text-dimmed">
                {{ t('admin.usage.filters.groupBilling') }}
              </legend>

              <UFormField
                v-bind="{ 'data-testid': FIELD_TESTID }"
                :label="t('admin.usage.billingType')"
                class="min-w-0"
              >
                <Select v-model="filters.billing_type" :options="billingTypeOptions" @change="emitQuery" />
              </UFormField>

              <!-- 用户排行的 user-breakdown 接口不支持计费模式维度 -->
              <UFormField
                v-bind="{ 'data-testid': FIELD_TESTID }"
                v-if="mode === 'usage'"
                :label="t('admin.usage.billingMode')"
                class="min-w-0"
              >
                <Select v-model="filters.billing_mode" :options="billingModeOptions" @change="emitQuery" />
              </UFormField>

              <UFormField
                v-bind="{ 'data-testid': FIELD_TESTID }"
                v-if="mode === 'usage'"
                :label="t('admin.usage.upstreamModelAudit')"
                :description="t('admin.usage.filters.upstreamModelAuditHint')"
                class="min-w-0"
              >
                <Select v-model="filters.upstream_model_mismatch" :options="upstreamModelMismatchOptions" @change="emitQuery" />
              </UFormField>
            </fieldset>

            <!-- 范围 -->
            <fieldset class="min-w-0 space-y-3" data-testid="usage-filters-group-scope">
              <legend class="text-xs font-semibold uppercase tracking-wide text-dimmed">
                {{ t('admin.usage.filters.groupScope') }}
              </legend>

          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
            v-if="!observer"
            :label="t('admin.usage.account')"
            class="min-w-0"
            :ui="{ container: 'relative' }"
          >
            <UInput
                  v-model="accountKeyword"
                  icon="i-lucide-server"
                  :loading="accountSearching"
                  :placeholder="t('admin.usage.searchAccountPlaceholder')"
                  class="w-full"
                  :aria-label="t('admin.usage.account')"
                  @change="debounceAccountSearch"
                  @focus="showAccountDropdown = true"
                >
                  <template v-if="filters.account_id" #trailing>
                    <UButton
                      icon="i-lucide-x"
                      color="neutral"
                      variant="link"
                      size="xs"
                      :padded="false"
                      :aria-label="t('admin.usage.filters.clearAccount')"
                      data-testid="usage-filter-clear-account"
                      @click="clearAccount"
                    />
                  </template>
                </UInput>
                <div
                  v-if="showAccountDropdown && accountResults.length > 0"
                  class="absolute inset-x-0 top-full z-50 mt-1 max-h-60 overflow-auto rounded-md border border-default bg-default py-1 shadow-lg"
                  data-testid="usage-filter-account-options"
                >
                  <button
                    v-for="a in accountResults"
                    :key="a.id"
                    type="button"
                    class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm text-default hover:bg-elevated"
                    @click="selectAccount(a)"
                  >
                    <span class="min-w-0 truncate">{{ a.name }}</span>
                    <span class="ml-auto text-xs text-dimmed">#{{ a.id }}</span>
                  </button>
                </div>
              </UFormField>

          <UFormField
            v-bind="{ 'data-testid': FIELD_TESTID }"
            v-if="!observer"
            :label="t('admin.usage.group')"
            class="min-w-0"
          >
            <!-- 分组菜单复用 GroupOptionItem（平台徽章 + 描述），和其它页面的分组选择器一致 -->
            <Select v-model="filters.group_id" :options="groupOptions" searchable @change="emitQuery">
              <template v-if="groupOptionMeta.size" #option="{ option, selected }">
                <GroupOptionItem
                  v-if="groupMetaOf(option)"
                  :name="String(option?.label ?? '')"
                  :platform="groupMetaOf(option)!.platform"
                  :subscription-type="groupMetaOf(option)!.subscriptionType ?? 'standard'"
                  :description="groupMetaOf(option)!.description ?? null"
                  :selected="selected"
                />
                <span v-else class="truncate text-sm">{{ option?.label }}</span>
              </template>
            </Select>
          </UFormField>
        </fieldset>
          </div>
          <slot name="after-advanced" />
        </div>
        </div>
      </UCollapsible>

      <!-- 操作条：主操作 查询；次 重置/刷新；更多（列设置/导出）；危险 清理 独立右侧 -->
      <div
        v-if="showActions"
        class="mt-4 flex flex-wrap items-center gap-2 border-t border-default pt-3"
        data-testid="usage-filters-actions"
      >
        <UButton
          icon="i-lucide-search"
          :label="t('admin.usage.filters.search')"
          data-testid="usage-filters-search"
          @click="emitQuery"
        />
        <UButton
          color="neutral"
          variant="outline"
          icon="i-lucide-rotate-ccw"
          :label="t('common.reset')"
          data-testid="usage-filters-reset"
          @click="$emit('reset')"
        />
        <UButton
          color="neutral"
          variant="outline"
          icon="i-lucide-refresh-cw"
          :label="t('common.refresh')"
          data-testid="usage-filters-refresh"
          @click="$emit('refresh')"
        />
        <slot name="after-reset" />
        <span data-testid="usage-filters-more">
        <UDropdownMenu :items="moreMenuItems" :content="{ align: 'start' }">
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-ellipsis"
            :label="t('common.more')"
          />
        </UDropdownMenu>
        </span>

        <div v-if="mode === 'usage' && !observer" class="ml-auto flex items-center gap-2">
          <UButton
            color="error"
            variant="subtle"
            icon="i-lucide-trash-2"
            :label="t('admin.usage.cleanup.button')"
            data-testid="usage-filters-cleanup"
            @click="confirmCleanupOpen = true"
          />
        </div>
      </div>
    </UCard>

    <!-- 清理二次确认：确认后才打开原有的清理对话框，行为与改前一致 -->
    <UModal
      v-model:open="confirmCleanupOpen"
      :title="t('admin.usage.cleanup.confirmTitle')"
      :description="t('admin.usage.cleanup.warning')"
      :ui="{ footer: 'justify-end gap-2' }"
      data-testid="usage-filters-cleanup-confirm"
    >
      <template #body>
        <p class="text-sm text-muted">{{ t('admin.usage.cleanup.confirmMessage') }}</p>
      </template>
      <template #footer>
        <UButton
          color="neutral"
          variant="outline"
          :label="t('common.cancel')"
          data-testid="usage-filters-cleanup-cancel"
          @click="confirmCleanupOpen = false"
        />
        <UButton
          color="error"
          icon="i-lucide-trash-2"
          :label="t('admin.usage.cleanup.confirmSubmit')"
          data-testid="usage-filters-cleanup-confirm-button"
          @click="onConfirmCleanup"
        />
      </template>
    </UModal>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, toRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem } from '@nuxt/ui'
import { adminAPI } from '@/api/admin'
import { fetchPaginatedItems } from '@/utils/paginatedItems'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
import { COMMON_ERROR_STATUS_CODES } from '@/utils/errorBadges'
import type { SimpleApiKey, SimpleUser } from '@/api/admin/usage'
import type { GroupPlatform, SubscriptionType } from '@/types'

type ModelValue = Record<string, any>

interface Props {
  modelValue: ModelValue
  exporting: boolean
  startDate: string
  endDate: string
  showActions?: boolean
  modelOptions?: string[]
  /**
   * errors 模式：隐藏用量专属字段/按钮，显示错误类型+状态码（错误请求 tab 用）
   * ranking 模式：同 usage 但隐藏计费模式筛选与清理/导出按钮（用户排行 tab 用）
   */
  mode?: 'usage' | 'errors' | 'ranking'
  /** 嵌入统一卡片内使用：去掉自身卡片外观 */
  flat?: boolean
  observer?: boolean
  /** 列设置菜单项（由页面注入，本组件只负责放到「更多」菜单里） */
  columnMenuItems?: DropdownMenuItem[]
}

const props = withDefaults(defineProps<Props>(), {
  showActions: true,
  mode: 'usage',
  flat: false,
  observer: false,
  columnMenuItems: () => []
})
const emit = defineEmits([
  'update:modelValue',
  'update:startDate',
  'update:endDate',
  'date-range-change',
  'change',
  'query',
  'refresh',
  'reset',
  'export',
  'export-csv',
  'cleanup'
])

const ICON_TRANSITION = 'transition-transform duration-200 data-[state=open]:rotate-180'
// 每个筛选项都是一个 UFormField，字段标记用 v-bind 透传到它的根节点（测试与样式都靠它找字段）。
const FIELD_TESTID = 'usage-filters-field'

const observerIdFilters = [
  { key: 'user_id', label: 'admin.usage.user' },
  { key: 'api_key_id', label: 'usage.apiKeyFilter' },
  { key: 'account_id', label: 'admin.usage.account' },
  { key: 'group_id', label: 'admin.usage.group' },
]

const { t } = useI18n()
const filters = toRef(props, 'modelValue')
/**
 * 筛选条件只在手动提交时生效：点「查询」或输入框回车都走这里。
 * 下拉框改值不再自动查询（改前每改一个条件就立刻打一轮接口）。
 */
const emitQuery = () => emit('query')
const onDateRangeChange = (range: { startDate: string; endDate: string; preset: string | null }) => {
  emit('update:startDate', range.startDate)
  emit('update:endDate', range.endDate)
  emit('date-range-change', range)
}

/**
 * 高级筛选默认收起；窄屏（手机）下同样收起，且不随窗口变宽自动展开——
 * 只有用户自己点过才会展开。这样 12 个筛选器不会在小屏一进页面就铺满。
 */
const MIN_ADVANCED_WIDTH = 768
const initialAdvancedOpen = () =>
  typeof window === 'undefined' ? false : window.innerWidth >= MIN_ADVANCED_WIDTH
// 初值直接按视口宽算（不等 onMounted）：桌面/平板展开、手机收起，首帧就不会闪一下。
const advancedOpen = ref(initialAdvancedOpen())
const toggleAdvanced = () => {
  advancedOpen.value = !advancedOpen.value
}

// --- 已生效筛选计数（只统计有值的项，用于标题栏提示） ---
const activeFilterCount = computed(() => {
  const f = filters.value || {}
  let count = 0
  if (!props.observer) {
    if (f.request_id?.trim?.()) count += 1
    if (f.user_id != null && f.user_id !== '') count += 1
    if (f.api_key_id != null && f.api_key_id !== '') count += 1
    if (f.account_id != null && f.account_id !== '') count += 1
    if (f.group_id != null && f.group_id !== '') count += 1
  } else {
    for (const filter of observerIdFilters) {
      if (f[filter.key] != null && f[filter.key] !== '') count += 1
    }
  }
  if (f.model != null && f.model !== '') count += 1
  if (f.request_type != null) count += 1
  if (f.native_compaction_v2 != null) count += 1
  if (f.billing_type != null) count += 1
  if (f.billing_mode != null) count += 1
  if (f.upstream_model_mismatch != null) count += 1
  if (f.error_phase) count += 1
  if (f.error_category) count += 1
  if (f.status_code != null) count += 1
  return count
})

/** 高级区里生效的项（常用 4 项之外），显示在「高级筛选」按钮上。 */
const advancedFilterCount = computed(() => {
  const f = filters.value || {}
  let count = 0
  if (!props.observer && f.request_id?.trim?.()) count += 1
  if (!props.observer && f.account_id != null && f.account_id !== '') count += 1
  if (!props.observer && f.group_id != null && f.group_id !== '') count += 1
  if (f.request_type != null) count += 1
  if (f.native_compaction_v2 != null) count += 1
  if (f.billing_type != null) count += 1
  if (f.billing_mode != null) count += 1
  if (f.upstream_model_mismatch != null) count += 1
  if (f.error_phase) count += 1
  if (f.error_category) count += 1
  if (f.status_code != null) count += 1
  return count
})

const dateRangeRef = ref<InstanceType<typeof DateRangePicker> | null>(null)

// --- 用户搜索 ---
const userKeyword = ref('')
const userResults = ref<SimpleUser[]>([])
const showUserDropdown = ref(false)
const userSearching = ref(false)
let userSearchTimeout: ReturnType<typeof setTimeout> | null = null
let userSearchSequence = 0

// --- API 密钥搜索 ---
const apiKeyKeyword = ref('')
const apiKeyResults = ref<SimpleApiKey[]>([])
const showApiKeyDropdown = ref(false)
const apiKeySearching = ref(false)
let apiKeySearchTimeout: ReturnType<typeof setTimeout> | null = null

// --- 账户搜索 ---
interface SimpleAccount {
  id: number
  name: string
}
const accountKeyword = ref('')
const accountResults = ref<SimpleAccount[]>([])
const showAccountDropdown = ref(false)
const accountSearching = ref(false)
let accountSearchTimeout: ReturnType<typeof setTimeout> | null = null

// --- 清理二次确认 ---
const confirmCleanupOpen = ref(false)
const onConfirmCleanup = () => {
  confirmCleanupOpen.value = false
  emit('cleanup')
}

// --- 「更多」菜单：列设置 / 导出 Excel / 导出 CSV ---
const moreMenuItems = computed<DropdownMenuItem[][]>(() => {
  const groups: DropdownMenuItem[][] = []
  if (props.columnMenuItems.length > 0) {
    groups.push(props.columnMenuItems)
  }
  if (props.mode === 'usage' && !props.observer) {
    groups.push([
      {
        label: t('usage.exportExcel'),
        icon: 'i-lucide-file-spreadsheet',
        disabled: props.exporting,
        onSelect: () => emit('export')
      },
      {
        label: t('usage.exportCsv'),
        icon: 'i-lucide-file-down',
        disabled: props.exporting,
        onSelect: () => emit('export-csv')
      }
    ])
  }
  return groups
})

const modelOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allModels') },
  ...(props.modelOptions ?? []).map((m) => ({ value: m, label: m })),
])
const groupOptions = ref<SelectOption[]>([{ value: null, label: t('admin.usage.allGroups') }])
// 分组菜单里复用 GroupOptionItem（平台徽章 + 描述），只在拿到分组元数据时渲染
interface GroupOptionMeta {
  platform: GroupPlatform
  subscriptionType: SubscriptionType | null
  description: string | null
}
const groupOptionMeta = ref(new Map<number, GroupOptionMeta>())
const groupMetaOf = (option: SelectOption | null | undefined): GroupOptionMeta | null =>
  typeof option?.value === 'number' ? groupOptionMeta.value.get(option.value) ?? null : null

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

// 错误类型对应后端 phase 参数（与错误表「类型」徽章同语义）
const errorPhaseOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allTypes') },
  { value: 'upstream', label: t('admin.ops.errorLog.typeUpstream') },
  { value: 'account_auth', label: t('admin.ops.errorLog.typeAccountAuth') },
  { value: 'request', label: t('admin.ops.errorLog.typeRequest') },
  { value: 'auth', label: t('admin.ops.errorLog.typeAuth') },
  { value: 'routing', label: t('admin.ops.errorLog.typeRouting') },
  { value: 'internal', label: t('admin.ops.errorLog.typeInternal') },
])

// 分类码同用户端 /usage 错误筛选；"other" 无法反查为过滤条件，刻意不列
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
    userSearching.value = false
    return
  }

  const sequence = userSearchSequence
  userSearching.value = true
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
    } finally {
      if (sequence === userSearchSequence) userSearching.value = false
    }
  }, 300)
}

const debounceApiKeySearch = () => {
  if (props.observer) return
  if (apiKeySearchTimeout) clearTimeout(apiKeySearchTimeout)
  apiKeySearching.value = true
  apiKeySearchTimeout = setTimeout(async () => {
    try {
      apiKeyResults.value = await adminAPI.usage.searchApiKeys(
        filters.value.user_id,
        apiKeyKeyword.value || ''
      )
    } catch {
      apiKeyResults.value = []
    } finally {
      apiKeySearching.value = false
    }
  }, 300)
}

const selectUser = async (u: SimpleUser) => {
  clearPendingUserSearch()
  userKeyword.value = u.email
  showUserDropdown.value = false
  filters.value.user_id = u.id
  clearApiKey()

  // 选定用户后自动带出该用户的 API 密钥候选
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

const onClearApiKey = () => {
  clearApiKey()
}

const debounceAccountSearch = () => {
  if (props.observer) return
  if (accountSearchTimeout) clearTimeout(accountSearchTimeout)
  accountSearching.value = true
  accountSearchTimeout = setTimeout(async () => {
    if (!accountKeyword.value) {
      accountResults.value = []
      accountSearching.value = false
      return
    }
    try {
      const res = await adminAPI.accounts.list(1, 20, { search: accountKeyword.value })
      accountResults.value = res.items.map((a) => ({ id: a.id, name: a.name }))
    } catch {
      accountResults.value = []
    } finally {
      accountSearching.value = false
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
  // 还没有候选时先按当前关键字拉一次
  if (apiKeyResults.value.length === 0) {
    debounceApiKeySearch()
  }
}

onMounted(async () => {
  if (props.observer) return
  try {
    const groups = await fetchPaginatedItems(
      (page, size) => adminAPI.groups.list(page, size), 1000, 1000
    )
    groupOptions.value.push(...groups.map((g) => ({ value: g.id, label: g.name })))
    const meta = new Map<number, GroupOptionMeta>()
    for (const group of groups) {
      meta.set(group.id, {
        platform: (group as { platform?: GroupPlatform }).platform ?? 'anthropic',
        subscriptionType: (group as { subscription_type?: SubscriptionType }).subscription_type ?? null,
        description: (group as { description?: string | null }).description ?? null,
      })
    }
    groupOptionMeta.value = meta
  } catch {
    // 分组选项加载失败不影响页面继续使用
  }
})

onUnmounted(() => {
  clearPendingUserSearch()
  if (apiKeySearchTimeout) clearTimeout(apiKeySearchTimeout)
  if (accountSearchTimeout) clearTimeout(accountSearchTimeout)
})

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

// 供外部（如用户排行下钻）在程序化设置 user_id 后回显选中的用户邮箱
const setUserKeyword = (email: string) => {
  clearPendingUserSearch()
  userKeyword.value = email
  userResults.value = []
  showUserDropdown.value = false
}

const getUserSearchRevision = () => userSearchSequence

// refreshActivePreset re-applies the active quick time-range preset (近24小时
// 等) so the refresh button slides a relative window to "now". Returns null
// for a custom range — custom windows stay put.
const refreshActivePreset = (): { startDate: string; endDate: string } | null =>
  dateRangeRef.value?.refreshActivePreset?.() ?? null

defineExpose({ getUserSearchRevision, setUserKeyword, refreshActivePreset, advancedOpen })
</script>
