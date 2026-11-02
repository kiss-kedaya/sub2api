<template>
  <section class="mt-4 space-y-3">
    <div class="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
      <div class="min-w-0 flex-1 sm:max-w-2xl">
        <h5 class="text-xs font-medium text-highlighted">
          {{ t('admin.channels.form.timePricing') }}
        </h5>
        <div class="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-2">
          <UFormField :label="t('admin.channels.form.timezone')" size="xs" :ui="fieldUi" class="min-w-0">
            <Select
              :model-value="modelValue.timezone"
              :options="timezoneOptions"
              :aria-label="t('admin.channels.form.timezone')"
              data-testid="time-pricing-timezone"
              searchable
              creatable
              class="w-full"
              @update:model-value="updateTimezone"
            />
          </UFormField>
          <UFormField :label="t('admin.channels.form.timePricingDayScope')" size="xs" :ui="fieldUi" class="min-w-0">
            <Select
              :model-value="modelValue.weekdays_only"
              :options="dayScopeOptions"
              :aria-label="t('admin.channels.form.timePricingDayScope')"
              data-testid="time-pricing-day-scope"
              class="w-full"
              @update:model-value="updateDayScope"
            />
          </UFormField>
        </div>
      </div>
      <UButton
        icon="i-lucide-plus"
        color="neutral"
        variant="ghost"
        size="xs"
        class="self-start sm:self-end sm:mb-1"
        data-testid="add-time-period"
        :label="t('admin.channels.form.addTimePeriod')"
        @click="addPeriod"
      />
    </div>

    <div v-if="modelValue.periods.length > 0" class="space-y-2">
      <div
        v-for="(period, index) in modelValue.periods"
        :key="index"
        class="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_2rem] sm:items-end"
      >
        <UFormField :label="t('admin.channels.form.startTime')" size="xs" :ui="fieldUi" class="min-w-0">
          <UInput
            :id="`${inputIdPrefix}-start-${index}`"
            :model-value="period.start_time"
            type="text"
            inputmode="numeric"
            maxlength="8"
            placeholder="HH:mm:ss"
            pattern="[0-9]{2}:[0-9]{2}:[0-9]{2}"
            autocomplete="off"
            size="sm"
            class="w-full"
            @input="updatePeriod(index, 'start_time', normalizeClockTime(($event.target as HTMLInputElement).value))"
          />
        </UFormField>
        <UFormField :label="t('admin.channels.form.endTime')" size="xs" :ui="fieldUi" class="min-w-0">
          <UInput
            :id="`${inputIdPrefix}-end-${index}`"
            :model-value="period.end_time"
            type="text"
            inputmode="numeric"
            maxlength="8"
            placeholder="HH:mm:ss"
            pattern="[0-9]{2}:[0-9]{2}:[0-9]{2}"
            autocomplete="off"
            size="sm"
            class="w-full"
            @input="updatePeriod(index, 'end_time', normalizeClockTime(($event.target as HTMLInputElement).value))"
          />
        </UFormField>
        <UFormField :label="t('admin.channels.form.multiplier')" size="xs" :ui="fieldUi" class="min-w-0">
          <UInput
            :id="`${inputIdPrefix}-multiplier-${index}`"
            :model-value="String(period.multiplier)"
            type="number"
            min="0.01"
            step="0.01"
            size="sm"
            class="w-full"
            @input="updatePeriod(index, 'multiplier', ($event.target as HTMLInputElement).value)"
            @blur="formatMultiplier(index, ($event.target as HTMLInputElement).value)"
          />
        </UFormField>
        <UButton
          icon="i-lucide-trash-2"
          color="neutral"
          variant="ghost"
          size="sm"
          square
          class="justify-center"
          :title="t('admin.channels.form.removeTimePeriod')"
          :aria-label="t('admin.channels.form.removeTimePeriod')"
          :data-testid="`remove-time-period-${index}`"
          @click="removePeriod(index)"
        />
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, getCurrentInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import {
  COMMON_TIMEZONES,
  formatTimezoneOffset,
  isValidTimePricingMultiplier,
  type TimePricingFormEntry,
  type TimePricingPeriodFormEntry,
} from './types'

const { t } = useI18n()

const props = defineProps<{ modelValue: TimePricingFormEntry }>()
const emit = defineEmits<{ 'update:modelValue': [value: TimePricingFormEntry] }>()
const inputIdPrefix = `time-pricing-${getCurrentInstance()?.uid}`
const fieldUi = { label: 'font-normal text-muted', container: 'mt-1' }

const timezoneOptions = COMMON_TIMEZONES.map(value => {
  const offset = formatTimezoneOffset(value)
  return { value, label: offset ? `${value} (${offset})` : value }
})

const dayScopeOptions = computed(() => [
  { value: false, label: t('admin.channels.form.timePricingEveryDay') },
  { value: true, label: t('admin.channels.form.timePricingWeekdaysOnly') },
])

function updateTimezone(value: string | number | boolean | null) {
  emit('update:modelValue', { ...props.modelValue, timezone: String(value ?? '') })
}

function updateDayScope(value: string | number | boolean | null) {
  emit('update:modelValue', { ...props.modelValue, weekdays_only: value === true })
}

function normalizeClockTime(value: string): string {
  const normalized = value.replace(/：/g, ':')
  return normalized === '24:00:00' ? '00:00:00' : normalized
}

function addPeriod() {
  emit('update:modelValue', {
    ...props.modelValue,
    periods: [
      ...props.modelValue.periods,
      { start_time: '', end_time: '', multiplier: '1.00' },
    ],
  })
}

function updatePeriod(index: number, field: keyof TimePricingPeriodFormEntry, value: string) {
  const periods = props.modelValue.periods.map((period, current) =>
    current === index ? { ...period, [field]: value } : period)
  emit('update:modelValue', { ...props.modelValue, periods })
}

function formatMultiplier(index: number, value: string) {
  if (!isValidTimePricingMultiplier(value)) return
  updatePeriod(index, 'multiplier', Number(value).toFixed(2))
}

function removePeriod(index: number) {
  emit('update:modelValue', {
    ...props.modelValue,
    periods: props.modelValue.periods.filter((_period, current) => current !== index),
  })
}
</script>
