<template>
  <div class="relative">
    <UPopover v-model:open="isOpen" :content="{ align: 'start' }">
      <UButton
        color="neutral"
        variant="outline"
        icon="i-lucide-calendar"
        class="group w-full max-w-full data-[state=open]:bg-elevated"
        data-test="date-range-trigger"
      >
        <span class="truncate font-medium">{{ displayValue }}</span>
        <template #trailing>
          <UIcon
            name="i-lucide-chevron-down"
            class="size-5 shrink-0 text-dimmed transition-transform duration-200 group-data-[state=open]:rotate-180"
          />
        </template>
      </UButton>

      <template #content>
        <div class="flex max-w-[calc(100vw-1rem)] flex-col sm:flex-row sm:items-stretch sm:divide-x sm:divide-default" data-test="date-range-panel">
          <!-- Quick presets -->
          <div class="grid grid-cols-2 gap-0.5 p-1.5 sm:flex sm:flex-col sm:justify-center sm:gap-0 sm:p-0">
            <UButton
              v-for="preset in presets"
              :key="preset.value"
              color="neutral"
              variant="ghost"
              data-test="date-range-preset"
              :aria-pressed="isPresetActive(preset) ? 'true' : 'false'"
              :class="['justify-center sm:justify-start sm:rounded-none sm:px-4', isPresetActive(preset) ? 'bg-elevated text-highlighted' : 'text-muted hover:bg-elevated/50']"
              :label="t(preset.labelKey)"
              truncate
              @click="selectPreset(preset)"
            />
          </div>

          <div class="flex min-w-0 flex-col">
            <UCalendar
              v-model="calendarRange"
              v-model:placeholder="calendarPlaceholder"
              range
              class="p-2"
              :number-of-months="monthsShown"
              :max-value="calendarMax"
            />

            <!-- Exact range (with seconds when includeTime) -->
            <div :class="['flex gap-2 px-3 pb-3', includeTime ? 'flex-col' : 'items-end']">
              <UFormField :label="t('dates.startDate')" class="min-w-0 flex-1">
                <UInput
                  :type="includeTime ? 'datetime-local' : 'date'"
                  :step="includeTime ? 1 : undefined"
                  :aria-label="t('dates.startDate')"
                  :model-value="localStartDate"
                  :max="localEndDate || maximumDate"
                  class="w-full"
                  @update:model-value="setStart"
                />
              </UFormField>
              <UFormField :label="t('dates.endDate')" class="min-w-0 flex-1">
                <UInput
                  :type="includeTime ? 'datetime-local' : 'date'"
                  :step="includeTime ? 1 : undefined"
                  :aria-label="t('dates.endDate')"
                  :model-value="localEndDate"
                  :min="localStartDate"
                  :max="maximumDate"
                  class="w-full"
                  @update:model-value="setEnd"
                />
              </UFormField>
              <div :class="includeTime ? 'flex justify-end' : 'shrink-0'">
                <UButton
                  color="primary"
                  :label="t('dates.apply')"
                  :disabled="!validRange"
                  data-test="date-range-apply"
                  @click="apply"
                />
              </div>
            </div>
          </div>
        </div>
      </template>
    </UPopover>
  </div>
</template>
<script setup lang="ts">
import { ref, shallowRef, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMediaQuery } from '@vueuse/core'
import { CalendarDate, getLocalTimeZone, today as calendarToday } from '@internationalized/date'

interface DatePreset {
  labelKey: string
  value: string
  getRange: () => { start: string; end: string }
}

interface Props {
  startDate: string
  endDate: string
  includeTime?: boolean
}

interface Emits {
  (e: 'update:startDate', value: string): void
  (e: 'update:endDate', value: string): void
  (e: 'change', range: { startDate: string; endDate: string; preset: string | null }): void
}

const props = withDefaults(defineProps<Props>(), { includeTime: false })
const emit = defineEmits<Emits>()

const { t, locale } = useI18n()

const isOpen = ref(false)
const normalizeInput = (value: string, end = false): string => {
  if (!props.includeTime || !value) return value
  if (!value.includes('T')) return value + (end ? 'T23:59:59' : 'T00:00:00')
  return value.length === 16 ? value + ':00' : value.slice(0, 19)
}
const localStartDate = ref(normalizeInput(props.startDate))
const localEndDate = ref(normalizeInput(props.endDate, true))
const activePreset = ref<string | null>('last24Hours')
const maximumDate = computed(() => normalizeInput(tomorrow.value, true))
const validRange = computed(() => {
  const start = new Date(localStartDate.value).getTime()
  const end = new Date(localEndDate.value).getTime()
  return Number.isFinite(start) && Number.isFinite(end) && start <= end
})

const today = computed(() => {
  // Use local timezone to avoid UTC timezone issues
  const now = new Date()
  const year = now.getFullYear()
  const month = String(now.getMonth() + 1).padStart(2, '0')
  const day = String(now.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
})

// Tomorrow's date - used for max date to handle timezone differences
// When user is in a timezone behind the server, "today" on server might be "tomorrow" locally
const tomorrow = computed(() => {
  const d = new Date()
  d.setDate(d.getDate() + 1)
  return formatDateToString(d)
})

// Helper function to format date to YYYY-MM-DD using local timezone
const formatDateToString = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const formatTimestamp = (date: Date): string => {
  const time = [date.getHours(), date.getMinutes(), date.getSeconds()]
    .map(value => String(value).padStart(2, '0')).join(':')
  return `${formatDateToString(date)}T${time}`
}
const daysAgo = (days: number) => {
  const d = new Date()
  d.setDate(d.getDate() - days)
  return formatDateToString(d)
}

const presets: DatePreset[] = [
  { labelKey: 'dates.today', value: 'today', getRange: () => ({ start: today.value, end: today.value }) },
  {
    labelKey: 'dates.yesterday',
    value: 'yesterday',
    getRange: () => {
      const yesterday = daysAgo(1)
      return { start: yesterday, end: yesterday }
    }
  },
  {
    labelKey: 'dates.last24Hours',
    value: 'last24Hours',
    getRange: () => {
      const end = new Date()
      const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
      return {
        start: props.includeTime ? formatTimestamp(start) : formatDateToString(start),
        end: props.includeTime ? formatTimestamp(end) : formatDateToString(end)
      }
    }
  },
  { labelKey: 'dates.last7Days', value: '7days', getRange: () => ({ start: daysAgo(6), end: today.value }) },
  { labelKey: 'dates.last14Days', value: '14days', getRange: () => ({ start: daysAgo(13), end: today.value }) },
  { labelKey: 'dates.last30Days', value: '30days', getRange: () => ({ start: daysAgo(29), end: today.value }) },
  {
    labelKey: 'dates.thisMonth',
    value: 'thisMonth',
    getRange: () => {
      const now = new Date()
      return { start: formatDateToString(new Date(now.getFullYear(), now.getMonth(), 1)), end: today.value }
    }
  },
  {
    labelKey: 'dates.lastMonth',
    value: 'lastMonth',
    getRange: () => {
      const now = new Date()
      const start = formatDateToString(new Date(now.getFullYear(), now.getMonth() - 1, 1))
      const end = formatDateToString(new Date(now.getFullYear(), now.getMonth(), 0))
      return { start, end }
    }
  }
]

const displayValue = computed(() => {
  if (activePreset.value) {
    const preset = presets.find((p) => p.value === activePreset.value)
    if (preset) return t(preset.labelKey)
  }

  if (localStartDate.value && localEndDate.value) {
    if (localStartDate.value === localEndDate.value) {
      return formatDate(localStartDate.value)
    }
    return `${formatDate(localStartDate.value)} - ${formatDate(localEndDate.value)}`
  }

  return t('dates.selectDateRange')
})

const formatDate = (dateStr: string): string => {
  const date = new Date(dateStr.includes('T') ? dateStr : dateStr + 'T00:00:00')
  const dateLocale = locale.value === 'zh' ? 'zh-CN' : 'en-US'
  if (props.includeTime) {
    return date.toLocaleString(dateLocale, {
      year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
    })
  }
  return date.toLocaleDateString(dateLocale, { month: 'short', day: 'numeric' })
}
// ---- Calendar bridge: the strings above stay the source of truth (they carry seconds). ----
type DayValue = { year: number; month: number; day: number }

const toCalendarDate = (value: string): CalendarDate | undefined => {
  const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(value || '')
  if (!match) return undefined
  return new CalendarDate(Number(match[1]), Number(match[2]), Number(match[3]))
}

const fromCalendarDate = (value: DayValue, end: boolean): string => {
  const day = `${value.year}-${String(value.month).padStart(2, '0')}-${String(value.day).padStart(2, '0')}`
  return normalizeInput(day, end)
}

const calendarRange = computed({
  get: () => ({ start: toCalendarDate(localStartDate.value), end: toCalendarDate(localEndDate.value) }),
  set: (range: { start?: DayValue | null; end?: DayValue | null } | null) => {
    if (!range?.start) return
    // A day that did not change keeps its exact time (the calendar only knows days).
    const nextStart = fromCalendarDate(range.start, false)
    if (!sameDay(localStartDate.value, nextStart)) localStartDate.value = nextStart
    if (!range.end) {
      // While the second day is not picked yet the range stays incomplete and Apply stays disabled.
      if (localEndDate.value && !sameDay(localEndDate.value, nextStart)) localEndDate.value = ''
    } else {
      const nextEnd = fromCalendarDate(range.end, true)
      if (!sameDay(localEndDate.value, nextEnd)) localEndDate.value = nextEnd
    }
    onDateChange()
  }
})

function sameDay(a: string, b: string) {
  return !!a && !!b && a.slice(0, 10) === b.slice(0, 10)
}

const calendarMax = computed(() => toCalendarDate(tomorrow.value))
const wideScreen = useMediaQuery('(min-width: 640px)')
const monthsShown = computed(() => (wideScreen.value ? 2 : 1))
// shallowRef keeps the CalendarDate class type (ref would unwrap it); dates are replaced, never mutated.
const calendarPlaceholder = shallowRef<CalendarDate>(calendarToday(getLocalTimeZone()))

// Show the months that hold the selected range; the right-most month is the end month.
const syncPlaceholder = () => {
  const end = toCalendarDate(localEndDate.value) ?? toCalendarDate(localStartDate.value) ?? calendarToday(getLocalTimeZone())
  const start = toCalendarDate(localStartDate.value) ?? end
  const first = end.subtract({ months: monthsShown.value - 1 }).set({ day: 1 })
  calendarPlaceholder.value = start.compare(first) < 0 ? start.set({ day: 1 }) : first
}

watch(isOpen, (open) => {
  if (open) syncPlaceholder()
})

const setStart = (value: string | null | undefined) => {
  localStartDate.value = value ?? ''
  onDateChange()
}

const setEnd = (value: string | null | undefined) => {
  localEndDate.value = value ?? ''
  onDateChange()
}

const isPresetActive = (preset: DatePreset): boolean => {
  return activePreset.value === preset.value
}

const selectPreset = (preset: DatePreset) => {
  const range = preset.getRange()
  localStartDate.value = normalizeInput(range.start)
  localEndDate.value = normalizeInput(range.end, true)
  activePreset.value = preset.value
  syncPlaceholder()
}

const onDateChange = () => {
  // Check if current dates match any preset
  activePreset.value = null
  for (const preset of presets) {
    const range = preset.getRange()
    if (normalizeInput(range.start) === localStartDate.value && normalizeInput(range.end, true) === localEndDate.value) {
      activePreset.value = preset.value
      break
    }
  }
}

const apply = () => {
  if (!validRange.value) return
  localStartDate.value = normalizeInput(localStartDate.value)
  localEndDate.value = normalizeInput(localEndDate.value, true)
  emit('update:startDate', localStartDate.value)
  emit('update:endDate', localEndDate.value)
  emit('change', {
    startDate: localStartDate.value,
    endDate: localEndDate.value,
    preset: activePreset.value
  })
  isOpen.value = false
}

// refreshActivePreset re-applies the currently active quick preset so an
// external refresh button can slide a relative window (e.g. 近24小时) to
// "now". Returns the recomputed range, or null when the user set a custom
// range — a custom range must stay exactly where the user put it.
const refreshActivePreset = (): { startDate: string; endDate: string } | null => {
  if (!activePreset.value) return null
  const preset = presets.find((p) => p.value === activePreset.value)
  if (!preset) return null
  const range = preset.getRange()
  localStartDate.value = normalizeInput(range.start)
  localEndDate.value = normalizeInput(range.end, true)
  return { startDate: localStartDate.value, endDate: localEndDate.value }
}

defineExpose({ refreshActivePreset })

// Sync local state with props
watch(
  () => props.startDate,
  (val) => {
    localStartDate.value = normalizeInput(val)
    onDateChange()
  }
)

watch(
  () => props.endDate,
  (val) => {
    localEndDate.value = normalizeInput(val, true)
    onDateChange()
  }
)

onMounted(() => {
  // Initialize active preset detection
  onDateChange()
})
</script>
