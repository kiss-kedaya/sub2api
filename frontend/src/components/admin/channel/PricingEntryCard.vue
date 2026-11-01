<template>
  <UCollapsible
    :open="!collapsed"
    :unmount-on-hide="false"
    class="rounded-lg bg-elevated/50 px-3 py-2.5 ring ring-inset ring-default"
    @update:open="collapsed = !$event"
  >
    <!-- Summary header (the collapsible trigger) -->
    <div class="flex cursor-pointer select-none items-center gap-2">
      <UIcon
        name="i-lucide-chevron-right"
        class="size-4 shrink-0 text-dimmed transition-transform duration-200"
        :class="{ 'rotate-90': !collapsed }"
      />

      <!-- Collapsed: model tags + billing mode -->
      <div v-if="collapsed" class="flex min-w-0 flex-1 items-center gap-2 overflow-hidden">
        <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1">
          <UBadge
            v-for="(m, i) in entry.models.slice(0, 3)"
            :key="i"
            :label="m"
            color="neutral"
            variant="subtle"
            size="sm"
            class="shrink-0"
          />
          <span v-if="entry.models.length > 3" class="whitespace-nowrap text-xs text-muted">
            +{{ entry.models.length - 3 }}
          </span>
          <span v-if="entry.models.length === 0" class="text-xs text-muted">
            {{ t('admin.channels.form.noModels') }}
          </span>
        </div>
        <UBadge :label="billingModeLabel" color="primary" variant="subtle" size="sm" class="shrink-0" />
      </div>

      <!-- Expanded: section label -->
      <div v-else class="flex-1 text-xs font-medium text-muted">
        {{ t('admin.channels.form.pricingEntry') }}
      </div>

      <UButton
        icon="i-lucide-trash-2"
        color="neutral"
        variant="ghost"
        size="xs"
        class="shrink-0"
        :aria-label="t('common.delete')"
        :title="t('common.delete')"
        @click.stop="emit('remove')"
      />
    </div>

    <template #content>
        <!-- Models + Billing Mode -->
        <div class="mt-3 flex items-start gap-2">
          <UFormField :label="t('admin.channels.form.models')" required size="xs" :ui="fieldUi" class="flex-1">
            <ModelTagInput
              :models="entry.models"
              :platform="props.platform"
              @update:models="onModelsUpdate($event)"
              :placeholder="t('admin.channels.form.modelsPlaceholder')"
            />
          </UFormField>
          <UFormField :label="t('admin.channels.form.billingMode')" size="xs" :ui="fieldUi" class="w-40">
            <Select
              :modelValue="entry.billing_mode"
              @update:modelValue="emit('update', {
                ...entry,
                billing_mode: $event as BillingMode,
                intervals: [],
                time_pricing: { ...entry.time_pricing, periods: [] },
              })"
              :options="billingModeOptions"
            />
          </UFormField>
        </div>

        <!-- Token mode -->
        <div v-if="entry.billing_mode === 'token'">
          <!-- Default prices (fallback when no interval matches) -->
          <h5 class="mt-4 text-xs font-medium text-highlighted">
            {{ t('admin.channels.form.defaultPrices') }}
            <span class="ms-1 font-normal text-dimmed">$/MTok</span>
          </h5>
          <div class="pricing-default-grid mt-1.5 grid gap-2">
            <UFormField :label="t('admin.channels.form.inputPrice')" size="xs" :ui="fieldUi">
              <UInput :model-value="String(entry.input_price ?? '')" @input="emitField('input_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" size="sm" class="w-full" :placeholder="t('admin.channels.form.pricePlaceholder')" />
            </UFormField>
            <UFormField :label="t('admin.channels.form.outputPrice')" size="xs" :ui="fieldUi">
              <UInput :model-value="String(entry.output_price ?? '')" @input="emitField('output_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" size="sm" class="w-full" :placeholder="t('admin.channels.form.pricePlaceholder')" />
            </UFormField>
            <UFormField :label="t('admin.channels.form.cacheWrite5mPrice')" size="xs" :ui="fieldUi">
              <UInput :model-value="String(entry.cache_write_price ?? '')" @input="emitField('cache_write_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" size="sm" class="w-full" :placeholder="t('admin.channels.form.pricePlaceholder')" />
            </UFormField>
            <UFormField :label="t('admin.channels.form.cacheWrite1hPrice')" size="xs" :ui="fieldUi">
              <UInput :model-value="String(entry.cache_write_1h_price ?? '')" @input="emitField('cache_write_1h_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" size="sm" class="w-full" :placeholder="t('admin.channels.form.pricePlaceholder')" />
            </UFormField>
            <UFormField :label="t('admin.channels.form.cacheReadPrice')" size="xs" :ui="fieldUi">
              <UInput :model-value="String(entry.cache_read_price ?? '')" @input="emitField('cache_read_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" size="sm" class="w-full" :placeholder="t('admin.channels.form.pricePlaceholder')" />
            </UFormField>
            <UFormField :label="t('admin.channels.form.imageInputPrice')" size="xs" :ui="fieldUi">
              <UInput :model-value="String(entry.image_input_price ?? '')" @input="emitField('image_input_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" size="sm" class="w-full" :placeholder="t('admin.channels.form.pricePlaceholder')" />
            </UFormField>
            <UFormField :label="t('admin.channels.form.imageTokenPrice')" size="xs" :ui="fieldUi">
              <UInput :model-value="String(entry.image_output_price ?? '')" @input="emitField('image_output_price', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0" size="sm" class="w-full" :placeholder="t('admin.channels.form.pricePlaceholder')" />
            </UFormField>
          </div>

          <div v-if="enableTierMultipliers" class="mt-3 grid max-w-2xl grid-cols-1 gap-2 sm:grid-cols-3">
            <UFormField :label="t('admin.channels.form.fastMultiplier')" size="xs" :ui="fieldUi">
              <UInput :model-value="String(entry.fast_multiplier ?? '')" @input="emitField('fast_multiplier', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0.000001" size="sm" class="w-full" :placeholder="t('admin.channels.form.multiplierPlaceholder')" />
            </UFormField>
            <UFormField :label="t('admin.channels.form.flexMultiplier')" size="xs" :ui="fieldUi">
              <UInput :model-value="String(entry.flex_multiplier ?? '')" @input="emitField('flex_multiplier', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0.000001" size="sm" class="w-full" :placeholder="t('admin.channels.form.multiplierPlaceholder')" />
            </UFormField>
            <UFormField :label="t('admin.channels.form.maxReasoningEffortMultiplier')" size="xs" :ui="fieldUi">
              <UInput :model-value="String(entry.max_reasoning_effort_multiplier ?? '')" @input="emitField('max_reasoning_effort_multiplier', ($event.target as HTMLInputElement).value)"
                type="number" step="any" min="0.000001" size="sm" class="w-full" :placeholder="maxReasoningEffortMultiplierPlaceholder" />
            </UFormField>
          </div>

          <!-- Channel token intervals; the group long-context toggle controls whether tiers apply. -->
          <div v-if="!hideTokenIntervals" class="mt-4">
            <div class="flex items-center justify-between">
              <h5 class="text-xs font-medium text-highlighted">
                {{ t('admin.channels.form.intervals') }}
                <span class="ms-1 font-normal text-dimmed">(min, max]</span>
              </h5>
              <UButton icon="i-lucide-plus" color="neutral" variant="ghost" size="xs" :label="t('admin.channels.form.addInterval')" @click="addInterval" />
            </div>
            <div v-if="entry.intervals && entry.intervals.length > 0" class="mt-2 space-y-2">
              <IntervalRow
                v-for="(iv, idx) in entry.intervals"
                :key="idx"
                :interval="iv"
                :mode="entry.billing_mode"
                :enable-multipliers="enableTierMultipliers"
                @update="updateInterval(idx, $event)"
                @remove="removeInterval(idx)"
              />
            </div>
          </div>

          <TimePricingSection
            v-if="enableTimePricing"
            :model-value="entry.time_pricing"
            @update:model-value="emit('update', { ...entry, time_pricing: $event })"
          />
        </div>

        <!-- Per-request mode -->
        <div v-else-if="entry.billing_mode === 'per_request'">
          <!-- Default per-request price -->
          <UFormField :label="t('admin.channels.form.defaultPerRequestPrice')" hint="$" size="xs" :ui="sectionFieldUi" class="mt-4 w-48">
            <UInput :model-value="String(entry.per_request_price ?? '')" @input="emitField('per_request_price', ($event.target as HTMLInputElement).value)"
              type="number" step="any" min="0" size="sm" class="w-full" :placeholder="t('admin.channels.form.pricePlaceholder')" />
          </UFormField>

          <!-- Tiers -->
          <div class="mt-4 flex items-center justify-between">
            <h5 class="text-xs font-medium text-highlighted">
              {{ t('admin.channels.form.requestTiers') }}
            </h5>
            <UButton icon="i-lucide-plus" color="neutral" variant="ghost" size="xs" :label="t('admin.channels.form.addTier')" @click="addInterval" />
          </div>
          <div v-if="entry.intervals && entry.intervals.length > 0" class="mt-2 space-y-2">
            <IntervalRow
              v-for="(iv, idx) in entry.intervals"
              :key="idx"
              :interval="iv"
              :mode="entry.billing_mode"
              @update="updateInterval(idx, $event)"
              @remove="removeInterval(idx)"
            />
          </div>
          <p v-else class="mt-1 text-xs text-muted">
            {{ t('admin.channels.form.noTiersYet') }}
          </p>
        </div>

        <!-- Image/video mode -->
        <div v-else-if="entry.billing_mode === 'image' || entry.billing_mode === 'video'">
          <!-- Default image price (per-request, same as per_request mode) -->
          <UFormField
            :label="entry.billing_mode === 'video' ? t('admin.channels.form.defaultVideoPrice') : t('admin.channels.form.defaultImagePrice')"
            hint="$"
            size="xs"
            :ui="sectionFieldUi"
            class="mt-4 w-48"
          >
            <UInput :model-value="String(entry.per_request_price ?? '')" @input="emitField('per_request_price', ($event.target as HTMLInputElement).value)"
              type="number" step="any" min="0" size="sm" class="w-full" :placeholder="t('admin.channels.form.pricePlaceholder')" />
          </UFormField>

          <!-- Image tiers -->
          <div class="mt-4 flex items-center justify-between">
            <h5 class="text-xs font-medium text-highlighted">
              {{ entry.billing_mode === 'video' ? t('admin.channels.form.videoTiers') : t('admin.channels.form.imageTiers') }}
            </h5>
            <UButton icon="i-lucide-plus" color="neutral" variant="ghost" size="xs" :label="t('admin.channels.form.addTier')" @click="addMediaTier" />
          </div>
          <div v-if="entry.intervals && entry.intervals.length > 0" class="mt-2 space-y-2">
            <IntervalRow
              v-for="(iv, idx) in entry.intervals"
              :key="idx"
              :interval="iv"
              :mode="entry.billing_mode"
              @update="updateInterval(idx, $event)"
              @remove="removeInterval(idx)"
            />
          </div>
        </div>
    </template>
  </UCollapsible>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import IntervalRow from './IntervalRow.vue'
import ModelTagInput from './ModelTagInput.vue'
import TimePricingSection from './TimePricingSection.vue'
import type { PricingFormEntry, IntervalFormEntry } from './types'
import { perTokenToMTok } from './types'
import type { BillingMode } from '@/api/admin/channels'
import channelsAPI from '@/api/admin/channels'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  entry: PricingFormEntry
  platform?: string
  hideTokenIntervals?: boolean
  enableTimePricing?: boolean
  enableTierMultipliers?: boolean
}>(), {
  hideTokenIntervals: false,
  enableTimePricing: false,
  enableTierMultipliers: false,
})

const emit = defineEmits<{
  update: [entry: PricingFormEntry]
  remove: []
}>()

// Collapse state: entries with existing models default to collapsed
const collapsed = ref(props.entry.models.length > 0)

// Price inputs: quiet labels; section-level fields (a single default price) read as a heading.
const fieldUi = { label: 'font-normal text-muted', container: 'mt-0.5' }
const sectionFieldUi = { label: 'text-highlighted', hint: 'text-dimmed', container: 'mt-1.5' }

const billingModeOptions = computed(() => [
  { value: 'token', label: t('admin.channels.billingMode.token') },
  { value: 'per_request', label: t('admin.channels.billingMode.perRequest') },
  { value: 'image', label: t('admin.channels.billingMode.image') },
  { value: 'video', label: t('admin.channels.billingMode.video') }
])

const billingModeLabel = computed(() => {
  const opt = billingModeOptions.value.find(o => o.value === props.entry.billing_mode)
  return opt ? opt.label : props.entry.billing_mode
})

const maxReasoningEffortMultiplierPlaceholder = computed(() =>
  props.entry.models.some(model => /fable(?:-5-1|-5\.1|5\.1|51)(?!\d)/i.test(model))
    ? t('admin.channels.form.fable51DefaultMaxReasoningMultiplier')
    : t('admin.channels.form.multiplierPlaceholder')
)

function emitField(field: keyof PricingFormEntry, value: string) {
  emit('update', { ...props.entry, [field]: value === '' ? null : value })
}

function addInterval() {
  const intervals = [...(props.entry.intervals || [])]
  intervals.push({
    min_tokens: 0, max_tokens: null, tier_label: '',
    input_price: null, output_price: null, cache_write_price: null,
    cache_write_1h_price: null,
    cache_read_price: null, per_request_price: null,
    input_multiplier: null, output_multiplier: null,
    cache_write_multiplier: null, cache_read_multiplier: null,
    sort_order: intervals.length
  })
  emit('update', { ...props.entry, intervals })
}

function addMediaTier() {
  const intervals = [...(props.entry.intervals || [])]
  const labels = props.entry.billing_mode === 'video'
    ? ['480p', '720p', '1080p']
    : ['1K', '2K', '4K', 'HD']
  intervals.push({
    min_tokens: 0, max_tokens: null, tier_label: labels[intervals.length] || '',
    input_price: null, output_price: null, cache_write_price: null,
    cache_write_1h_price: null,
    cache_read_price: null, per_request_price: null,
    input_multiplier: null, output_multiplier: null,
    cache_write_multiplier: null, cache_read_multiplier: null,
    sort_order: intervals.length
  })
  emit('update', { ...props.entry, intervals })
}

function updateInterval(idx: number, updated: IntervalFormEntry) {
  const intervals = [...(props.entry.intervals || [])]
  intervals[idx] = updated
  emit('update', { ...props.entry, intervals })
}

function removeInterval(idx: number) {
  const intervals = [...(props.entry.intervals || [])]
  intervals.splice(idx, 1)
  emit('update', { ...props.entry, intervals })
}

async function onModelsUpdate(newModels: string[]) {
  const oldModels = props.entry.models
  emit('update', { ...props.entry, models: newModels })

  // 只在新增模型且当前无价格时自动填充
  const addedModels = newModels.filter(m => !oldModels.includes(m))
  if (addedModels.length === 0) return

  // 检查是否所有价格字段都为空
  const e = props.entry
  const hasPrice = e.input_price != null || e.output_price != null ||
                   e.cache_write_price != null || e.cache_write_1h_price != null || e.cache_read_price != null
  if (hasPrice) return

  // 查询第一个新增模型的默认价格
  try {
    const result = await channelsAPI.getModelDefaultPricing(addedModels[0])
    if (result.found) {
      emit('update', {
        ...props.entry,
        models: newModels,
        input_price: perTokenToMTok(result.input_price ?? null),
        output_price: perTokenToMTok(result.output_price ?? null),
        cache_write_price: perTokenToMTok(result.cache_write_price ?? null),
        cache_write_1h_price: perTokenToMTok(result.cache_write_1h_price ?? null),
        cache_read_price: perTokenToMTok(result.cache_read_price ?? null),
        image_input_price: perTokenToMTok(result.image_input_price ?? null),
        image_output_price: perTokenToMTok(result.image_output_price ?? null),
        max_reasoning_effort_multiplier: result.max_reasoning_effort_multiplier ?? null,
      })
    }
  } catch {
    // 查询失败不影响用户操作
  }
}
</script>

<style scoped>
.pricing-default-grid {
  grid-template-columns: repeat(auto-fit, minmax(8rem, 1fr));
}
</style>
