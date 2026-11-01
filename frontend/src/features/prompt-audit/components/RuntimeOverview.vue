<template>
  <section aria-labelledby="prompt-runtime-title">
    <UPageCard variant="naked" orientation="horizontal" class="mb-4" :description="t('admin.promptAudit.runtime.description')">
      <template #title><h2 id="prompt-runtime-title">{{ t('admin.promptAudit.runtime.title') }}</h2></template>
      <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :loading="loading" :label="t('admin.promptAudit.actions.refresh')" class="w-fit lg:ms-auto" @click="$emit('refresh')" />
    </UPageCard>

    <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
    <div v-else-if="loading && !runtime" class="grid grid-cols-2 gap-px overflow-hidden rounded-lg md:grid-cols-3 xl:grid-cols-6" aria-busy="true">
      <USkeleton v-for="index in 6" :key="index" class="h-20 rounded-none" />
    </div>
    <template v-else-if="runtime">
      <dl class="grid grid-cols-2 gap-px overflow-hidden rounded-lg bg-(--ui-border) ring ring-default md:grid-cols-3 xl:grid-cols-6">
        <div v-for="item in statusItems" :key="item.label" class="bg-elevated/50 px-4 py-3">
          <dt class="text-xs text-muted">{{ item.label }}</dt>
          <dd class="mt-1.5 flex min-w-0 items-center gap-2 text-sm font-semibold text-highlighted">
            <UBadge v-if="item.badge" :color="item.badge" variant="subtle" :label="item.value" class="max-w-full truncate" />
            <span v-else class="min-w-0 truncate">{{ item.value }}</span>
          </dd>
        </div>
      </dl>

      <div class="mt-4 grid gap-6 rounded-lg bg-elevated/50 p-4 ring ring-default sm:p-6 lg:grid-cols-[minmax(0,1.4fr)_minmax(220px,0.6fr)]">
        <div>
          <h3 class="text-sm font-medium text-highlighted">{{ t('admin.promptAudit.runtime.guardMetrics') }}</h3>
          <dl class="mt-3 grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-4">
            <div v-for="metric in guardMetricItems" :key="metric.label">
              <dt class="text-xs text-muted">{{ metric.label }}</dt>
              <dd class="mt-0.5 text-sm font-semibold tabular-nums text-highlighted">{{ metric.value }}</dd>
            </div>
          </dl>
          <p class="mt-4 text-xs leading-5 text-muted">
            {{ t('admin.promptAudit.runtime.queueBreakdown', {
              queued: runtime.queue.queued,
              processing: runtime.queue.processing,
              retry: runtime.queue.retry,
              done: runtime.queue.done,
              failed: runtime.queue.failed,
            }) }}
            <span class="mx-1.5 text-dimmed">·</span>
            {{ t('admin.promptAudit.runtime.deliveryTotals', { enqueued: runtime.enqueued_total, dropped: runtime.dropped_total, processed: runtime.processed_total, failed: runtime.failed_total }) }}
          </p>
        </div>
        <div>
          <h3 class="text-sm font-medium text-highlighted">{{ t('admin.promptAudit.runtime.latest') }}</h3>
          <p class="mt-2 text-sm text-toned">
            {{ runtime.last_processed_at ? formatDate(runtime.last_processed_at) : t('admin.promptAudit.common.never') }}
          </p>
          <p v-if="runtime.last_error_code" class="mt-1 wrap-break-word text-sm text-error">
            {{ runtime.last_error_code }}<span v-if="runtime.last_error_message"> · {{ runtime.last_error_message }}</span>
          </p>
          <div v-if="Object.keys(runtime.endpoints).length" class="mt-3 flex flex-wrap gap-1.5">
            <UBadge v-for="(probe, id) in runtime.endpoints" :key="id" :color="probe.ok ? 'success' : 'error'" variant="subtle">
              {{ id }} · {{ probe.status }} · {{ probe.latency_ms }} ms
            </UBadge>
          </div>
        </div>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PromptAuditRuntime } from '../types'

const props = defineProps<{ runtime: PromptAuditRuntime | null; loading: boolean; error: string }>()
defineEmits<{ (event: 'refresh'): void }>()
const { t, locale } = useI18n()

const statusItems = computed(() => {
  const runtime = props.runtime
  if (!runtime) return []
  return [
    { label: t('admin.promptAudit.runtime.process'), value: t(`admin.promptAudit.status.${runtime.process_status}`), badge: statusColor(runtime.process_status) },
    { label: t('admin.promptAudit.runtime.mode'), value: t(`admin.promptAudit.mode.${runtime.effective_mode}`) },
    { label: t('admin.promptAudit.runtime.version'), value: `${runtime.active_config_version} / ${runtime.expected_config_version}` },
    { label: t('admin.promptAudit.runtime.workers'), value: `${runtime.worker_active} / ${runtime.worker_total}` },
    { label: t('admin.promptAudit.runtime.queue'), value: `${runtime.queue.active} / ${runtime.queue_capacity}` },
    { label: t('admin.promptAudit.runtime.dependencies'), value: `DB ${runtime.database_status} · Redis ${runtime.redis_status}` },
  ]
})

const guardMetricItems = computed(() => {
  const metrics = props.runtime?.guard_metrics
  if (!metrics) return []
  return [
    { label: t('admin.promptAudit.metrics.total'), value: metrics.total },
    { label: t('admin.promptAudit.metrics.allowed'), value: metrics.allowed },
    { label: t('admin.promptAudit.metrics.flagged'), value: metrics.flagged },
    { label: t('admin.promptAudit.metrics.blocked'), value: metrics.blocked },
    { label: t('admin.promptAudit.metrics.unavailable'), value: metrics.unavailable },
    { label: t('admin.promptAudit.metrics.timeouts'), value: metrics.timeouts },
    { label: t('admin.promptAudit.metrics.failovers'), value: metrics.failovers },
    { label: 'P95', value: metrics.latency_p95_ms != null ? `${metrics.latency_p95_ms} ms` : '—' },
  ]
})

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value))
}

function statusColor(status: string): 'success' | 'neutral' | 'warning' | 'error' {
  if (status === 'running') return 'success'
  if (status === 'disabled') return 'neutral'
  if (status === 'degraded') return 'warning'
  return 'error'
}
</script>
