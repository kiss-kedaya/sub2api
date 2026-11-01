<template>
  <AppLayout>
    <TablePageLayout>
      <!-- Toolbar row (template customers page): rarer filters live in a popover -->
      <template #filters>
        <AdminPageHeader
          class="mb-4"
          :title="t('admin.audit.title')"
          :description="t('admin.audit.description')"
        />
        <PageToolbar>
          <template #left>
            <UInput
              v-model.trim="filters.q"
              icon="i-lucide-search"
              class="w-56 sm:w-64"
              :placeholder="t('admin.audit.filters.qPlaceholder')"
              :aria-label="t('admin.audit.filters.q')"
              @keyup.enter="search"
            />
            <div class="w-32">
              <Select v-model="filters.success" :options="resultOptions" :aria-label="t('admin.audit.filters.result')" @change="search" />
            </div>
            <div class="w-36">
              <Select :model-value="timeRange" :options="timeRangeOptions" :aria-label="t('admin.dashboard.timeRange')" @update:model-value="handleTimeRangeChange" />
            </div>
            <UPopover :content="{ align: 'start' }">
              <UButton color="neutral" variant="outline" icon="i-lucide-list-filter" :label="t('admin.audit.filters.more')">
                <template v-if="extraFilterCount > 0" #trailing>
                  <UBadge :label="String(extraFilterCount)" color="neutral" variant="outline" size="sm" />
                </template>
              </UButton>
              <template #content>
                <div class="w-72 space-y-3 p-4">
                  <UFormField :label="t('admin.audit.filters.actorEmail')">
                    <UInput v-model.trim="filters.actor_email" class="w-full" @keyup.enter="search" />
                  </UFormField>
                  <UFormField :label="t('admin.audit.filters.action')">
                    <UInput v-model.trim="filters.action" class="w-full" @keyup.enter="search" />
                  </UFormField>
                  <UFormField :label="t('admin.audit.filters.clientIp')">
                    <UInput v-model.trim="filters.client_ip" class="w-full" @keyup.enter="search" />
                  </UFormField>
                  <UFormField :label="t('admin.audit.filters.method')">
                    <Select v-model="filters.method" :options="methodOptions" @change="search" />
                  </UFormField>
                  <UFormField :label="t('admin.audit.filters.authMethod')">
                    <Select v-model="filters.auth_method" :options="authMethodOptions" @change="search" />
                  </UFormField>
                  <div class="flex justify-end gap-2 pt-1">
                    <UButton color="neutral" variant="ghost" size="sm" :disabled="loading" :label="t('common.reset')" @click="resetFilters" />
                    <UButton size="sm" :disabled="loading" :label="t('common.search')" @click="search" />
                  </div>
                </div>
              </template>
            </UPopover>
          </template>
          <template #right>
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-refresh-cw"
              square
              :disabled="loading"
              :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
              :title="t('common.search')"
              :aria-label="t('common.search')"
              @click="search"
            />
            <UButton color="error" variant="subtle" icon="i-lucide-trash" :label="t('admin.audit.clearAll')" @click="openClearDialog" />
            <UDropdownMenu :items="columnMenuItems" :content="{ align: 'end' }">
              <UButton color="neutral" variant="outline" trailing-icon="i-lucide-settings-2" :aria-label="t('admin.audit.displayColumns')">
                <span class="hidden sm:inline">{{ t('admin.audit.displayColumns') }}</span>
              </UButton>
            </UDropdownMenu>
          </template>
        </PageToolbar>
      </template>

      <template #table>
        <DataTable :columns="visibleColumns" :data="logs" :loading="loading" row-key="id">
          <template #cell-created_at="{ value }">
            <span class="whitespace-nowrap text-muted">{{ formatTime(value) }}</span>
          </template>
          <template #cell-actor="{ row }">
            <div class="min-w-0 max-w-[220px]">
              <p class="truncate font-medium text-highlighted" :title="row.actor_email">{{ row.actor_email || '—' }}</p>
              <p class="mt-0.5 truncate text-xs text-muted">
                {{ row.actor_role }}<span v-if="row.auth_method"> · {{ authMethodLabel(row.auth_method) }}</span>
              </p>
            </div>
          </template>
          <template #cell-action="{ row }">
            <div class="min-w-0 max-w-xs">
              <p class="truncate font-mono text-sm text-default" :title="row.action">{{ row.action }}</p>
              <p class="mt-0.5 truncate font-mono text-xs text-muted" :title="row.method + ' ' + row.path">{{ row.method }} {{ row.path }}</p>
            </div>
          </template>
          <template #cell-status_code="{ row }">
            <UBadge :color="statusColor(row.status_code)" variant="subtle" class="tabular-nums" :label="String(row.status_code)" />
          </template>
          <template #cell-latency_ms="{ value }">
            <span class="whitespace-nowrap tabular-nums text-muted">{{ value }} ms</span>
          </template>
          <template #cell-client_ip="{ value }">
            <span class="whitespace-nowrap font-mono text-default">{{ value || '—' }}</span>
          </template>
          <template #cell-actions="{ row }">
            <div class="flex justify-end">
              <AdminRowAction :label="t('admin.audit.columns.detail')" icon="eye" @click="openDetail(row.id)" />
            </div>
          </template>
          <template #empty>
            <div class="flex flex-col items-center py-8">
              <UIcon name="i-lucide-shield" class="mb-3 size-10 text-dimmed" />
              <p class="text-sm font-medium text-muted">{{ t('admin.audit.empty') }}</p>
            </div>
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="total > 0"
          :total="total"
          :page="page"
          :page-size="pageSize"
          @update:page="onPageChange"
          @update:pageSize="onPageSizeChange"
        />
      </template>
    </TablePageLayout>

    <!-- Detail dialog -->
    <BaseDialog
      :show="detailVisible"
      :title="t('admin.audit.detail.title')"
      width="wide"
      :close-on-click-outside="true"
      @close="detailVisible = false"
    >
      <div v-if="detailLoading" class="flex items-center justify-center gap-2 py-16 text-sm text-muted">
        <UIcon name="i-lucide-loader-circle" class="size-5 animate-spin" />
        {{ t('common.loading') }}
      </div>
      <div v-else-if="detail" class="space-y-5">
        <div class="space-y-2">
          <div class="flex flex-wrap items-center gap-2">
            <UBadge :color="statusColor(detail.status_code)" variant="subtle" :label="detail.status_code + ' ' + statusText(detail.status_code)" />
            <span class="break-all font-mono text-base font-semibold text-highlighted">{{ detail.action }}</span>
          </div>
          <p class="flex items-center gap-2 break-all font-mono text-xs text-default">
            <UBadge color="neutral" variant="outline" size="sm" :label="detail.method" />
            {{ detail.path }}
          </p>
          <p class="flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-muted">
            <span class="inline-flex items-center gap-1"><UIcon name="i-lucide-clock" class="size-3.5" />{{ formatTime(detail.created_at) }}</span>
            <span>{{ t('admin.audit.detail.latency') }} {{ detail.latency_ms }} ms</span>
            <span v-if="detail.request_id">{{ t('admin.audit.detail.requestId') }} <span class="break-all font-mono">{{ detail.request_id }}</span></span>
          </p>
        </div>
        <USeparator />
        <dl class="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div>
            <dt class="text-xs text-muted">{{ t('admin.audit.columns.actor') }}</dt>
            <dd class="mt-1 break-all text-sm font-medium text-highlighted">{{ detail.actor_email || '—' }}</dd>
            <dd class="text-xs text-muted">{{ detail.actor_role }}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted">{{ t('admin.audit.filters.authMethod') }}</dt>
            <dd class="mt-1 text-sm font-medium text-highlighted">{{ authMethodLabel(detail.auth_method) || '—' }}</dd>
            <dd v-if="detail.credential_masked" class="break-all font-mono text-xs text-muted">{{ detail.credential_masked }}</dd>
          </div>
          <div>
            <dt class="text-xs text-muted">{{ t('admin.audit.columns.clientIp') }}</dt>
            <dd class="mt-1 break-all font-mono text-sm font-medium text-highlighted">{{ detail.client_ip || '—' }}</dd>
          </div>
        </dl>
        <section>
          <h4 class="mb-1.5 text-xs text-muted">{{ t('admin.audit.detail.userAgent') }}</h4>
          <p class="break-all font-mono text-xs leading-relaxed text-default">{{ detail.user_agent || '—' }}</p>
        </section>
        <section v-if="detail.request_body">
          <h4 class="mb-1.5 text-xs text-muted">{{ t('admin.audit.detail.requestBody') }}</h4>
          <pre class="max-h-72 overflow-auto rounded-md bg-elevated p-3 font-mono text-xs leading-relaxed text-default">{{ prettyBody(detail.request_body) }}</pre>
        </section>
        <section v-if="detail.extra && Object.keys(detail.extra).length">
          <h4 class="mb-1.5 text-xs text-muted">{{ t('admin.audit.detail.extra') }}</h4>
          <pre class="max-h-48 overflow-auto rounded-md bg-elevated p-3 font-mono text-xs leading-relaxed text-default">{{ JSON.stringify(detail.extra, null, 2) }}</pre>
        </section>
      </div>
    </BaseDialog>

    <!-- Custom time range dialog (与 /admin/ops 时间下拉一致的自定义范围，支持时分) -->
    <BaseDialog :show="showCustomTimeRangeDialog" :title="t('admin.ops.timeRange.custom')" width="narrow" @close="handleCustomTimeRangeCancel">
      <div class="space-y-4">
        <UFormField :label="t('admin.ops.customTimeRange.startTime')">
          <UInput v-model="customStartTimeInput" type="datetime-local" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.ops.customTimeRange.endTime')">
          <UInput v-model="customEndTimeInput" type="datetime-local" class="w-full" />
        </UFormField>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="handleCustomTimeRangeCancel" />
          <UButton :disabled="!customStartTimeInput || !customEndTimeInput" :label="t('common.confirm')" @click="handleCustomTimeRangeConfirm" />
        </div>
      </template>
    </BaseDialog>

    <!-- Clear confirmation → step-up TOTP -->
    <ConfirmDialog
      :show="clearConfirmVisible"
      :title="t('admin.audit.clearConfirm.title')"
      :message="t('admin.audit.clearConfirm.message')"
      :confirm-text="t('admin.audit.clearAll')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="onClearConfirmed"
      @cancel="clearConfirmVisible = false"
    />

    <!-- TOTP prompt for the clear operation -->
    <BaseDialog :show="clearTotpVisible" :title="t('admin.audit.clearConfirm.totpTitle')" width="narrow" :z-index="60" @close="cancelClearTotp">
      <p class="text-sm text-muted">{{ t('admin.audit.clearConfirm.totpHint') }}</p>
      <UInput
        v-model.trim="clearTotpCode"
        inputmode="numeric"
        maxlength="6"
        autocomplete="one-time-code"
        placeholder="••••••"
        class="mt-4 w-full"
        :ui="{ base: 'text-center text-lg tracking-[0.5em]' }"
        @keyup.enter="submitClear"
      />
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :disabled="clearing" :label="t('common.cancel')" @click="cancelClearTotp" />
          <UButton
            color="error"
            :loading="clearing"
            :disabled="clearing || clearTotpCode.length !== 6"
            :label="clearing ? t('common.loading') : t('admin.audit.clearAll')"
            @click="submitClear"
          />
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI, type AuditLog } from '@/api/admin'
import { totpAPI } from '@/api'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import type { Column } from '@/components/common/types'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'
import { useListColumnMenu } from './useListColumnMenu'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import { useAppStore } from '@/stores'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const logs = ref<AuditLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)

const filters = reactive({
  q: '',
  actor_email: '',
  action: '',
  client_ip: '',
  method: '',
  auth_method: '',
  success: ''
})

// 时间范围：预设窗口（同 /admin/ops 时间下拉）+ 自定义起止（datetime-local，支持时分）
const timeRange = ref('')
const customStartTime = ref('')
const customEndTime = ref('')
const showCustomTimeRangeDialog = ref(false)
const customStartTimeInput = ref('')
const customEndTimeInput = ref('')

const TIME_RANGE_MINUTES: Record<string, number> = {
  '30m': 30,
  '1h': 60,
  '6h': 6 * 60,
  '24h': 24 * 60,
  '7d': 7 * 24 * 60,
  '30d': 30 * 24 * 60
}

const timeRangeOptions = computed(() => [
  { value: '', label: t('admin.audit.filters.all') },
  { value: '30m', label: t('admin.ops.timeRange.30m') },
  { value: '1h', label: t('admin.ops.timeRange.1h') },
  { value: '6h', label: t('admin.ops.timeRange.6h') },
  { value: '24h', label: t('admin.ops.timeRange.24h') },
  { value: '7d', label: t('admin.ops.timeRange.7d') },
  { value: '30d', label: t('admin.ops.timeRange.30d') },
  {
    value: 'custom',
    label:
      timeRange.value === 'custom' && customStartTime.value && customEndTime.value
        ? `${t('admin.ops.timeRange.custom')} (${formatCustomTimeRangeLabel(customStartTime.value, customEndTime.value)})`
        : t('admin.ops.timeRange.custom')
  }
])

function formatCustomTimeRangeLabel(startTime: string, endTime: string): string {
  const fmt = (raw: string) => {
    const d = new Date(raw)
    if (Number.isNaN(d.getTime())) return raw
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  }
  return `${fmt(startTime)} ~ ${fmt(endTime)}`
}

function toDatetimeLocal(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function handleTimeRangeChange(val: string | number | boolean | null) {
  const value = String(val ?? '')
  if (value === 'custom') {
    // 预填：已有自定义值沿用，否则默认最近1小时（本地时区）
    const now = new Date()
    customStartTimeInput.value = customStartTime.value || toDatetimeLocal(new Date(now.getTime() - 60 * 60 * 1000))
    customEndTimeInput.value = customEndTime.value || toDatetimeLocal(now)
    showCustomTimeRangeDialog.value = true
    return
  }
  timeRange.value = value
  search()
}

function handleCustomTimeRangeConfirm() {
  if (!customStartTimeInput.value || !customEndTimeInput.value) return
  customStartTime.value = customStartTimeInput.value
  customEndTime.value = customEndTimeInput.value
  timeRange.value = 'custom'
  showCustomTimeRangeDialog.value = false
  search()
}

function handleCustomTimeRangeCancel() {
  // 未确认不改变当前时间范围；Select 是受控组件，展示值保持不变。
  showCustomTimeRangeDialog.value = false
}

const allColumns = computed<Column[]>(() => [
  { key: 'created_at', label: t('admin.audit.columns.time') },
  { key: 'actor', label: t('admin.audit.columns.actor') },
  { key: 'action', label: t('admin.audit.columns.action') },
  { key: 'status_code', label: t('admin.audit.columns.result') },
  { key: 'latency_ms', label: t('admin.audit.detail.latency') },
  { key: 'client_ip', label: t('admin.audit.columns.clientIp') },
  { key: 'actions', label: t('common.actions') }
])

// "显示列": time and the detail action are fixed; everything starts visible
const { visibleColumns, menuItems: columnMenuItems } = useListColumnMenu({
  storageKey: 'admin-audit-hidden-columns',
  columns: allColumns,
  fixed: ['created_at', 'actions']
})

// Filters kept in the "更多筛选" popover that are currently set (shown as a count on its button)
const extraFilterCount = computed(() =>
  [filters.actor_email, filters.action, filters.client_ip, filters.method, filters.auth_method].filter(Boolean).length
)

const methodOptions = computed(() => [
  { value: '', label: t('admin.audit.filters.all') },
  { value: 'POST', label: 'POST' },
  { value: 'PUT', label: 'PUT' },
  { value: 'PATCH', label: 'PATCH' },
  { value: 'DELETE', label: 'DELETE' },
  { value: 'GET', label: 'GET' }
])

const authMethodOptions = computed(() => [
  { value: '', label: t('admin.audit.filters.all') },
  { value: 'jwt', label: 'JWT' },
  { value: 'admin_api_key', label: 'Admin API Key' }
])

const resultOptions = computed(() => [
  { value: '', label: t('admin.audit.filters.all') },
  { value: 'true', label: t('admin.audit.filters.resultSuccess') },
  { value: 'false', label: t('admin.audit.filters.resultFailure') }
])

function authMethodLabel(method: string): string {
  const found = authMethodOptions.value.find((o) => o.value === method)
  return found && found.value ? found.label : method
}

function toRFC3339(local: string): string | undefined {
  if (!local) return undefined
  const d = new Date(local)
  if (Number.isNaN(d.getTime())) return undefined
  return d.toISOString()
}

function buildTimeRangeQuery(): { start_time?: string; end_time?: string } {
  if (timeRange.value === 'custom') {
    return {
      start_time: toRFC3339(customStartTime.value),
      end_time: toRFC3339(customEndTime.value)
    }
  }
  const minutes = TIME_RANGE_MINUTES[timeRange.value]
  if (!minutes) return {}
  return { start_time: new Date(Date.now() - minutes * 60 * 1000).toISOString() }
}

function buildQuery() {
  return {
    page: page.value,
    page_size: pageSize.value,
    q: filters.q || undefined,
    actor_email: filters.actor_email || undefined,
    action: filters.action || undefined,
    client_ip: filters.client_ip || undefined,
    method: filters.method || undefined,
    auth_method: filters.auth_method || undefined,
    success: filters.success || undefined,
    ...buildTimeRangeQuery()
  }
}

async function fetchLogs() {
  loading.value = true
  try {
    const res = await adminAPI.audit.list(buildQuery())
    logs.value = res.items
    total.value = res.total
  } catch (err: any) {
    appStore.showError(err?.message || t('admin.audit.loadFailed'))
  } finally {
    loading.value = false
  }
}

function search() {
  page.value = 1
  fetchLogs()
}

function resetFilters() {
  filters.q = ''
  filters.actor_email = ''
  filters.action = ''
  filters.client_ip = ''
  filters.method = ''
  filters.auth_method = ''
  filters.success = ''
  timeRange.value = ''
  customStartTime.value = ''
  customEndTime.value = ''
  search()
}

function onPageChange(p: number) {
  page.value = p
  fetchLogs()
}

function onPageSizeChange(ps: number) {
  pageSize.value = ps
  page.value = 1
  fetchLogs()
}

// Detail dialog
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<AuditLog | null>(null)

async function openDetail(id: number) {
  detailVisible.value = true
  detailLoading.value = true
  detail.value = null
  try {
    detail.value = await adminAPI.audit.get(id)
  } catch (err: any) {
    appStore.showError(err?.message || t('admin.audit.loadFailed'))
    detailVisible.value = false
  } finally {
    detailLoading.value = false
  }
}

function prettyBody(body: string): string {
  try {
    return JSON.stringify(JSON.parse(body), null, 2)
  } catch {
    return body
  }
}

// Clear-all flow: confirm → TOTP → clear
const clearConfirmVisible = ref(false)
const clearTotpVisible = ref(false)
const clearTotpCode = ref('')
const clearing = ref(false)
const checkingTotpStatus = ref(false)

// 与其他敏感操作一致：未启用 2FA 时直接提示去个人资料启用 TOTP，
// 而不是弹出一个无法完成的验证码输入框（后端会以 TOTP_NOT_SETUP 拒绝）。
async function openClearDialog() {
  if (checkingTotpStatus.value) return
  checkingTotpStatus.value = true
  try {
    const status = await totpAPI.getStatus()
    if (!status.enabled) {
      appStore.showError(t('stepUp.notEnabled'))
      return
    }
  } catch (err: any) {
    appStore.showError(err?.message || t('admin.audit.loadFailed'))
    return
  } finally {
    checkingTotpStatus.value = false
  }
  clearConfirmVisible.value = true
}

function onClearConfirmed() {
  clearConfirmVisible.value = false
  clearTotpCode.value = ''
  clearTotpVisible.value = true
}

function cancelClearTotp() {
  if (clearing.value) return
  clearTotpVisible.value = false
}

async function submitClear() {
  if (clearTotpCode.value.length !== 6) return
  clearing.value = true
  try {
    const res = await adminAPI.audit.clear(clearTotpCode.value)
    clearTotpVisible.value = false
    appStore.showSuccess(t('admin.audit.clearConfirm.success', { count: res.deleted }))
    search()
  } catch (err: any) {
    appStore.showError(err?.message || t('admin.audit.clearConfirm.failed'))
    clearTotpCode.value = ''
  } finally {
    clearing.value = false
  }
}

// Helpers
function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

function statusText(status: number): string {
  return status < 400 ? t('admin.audit.filters.resultSuccess') : t('admin.audit.filters.resultFailure')
}

function statusColor(status: number): 'error' | 'warning' | 'success' {
  if (status >= 500) return 'error'
  if (status >= 400) return 'warning'
  return 'success'
}

onMounted(fetchLogs)
</script>
