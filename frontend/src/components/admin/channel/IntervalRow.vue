<template>
  <div
    class="flex items-start gap-2 rounded-md p-2 ring ring-inset"
    :class="isEmpty ? 'bg-error/5 ring-error/40' : 'bg-default ring-default'"
  >
    <!-- Token mode: context range + prices ($/MTok) -->
    <template v-if="mode === 'token'">
      <div class="pricing-interval-grid grid min-w-0 flex-1 gap-2">
        <UFormField :label="t('admin.channels.form.minTokens')" size="xs" :ui="fieldUi">
          <UInput :model-value="String(interval.min_tokens ?? '')" @input="emitField('min_tokens', toInt(($event.target as HTMLInputElement).value))"
            type="number" min="0" size="sm" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.channels.form.maxTokens')" :hint="t('admin.channels.form.inclusive')" size="xs" :ui="fieldUi">
          <UInput :model-value="String(interval.max_tokens ?? '')" @input="emitField('max_tokens', toIntOrNull(($event.target as HTMLInputElement).value))"
            type="number" min="0" size="sm" class="w-full" :placeholder="'∞'" />
        </UFormField>
        <UFormField :label="t('admin.channels.form.inputPrice')" hint="$/M" :required="isEmpty" size="xs" :ui="fieldUi">
          <UInput :model-value="String(interval.input_price ?? '')" @input="emitField('input_price', ($event.target as HTMLInputElement).value)"
            type="number" step="any" min="0" size="sm" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.channels.form.outputPrice')" hint="$/M" :required="isEmpty" size="xs" :ui="fieldUi">
          <UInput :model-value="String(interval.output_price ?? '')" @input="emitField('output_price', ($event.target as HTMLInputElement).value)"
            type="number" step="any" min="0" size="sm" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.channels.form.cacheWrite5mPriceShort')" hint="$/M" size="xs" :ui="fieldUi">
          <UInput :model-value="String(interval.cache_write_price ?? '')" @input="emitField('cache_write_price', ($event.target as HTMLInputElement).value)"
            type="number" step="any" min="0" size="sm" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.channels.form.cacheWrite1hPriceShort')" hint="$/M" size="xs" :ui="fieldUi">
          <UInput :model-value="String(interval.cache_write_1h_price ?? '')" @input="emitField('cache_write_1h_price', ($event.target as HTMLInputElement).value)"
            type="number" step="any" min="0" size="sm" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.channels.form.cacheReadPriceShort')" hint="$/M" size="xs" :ui="fieldUi">
          <UInput :model-value="String(interval.cache_read_price ?? '')" @input="emitField('cache_read_price', ($event.target as HTMLInputElement).value)"
            type="number" step="any" min="0" size="sm" class="w-full" />
        </UFormField>
        <template v-if="enableMultipliers">
          <UFormField :label="t('admin.channels.form.inputMultiplier')" size="xs" :ui="fieldUi">
            <UInput :model-value="String(interval.input_multiplier ?? '')" @input="emitField('input_multiplier', ($event.target as HTMLInputElement).value)"
              type="number" step="any" min="0.000001" size="sm" class="w-full" />
          </UFormField>
          <UFormField :label="t('admin.channels.form.outputMultiplier')" size="xs" :ui="fieldUi">
            <UInput :model-value="String(interval.output_multiplier ?? '')" @input="emitField('output_multiplier', ($event.target as HTMLInputElement).value)"
              type="number" step="any" min="0.000001" size="sm" class="w-full" />
          </UFormField>
          <UFormField :label="t('admin.channels.form.cacheWriteMultiplier')" size="xs" :ui="fieldUi">
            <UInput :model-value="String(interval.cache_write_multiplier ?? '')" @input="emitField('cache_write_multiplier', ($event.target as HTMLInputElement).value)"
              type="number" step="any" min="0.000001" size="sm" class="w-full" />
          </UFormField>
          <UFormField :label="t('admin.channels.form.cacheReadMultiplier')" size="xs" :ui="fieldUi">
            <UInput :model-value="String(interval.cache_read_multiplier ?? '')" @input="emitField('cache_read_multiplier', ($event.target as HTMLInputElement).value)"
              type="number" step="any" min="0.000001" size="sm" class="w-full" />
          </UFormField>
        </template>
      </div>
    </template>

    <!-- Per-request / Image mode: tier label + context range + price -->
    <template v-else>
      <UFormField
        :label="mode === 'image' ? t('admin.channels.form.resolution') : t('admin.channels.form.tierLabel')"
        size="xs"
        :ui="fieldUi"
        class="w-24"
      >
        <UInput :model-value="String(interval.tier_label ?? '')" @input="emitField('tier_label', ($event.target as HTMLInputElement).value)"
          type="text" size="sm" class="w-full" :placeholder="mode === 'image' ? '1K / 2K / 4K' : ''" />
      </UFormField>
      <UFormField :label="t('admin.channels.form.minTokens')" size="xs" :ui="fieldUi" class="w-20">
        <UInput :model-value="String(interval.min_tokens ?? '')" @input="emitField('min_tokens', toInt(($event.target as HTMLInputElement).value))"
          type="number" min="0" size="sm" class="w-full" />
      </UFormField>
      <UFormField :label="t('admin.channels.form.maxTokens')" :hint="t('admin.channels.form.inclusive')" size="xs" :ui="fieldUi" class="w-20">
        <UInput :model-value="String(interval.max_tokens ?? '')" @input="emitField('max_tokens', toIntOrNull(($event.target as HTMLInputElement).value))"
          type="number" min="0" size="sm" class="w-full" :placeholder="'∞'" />
      </UFormField>
      <UFormField :label="t('admin.channels.form.perRequestPrice')" hint="$" :required="isEmpty" size="xs" :ui="fieldUi" class="flex-1">
        <UInput :model-value="String(interval.per_request_price ?? '')" @input="emitField('per_request_price', ($event.target as HTMLInputElement).value)"
          type="number" step="any" min="0" size="sm" class="w-full" />
      </UFormField>
    </template>

    <UButton
      icon="i-lucide-x"
      color="neutral"
      variant="ghost"
      size="xs"
      class="mt-4 shrink-0"
      :aria-label="t('common.remove')"
      :title="t('common.remove')"
      @click="emit('remove')"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { IntervalFormEntry } from './types'
import type { BillingMode } from '@/api/admin/channels'

const { t } = useI18n()

// Compact field labels for the dense tier grid: muted, no bold, unit as a dimmed hint.
const fieldUi = { label: 'font-normal text-muted', hint: 'text-dimmed', container: 'mt-0.5' }

const props = defineProps<{
  interval: IntervalFormEntry
  mode: BillingMode
  enableMultipliers?: boolean
}>()

const emit = defineEmits<{
  update: [interval: IntervalFormEntry]
  remove: []
}>()

// 检测所有价格字段是否都为空
const isEmpty = computed(() => {
  const iv = props.interval
  return (iv.input_price == null || iv.input_price === '') &&
    (iv.output_price == null || iv.output_price === '') &&
    (iv.cache_write_price == null || iv.cache_write_price === '') &&
    (iv.cache_write_1h_price == null || iv.cache_write_1h_price === '') &&
    (iv.cache_read_price == null || iv.cache_read_price === '') &&
    (iv.input_multiplier == null || iv.input_multiplier === '') &&
    (iv.output_multiplier == null || iv.output_multiplier === '') &&
    (iv.cache_write_multiplier == null || iv.cache_write_multiplier === '') &&
    (iv.cache_read_multiplier == null || iv.cache_read_multiplier === '') &&
    (iv.per_request_price == null || iv.per_request_price === '')
})

function emitField(field: keyof IntervalFormEntry, value: string | number | null) {
  emit('update', { ...props.interval, [field]: value === '' ? null : value })
}

function toInt(val: string): number {
  const n = parseInt(val, 10)
  return isNaN(n) ? 0 : n
}

function toIntOrNull(val: string): number | null {
  if (val === '') return null
  const n = parseInt(val, 10)
  return isNaN(n) ? null : n
}
</script>

<style scoped>
.pricing-interval-grid {
  grid-template-columns: repeat(auto-fit, minmax(7.5rem, 1fr));
}
</style>
