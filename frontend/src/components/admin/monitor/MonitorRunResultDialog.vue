<template>
  <BaseDialog
    :show="show"
    :title="t('admin.channelMonitor.runResultTitle')"
    width="normal"
    @close="$emit('close')"
  >
    <ul class="flex flex-col divide-y divide-default">
      <li
        v-for="r in results"
        :key="r.model"
        class="flex items-center justify-between gap-3 py-2.5 text-sm first:pt-0 last:pb-0"
      >
        <div class="flex min-w-0 flex-col">
          <span class="font-medium text-highlighted">{{ formatMonitorModel(r.model) }}</span>
          <span v-if="r.message" class="text-xs text-muted">{{ r.message }}</span>
          <MonitorQuotaView :snapshot="r.quota" class="mt-1" />
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <UBadge :color="monitorStatusColor(r.status)" variant="subtle" size="sm">{{ statusLabel(r.status) }}</UBadge>
          <span class="text-xs tabular-nums text-muted">{{ formatLatency(r.latency_ms) }} ms</span>
        </div>
      </li>
    </ul>
    <template #footer>
      <div class="flex justify-end">
        <UButton :label="t('common.close')" @click="$emit('close')" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { monitorStatusColor } from '@/views/admin/ops/utils/templateUi'
import type { CheckResult } from '@/api/admin/channelMonitor'
import BaseDialog from '@/components/common/BaseDialog.vue'
import MonitorQuotaView from '@/components/common/MonitorQuotaView.vue'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'

defineProps<{
  show: boolean
  results: CheckResult[]
}>()

defineEmits<{
  (e: 'close'): void
}>()

const { t } = useI18n()
const { statusLabel, formatLatency, formatMonitorModel } = useChannelMonitorFormat()
</script>
