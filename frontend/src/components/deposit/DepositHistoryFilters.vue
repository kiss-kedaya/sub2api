<template>
  <div class="deposit-filters">
    <div class="deposit-filters__group" role="group" :aria-label="t('deposit.filterStatusLabel')">
      <span class="deposit-filters__label">{{ t('deposit.filterStatusLabel') }}</span>
      <button v-for="option in statusOptions" :key="option.value" type="button" class="deposit-filters__pill"
        :class="{ 'deposit-filters__pill--active': status === option.value }"
        :aria-pressed="status === option.value" :data-testid="'deposit-filter-status-' + option.value"
        @click="emit('update:status', option.value)">
        {{ t(option.labelKey) }}
      </button>
    </div>
    <div class="deposit-filters__group" role="group" :aria-label="t('deposit.filterTimeLabel')">
      <span class="deposit-filters__label">{{ t('deposit.filterTimeLabel') }}</span>
      <button v-for="option in rangeOptions" :key="option.value" type="button" class="deposit-filters__pill"
        :class="{ 'deposit-filters__pill--active': range === option.value }"
        :aria-pressed="range === option.value" :data-testid="'deposit-filter-range-' + option.value"
        @click="emit('update:range', option.value)">
        {{ t(option.labelKey) }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import {
  DEPOSIT_STATUS_FILTERS, DEPOSIT_TIME_RANGES,
  type DepositStatusFilter, type DepositTimeRange
} from '@/utils/depositHistoryFilter'

// Local-only filters: the history list is small and already in memory, so
// changing a filter only re-renders the table and never re-reads the API.
defineProps<{ status: DepositStatusFilter; range: DepositTimeRange }>()
const emit = defineEmits<{
  (event: 'update:status', value: DepositStatusFilter): void
  (event: 'update:range', value: DepositTimeRange): void
}>()

const { t } = useI18n()

// Labels are keyed by the exported unions, so a new filter value is a compile
// error here instead of a silently unlabelled button.
const STATUS_LABEL_KEYS: Record<DepositStatusFilter, string> = {
  all: 'deposit.filterStatusAll',
  credited: 'deposit.filterStatusCredited',
  provisional: 'deposit.filterStatusProvisional',
  revoked: 'deposit.filterStatusRevoked',
  ignored: 'deposit.filterStatusIgnored'
}

const RANGE_LABEL_KEYS: Record<DepositTimeRange, string> = {
  all: 'deposit.filterTimeAll',
  '7d': 'deposit.filterTime7d',
  '30d': 'deposit.filterTime30d',
  '90d': 'deposit.filterTime90d'
}

const statusOptions = DEPOSIT_STATUS_FILTERS.map(value => ({ value, labelKey: STATUS_LABEL_KEYS[value] }))
const rangeOptions = DEPOSIT_TIME_RANGES.map(value => ({ value, labelKey: RANGE_LABEL_KEYS[value] }))
</script>

<style scoped>
.deposit-filters { display: flex; flex-wrap: wrap; gap: .5rem 1.25rem; margin-bottom: 1rem; }
.deposit-filters__group { display: flex; flex-wrap: wrap; align-items: center; gap: .375rem; }
.deposit-filters__label { color: var(--signal-muted, #8c99a6); font-size: .6875rem; }
.deposit-filters__pill {
  min-height: 1.875rem; padding: .25rem .625rem; border: 1px solid var(--ui-border);
  border-radius: 999px; font-size: .75rem; transition: border-color .15s, background .15s;
}
.deposit-filters__pill:hover { background: var(--signal-accent-soft, #0891b212); }
.deposit-filters__pill--active { border-color: var(--signal-accent, #0891b2); background: var(--signal-accent-soft, #0891b218); font-weight: 600; }
</style>
