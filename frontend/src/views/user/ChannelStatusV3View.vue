<template>
  <AppLayout>
    <div class="signal-channel-page signal-channel-v3 space-y-5 pb-12">
      <UserPageHeader flush :title="t('channelMonitorV3.title')">
        <template #description>
          <span class="inline-flex flex-wrap items-center gap-2">
            <span class="size-2 shrink-0 rounded-full" :class="refreshing ? 'bg-(--ui-text-dimmed)' : 'bg-emerald-500'" />
            <span>{{ snapshot ? t('channelMonitorV3.updatedTo', { time: formatTime(snapshot.coverage.data_through) }) : t('common.loading') }}</span>
            <UBadge v-if="snapshot && !snapshot.coverage.coverage_complete" color="warning" variant="subtle">{{ t('channelMonitorV3.partialCoverage') }}</UBadge>
          </span>
        </template>
        <template #actions>
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-refresh-cw"
            :ui="{ leadingIcon: refreshing ? 'animate-spin motion-reduce:animate-none' : '' }"
            :disabled="loading || refreshing"
            :title="t('common.refresh')"
            :aria-label="t('common.refresh')"
            @click="reload(false)"
          />
        </template>
      </UserPageHeader>
      <section class="signal-channel-toolbar flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg bg-default px-3 py-2.5 shadow-(--app-shadow-card) ring ring-default sm:px-4">
        <div class="channel-range-switch rounded-lg" role="group" :aria-label="t('channelMonitorV2.timeRange')">
          <button v-for="option in ranges" :key="option.value" type="button" :aria-pressed="filter.range === option.value" @click="setRange(option.value)">{{ option.label }}</button>
        </div>
        <span class="text-xs text-muted">{{ t('channelMonitorV3.description') }}</span>
        <span v-if="snapshot" class="ml-auto text-xs font-medium tabular-nums text-muted">{{ t('channelMonitorV3.summary', { success: formatPercent(1 - (latestSnapshotMetrics?.error_rate ?? 0)), cache: formatPercent(latestSnapshotMetrics?.cache_rate ?? 0) }) }}</span>
      </section>

      <div
        v-if="watchSubscriptionsState === 'error'"
        class="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-amber-500/25 bg-amber-500/[0.06] px-3.5 py-2 text-xs text-amber-800 dark:border-amber-400/25 dark:bg-amber-400/[0.08] dark:text-amber-200"
        role="status"
        data-testid="group-watch-load-error"
      >
        <span>{{ t('channelMonitorV3.watchSubscriptionsLoadFailed') }}</span>
        <UButton
          color="neutral"
          variant="subtle"
          size="xs"
          icon="i-lucide-refresh-cw"
          :label="t('channelMonitorV3.watchSubscriptionsRetry')"
          @click="loadWatchSubscriptions()"
        />
      </div>

      <div v-if="loading && rows.length === 0" class="signal-channel-loading-grid grid grid-cols-1 gap-5 rounded-lg md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
        <div v-for="i in 8" :key="i" class="h-72 animate-pulse rounded-lg bg-elevated motion-reduce:animate-none" />
      </div>
      <EmptyState v-else-if="rows.length === 0" :title="t('channelMonitorV3.emptyTitle')" :description="t('channelMonitorV3.emptyDescription')" />
      <div v-else class="channel-status-board space-y-8" data-testid="channel-status-board">
        <section
          v-for="(block, index) in layoutBlocks"
          :key="channelStatusLayoutBlockKey(block, index)"
          :data-layout="block.kind"
          :data-testid="block.kind === 'cluster' ? `channel-status-platform-${block.platform}` : 'channel-status-compact-platforms'"
        >
          <template v-if="block.kind === 'cluster'">
            <h2 class="signal-platform-heading">
              {{ providerLabel(block.platform) }}
              <UBadge color="neutral" variant="subtle" size="sm" class="tabular-nums">{{ block.rows.length }}</UBadge>
            </h2>
            <div class="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
              <ChannelMonitorV3Card
                v-for="row in block.rows"
                :key="row.group_id ?? `${row.platform}:${row.group_name ?? ''}`"
                :row="row"
                :user-rate-multiplier="getUserRateMultiplier(row.group_id)"
                :signals="groupSignals[row.group_id ?? 0] ?? null"
                :subscribed="isGroupSubscribed(row.group_id)"
                :watch-pending="isGroupWatchPending(row.group_id)"
                :watch-disabled="watchSubscriptionsState !== 'ready'"
                :countdown-seconds="countdownSeconds"
                :timeline-length="timelineLength"
                :anchor-bucket-start="timelineAnchorBucketStart"
                :bucket-seconds="matrix?.coverage.bucket_seconds"
                @toggle-watch="toggleGroupWatch"
              />
            </div>
          </template>
          <div
            v-else
            class="grid grid-cols-1 gap-5 rounded-md md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4"
          >
            <div
              v-for="item in block.items"
              :key="item.row.group_id ?? `${item.platform}:${item.row.group_name ?? ''}`"
              :data-testid="`channel-status-platform-${item.platform}`"
            >
              <h2 class="signal-platform-heading">{{ providerLabel(item.platform) }}</h2>
              <ChannelMonitorV3Card
                :row="item.row"
                :user-rate-multiplier="getUserRateMultiplier(item.row.group_id)"
                :signals="groupSignals[item.row.group_id ?? 0] ?? null"
                :subscribed="isGroupSubscribed(item.row.group_id)"
                :watch-pending="isGroupWatchPending(item.row.group_id)"
                :watch-disabled="watchSubscriptionsState !== 'ready'"
                :countdown-seconds="countdownSeconds"
                :timeline-length="timelineLength"
                :anchor-bucket-start="timelineAnchorBucketStart"
                :bucket-seconds="matrix?.coverage.bucket_seconds"
                @toggle-watch="toggleGroupWatch"
              />
            </div>
          </div>
        </section>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import './channel-signal.css'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import * as api from '@/api/channelMonitorV2'
import { groupWatchAPI } from '@/api/groupWatch'
import userGroupsAPI from '@/api/groups'
import type { MonitorFilter, MonitorMatrixResponse, MonitorRange, MonitorSnapshot } from '@/api/channelMonitorV2'
import type { Group } from '@/types'
import ChannelMonitorV3Card from '@/components/user/monitor/ChannelMonitorV3Card.vue'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import { formatMonitorPercent } from '@/features/channel-monitor-v2/monitorFormat'
import { buildChannelStatusLayout, channelStatusLayoutBlockKey } from '@/features/channel-monitor-v2/channelStatusLayout'
import type { GroupSignalSource } from '@/utils/groupRateSignals'

const { t, locale } = useI18n()
const appStore = useAppStore()
const { providerLabel } = useChannelMonitorFormat()
const ranges = computed(() => [
  { value: '90m' as MonitorRange, label: t('channelMonitorV3.ranges.90m') },
  { value: '24h' as MonitorRange, label: t('channelMonitorV3.ranges.24h') },
  { value: '7d' as MonitorRange, label: t('channelMonitorV3.ranges.7d') },
  { value: '30d' as MonitorRange, label: t('channelMonitorV3.ranges.30d') },
])
const filter = ref<MonitorFilter>({ range: '90m', platforms: [], groupIds: [], models: [] })
const snapshot = ref<MonitorSnapshot | null>(null)
const matrix = ref<MonitorMatrixResponse | null>(null)
const loading = ref(false)
const refreshing = ref(false)
const userGroupRates = ref<Record<number, number>>({})
// Backend-computed display hints per group (recent base rate change, new group).
const groupSignals = ref<Record<number, GroupSignalSource>>({})
const subscribedGroupIds = ref<Set<number>>(new Set())
const watchPendingIds = ref<Set<number>>(new Set())
const watchSubscriptionsState = ref<'loading' | 'ready' | 'error'>('loading')
let watchLoadSeq = 0
const countdownSeconds = ref(0)
let controller: AbortController | null = null
let refreshTimer: number | null = null
let countdownTimer: number | null = null

// platform_group is intentionally used here: the backend scopes it to the
// monitor group_ids selected by the operator, so unrelated groups never appear.
const rows = computed(() => [...(matrix.value?.items ?? [])]
  .filter(row => row.group_id != null && row.group_id > 0)
  .sort((a, b) => (a.group_id ?? 0) - (b.group_id ?? 0)))
const platformSections = computed(() => {
  const grouped = new Map<string, typeof rows.value>()
  for (const row of rows.value) {
    const platform = row.platform || 'unknown'
    const current = grouped.get(platform) ?? []
    current.push(row)
    grouped.set(platform, current)
  }
  return [...grouped.entries()].map(([platform, sectionRows]) => ({
    platform,
    rows: sectionRows.sort((a, b) => (a.group_id ?? 0) - (b.group_id ?? 0)),
  }))
})
const layoutBlocks = computed(() => buildChannelStatusLayout(platformSections.value))
const timelineLength = computed(() => ({ '90m': 18, '24h': 24, '7d': 14, '30d': 30 })[filter.value.range])
const timelineAnchorBucketStart = computed(() => {
  let latest = 0
  for (const row of rows.value) {
    for (const bucket of row.buckets) {
      const start = Date.parse(bucket.bucket_start)
      if (Number.isFinite(start) && start > latest) latest = start
    }
  }
  return latest > 0 ? new Date(latest).toISOString() : undefined
})
const latestSnapshotMetrics = computed(() => {
  const trend = [...(snapshot.value?.trend ?? [])]
    .filter(point => point.bucket_start && point.metrics)
    .sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start))
  return trend.at(-1)?.metrics ?? snapshot.value?.metrics
})

function formatPercent(value: number) { return formatMonitorPercent(value, locale.value || 'zh-CN') }
function formatTime(value?: string) { if (!value) return '-'; return new Intl.DateTimeFormat(locale.value || undefined, { dateStyle: 'short', timeStyle: 'short' }).format(new Date(value)) }
function setRange(value: MonitorRange) { filter.value = { ...filter.value, range: value } }

function getUserRateMultiplier(groupId?: number) {
  if (!groupId) return null
  const value = userGroupRates.value[groupId]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function isGroupSubscribed(groupId?: number): boolean {
  return typeof groupId === 'number' && groupId > 0 && subscribedGroupIds.value.has(groupId)
}

function isGroupWatchPending(groupId?: number): boolean {
  return typeof groupId === 'number' && groupId > 0 && watchPendingIds.value.has(groupId)
}

function resolveGroupName(groupId: number): string {
  const row = rows.value.find(item => item.group_id === groupId)
  return row?.group_name || t('channelMonitorV3.unknownGroup')
}

async function loadWatchSubscriptions() {
  const seq = ++watchLoadSeq
  watchSubscriptionsState.value = 'loading'
  try {
    const ids = await groupWatchAPI.listSubscriptions()
    if (seq !== watchLoadSeq) return
    subscribedGroupIds.value = new Set(ids)
    watchSubscriptionsState.value = 'ready'
  } catch (error) {
    if (seq !== watchLoadSeq) return
    watchSubscriptionsState.value = 'error'
    console.warn('Failed to load group watch subscriptions', error)
  }
}

async function toggleGroupWatch(groupId: number) {
  if (!groupId || watchSubscriptionsState.value !== 'ready' || watchPendingIds.value.has(groupId)) return
  const wasSubscribed = subscribedGroupIds.value.has(groupId)
  const nextSubscribed = !wasSubscribed

  const nextPending = new Set(watchPendingIds.value)
  nextPending.add(groupId)
  watchPendingIds.value = nextPending

  // Optimistic toggle so the bell animation and brand tint respond on frame 1.
  const optimistic = new Set(subscribedGroupIds.value)
  if (nextSubscribed) optimistic.add(groupId)
  else optimistic.delete(groupId)
  subscribedGroupIds.value = optimistic

  try {
    if (nextSubscribed) {
      await groupWatchAPI.subscribe(groupId)
      appStore.showSuccess(t('channelMonitorV3.watchSubscribedToast', { name: resolveGroupName(groupId) }))
    } else {
      await groupWatchAPI.unsubscribe(groupId)
      appStore.showSuccess(t('channelMonitorV3.watchUnsubscribedToast', { name: resolveGroupName(groupId) }))
    }
  } catch (error) {
    const reverted = new Set(subscribedGroupIds.value)
    if (wasSubscribed) reverted.add(groupId)
    else reverted.delete(groupId)
    subscribedGroupIds.value = reverted
    appStore.showError(extractApiErrorMessage(error, t('channelMonitorV3.watchToggleFailed')))
  } finally {
    const cleared = new Set(watchPendingIds.value)
    cleared.delete(groupId)
    watchPendingIds.value = cleared
  }
}

async function loadUserGroupRates() {
  try {
    const [groups, customRates] = await Promise.all([
      userGroupsAPI.getAvailable(),
      userGroupsAPI.getUserGroupRates(),
    ])
    const typedGroups = groups as Group[]
    userGroupRates.value = Object.fromEntries(
      typedGroups.map(group => [group.id, customRates[group.id] ?? group.rate_multiplier]),
    )
    // The backend already withholds the base rate change for groups where this user pays a personal rate.
    groupSignals.value = Object.fromEntries(typedGroups.map(group => [group.id, group]))
  } catch (error) {
    // Monitoring remains usable when the optional pricing endpoint is unavailable.
    console.warn('Failed to load channel monitor group rates', error)
  }
}

async function reload(silent = true) {
  if (!silent && watchSubscriptionsState.value === 'error') {
    void loadWatchSubscriptions()
  }
  controller?.abort()
  const request = new AbortController()
  controller = request
  refreshing.value = true
  if (!silent) loading.value = true
  try {
    const [nextSnapshot, nextMatrix] = await Promise.all([
      api.getSnapshot(filter.value, false, request.signal),
      api.getMatrix(filter.value, 'platform_group', false, request.signal),
    ])
    if (request.signal.aborted || controller !== request) return
    snapshot.value = nextSnapshot
    matrix.value = nextMatrix
    scheduleRefresh(nextSnapshot.coverage.bootstrap?.active ? 10 : nextSnapshot.config.refresh_interval_seconds)
  } catch (error) {
    const e = error as { name?: string; code?: string }
    if (e.name !== 'AbortError' && e.code !== 'ERR_CANCELED') appStore.showError(extractApiErrorMessage(error, t('channelMonitorV3.loadFailed')))
  } finally {
    if (controller === request) { loading.value = false; refreshing.value = false }
  }
}

function scheduleRefresh(seconds: number) {
  const interval = Math.max(10, seconds)
  if (refreshTimer) window.clearInterval(refreshTimer)
  if (countdownTimer) window.clearInterval(countdownTimer)
  countdownSeconds.value = interval
  countdownTimer = window.setInterval(() => {
    if (!document.hidden && countdownSeconds.value > 0) countdownSeconds.value -= 1
  }, 1000)
  refreshTimer = window.setInterval(() => {
    if (!loading.value && !refreshing.value && !document.hidden) void reload(true)
  }, interval * 1000)
}

watch(() => filter.value.range, () => void reload(false))
onMounted(() => { void loadUserGroupRates(); void loadWatchSubscriptions(); void reload(false) })
onBeforeUnmount(() => {
  controller?.abort()
  if (refreshTimer) window.clearInterval(refreshTimer)
  if (countdownTimer) window.clearInterval(countdownTimer)
})
</script>
