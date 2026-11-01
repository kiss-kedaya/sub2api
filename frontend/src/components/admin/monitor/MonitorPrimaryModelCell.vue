<template>
  <div class="flex flex-col gap-0.5">
    <div class="flex items-center gap-2">
      <!-- 纯配额模式主模型是占位符 "quota"（数据源是账号不是模型），展示层替换为本地化标签 -->
      <span class="text-sm text-default">{{ formatMonitorModel(row.primary_model) }}</span>
      <HelpTooltip>
      <template #trigger>
        <UBadge :color="monitorStatusColor(row.primary_status)" variant="subtle" size="sm" class="cursor-default">{{ statusLabel(row.primary_status) }}</UBadge>
      </template>
      <div class="flex flex-col gap-2">
        <div class="flex items-center gap-1.5 text-xs font-semibold">
          {{ formatMonitorModel(row.primary_model) }}
          <UBadge :color="monitorStatusColor(row.primary_status)" variant="subtle" size="xs">{{ statusLabel(row.primary_status) }}</UBadge>
        </div>
        <div v-if="(row.extra_models?.length ?? 0) === 0" class="text-[11px] opacity-80">
          {{ t('monitorCommon.extraModelsEmpty') }}
        </div>
        <div v-else class="flex flex-col gap-1">
          <div class="text-[11px] font-semibold uppercase tracking-wide opacity-70">
            {{ t('monitorCommon.extraModelsHeader') }}
          </div>
          <div class="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-x-3 gap-y-1 text-[11px]" role="table">
            <span class="font-medium opacity-70" role="columnheader">{{ t('admin.channelMonitor.columns.primaryModel') }}</span>
            <span class="font-medium opacity-70" role="columnheader">{{ t('admin.channelMonitor.columns.actions') }}</span>
            <span class="font-medium opacity-70" role="columnheader">{{ t('admin.channelMonitor.columns.latency') }}</span>
            <template v-for="m in (row.extra_models_status || [])" :key="m.model">
              <span class="truncate" role="cell">{{ m.model }}</span>
              <span role="cell"><UBadge :color="monitorStatusColor(m.status)" variant="subtle" size="xs">{{ statusLabel(m.status) }}</UBadge></span>
              <span class="tabular-nums" role="cell">{{ formatLatency(m.latency_ms) }}</span>
            </template>
          </div>
        </div>
      </div>
      </HelpTooltip>
    </div>
    <!-- 配额模式监控：主模型行内联展示最新用量/余额快照（管理端不受用户端开关限制） -->
    <MonitorQuotaView :snapshot="row.latest_quota" />
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { monitorStatusColor } from '@/views/admin/ops/utils/templateUi'
import type { ChannelMonitor } from '@/api/admin/channelMonitor'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import MonitorQuotaView from '@/components/common/MonitorQuotaView.vue'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'

defineProps<{
  row: ChannelMonitor
}>()

const { t } = useI18n()
const { statusLabel, formatLatency, formatMonitorModel } = useChannelMonitorFormat()
</script>
