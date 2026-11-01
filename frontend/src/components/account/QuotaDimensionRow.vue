<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import QuotaNotifyToggle from './QuotaNotifyToggle.vue'
import type { QuotaThresholdType, QuotaResetMode } from '@/constants/account'

const { t } = useI18n()

const props = defineProps<{
  dim: 'daily' | 'weekly' | 'total'
  label: string
  limit: number | null
  quotaNotifyGlobalEnabled: boolean
  notifyEnabled: boolean | null
  notifyThreshold: number | null
  notifyThresholdType: QuotaThresholdType | null
  // Reset mode (only for daily/weekly, null for total)
  resetMode: QuotaResetMode | null
  resetHour: number | null
  resetDay: number | null  // weekly only
  resetTimezone: string | null
  hintRolling: string
  hintFixed: string
  // Shared options passed from parent
  hourOptions: number[]
  dayOptions: { value: number; key: string }[]
  timezoneOptions?: string[]
}>()

const emit = defineEmits<{
  'update:limit': [value: number | null]
  'update:notifyEnabled': [value: boolean | null]
  'update:notifyThreshold': [value: number | null]
  'update:notifyThresholdType': [value: QuotaThresholdType | null]
  'update:resetMode': [value: QuotaResetMode | null]
  'update:resetHour': [value: number | null]
  'update:resetDay': [value: number | null]
  'update:resetTimezone': [value: string | null]
}>()

const hasResetMode = props.dim !== 'total'

const limitChanged = (value: unknown) => {
  const raw = typeof value === 'number' ? value : Number.parseFloat(String(value))
  emit('update:limit', Number.isNaN(raw) ? null : raw)
}

const modeChanged = (value: unknown) => {
  const val = value as QuotaResetMode
  emit('update:resetMode', val)
  if (val === 'fixed') {
    if (props.resetHour == null) emit('update:resetHour', 0)
    if (props.dim === 'weekly' && props.resetDay == null) emit('update:resetDay', 1)
    if (!props.resetTimezone) emit('update:resetTimezone', 'UTC')
  }
}

function getTimezoneOffsetLabel(tz: string): string {
  try {
    const dtf = new Intl.DateTimeFormat('en-US', { timeZone: tz, timeZoneName: 'shortOffset' })
    const parts = dtf.formatToParts(new Date())
    const tzPart = parts.find(p => p.type === 'timeZoneName')
    return tzPart ? (tzPart.value === 'GMT' ? 'GMT+0' : tzPart.value) : ''
  } catch {
    return ''
  }
}
</script>

<template>
  <div>
    <!-- Title row (only when global notify is enabled) -->
    <div v-if="quotaNotifyGlobalEnabled" class="flex items-center gap-2 mb-1">
      <span class="text-xs font-medium text-default flex-1 min-w-0">{{ label }}</span>
      <span v-if="limit && limit > 0" class="text-xs font-medium text-default flex-1 min-w-0">{{ t('admin.accounts.quotaNotify.alert') }}</span>
    </div>
    <label v-else class="text-xs font-medium text-default mb-1 block">{{ label }}</label>

    <!-- Input row -->
    <div class="flex items-center gap-2">
      <div :class="['relative', quotaNotifyGlobalEnabled ? 'flex-1 min-w-0' : 'flex-1']">
        <UInput :model-value="limit == null ? '' : String(limit)" type="number" min="0" step="0.01" class="w-full" :placeholder="t('admin.accounts.quotaLimitPlaceholder')" @update:model-value="limitChanged">
          <template #leading><span class="text-sm text-muted">$</span></template>
        </UInput>
      </div>
      <QuotaNotifyToggle
        v-if="quotaNotifyGlobalEnabled && limit && limit > 0"
        class="flex-1 min-w-0"
        :enabled="notifyEnabled" :threshold="notifyThreshold" :threshold-type="notifyThresholdType"
        @update:enabled="emit('update:notifyEnabled', $event)" @update:threshold="emit('update:notifyThreshold', $event)" @update:threshold-type="emit('update:notifyThresholdType', $event)"
      />
    </div>

    <!-- Reset mode row (daily/weekly only) -->
    <div v-if="hasResetMode" class="mt-1 flex items-center gap-2 flex-wrap">
      <label class="text-xs text-muted whitespace-nowrap">{{ t('admin.accounts.quotaResetMode') }}</label>
      <USelect :model-value="resetMode || 'rolling'" :items="[{ value: 'rolling', label: t('admin.accounts.quotaResetModeRolling') }, { value: 'fixed', label: t('admin.accounts.quotaResetModeFixed') }]" size="sm" class="min-w-28" @update:model-value="modeChanged" />
      <template v-if="resetMode === 'fixed'">
        <!-- Weekly: day of week selector -->
        <template v-if="dim === 'weekly'">
          <label class="text-xs text-muted whitespace-nowrap">{{ t('admin.accounts.quotaWeeklyResetDay') }}</label>
          <USelect :model-value="resetDay ?? 1" :items="dayOptions.map(d => ({ value: d.value, label: t('admin.accounts.dayOfWeek.' + d.key) }))" size="sm" class="w-28" @update:model-value="emit('update:resetDay', Number($event))" />
        </template>
        <label class="text-xs text-muted whitespace-nowrap">{{ t('admin.accounts.quotaResetHour') }}</label>
        <USelect :model-value="resetHour ?? 0" :items="hourOptions.map(h => ({ value: h, label: String(h).padStart(2, '0') + ':00' }))" size="sm" class="w-24" @update:model-value="emit('update:resetHour', Number($event))" />
        <template v-if="timezoneOptions && timezoneOptions.length > 0">
          <USelect :model-value="resetTimezone || 'UTC'" :items="(timezoneOptions || []).map(tz => ({ value: tz, label: tz + ' (' + getTimezoneOffsetLabel(tz) + ')' }))" size="sm" class="min-w-40" @update:model-value="emit('update:resetTimezone', String($event))" />
        </template>
      </template>
      <span class="text-xs text-muted">
        <template v-if="resetMode === 'fixed'">{{ hintFixed }}</template>
        <template v-else>{{ hintRolling }}</template>
      </span>
    </div>

    <!-- Total dimension hint (no reset mode) -->
    <p v-if="!hasResetMode" class="input-hint mb-0 text-[11px]">{{ hintRolling }}</p>
  </div>
</template>
