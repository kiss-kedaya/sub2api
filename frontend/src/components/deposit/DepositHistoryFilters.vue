<template>
  <div class="deposit-filters">
    <div class="deposit-filters__group" role="group" :aria-label="t('deposit.filterStatusLabel')">
      <span class="deposit-filters__label">{{ t('deposit.filterStatusLabel') }}</span>
      <div class="deposit-filters__track">
        <button v-for="option in statusOptions" :key="option.value" type="button" class="deposit-filters__pill"
          :class="{ 'deposit-filters__pill--active': status === option.value }"
          :aria-pressed="status === option.value" :data-testid="'deposit-filter-status-' + option.value"
          @click="emit('update:status', option.value)">
          {{ t(option.labelKey) }}
        </button>
      </div>
    </div>
    <div class="deposit-filters__group deposit-filters__group--range" role="group" :aria-label="t('deposit.filterTimeLabel')">
      <span class="deposit-filters__label">{{ t('deposit.filterTimeLabel') }}</span>
      <div class="deposit-filters__track">
        <button v-for="option in rangeOptions" :key="option.value" type="button" class="deposit-filters__pill"
          :class="{ 'deposit-filters__pill--active': range === option.value }"
          :aria-pressed="range === option.value" :data-testid="'deposit-filter-range-' + option.value"
          @click="emit('update:range', option.value)">
          {{ t(option.labelKey) }}
        </button>
      </div>
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
.deposit-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.625rem 1.25rem;
}

.deposit-filters__group {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}

.deposit-filters__label {
  color: var(--ui-text-muted);
  font-size: 0.75rem;
  font-weight: 500;
  white-space: nowrap;
}

.deposit-filters__track {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.1875rem;
  padding: 0.1875rem;
  border: 1px solid var(--ui-border);
  border-radius: 0.5rem;
  background: var(--ui-bg-muted);
}

.deposit-filters__pill {
  min-height: 1.75rem;
  padding: 0.1875rem 0.65rem;
  border: 1px solid transparent;
  border-radius: 0.375rem;
  background: transparent;
  color: var(--ui-text-muted);
  font-size: 0.75rem;
  font-weight: 500;
  line-height: 1.25rem;
  cursor: pointer;
  transition: color 140ms ease, background-color 140ms ease, border-color 140ms ease, box-shadow 140ms ease;
}

.deposit-filters__pill:focus-visible {
  outline: 2px solid var(--ui-primary);
  outline-offset: 1px;
}

@media (hover: hover) and (pointer: fine) {
  .deposit-filters__pill:hover:not(.deposit-filters__pill--active) {
    color: var(--ui-text-highlighted);
    background: color-mix(in oklab, var(--ui-bg) 60%, transparent);
  }
}

.deposit-filters__pill--active {
  background: var(--ui-bg);
  border-color: color-mix(in oklab, var(--ui-primary) 38%, var(--ui-border));
  color: var(--ui-primary);
  font-weight: 600;
  box-shadow: 0 1px 2px rgb(15 23 42 / 0.06);
}

@media (max-width: 640px) {
  .deposit-filters {
    gap: 0.625rem;
  }

  .deposit-filters__group {
    flex-direction: column;
    align-items: flex-start;
    gap: 0.375rem;
    width: 100%;
  }

  .deposit-filters__pill {
    padding: 0.1875rem 0.5rem;
  }
}

@media (prefers-reduced-motion: reduce) {
  .deposit-filters__pill {
    transition: none;
  }
}
</style>

<style>
html.dark .deposit-filters__track {
  background: var(--ui-bg-elevated);
  border-color: var(--ui-border);
}

html.dark .deposit-filters__pill--active {
  background: color-mix(in oklab, var(--ui-primary) 16%, var(--ui-bg));
  border-color: color-mix(in oklab, var(--ui-primary) 45%, transparent);
  color: var(--ui-primary);
  box-shadow: none;
}
</style>
