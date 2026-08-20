<template>
  <AppLayout>
    <div class="space-y-5 pb-12">
      <section class="card !rounded-3xl !border-0 p-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700">
        <header class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
          <div class="min-w-0">
            <h1 class="page-title flex items-center gap-2 text-xl font-black text-gray-900 dark:text-white">
              <span class="grid h-8 w-8 place-items-center rounded-xl bg-primary-50 text-primary-500 dark:bg-primary-900/30 dark:text-primary-300"><Icon name="chart" size="sm" /></span>
              {{ t('channelMonitorV3.title') }}
            </h1>
            <div class="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
              <span class="h-2 w-2 rounded-full" :class="refreshing ? 'bg-gray-400' : 'bg-emerald-500'" />
              <span>{{ snapshot ? t('channelMonitorV3.updatedTo', { time: formatTime(snapshot.coverage.data_through) }) : t('common.loading') }}</span>
              <span v-if="snapshot && !snapshot.coverage.coverage_complete" class="badge badge-warning">{{ t('channelMonitorV3.partialCoverage') }}</span>
            </div>
          </div>
          <button class="btn btn-secondary btn-icon h-8 w-8 rounded-lg" type="button" :disabled="loading || refreshing" :title="t('common.refresh')" @click="reload(false)"><Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" /></button>
        </header>
        <div class="flex flex-wrap items-center gap-2 px-4 py-3 sm:px-5">
          <button v-for="option in ranges" :key="option.value" type="button" class="tab !px-2.5 !py-1 text-xs" :class="filter.range === option.value ? 'tab-active' : ''" @click="setRange(option.value)">{{ option.label }}</button>
          <span class="mx-1 hidden h-5 w-px bg-gray-200 dark:bg-dark-700 sm:block" />
          <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.description') }}</span>
          <span v-if="snapshot" class="ml-auto text-xs font-medium tabular-nums text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.summary', { success: formatPercent(1 - snapshot.metrics.error_rate), cache: formatPercent(snapshot.metrics.cache_rate) }) }}</span>
        </div>
      </section>

      <div v-if="loading && rows.length === 0" class="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
        <div v-for="i in 6" :key="i" class="h-64 animate-pulse rounded-2xl bg-gray-100 dark:bg-dark-800" />
      </div>
      <EmptyState v-else-if="rows.length === 0" :title="t('channelMonitorV3.emptyTitle')" :description="t('channelMonitorV3.emptyDescription')" />
      <div v-else class="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
        <ChannelMonitorV3Card v-for="row in rows" :key="`${row.platform}:${row.group_id ?? ''}:${row.model ?? ''}`" :row="row" />
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import * as api from '@/api/channelMonitorV2'
import type { MonitorFilter, MonitorMatrixResponse, MonitorRange, MonitorSnapshot } from '@/api/channelMonitorV2'
import ChannelMonitorV3Card from '@/components/user/monitor/ChannelMonitorV3Card.vue'
import { formatMonitorPercent } from '@/features/channel-monitor-v2/monitorFormat'

const { t, locale } = useI18n()
const appStore = useAppStore()
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
let controller: AbortController | null = null
let refreshTimer: number | null = null

const rows = computed(() => matrix.value?.items ?? [])
function formatPercent(value: number) { return formatMonitorPercent(value, locale.value || 'zh-CN') }
function formatTime(value?: string) { if (!value) return '-'; return new Intl.DateTimeFormat(locale.value || undefined, { dateStyle: 'short', timeStyle: 'short' }).format(new Date(value)) }
function setRange(value: MonitorRange) { filter.value = { ...filter.value, range: value } }

async function reload(silent = true) {
  controller?.abort()
  const request = new AbortController()
  controller = request
  refreshing.value = true
  if (!silent) loading.value = true
  try {
    const [nextSnapshot, nextMatrix] = await Promise.all([
      api.getSnapshot(filter.value, false, request.signal),
      api.getMatrix(filter.value, 'platform', false, request.signal),
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
  if (refreshTimer) window.clearInterval(refreshTimer)
  refreshTimer = window.setInterval(() => { if (!loading.value && !refreshing.value && !document.hidden) void reload(true) }, Math.max(10, seconds) * 1000)
}
watch(() => filter.value.range, () => void reload(false))
onMounted(() => void reload(false))
onBeforeUnmount(() => { controller?.abort(); if (refreshTimer) window.clearInterval(refreshTimer) })
</script>
