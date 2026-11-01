<template>
  <AppLayout>
    <div class="flex w-full min-w-0 flex-col gap-6 pb-8">
      <AdminPageHeader
        :title="t('admin.channelMonitor.title')"
        :description="isV1Mode ? t('channelMonitorV2.admin.descriptionV1') : t('channelMonitorV2.admin.descriptionV2')"
      />
      <PageToolbar>
        <UNavigationMenu :items="monitorSubnav" highlight class="-mx-1 overflow-x-auto" :aria-label="t('channelMonitorV2.admin.tabAria')" />
      </PageToolbar>

      <MonitorSettingsPanel v-if="adminMonitorTab === 'v2'" />

      <TablePageLayout v-else>
      <template #filters>
        <MonitorFiltersBar
          v-model:search="searchQuery"
          v-model:provider="providerFilter"
          v-model:enabled="enabledFilter"
          :loading="loading"
          @reload="reload"
          @create="openCreateDialog"
          @manage-templates="showTemplateManager = true"
          @search-input="handleSearch"
        />
      </template>

      <template #table>
        <DataTable :columns="columns" :data="monitors" :loading="loading">
          <template #cell-name="{ row, value }">
            <div class="flex items-center gap-1.5">
              <UButton variant="link" class="p-0 font-medium" data-testid="monitor-open-detail" @click="openDetail(row)">{{ value }}</UButton>
              <HelpTooltip v-if="row.api_key_decrypt_failed" :content="t('admin.channelMonitor.apiKeyDecryptFailed')">
                <UIcon name="i-lucide-triangle-alert" class="size-4 text-error" />
              </HelpTooltip>
            </div>
          </template>

          <template #cell-provider="{ row }">
            <span class="inline-flex flex-wrap items-center gap-1">
              <UBadge color="neutral" variant="outline" size="sm">{{ providerLabel(row.provider) }}</UBadge>
              <!-- 三种检测模式并列展示，quota 系配额数据源与纯探活一眼可分 -->
              <UBadge :color="checkModeColor(row.check_mode)" variant="subtle" size="sm" :data-check-mode="row.check_mode">{{ checkModeLabel(row.check_mode) }}</UBadge>
            </span>
          </template>

          <template #cell-primary_model="{ row }">
            <MonitorPrimaryModelCell :row="row" />
          </template>

          <template #cell-availability_7d="{ row }">
            <span class="text-sm tabular-nums text-default">{{ formatAvailability(row) }}</span>
          </template>

          <template #cell-latency="{ row }">
            <span class="text-sm tabular-nums text-default">{{ formatLatency(row.primary_latency_ms) }}</span>
          </template>

          <template #cell-enabled="{ row }">
            <Toggle :modelValue="row.enabled" @update:modelValue="toggleEnabled(row)" />
          </template>

          <template #cell-actions="{ row }">
            <MonitorActionsCell
              :row="row"
              :running="runningId === row.id"
              :duplicating="duplicatingIds.has(row.id)"
              @run="handleRunNow"
              @duplicate="handleDuplicate"
              @edit="openEditDialog"
              @delete="handleDelete"
            />
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.channelMonitor.noMonitorsYet')"
              :description="t('admin.channelMonitor.createFirstMonitor')"
              :action-text="t('admin.channelMonitor.createButton')"
              @action="openCreateDialog"
            />
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="onPageChange"
          @update:pageSize="onPageSizeChange"
        />
      </template>
      </TablePageLayout>
    </div>

    <USlideover v-model:open="detailOpen" :title="detail?.name || ''" :description="detail ? providerLabel(detail.provider) + ' · ' + checkModeLabel(detail.check_mode) : ''">
      <template #body>
        <div v-if="detail" class="flex flex-col gap-5 text-sm">
          <div class="flex flex-wrap items-center gap-1.5">
            <UBadge color="neutral" variant="outline" size="sm">{{ providerLabel(detail.provider) }}</UBadge>
            <UBadge :color="checkModeColor(detail.check_mode)" variant="subtle" size="sm">{{ checkModeLabel(detail.check_mode) }}</UBadge>
            <UBadge :color="detail.enabled ? 'success' : 'neutral'" variant="subtle" size="sm">{{ t('admin.channelMonitor.columns.enabled') }}: {{ detail.enabled ? '✓' : '—' }}</UBadge>
          </div>
          <MonitorPrimaryModelCell :row="detail" />
          <dl class="grid grid-cols-[8rem_minmax(0,1fr)] gap-x-3 gap-y-2">
            <dt class="text-muted">{{ t('admin.channelMonitor.form.endpoint') }}</dt><dd class="break-all font-mono text-xs text-default">{{ detail.endpoint || '—' }}</dd>
            <dt class="text-muted">{{ t('admin.channelMonitor.form.extraModels') }}</dt><dd class="text-default">{{ detail.extra_models?.length ? detail.extra_models.join(', ') : '—' }}</dd>
            <dt class="text-muted">{{ t('admin.channelMonitor.columns.availability7d') }}</dt><dd class="tabular-nums text-default">{{ formatAvailability(detail) }}</dd>
            <dt class="text-muted">{{ t('admin.channelMonitor.columns.latency') }}</dt><dd class="tabular-nums text-default">{{ formatLatency(detail.primary_latency_ms) }}</dd>
            <dt class="text-muted">{{ t('admin.channelMonitor.form.intervalSeconds') }}</dt><dd class="tabular-nums text-default">{{ detail.interval_seconds }}s · ±{{ detail.jitter_seconds }}s</dd>
            <dt class="text-muted">{{ t('admin.channelMonitor.form.groupName') }}</dt><dd class="text-default">{{ detail.group_name || '—' }}</dd>
          </dl>
          <UAlert v-if="detail.api_key_decrypt_failed" color="error" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.channelMonitor.apiKeyDecryptFailed')" />
        </div>
      </template>
      <template #footer>
        <div v-if="detail" class="flex w-full flex-wrap justify-end gap-2">
          <UButton color="error" variant="ghost" icon="i-lucide-trash-2" :label="t('common.delete')" @click="detailAction('delete')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-copy" :label="t('admin.channelMonitor.duplicate')" :disabled="Boolean(detail.api_key_decrypt_failed) || duplicatingIds.has(detail.id)" @click="detailAction('duplicate')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-play" :label="t('admin.channelMonitor.runNow')" :loading="runningId === detail.id" @click="detailAction('run')" />
          <UButton icon="i-lucide-pencil" :label="t('common.edit')" @click="detailAction('edit')" />
        </div>
      </template>
    </USlideover>

    <MonitorFormDialog
      :show="showDialog"
      :monitor="editing"
      @close="closeDialog"
      @saved="reload"
    />

    <MonitorTemplateManagerDialog
      :show="showTemplateManager"
      @close="showTemplateManager = false"
      @updated="reload"
    />

    <MonitorRunResultDialog
      :show="showRunResult"
      :results="runResults"
      @close="showRunResult = false"
    />

    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('common.delete')"
      :message="deleteConfirmMessage"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, getCurrentInstance, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { adminAPI } from '@/api/admin'
import type {
  ChannelMonitor,
  CheckResult,
  ListParams,
  Provider,
} from '@/api/admin/channelMonitor'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import '@/components/admin/page/admin-page.css'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import { PageToolbar } from '@/components/layout'
import { useRoute } from 'vue-router'
import Toggle from '@/components/common/Toggle.vue'
import MonitorFiltersBar from '@/components/admin/monitor/MonitorFiltersBar.vue'
import MonitorFormDialog from '@/components/admin/monitor/MonitorFormDialog.vue'
import MonitorTemplateManagerDialog from '@/components/admin/monitor/MonitorTemplateManagerDialog.vue'
import MonitorRunResultDialog from '@/components/admin/monitor/MonitorRunResultDialog.vue'
import MonitorPrimaryModelCell from '@/components/admin/monitor/MonitorPrimaryModelCell.vue'
import MonitorActionsCell from '@/components/admin/monitor/MonitorActionsCell.vue'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import MonitorSettingsPanel from '@/features/channel-monitor-v2/MonitorSettingsPanel.vue'
import { isChannelMonitorV1Mode } from '@/utils/featureFlags'

const { t } = useI18n()
const appStore = useAppStore()
const isV1Mode = computed(() => isChannelMonitorV1Mode())
const adminMonitorTab = ref<'v2' | 'legacy'>(isChannelMonitorV1Mode() ? 'legacy' : 'v2')
// Sub-pages: /admin/channels/monitor/settings (V2) and /list (V1 monitors); the bare path follows the system mode.
const monitorRoute = useRoute() as ReturnType<typeof useRoute> | undefined
const monitorRouter = getCurrentInstance()?.proxy?.$router
const monitorPaths = { v2: '/admin/channels/monitor/settings', legacy: '/admin/channels/monitor/list' } as const
watch(() => monitorRoute?.meta?.monitorSection, (section) => {
  if (section === 'v2' || section === 'legacy') adminMonitorTab.value = section
}, { immediate: true })
function selectMonitorSection(section: 'v2' | 'legacy') {
  adminMonitorTab.value = section
  if (monitorRouter && monitorRoute?.path !== monitorPaths[section]) void monitorRouter.push(monitorPaths[section])
}
const monitorSubnav = computed(() => monitorTabs.value.map(tab => ({ ...tab, active: adminMonitorTab.value === tab.value, onSelect: () => selectMonitorSection(tab.value as 'v2' | 'legacy') })))
const monitorTabs = computed(() => [
  { value: 'v2', label: t('channelMonitorV2.admin.tabV2'), icon: 'i-lucide-activity' },
  { value: 'legacy', label: isV1Mode.value ? t('channelMonitorV2.admin.tabV1Active') : t('channelMonitorV2.admin.tabV1History'), icon: 'i-lucide-history' },
])
/** quota 系（账号配额数据源）= info，纯探活 probe = neutral。 */
function checkModeColor(mode: string): 'info' | 'neutral' { return mode === 'probe' ? 'neutral' : 'info' }
const {
  providerLabel,
  checkModeLabel,
  formatLatency,
  formatAvailability,
} = useChannelMonitorFormat()

const monitors = ref<ChannelMonitor[]>([])
const loading = ref(false)
const runningId = ref<number | null>(null)
const searchQuery = ref('')
const providerFilter = ref<Provider | ''>('')
const enabledFilter = ref<'' | 'true' | 'false'>('')
const pagination = reactive({ page: 1, page_size: getPersistedPageSize(), total: 0 })

const showDialog = ref(false)
const showTemplateManager = ref(false)
const editing = ref<ChannelMonitor | null>(null)
const showDeleteDialog = ref(false)
const deleting = ref<ChannelMonitor | null>(null)
const showRunResult = ref(false)
const runResults = ref<CheckResult[]>([])
const duplicatingIds = reactive(new Set<number>())

let abortController: AbortController | null = null
let searchTimeout: ReturnType<typeof setTimeout> | null = null

const columns = computed<Column[]>(() => [
  { key: 'name', label: t('admin.channelMonitor.columns.name'), sortable: false },
  { key: 'provider', label: t('admin.channelMonitor.columns.provider'), sortable: false },
  { key: 'primary_model', label: t('admin.channelMonitor.columns.primaryModel'), sortable: false },
  { key: 'availability_7d', label: t('admin.channelMonitor.columns.availability7d'), sortable: false },
  { key: 'latency', label: t('admin.channelMonitor.columns.latency'), sortable: false },
  { key: 'enabled', label: t('admin.channelMonitor.columns.enabled'), sortable: false },
  { key: 'actions', label: t('admin.channelMonitor.columns.actions'), sortable: false },
])

const deleteConfirmMessage = computed(() => {
  const name = deleting.value?.name || ''
  return t('admin.channelMonitor.deleteConfirm', { name })
})

async function reload() {
  if (abortController) abortController.abort()
  const ctrl = new AbortController()
  abortController = ctrl
  loading.value = true
  try {
    const params: ListParams = {
      page: pagination.page,
      page_size: pagination.page_size,
    }
    if (providerFilter.value) params.provider = providerFilter.value
    if (enabledFilter.value === 'true') params.enabled = true
    if (enabledFilter.value === 'false') params.enabled = false
    if (searchQuery.value.trim()) params.search = searchQuery.value.trim()

    const res = await adminAPI.channelMonitor.list(params, { signal: ctrl.signal })
    if (ctrl.signal.aborted || abortController !== ctrl) return
    monitors.value = res.items || []
    pagination.total = res.total
  } catch (err: unknown) {
    const e = err as { name?: string; code?: string }
    if (e?.name === 'AbortError' || e?.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(err, t('admin.channelMonitor.loadError')))
  } finally {
    if (abortController === ctrl) {
      loading.value = false
      abortController = null
    }
  }
}

function handleSearch() {
  if (searchTimeout) clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    reload()
  }, 300)
}

function onPageChange(page: number) {
  pagination.page = page
  reload()
}

function onPageSizeChange(size: number) {
  pagination.page_size = size
  pagination.page = 1
  reload()
}

// List + detail: a row's name opens its detail slideover.
const detailOpen = ref(false)
const detail = ref<ChannelMonitor | null>(null)
function openDetail(row: ChannelMonitor) {
  detail.value = row
  detailOpen.value = true
}
function detailAction(action: 'run' | 'duplicate' | 'edit' | 'delete') {
  const row = detail.value
  if (!row) return
  if (action !== 'run') detailOpen.value = false
  if (action === 'run') void handleRunNow(row)
  else if (action === 'duplicate') void handleDuplicate(row)
  else if (action === 'edit') openEditDialog(row)
  else handleDelete(row)
}

function openCreateDialog() {
  editing.value = null
  showDialog.value = true
}

function openEditDialog(row: ChannelMonitor) {
  editing.value = row
  showDialog.value = true
}

function closeDialog() {
  showDialog.value = false
  editing.value = null
}

async function toggleEnabled(row: ChannelMonitor) {
  const next = !row.enabled
  try {
    await adminAPI.channelMonitor.update(row.id, { enabled: next })
    row.enabled = next
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  }
}

async function handleRunNow(row: ChannelMonitor) {
  if (!isV1Mode.value) {
    appStore.showError(t('admin.channelMonitor.runFailed'))
    return
  }
  if (runningId.value != null) return
  runningId.value = row.id
  try {
    const res = await adminAPI.channelMonitor.runNow(row.id)
    runResults.value = res.results || []
    showRunResult.value = true
    appStore.showSuccess(t('admin.channelMonitor.runSuccess'))
    // Refresh row to get latest status from backend
    void reload()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.channelMonitor.runFailed')))
  } finally {
    runningId.value = null
  }
}

async function handleDuplicate(row: ChannelMonitor) {
  if (row.api_key_decrypt_failed) {
    appStore.showError(t('admin.channelMonitor.duplicateKeyUnavailable'))
    return
  }
  if (duplicatingIds.has(row.id)) return

  duplicatingIds.add(row.id)
  try {
    const duplicate = await adminAPI.channelMonitor.duplicate(row.id)
    appStore.showSuccess(t('admin.channelMonitor.duplicateSuccess', { name: duplicate.name }))
    await reload()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.channelMonitor.duplicateFailed')))
  } finally {
    duplicatingIds.delete(row.id)
  }
}

function handleDelete(row: ChannelMonitor) {
  deleting.value = row
  showDeleteDialog.value = true
}

async function confirmDelete() {
  if (!deleting.value) return
  try {
    await adminAPI.channelMonitor.del(deleting.value.id)
    appStore.showSuccess(t('admin.channelMonitor.deleteSuccess'))
    showDeleteDialog.value = false
    deleting.value = null
    reload()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  }
}

watch(adminMonitorTab, (tab) => {
  if (tab === 'legacy' && monitors.value.length === 0) void reload()
})
onMounted(() => {
  if (adminMonitorTab.value === 'legacy') void reload()
})
onUnmounted(() => {
  if (searchTimeout) clearTimeout(searchTimeout)
  abortController?.abort()
})
</script>
