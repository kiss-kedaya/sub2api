<template>
  <UPopover
    v-model:open="isOpen"
    :content="{ align: 'start', sideOffset: 4 }"
    :ui="{ content: 'filter-dropdown dropdown w-max min-w-[200px] max-h-[min(50vh,360px)] overflow-y-auto p-1' }"
  >
    <UButton
      color="neutral"
      variant="outline"
      :size="compact ? 'sm' : 'md'"
      trailing-icon="i-lucide-chevron-down"
      class="filter-menu justify-between max-sm:min-w-full"
      :class="compact ? 'min-w-26 sm:min-w-29' : 'min-w-[150px] sm:min-w-[160px]'"
      :ui="{ trailingIcon: isOpen ? 'rotate-180 transition-transform' : 'transition-transform' }"
      :aria-expanded="isOpen"
      aria-haspopup="listbox"
      :aria-label="label"
    >
      <span class="min-w-0 truncate" :class="compact ? 'max-w-22 sm:max-w-26' : 'max-w-44'">
        {{ t('channelMonitorV2.filters.labelValue', { label, value: selectionLabel }) }}
      </span>
    </UButton>

    <template #content>
      <div class="flex flex-col" role="listbox" aria-multiselectable="true">
        <UButton
          color="neutral"
          variant="ghost"
          block
          class="dropdown-item justify-between font-semibold"
          :trailing-icon="modelValue.length === 0 ? 'i-lucide-check' : undefined"
          :label="allLabel"
          @click="clear"
        />
        <USeparator class="my-1" />
        <UButton
          v-for="option in options"
          :key="option.value"
          color="neutral"
          variant="ghost"
          block
          role="option"
          :aria-selected="modelValue.includes(option.value)"
          class="dropdown-item justify-between gap-3 font-normal"
          @click="toggle(option.value)"
        >
          <span class="flex min-w-0 flex-1 items-center gap-2">
            <UIcon
              :name="modelValue.includes(option.value) ? 'i-lucide-square-check' : 'i-lucide-square'"
              class="size-4 shrink-0"
              :class="modelValue.includes(option.value) ? 'text-primary' : 'text-dimmed'"
            />
            <span class="min-w-0 flex-1 truncate text-left">{{ option.label }}</span>
          </span>
          <small v-if="option.count != null" class="text-xs text-muted">{{ formatCount(option.count) }}</small>
        </UButton>
        <p v-if="options.length === 0" class="px-4 py-3 text-center text-xs text-muted">{{ t('channelMonitorV2.filters.empty') }}</p>
      </div>
    </template>
  </UPopover>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed, ref } from 'vue'
import { monitorIntlLocale } from '@/features/channel-monitor-v2/monitorFormat'

interface FilterOption {
  value: string
  label: string
  count?: number
}

const props = withDefaults(
  defineProps<{
    label: string
    allLabel: string
    modelValue: string[]
    /** Options for this picker (parent may cascade by platform). */
    options: FilterOption[]
    /** Compact trigger for single-row toolbars. */
    compact?: boolean
  }>(),
  { compact: false },
)
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const { t, locale } = useI18n()

// UPopover handles outside clicks, Escape and positioning.
const isOpen = ref(false)

const selectionLabel = computed(() => {
  if (props.modelValue.length === 0) return props.allLabel
  if (props.modelValue.length === 1) {
    return props.options.find((item) => item.value === props.modelValue[0])?.label || props.modelValue[0]
  }
  return t('channelMonitorV2.filters.selectedCount', { count: props.modelValue.length })
})

function clear() {
  emit('update:modelValue', [])
  // Stay open so users can re-select without reopening.
}

function toggle(value: string) {
  const selected = new Set(props.modelValue)
  if (selected.has(value)) selected.delete(value)
  else selected.add(value)
  emit('update:modelValue', [...selected])
  // Stay open on toggle (multi-select).
}

function formatCount(value: number) {
  return Intl.NumberFormat(locale.value || monitorIntlLocale(), {
    notation: value >= 10000 ? 'compact' : 'standard',
  }).format(value)
}
</script>
