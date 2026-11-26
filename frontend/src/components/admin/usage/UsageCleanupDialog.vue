<template>
  <BaseDialog
    :show="show"
    :title="t('admin.usage.cleanup.title')"
    :description="t('admin.usage.cleanup.subtitle')"
    width="wide"
    @close="handleClose"
  >
    <div class="space-y-4">
      <!-- Filter configuration card (compact 2-column layout to eliminate truncation and row misalignment) -->
      <section class="rounded-xl border border-default bg-default p-4" data-testid="cleanup-filters-section">
        <UsageFilters
          ref="filtersRef"
          v-model="localFilters"
          v-model:startDate="localStartDate"
          v-model:endDate="localEndDate"
          :exporting="false"
          :show-actions="false"
          :model-options="effectiveModelOptions"
          :flat="true"
          :compact="true"
          mode="usage"
          @groups-loaded="onGroupsLoaded"
          @entity-label="onEntityLabel"
        >
          <template #header-actions>
            <UButton
              v-if="activeScopeChips.length > 0"
              color="neutral"
              variant="ghost"
              size="xs"
              icon="i-lucide-eraser"
              :label="t('admin.usage.cleanup.clearFilters')"
              data-testid="cleanup-clear-filters"
              @click="clearOptionalFilters"
            />
          </template>
        </UsageFilters>
      </section>

      <!-- Live impact preview + irreversible safety notice -->
      <section
        class="rounded-xl border border-default bg-elevated/40 p-4 space-y-3"
        data-testid="cleanup-preview-section"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex flex-wrap items-center gap-2">
            <UIcon name="i-lucide-scan-search" class="size-4 shrink-0 text-primary" />
            <h4 class="text-sm font-semibold text-highlighted">
              {{ t('admin.usage.cleanup.previewTitle') }}
            </h4>
            <UBadge
              v-if="maxRangeDays"
              :label="t('admin.usage.cleanup.maxRangeHint', { days: maxRangeDays })"
              color="neutral"
              variant="subtle"
              size="sm"
            />
          </div>
          <UButton
            color="neutral"
            variant="ghost"
            size="xs"
            icon="i-lucide-refresh-cw"
            :loading="isPreviewPending"
            :disabled="!hasDateRange"
            :label="t('admin.usage.cleanup.previewRefresh')"
            data-testid="cleanup-preview-refresh"
            @click="loadPreview()"
          />
        </div>

        <!-- Preview count / validation result -->
        <div
          v-if="previewError"
          role="alert"
          class="flex items-start gap-2 rounded-lg border border-error/30 bg-error/10 px-3 py-2 text-sm text-error"
          data-testid="cleanup-preview-error"
        >
          <UIcon name="i-lucide-circle-alert" class="mt-0.5 size-4 shrink-0" />
          <span class="min-w-0 break-words">{{ previewError }}</span>
        </div>

        <div
          v-else-if="isPreviewPending"
          class="flex items-center gap-2 rounded-lg border border-default bg-default/70 px-3 py-2.5 text-sm text-muted"
          data-testid="cleanup-preview-loading"
        >
          <UIcon name="i-lucide-loader-circle" class="size-4 shrink-0 animate-spin" />
          <span>{{ t('admin.usage.cleanup.previewLoading') }}</span>
        </div>

        <div
          v-else-if="isPreviewCurrent && preview"
          class="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-default bg-default px-3 py-2.5"
          data-testid="cleanup-preview-result"
        >
          <div class="flex min-w-0 items-center gap-2">
            <UIcon
              :name="preview.matched_rows > 0 ? 'i-lucide-database-zap' : 'i-lucide-circle-check'"
              :class="[
                'size-4 shrink-0',
                preview.matched_rows > 0 ? 'text-warning' : 'text-muted'
              ]"
            />
            <span
              :class="[
                'text-sm font-semibold tabular-nums',
                preview.matched_rows > 0 ? 'text-highlighted' : 'text-muted'
              ]"
              data-testid="cleanup-preview-count"
            >
              {{ formatPreviewSummary(preview) }}
            </span>
          </div>
          <span class="text-xs tabular-nums text-muted" data-testid="cleanup-preview-window">
            {{ formatDateTime(preview.start_time) }} ~ {{ formatDateTime(preview.end_time) }}
          </span>
        </div>

        <!-- Scope chips (individually removable for precise scope control) -->
        <div class="flex flex-wrap items-center gap-1.5 text-xs" data-testid="cleanup-scope-chips">
          <span class="text-muted">{{ t('admin.usage.cleanup.scopeFilters') }}:</span>
          <template v-if="activeScopeChips.length > 0">
            <span
              v-for="chip in activeScopeChips"
              :key="chip.key"
              class="inline-flex items-center gap-1 rounded-md bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary"
              :data-testid="`cleanup-scope-chip-${chip.key}`"
            >
              <span class="max-w-64 truncate" :title="chip.label">{{ chip.label }}</span>
              <button
                type="button"
                class="inline-flex size-3.5 shrink-0 items-center justify-center rounded-xs text-primary/70 transition-colors hover:bg-primary/15 hover:text-primary"
                :aria-label="t('admin.usage.cleanup.removeFilter', { label: chip.label })"
                :data-testid="`cleanup-scope-chip-remove-${chip.key}`"
                @click="removeScopeFilter(chip.key)"
              >
                <UIcon name="i-lucide-x" class="size-3" />
              </button>
            </span>
          </template>
          <UBadge
            v-else
            :label="t('admin.usage.cleanup.scopeAll')"
            color="warning"
            variant="soft"
            size="sm"
            data-testid="cleanup-scope-all-badge"
          />
        </div>

        <!-- Irreversible warning -->
        <div
          class="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50/90 px-3 py-2 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
          data-testid="cleanup-warning-banner"
        >
          <UIcon name="i-lucide-triangle-alert" class="mt-0.5 size-4 shrink-0" />
          <span>{{ t('admin.usage.cleanup.warning') }}</span>
        </div>
      </section>

      <!-- Recent cleanup tasks -->
      <section class="rounded-xl border border-default bg-default p-4" data-testid="cleanup-tasks-section">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex items-center gap-2">
            <h4 class="text-sm font-semibold text-highlighted">
              {{ t('admin.usage.cleanup.recentTasks') }}
            </h4>
            <UBadge
              v-if="activeTaskCount > 0"
              :label="t('admin.usage.cleanup.activeTasksBadge', { count: activeTaskCount })"
              color="info"
              variant="soft"
              size="sm"
              data-testid="cleanup-active-tasks-badge"
            />
          </div>
          <UButton
            color="neutral"
            variant="ghost"
            size="xs"
            icon="i-lucide-refresh-cw"
            :loading="tasksLoading"
            :label="t('common.refresh')"
            data-testid="cleanup-tasks-refresh"
            @click="loadTasks()"
          />
        </div>

        <div class="mt-3 space-y-2">
          <div
            v-if="tasksLoading && tasks.length === 0"
            class="flex items-center gap-2 py-4 text-sm text-muted"
            data-testid="cleanup-tasks-loading"
          >
            <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
            <span>{{ t('admin.usage.cleanup.loadingTasks') }}</span>
          </div>

          <div
            v-else-if="tasks.length === 0"
            class="flex flex-col items-center justify-center gap-1.5 rounded-lg border border-dashed border-default py-6 text-center text-sm text-muted"
            data-testid="cleanup-tasks-empty"
          >
            <UIcon name="i-lucide-inbox" class="size-5 text-dimmed" />
            <span>{{ t('admin.usage.cleanup.noTasks') }}</span>
          </div>

          <div v-else class="space-y-2" data-testid="cleanup-tasks-list">
            <article
              v-for="task in tasks"
              :key="task.id"
              class="flex flex-col gap-2 rounded-lg border border-default bg-elevated/30 px-3.5 py-2.5 text-sm"
              data-testid="cleanup-task-item"
            >
              <div class="flex flex-wrap items-center justify-between gap-2">
                <div class="flex flex-wrap items-center gap-2">
                  <UBadge
                    :color="statusColor(task.status)"
                    variant="soft"
                    size="sm"
                    :icon="task.status === 'running' ? 'i-lucide-loader-circle' : undefined"
                    :ui="task.status === 'running' ? { leadingIcon: 'animate-spin' } : undefined"
                    :label="statusLabel(task.status)"
                  />
                  <span class="font-mono text-xs text-muted">#{{ task.id }}</span>
                  <UButton
                    v-if="canCancel(task)"
                    color="error"
                    variant="ghost"
                    size="xs"
                    icon="i-lucide-ban"
                    :loading="cancelingTaskId === task.id"
                    :disabled="cancelingTaskId !== null"
                    :label="t('admin.usage.cleanup.cancel')"
                    data-testid="cleanup-task-cancel"
                    @click="openCancelConfirm(task)"
                  />
                </div>
                <div class="flex flex-wrap items-center gap-3 text-xs tabular-nums text-dimmed">
                  <span v-if="formatDuration(task)">
                    {{ t('admin.usage.cleanup.duration') }}: {{ formatDuration(task) }}
                  </span>
                  <span>{{ formatDateTime(task.created_at) }}</span>
                </div>
              </div>

              <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
                <span class="tabular-nums">{{ t('admin.usage.cleanup.range') }}: {{ formatRange(task) }}</span>
                <span class="font-medium tabular-nums text-default">
                  {{ t('admin.usage.cleanup.deletedRows') }}: {{ task.deleted_rows.toLocaleString() }}
                </span>
                <span v-if="task.status === 'canceled' && task.canceled_at" class="tabular-nums text-dimmed">
                  {{ t('admin.usage.cleanup.canceledAt') }}: {{ formatDateTime(task.canceled_at) }}
                </span>
              </div>

              <div class="flex flex-wrap items-center gap-1.5" data-testid="cleanup-task-filters">
                <template v-if="formatTaskFilterChips(task).length > 0">
                  <UBadge
                    v-for="chip in formatTaskFilterChips(task)"
                    :key="chip"
                    :label="chip"
                    color="neutral"
                    variant="subtle"
                    size="xs"
                  />
                </template>
                <UBadge
                  v-else
                  :label="t('admin.usage.cleanup.taskScopeAll')"
                  color="neutral"
                  variant="subtle"
                  size="xs"
                />
              </div>

              <div
                v-if="task.error_message"
                class="rounded-md border border-error/25 bg-error/10 px-2.5 py-1.5 text-xs text-error break-words"
                data-testid="cleanup-task-error"
              >
                {{ task.error_message }}
              </div>
            </article>
          </div>
        </div>

        <Pagination
          v-if="tasksTotal > tasksPageSize"
          class="mt-4"
          :total="tasksTotal"
          :page="tasksPage"
          :page-size="tasksPageSize"
          :page-size-options="[5]"
          :show-page-size-selector="false"
          :show-jump="true"
          @update:page="handleTaskPageChange"
          @update:pageSize="handleTaskPageSizeChange"
        />
      </section>
    </div>

    <template #footer>
      <div class="flex flex-wrap justify-end gap-2">
        <UButton
          color="neutral"
          variant="outline"
          :label="t('common.cancel')"
          data-testid="cleanup-dialog-close"
          @click="handleClose"
        />
        <UButton
          color="error"
          icon="i-lucide-trash-2"
          :loading="submitting"
          :disabled="submitDisabled"
          :label="submitting ? t('admin.usage.cleanup.submitting') : t('admin.usage.cleanup.submit')"
          data-testid="cleanup-dialog-submit"
          @click="openConfirm"
        />
      </div>
    </template>
  </BaseDialog>

  <ConfirmDialog
    :show="confirmVisible"
    :title="t('admin.usage.cleanup.confirmTitle')"
    :message="t('admin.usage.cleanup.confirmMessage')"
    :confirm-text="t('admin.usage.cleanup.confirmSubmit')"
    danger
    @confirm="submitCleanup"
    @cancel="confirmVisible = false"
  >
    <div
      class="space-y-2 rounded-lg border border-default bg-elevated/50 p-3 text-xs"
      data-testid="cleanup-confirm-summary"
    >
      <div class="flex flex-wrap items-baseline justify-between gap-2">
        <span class="text-muted">{{ t('admin.usage.cleanup.confirmRange') }}</span>
        <span class="font-medium tabular-nums text-highlighted">{{ confirmRangeDisplay }}</span>
      </div>
      <div v-if="isPreviewCurrent && preview" class="flex flex-wrap items-baseline justify-between gap-2">
        <span class="text-muted">{{ t('admin.usage.cleanup.confirmEstimated') }}</span>
        <span class="font-semibold tabular-nums text-error">{{ formatPreviewSummary(preview) }}</span>
      </div>
      <div class="flex flex-wrap items-center gap-1.5 pt-1">
        <span class="text-muted">{{ t('admin.usage.cleanup.confirmScope') }}:</span>
        <template v-if="activeScopeChips.length > 0">
          <UBadge
            v-for="chip in activeScopeChips"
            :key="chip.key"
            :label="chip.label"
            color="primary"
            variant="soft"
            size="xs"
          />
        </template>
        <UBadge
          v-else
          :label="t('admin.usage.cleanup.scopeAll')"
          color="warning"
          variant="soft"
          size="xs"
        />
      </div>
    </div>
  </ConfirmDialog>

  <ConfirmDialog
    :show="cancelConfirmVisible"
    :title="t('admin.usage.cleanup.cancelConfirmTitle')"
    :message="cancelConfirmMessage"
    :confirm-text="t('admin.usage.cleanup.cancelConfirm')"
    danger
    @confirm="cancelTask"
    @cancel="cancelConfirmVisible = false"
  />
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import UsageFilters from '@/components/admin/usage/UsageFilters.vue'
import { adminAPI } from '@/api/admin'
import { adminUsageAPI } from '@/api/admin/usage'
import type {
  AdminUsageQueryParams,
  UsageCleanupFilters,
  UsageCleanupPreview,
  UsageCleanupTask,
  CreateUsageCleanupTaskRequest
} from '@/api/admin/usage'
import { requestTypeToLegacyStream } from '@/utils/usageRequestType'

interface Props {
  show: boolean
  filters: AdminUsageQueryParams
  startDate: string
  endDate: string
  modelOptions?: string[]
}

const props = defineProps<Props>()
const emit = defineEmits(['close', 'submitted'])

const { t, locale } = useI18n()
const appStore = useAppStore()

const filtersRef = ref<InstanceType<typeof UsageFilters> | null>(null)
const localFilters = ref<AdminUsageQueryParams>({})
const localStartDate = ref('')
const localEndDate = ref('')

const loadedModelOptions = ref<string[]>([])
const userLabels = ref<Record<number, string>>({})
const apiKeyLabels = ref<Record<number, string>>({})
const accountLabels = ref<Record<number, string>>({})
const groupLabels = ref<Record<number, string>>({})

const preview = ref<UsageCleanupPreview | null>(null)
const previewSignature = ref('')
const maxRangeDays = ref<number | null>(null)
const previewLoading = ref(false)
const previewDebouncePending = ref(false)
const previewError = ref('')
const previewBlocked = ref(false)

const tasks = ref<UsageCleanupTask[]>([])
const tasksLoading = ref(false)
const tasksPage = ref(1)
const tasksPageSize = ref(5)
const tasksTotal = ref(0)
const submitting = ref(false)
const confirmVisible = ref(false)
const cancelConfirmVisible = ref(false)
const cancelingTaskId = ref<number | null>(null)
const cancelTarget = ref<UsageCleanupTask | null>(null)

let pollTimer: ReturnType<typeof setInterval> | null = null
let previewDebounceTimer: ReturnType<typeof setTimeout> | null = null
let tasksAbortController: AbortController | null = null
let previewAbortController: AbortController | null = null
let previousActiveTaskIds = new Set<number>()
let openSessionSeq = 0
let modelReqSeq = 0
let previewReqSeq = 0
let tasksReqSeq = 0
let initializingDialog = false

const ACTIVE_POLL_INTERVAL_MS = 3000
const PREVIEW_DEBOUNCE_MS = 350

const effectiveModelOptions = computed<string[]>(() => {
  const base = props.modelOptions && props.modelOptions.length > 0 ? props.modelOptions : loadedModelOptions.value
  const current = localFilters.value.model?.trim()
  if (current && !base.includes(current)) {
    return [current, ...base]
  }
  return base
})

const hasDateRange = computed(() => Boolean(localStartDate.value && localEndDate.value))

const activeTaskCount = computed(() =>
  tasks.value.filter((task) => task.status === 'pending' || task.status === 'running').length
)

const filterPreviewSignature = computed(() =>
  JSON.stringify([
    localStartDate.value,
    localEndDate.value,
    localFilters.value.user_id ?? null,
    localFilters.value.api_key_id ?? null,
    localFilters.value.account_id ?? null,
    localFilters.value.group_id ?? null,
    localFilters.value.model?.trim() || null,
    localFilters.value.request_id?.trim() || null,
    localFilters.value.request_type ?? null,
    localFilters.value.stream ?? null,
    localFilters.value.native_compaction_v2 ?? null,
    localFilters.value.billing_type ?? null,
    localFilters.value.billing_mode?.trim() || null,
    localFilters.value.upstream_model_mismatch ?? null
  ])
)

const isPreviewCurrent = computed(
  () => preview.value !== null && previewSignature.value === filterPreviewSignature.value
)

const isPreviewPending = computed(() => previewLoading.value || previewDebouncePending.value)

const submitDisabled = computed(() => {
  if (submitting.value || !hasDateRange.value || previewBlocked.value || isPreviewPending.value) {
    return true
  }
  if (isPreviewCurrent.value && !previewError.value && preview.value?.matched_rows === 0) {
    return true
  }
  return false
})

const requestTypeLabel = (value?: string | null): string => {
  const map: Record<string, string> = {
    ws_v2: t('usage.ws'),
    live: t('usage.live'),
    stream: t('usage.stream'),
    sync: t('usage.sync'),
    cyber: t('usage.cyber'),
    claude_refusal: t('usage.claudeRefusal')
  }
  return (value && map[value]) || value || ''
}

const billingTypeLabel = (value: number): string => {
  if (value === 0) return t('admin.usage.billingTypeBalance')
  if (value === 1) return t('admin.usage.billingTypeSubscription')
  return String(value)
}

const billingModeLabel = (value: string): string => {
  const map: Record<string, string> = {
    token: t('admin.usage.billingModeToken'),
    per_request: t('admin.usage.billingModePerRequest'),
    image: t('admin.usage.billingModeImage'),
    video: t('admin.usage.billingModeVideo')
  }
  return map[value] || value
}

const buildFilterChipList = (
  source: {
    user_id?: number | null
    api_key_id?: number | null
    account_id?: number | null
    group_id?: number | null
    model?: string | null
    request_id?: string | null
    request_type?: string | null
    stream?: boolean | null
    native_compaction_v2?: boolean | null
    billing_type?: number | null
    billing_mode?: string | null
    upstream_model_mismatch?: boolean | null
  }
): Array<{ key: string; label: string }> => {
  const chips: Array<{ key: string; label: string }> = []
  if (source.user_id && source.user_id > 0) {
    const label = userLabels.value[source.user_id] || `#${source.user_id}`
    chips.push({ key: 'user_id', label: `${t('admin.usage.userFilter')}: ${label}` })
  }
  if (source.api_key_id && source.api_key_id > 0) {
    const label = apiKeyLabels.value[source.api_key_id] || `#${source.api_key_id}`
    chips.push({ key: 'api_key_id', label: `${t('usage.apiKeyFilter')}: ${label}` })
  }
  if (source.model?.trim()) {
    chips.push({ key: 'model', label: `${t('usage.model')}: ${source.model.trim()}` })
  }
  if (source.request_id?.trim()) {
    chips.push({ key: 'request_id', label: `${t('admin.usage.requestId')}: ${source.request_id.trim()}` })
  }
  if (source.request_type) {
    chips.push({ key: 'request_type', label: `${t('usage.type')}: ${requestTypeLabel(source.request_type)}` })
  } else if (source.stream !== null && source.stream !== undefined) {
    chips.push({
      key: 'stream',
      label: `${t('usage.type')}: ${source.stream ? t('usage.stream') : t('usage.sync')}`
    })
  }
  if (source.native_compaction_v2 === true) {
    chips.push({ key: 'native_compaction_v2', label: t('usage.compactionOnly') })
  } else if (source.native_compaction_v2 === false) {
    chips.push({ key: 'native_compaction_v2', label: `${t('usage.compactionFilter')}: ${t('common.no')}` })
  }
  if (source.billing_type !== null && source.billing_type !== undefined) {
    chips.push({
      key: 'billing_type',
      label: `${t('admin.usage.billingType')}: ${billingTypeLabel(source.billing_type)}`
    })
  }
  if (source.billing_mode?.trim()) {
    chips.push({
      key: 'billing_mode',
      label: `${t('admin.usage.billingMode')}: ${billingModeLabel(source.billing_mode.trim())}`
    })
  }
  if (source.upstream_model_mismatch !== null && source.upstream_model_mismatch !== undefined) {
    chips.push({
      key: 'upstream_model_mismatch',
      label: `${t('admin.usage.upstreamModelAudit')}: ${
        source.upstream_model_mismatch
          ? t('admin.usage.upstreamModelMismatchOnly')
          : t('admin.usage.upstreamModelMatchedOnly')
      }`
    })
  }
  if (source.account_id && source.account_id > 0) {
    const label = accountLabels.value[source.account_id] || `#${source.account_id}`
    chips.push({ key: 'account_id', label: `${t('admin.usage.account')}: ${label}` })
  }
  if (source.group_id && source.group_id > 0) {
    const label = groupLabels.value[source.group_id] || `#${source.group_id}`
    chips.push({ key: 'group_id', label: `${t('admin.usage.group')}: ${label}` })
  }
  return chips
}

const activeScopeChips = computed(() => buildFilterChipList(localFilters.value))

const formatTaskFilterChips = (task: UsageCleanupTask): string[] => {
  const f: UsageCleanupFilters = task.filters || { start_time: '', end_time: '' }
  return buildFilterChipList(f).map((item) => item.label)
}

const confirmRangeDisplay = computed(() => {
  if (isPreviewCurrent.value && preview.value) {
    return `${formatDateTime(preview.value.start_time)} ~ ${formatDateTime(preview.value.end_time)}`
  }
  return `${formatDateTime(localStartDate.value)} ~ ${formatDateTime(localEndDate.value)}`
})

const cancelConfirmMessage = computed(() => {
  if (cancelTarget.value) {
    return t('admin.usage.cleanup.cancelConfirmMessageWithId', { id: cancelTarget.value.id })
  }
  return t('admin.usage.cleanup.cancelConfirmMessage')
})

const hasAdvancedFilters = (f: AdminUsageQueryParams): boolean =>
  Boolean(
    f.request_id?.trim() ||
      f.request_type != null ||
      f.stream != null ||
      f.native_compaction_v2 != null ||
      f.billing_type != null ||
      f.billing_mode?.trim() ||
      f.upstream_model_mismatch != null ||
      (f.account_id && f.account_id > 0) ||
      (f.group_id && f.group_id > 0)
  )

const isAbortError = (error: unknown): boolean => {
  if (!error || typeof error !== 'object') return false
  const err = error as { code?: string; name?: string }
  return err.code === 'ERR_CANCELED' || err.name === 'CanceledError' || err.name === 'AbortError'
}

const isStepUpCancelError = (error: unknown): boolean => {
  if (!error || typeof error !== 'object') return false
  return (error as { cancelled?: boolean }).cancelled === true
}

const formatCleanupError = (error: unknown, fallback: string): string => {
  if (!error || typeof error !== 'object') return fallback
  const err = error as {
    reason?: string
    message?: string
    metadata?: Record<string, string>
  }
  switch (err.reason) {
    case 'USAGE_CLEANUP_DISABLED':
      return t('admin.usage.cleanup.errors.disabled')
    case 'USAGE_CLEANUP_MISSING_RANGE':
      return t('admin.usage.cleanup.missingRange')
    case 'USAGE_CLEANUP_INVALID_RANGE':
      return t('admin.usage.cleanup.errors.invalidRange')
    case 'USAGE_CLEANUP_RANGE_TOO_LARGE': {
      const days = err.metadata?.max_range_days || String(maxRangeDays.value || 31)
      return t('admin.usage.cleanup.errors.rangeTooLarge', { days })
    }
    case 'USAGE_CLEANUP_INVALID_REQUEST_ID':
      return t('admin.usage.cleanup.errors.invalidRequestId')
    case 'USAGE_CLEANUP_REQUEST_ID_RANGE_TOO_LARGE':
      return t('admin.usage.cleanup.errors.requestIdRangeTooLarge')
    case 'USAGE_CLEANUP_INVALID_BILLING_MODE':
      return t('admin.usage.cleanup.errors.invalidBillingMode')
    case 'USAGE_CLEANUP_CANCEL_CONFLICT':
      return t('admin.usage.cleanup.errors.cancelConflict')
    case 'USAGE_CLEANUP_PREVIEW_TIMEOUT':
      return t('admin.usage.cleanup.errors.previewTimeout')
    default:
      return err.message?.trim() || fallback
  }
}

const syncFiltersUi = (instance: InstanceType<typeof UsageFilters> | null) => {
  if (!instance) return
  instance.setAdvancedOpen(hasAdvancedFilters(localFilters.value))
  const userId = localFilters.value.user_id
  const apiKeyId = localFilters.value.api_key_id
  const accountId = localFilters.value.account_id
  instance.setUserKeyword(userId && userId > 0 ? userLabels.value[userId] || `#${userId}` : '')
  instance.setApiKeyKeyword(apiKeyId && apiKeyId > 0 ? apiKeyLabels.value[apiKeyId] || `#${apiKeyId}` : '')
  instance.setAccountKeyword(accountId && accountId > 0 ? accountLabels.value[accountId] || `#${accountId}` : '')
}

const onGroupsLoaded = (groups: Array<{ id: number; name: string }>) => {
  const next = { ...groupLabels.value }
  for (const g of groups) {
    next[g.id] = g.name
  }
  groupLabels.value = next
}

const onEntityLabel = (payload: { kind: 'user' | 'apiKey' | 'account'; id: number; label: string }) => {
  if (payload.kind === 'user') {
    userLabels.value = { ...userLabels.value, [payload.id]: payload.label }
  } else if (payload.kind === 'apiKey') {
    apiKeyLabels.value = { ...apiKeyLabels.value, [payload.id]: payload.label }
  } else if (payload.kind === 'account') {
    accountLabels.value = { ...accountLabels.value, [payload.id]: payload.label }
  }
}

const hydrateEntityLabels = async (session: number) => {
  const userId = localFilters.value.user_id
  const apiKeyId = localFilters.value.api_key_id
  const accountId = localFilters.value.account_id
  const lookups: Array<Promise<void>> = []

  if (userId && userId > 0) {
    const cached = userLabels.value[userId]
    filtersRef.value?.setUserKeyword(cached || `#${userId}`)
    if (!cached) {
      lookups.push(
        adminAPI.users
          .getById(userId, true)
          .then((user) => {
            if (session !== openSessionSeq || localFilters.value.user_id !== userId) return
            if (user?.email) {
              userLabels.value = { ...userLabels.value, [userId]: user.email }
              filtersRef.value?.setUserKeyword(user.email)
            }
          })
          .catch(() => {
            // Keep fallback #id label if lookup fails
          })
      )
    }
  } else {
    filtersRef.value?.setUserKeyword('')
  }

  if (apiKeyId && apiKeyId > 0) {
    const cached = apiKeyLabels.value[apiKeyId]
    filtersRef.value?.setApiKeyKeyword(cached || `#${apiKeyId}`)
    if (!cached) {
      lookups.push(
        adminUsageAPI
          .searchApiKeys(userId && userId > 0 ? userId : undefined, '', apiKeyId)
          .then((keys) => {
            if (session !== openSessionSeq || localFilters.value.api_key_id !== apiKeyId) return
            const found = keys.find((k) => k.id === apiKeyId)
            if (found) {
              const label = found.name || `#${found.id}`
              apiKeyLabels.value = { ...apiKeyLabels.value, [apiKeyId]: label }
              filtersRef.value?.setApiKeyKeyword(label)
            }
          })
          .catch(() => {
            // Keep fallback #id label if lookup fails
          })
      )
    }
  } else {
    filtersRef.value?.setApiKeyKeyword('')
  }

  if (accountId && accountId > 0) {
    const cached = accountLabels.value[accountId]
    filtersRef.value?.setAccountKeyword(cached || `#${accountId}`)
    if (!cached) {
      lookups.push(
        adminAPI.accounts
          .getById(accountId)
          .then((account) => {
            if (session !== openSessionSeq || localFilters.value.account_id !== accountId) return
            if (account?.name) {
              accountLabels.value = { ...accountLabels.value, [accountId]: account.name }
              filtersRef.value?.setAccountKeyword(account.name)
            }
          })
          .catch(() => {
            // Keep fallback #id label if lookup fails
          })
      )
    }
  } else {
    filtersRef.value?.setAccountKeyword('')
  }

  if (lookups.length > 0) {
    await Promise.allSettled(lookups)
  }
}

const loadModelOptions = async () => {
  if (!props.show || (props.modelOptions && props.modelOptions.length > 0)) return
  if (!localStartDate.value || !localEndDate.value) return
  const seq = ++modelReqSeq
  try {
    const res = await adminAPI.dashboard.getModelStats({
      start_date: localStartDate.value,
      end_date: localEndDate.value
    })
    if (seq !== modelReqSeq || !props.show) return
    const models = (res?.models || [])
      .map((item) => item.model)
      .filter((name): name is string => Boolean(name && name.trim()))
    loadedModelOptions.value = Array.from(new Set(models)).sort()
  } catch {
    // Model list is optional; preserve existing options on failure
  }
}

const removeScopeFilter = (key: string) => {
  const next = { ...localFilters.value }
  switch (key) {
    case 'user_id':
      next.user_id = undefined
      filtersRef.value?.setUserKeyword('')
      break
    case 'api_key_id':
      next.api_key_id = undefined
      filtersRef.value?.setApiKeyKeyword('')
      break
    case 'account_id':
      next.account_id = undefined
      filtersRef.value?.setAccountKeyword('')
      break
    case 'group_id':
      next.group_id = undefined
      break
    case 'model':
      next.model = undefined
      break
    case 'request_id':
      next.request_id = undefined
      break
    case 'request_type':
      next.request_type = undefined
      next.stream = undefined
      break
    case 'stream':
      next.stream = undefined
      break
    case 'native_compaction_v2':
      next.native_compaction_v2 = null
      break
    case 'billing_type':
      next.billing_type = null
      break
    case 'billing_mode':
      next.billing_mode = undefined
      break
    case 'upstream_model_mismatch':
      next.upstream_model_mismatch = undefined
      break
  }
  localFilters.value = next
}

const clearOptionalFilters = () => {
  localFilters.value = {
    start_date: localStartDate.value,
    end_date: localEndDate.value
  }
  filtersRef.value?.setUserKeyword('')
  filtersRef.value?.setApiKeyKeyword('')
  filtersRef.value?.setAccountKeyword('')
}

const resetFilters = () => {
  const copied: AdminUsageQueryParams = { ...props.filters }
  if (copied.request_type) {
    copied.stream = undefined
  }
  localFilters.value = copied
  localStartDate.value = props.startDate
  localEndDate.value = props.endDate
  localFilters.value.start_date = localStartDate.value
  localFilters.value.end_date = localEndDate.value
  tasksPage.value = 1
  tasksTotal.value = 0
  previousActiveTaskIds = new Set()
  preview.value = null
  previewSignature.value = ''
  previewError.value = ''
  previewBlocked.value = false
  previewDebouncePending.value = false
}

const stopPolling = () => {
  if (pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

const syncPolling = () => {
  if (!props.show || activeTaskCount.value === 0) {
    stopPolling()
    return
  }
  if (pollTimer === null) {
    pollTimer = setInterval(() => {
      loadTasks({ silent: true })
    }, ACTIVE_POLL_INTERVAL_MS)
  }
}

const cancelPendingRequests = () => {
  if (previewDebounceTimer !== null) {
    clearTimeout(previewDebounceTimer)
    previewDebounceTimer = null
  }
  previewDebouncePending.value = false
  if (tasksAbortController) {
    tasksAbortController.abort()
    tasksAbortController = null
  }
  if (previewAbortController) {
    previewAbortController.abort()
    previewAbortController = null
  }
  tasksLoading.value = false
  previewLoading.value = false
}

const handleClose = () => {
  openSessionSeq += 1
  stopPolling()
  cancelPendingRequests()
  previousActiveTaskIds = new Set()
  confirmVisible.value = false
  cancelConfirmVisible.value = false
  cancelingTaskId.value = null
  cancelTarget.value = null
  submitting.value = false
  emit('close')
}

const statusLabel = (status: string) => {
  const map: Record<string, string> = {
    pending: t('admin.usage.cleanup.status.pending'),
    running: t('admin.usage.cleanup.status.running'),
    succeeded: t('admin.usage.cleanup.status.succeeded'),
    failed: t('admin.usage.cleanup.status.failed'),
    canceled: t('admin.usage.cleanup.status.canceled')
  }
  return map[status] || status
}

type BadgeColor = 'warning' | 'info' | 'success' | 'error' | 'neutral'

const statusColor = (status: string): BadgeColor => {
  const map: Record<string, BadgeColor> = {
    pending: 'warning',
    running: 'info',
    succeeded: 'success',
    failed: 'error',
    canceled: 'neutral'
  }
  return map[status] || 'neutral'
}

const formatDateTime = (value?: string | null) => {
  if (!value) return '--'
  const normalized =
    !value.includes('T') && /^\d{4}-\d{2}-\d{2}$/.test(value) ? `${value}T00:00:00` : value
  const date = new Date(normalized)
  if (Number.isNaN(date.getTime())) return value
  const dateLocale = locale?.value === 'zh' ? 'zh-CN' : 'en-US'
  return date.toLocaleString(dateLocale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false
  })
}

const formatRange = (task: UsageCleanupTask) => {
  const start = formatDateTime(task.filters.start_time)
  const end = formatDateTime(task.filters.end_time)
  return `${start} ~ ${end}`
}

const formatDuration = (task: UsageCleanupTask): string => {
  if (!task.started_at || !task.finished_at) return ''
  const ms = new Date(task.finished_at).getTime() - new Date(task.started_at).getTime()
  if (!Number.isFinite(ms) || ms < 0) return ''
  if (ms < 1000) return `${ms}ms`
  const sec = ms / 1000
  if (sec < 60) return `${sec.toFixed(1)}s`
  const mins = Math.floor(sec / 60)
  const remSec = Math.round(sec % 60)
  return `${mins}m ${remSec}s`
}

const formatPreviewSummary = (data: UsageCleanupPreview): string => {
  if (data.matched_rows === 0) {
    return t('admin.usage.cleanup.previewEmpty')
  }
  const formatted = data.matched_rows.toLocaleString()
  if (data.truncated) {
    return t('admin.usage.cleanup.previewMatchedTruncated', { count: formatted })
  }
  return t('admin.usage.cleanup.previewMatched', { count: formatted })
}

const getUserTimezone = () => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    return 'UTC'
  }
}

const buildPayload = (notifyOnMissingRange = true): CreateUsageCleanupTaskRequest | null => {
  if (!localStartDate.value || !localEndDate.value) {
    if (notifyOnMissingRange) {
      appStore.showError(t('admin.usage.cleanup.missingRange'))
    }
    return null
  }

  const payload: CreateUsageCleanupTaskRequest = {
    start_date: localStartDate.value,
    end_date: localEndDate.value,
    timezone: getUserTimezone()
  }

  if (localFilters.value.user_id && localFilters.value.user_id > 0) {
    payload.user_id = localFilters.value.user_id
  }
  if (localFilters.value.api_key_id && localFilters.value.api_key_id > 0) {
    payload.api_key_id = localFilters.value.api_key_id
  }
  if (localFilters.value.account_id && localFilters.value.account_id > 0) {
    payload.account_id = localFilters.value.account_id
  }
  if (localFilters.value.group_id && localFilters.value.group_id > 0) {
    payload.group_id = localFilters.value.group_id
  }
  if (localFilters.value.model?.trim()) {
    payload.model = localFilters.value.model.trim()
  }
  if (localFilters.value.request_id?.trim()) {
    payload.request_id = localFilters.value.request_id.trim()
  }
  if (localFilters.value.request_type) {
    payload.request_type = localFilters.value.request_type
    const legacyStream = requestTypeToLegacyStream(localFilters.value.request_type)
    if (legacyStream !== null && legacyStream !== undefined) {
      payload.stream = legacyStream
    }
  } else if (localFilters.value.stream !== null && localFilters.value.stream !== undefined) {
    payload.stream = localFilters.value.stream
  }
  if (
    localFilters.value.native_compaction_v2 !== null &&
    localFilters.value.native_compaction_v2 !== undefined
  ) {
    payload.native_compaction_v2 = localFilters.value.native_compaction_v2
  }
  if (localFilters.value.billing_type !== null && localFilters.value.billing_type !== undefined) {
    payload.billing_type = localFilters.value.billing_type
  }
  if (localFilters.value.billing_mode?.trim()) {
    payload.billing_mode = localFilters.value.billing_mode.trim()
  }
  if (
    localFilters.value.upstream_model_mismatch !== null &&
    localFilters.value.upstream_model_mismatch !== undefined
  ) {
    payload.upstream_model_mismatch = localFilters.value.upstream_model_mismatch
  }

  return payload
}

const canCancel = (task: UsageCleanupTask) => {
  return task.status === 'pending' || task.status === 'running'
}

const loadPreview = async () => {
  if (!props.show) return
  if (previewDebounceTimer !== null) {
    clearTimeout(previewDebounceTimer)
    previewDebounceTimer = null
  }
  previewDebouncePending.value = false
  const targetSignature = filterPreviewSignature.value
  const payload = buildPayload(false)
  if (!payload) {
    preview.value = null
    previewSignature.value = ''
    previewError.value = t('admin.usage.cleanup.missingRange')
    previewBlocked.value = true
    previewLoading.value = false
    return
  }

  if (previewAbortController) {
    previewAbortController.abort()
  }
  const controller = new AbortController()
  previewAbortController = controller
  const seq = ++previewReqSeq
  previewLoading.value = true
  previewError.value = ''
  previewBlocked.value = false

  try {
    const res = await adminUsageAPI.previewCleanupTask(payload, { signal: controller.signal })
    if (seq !== previewReqSeq || !props.show) return
    preview.value = res
    previewSignature.value = targetSignature
    if (res.max_range_days > 0) {
      maxRangeDays.value = res.max_range_days
    }
    previewError.value = ''
    previewBlocked.value = false
  } catch (error) {
    if (isAbortError(error) || seq !== previewReqSeq || !props.show) return
    const status = (error as { status?: number })?.status
    preview.value = null
    previewSignature.value = ''
    previewError.value = formatCleanupError(error, t('admin.usage.cleanup.previewFailed'))
    previewBlocked.value = status === 400 || status === 503
  } finally {
    if (seq === previewReqSeq) {
      previewLoading.value = false
      if (previewAbortController === controller) {
        previewAbortController = null
      }
    }
  }
}

const schedulePreview = () => {
  if (!props.show) return
  if (previewDebounceTimer !== null) {
    clearTimeout(previewDebounceTimer)
  }
  if (previewAbortController) {
    previewAbortController.abort()
    previewAbortController = null
  }
  previewReqSeq += 1
  preview.value = null
  previewSignature.value = ''
  previewError.value = ''
  previewBlocked.value = false
  previewLoading.value = false
  previewDebouncePending.value = true
  previewDebounceTimer = setTimeout(() => {
    previewDebounceTimer = null
    previewDebouncePending.value = false
    loadPreview()
  }, PREVIEW_DEBOUNCE_MS)
}

const loadTasks = async (options?: { silent?: boolean }): Promise<boolean> => {
  if (!props.show) return false
  if (tasksAbortController) {
    tasksAbortController.abort()
  }
  const controller = new AbortController()
  tasksAbortController = controller
  const seq = ++tasksReqSeq
  const silent = options?.silent === true
  if (!silent) {
    tasksLoading.value = true
  }
  let previewRefreshed = false
  try {
    const res = await adminUsageAPI.listCleanupTasks(
      {
        page: tasksPage.value,
        page_size: tasksPageSize.value
      },
      { signal: controller.signal }
    )
    if (seq !== tasksReqSeq || !props.show) return false
    const items = res.items || []
    const currentActiveIds = new Set(items.filter(canCancel).map((task) => task.id))
    const activeTaskFinished = Array.from(previousActiveTaskIds).some((id) => !currentActiveIds.has(id))
    previousActiveTaskIds = currentActiveIds

    tasks.value = items
    tasksTotal.value = res.total || 0
    if (res.page) {
      tasksPage.value = res.page
    }
    if (res.page_size) {
      tasksPageSize.value = res.page_size
    }
    syncPolling()
    if (activeTaskFinished && !isPreviewPending.value) {
      previewRefreshed = true
      loadPreview()
    }
    return previewRefreshed
  } catch (error) {
    if (isAbortError(error) || isStepUpCancelError(error) || seq !== tasksReqSeq || !props.show) {
      return false
    }
    console.error('Failed to load cleanup tasks:', error)
    if (!silent) {
      appStore.showError(formatCleanupError(error, t('admin.usage.cleanup.loadFailed')))
    }
    return false
  } finally {
    if (seq === tasksReqSeq) {
      tasksLoading.value = false
      if (tasksAbortController === controller) {
        tasksAbortController = null
      }
    }
  }
}

const handleTaskPageChange = (page: number) => {
  previousActiveTaskIds = new Set()
  tasksPage.value = page
  loadTasks()
}

const handleTaskPageSizeChange = (size: number) => {
  if (!Number.isFinite(size) || size <= 0) return
  previousActiveTaskIds = new Set()
  tasksPageSize.value = size
  tasksPage.value = 1
  loadTasks()
}

const openConfirm = () => {
  if (submitDisabled.value) return
  confirmVisible.value = true
}

const openCancelConfirm = (task: UsageCleanupTask) => {
  if (cancelingTaskId.value !== null) return
  cancelTarget.value = task
  cancelConfirmVisible.value = true
}

const submitCleanup = async () => {
  const payload = buildPayload(true)
  if (!payload) {
    confirmVisible.value = false
    return
  }
  submitting.value = true
  confirmVisible.value = false
  try {
    const created = await adminUsageAPI.createCleanupTask(payload)
    appStore.showSuccess(t('admin.usage.cleanup.submitSuccess'))
    emit('submitted', created)
    tasksPage.value = 1
    await loadTasks()
    loadPreview()
  } catch (error) {
    if (isStepUpCancelError(error)) return
    console.error('Failed to create cleanup task:', error)
    appStore.showError(formatCleanupError(error, t('admin.usage.cleanup.submitFailed')))
  } finally {
    submitting.value = false
  }
}

const cancelTask = async () => {
  const task = cancelTarget.value
  if (!task || cancelingTaskId.value !== null) {
    cancelConfirmVisible.value = false
    return
  }
  cancelingTaskId.value = task.id
  cancelConfirmVisible.value = false
  try {
    await adminUsageAPI.cancelCleanupTask(task.id)
    appStore.showSuccess(t('admin.usage.cleanup.cancelSuccess'))
    const previewRefreshed = await loadTasks()
    if (!previewRefreshed) {
      loadPreview()
    }
  } catch (error) {
    if (isStepUpCancelError(error)) return
    console.error('Failed to cancel cleanup task:', error)
    appStore.showError(formatCleanupError(error, t('admin.usage.cleanup.cancelFailed')))
  } finally {
    cancelingTaskId.value = null
    cancelTarget.value = null
  }
}

watch(
  () => localFilters.value.request_type,
  () => {
    if (!props.show || initializingDialog) return
    if (localFilters.value.stream !== undefined) {
      localFilters.value.stream = undefined
    }
  }
)

watch(filtersRef, (instance) => {
  if (!instance || !props.show) return
  syncFiltersUi(instance)
})

watch(filterPreviewSignature, () => {
  if (!props.show || initializingDialog) return
  schedulePreview()
})

watch([localStartDate, localEndDate], () => {
  if (!props.show || initializingDialog) return
  loadModelOptions()
})

watch(
  () => props.show,
  async (show) => {
    if (show) {
      const session = ++openSessionSeq
      initializingDialog = true
      resetFilters()
      await nextTick()
      initializingDialog = false
      if (session !== openSessionSeq || !props.show) return
      syncFiltersUi(filtersRef.value)
      hydrateEntityLabels(session)
      loadModelOptions()
      loadPreview()
      loadTasks()
    } else {
      openSessionSeq += 1
      initializingDialog = false
      previousActiveTaskIds = new Set()
      stopPolling()
      cancelPendingRequests()
    }
  },
  { immediate: true }
)

onUnmounted(() => {
  openSessionSeq += 1
  stopPolling()
  cancelPendingRequests()
})
</script>
