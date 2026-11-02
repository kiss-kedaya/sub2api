<template>
  <BaseDialog
    :show="show"
    :title="t('admin.proxies.qualityReportTitle')"
    width="normal"
    @close="emit('close')"
  >
    <div v-if="report" class="space-y-4">
      <div class="flex items-start justify-between gap-4">
        <div class="min-w-0">
          <p class="text-sm text-muted">{{ name || '-' }}</p>
          <p class="mt-1 text-sm text-default">{{ report.summary }}</p>
        </div>
        <div class="shrink-0 text-right">
          <p class="text-2xl font-semibold text-highlighted">{{ report.score }}</p>
          <p class="text-xs text-muted">{{ t('admin.proxies.qualityGrade', { grade: report.grade }) }}</p>
        </div>
      </div>
      <dl class="grid grid-cols-2 gap-x-4 gap-y-1.5 text-xs">
        <div class="flex gap-1.5"><dt class="text-muted">{{ t('admin.proxies.qualityExitIP') }}:</dt><dd class="text-default">{{ report.exit_ip || '-' }}</dd></div>
        <div class="flex gap-1.5"><dt class="text-muted">{{ t('admin.proxies.qualityCountry') }}:</dt><dd class="text-default">{{ report.country || '-' }}</dd></div>
        <div class="flex gap-1.5"><dt class="text-muted">{{ t('admin.proxies.qualityBaseLatency') }}:</dt><dd class="text-default">{{ typeof report.base_latency_ms === 'number' ? `${report.base_latency_ms}ms` : '-' }}</dd></div>
        <div class="flex gap-1.5"><dt class="text-muted">{{ t('admin.proxies.qualityCheckedAt') }}:</dt><dd class="text-default">{{ new Date(report.checked_at * 1000).toLocaleString() }}</dd></div>
      </dl>
      <UTable
        :data="report.items"
        :columns="columns"
        sticky
        class="max-h-80"
        :ui="{
          base: 'border-separate border-spacing-0',
          thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
          tbody: '[&>tr]:last:[&>td]:border-b-0',
          th: 'py-2 whitespace-nowrap first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
          td: 'border-b border-default',
          separator: 'h-0'
        }"
      >
        <template #target-cell="{ row }"><span class="whitespace-nowrap text-highlighted">{{ qualityTargetLabel(row.original.target) }}</span></template>
        <template #status-cell="{ row }"><UBadge :color="qualityStatusColor(row.original.status)" variant="subtle" class="whitespace-nowrap">{{ qualityStatusLabel(row.original.status) }}</UBadge></template>
        <template #http_status-cell="{ row }">{{ row.original.http_status ?? '-' }}</template>
        <template #latency_ms-cell="{ row }"><span class="whitespace-nowrap">{{ typeof row.original.latency_ms === 'number' ? `${row.original.latency_ms}ms` : '-' }}</span></template>
        <template #message-cell="{ row }">
          <span class="whitespace-normal">{{ row.original.message || '-' }}</span>
          <span v-if="row.original.cf_ray" class="ml-1 text-xs text-dimmed">(cf-ray: {{ row.original.cf_ray }})</span>
        </template>
      </UTable>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <UButton color="neutral" variant="subtle" :label="t('common.close')" @click="emit('close')" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { ProxyQualityCheckItem, ProxyQualityCheckResult } from '@/types'

defineProps<{
  show: boolean
  name?: string
  report: ProxyQualityCheckResult | null
}>()

const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()

const columns = computed<TableColumn<ProxyQualityCheckItem>[]>(() => [
  { accessorKey: 'target', header: t('admin.proxies.qualityTableTarget') },
  { accessorKey: 'status', header: t('admin.proxies.qualityTableStatus') },
  { accessorKey: 'http_status', header: 'HTTP' },
  { accessorKey: 'latency_ms', header: t('admin.proxies.qualityTableLatency') },
  { accessorKey: 'message', header: t('admin.proxies.qualityTableMessage') },
])

const qualityStatusColor = (status: string) => {
  if (status === 'pass') return 'success' as const
  if (status === 'warn') return 'warning' as const
  return 'error' as const
}

const qualityStatusLabel = (status: string) => {
  if (status === 'pass') return t('admin.proxies.qualityStatusPass')
  if (status === 'warn') return t('admin.proxies.qualityStatusWarn')
  if (status === 'challenge') return t('admin.proxies.qualityStatusChallenge')
  return t('admin.proxies.qualityStatusFail')
}

const qualityTargetLabel = (target: string) => {
  switch (target) {
    case 'base_connectivity':
      return t('admin.proxies.qualityTargetBase')
    case 'openai':
      return 'OpenAI'
    case 'anthropic':
      return 'Anthropic'
    case 'gemini':
      return 'Gemini'
    case 'grok':
      return 'Grok'
    case 'kimi':
      return 'Kimi'
    case 'zhipu':
      return 'Zhipu GLM'
    case 'deepseek':
      return 'DeepSeek'
    case 'minimax':
      return 'MiniMax'
    default:
      return target
  }
}
</script>
