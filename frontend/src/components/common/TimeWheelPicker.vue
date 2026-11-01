<template>
  <div class="relative">
    <UPopover v-model:open="open" :content="{ align: 'start' }">
      <UButton
        color="neutral"
        variant="outline"
        block
        trailing-icon="i-lucide-chevron-down"
        class="justify-between font-normal tabular-nums"
        :class="value ? 'text-highlighted' : 'text-dimmed'"
        :disabled="disabled"
        :aria-label="label || undefined"
        data-test="time-wheel-trigger"
      >
        <span>{{ value || '--:--' }}</span>
      </UButton>

      <template #content>
        <!-- iOS-style wheel: two snapping columns, the selected row centred. -->
        <div class="w-44 p-2" data-test="time-wheel-panel">
          <div class="flex gap-1">
            <div ref="hourCol" class="max-h-40 flex-1 snap-y snap-mandatory overflow-y-auto">
              <UButton
                v-for="h in 24"
                :key="h"
                :color="h - 1 === hour ? 'primary' : 'neutral'"
                :variant="h - 1 === hour ? 'soft' : 'ghost'"
                size="sm"
                block
                class="snap-center justify-center tabular-nums"
                :class="h - 1 === hour ? 'font-semibold' : 'text-muted'"
                @click="pickHour(h - 1)"
              >
                {{ pad(h - 1) }}
              </UButton>
            </div>
            <div ref="minuteCol" class="max-h-40 flex-1 snap-y snap-mandatory overflow-y-auto">
              <UButton
                v-for="m in minuteOptions"
                :key="m"
                :color="m === minute ? 'primary' : 'neutral'"
                :variant="m === minute ? 'soft' : 'ghost'"
                size="sm"
                block
                class="snap-center justify-center tabular-nums"
                :class="m === minute ? 'font-semibold' : 'text-muted'"
                @click="pickMinute(m)"
              >
                {{ pad(m) }}
              </UButton>
            </div>
          </div>
          <div class="mt-2 flex justify-end">
            <UButton color="primary" variant="link" size="xs" :label="doneLabel" @click="open = false" />
          </div>
        </div>
      </template>
    </UPopover>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(
  defineProps<{
    modelValue: string
    label?: string
    minuteStep?: number
    disabled?: boolean
  }>(),
  { label: '', minuteStep: 5, disabled: false },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const { t } = useI18n()
const hourCol = ref<HTMLElement | null>(null)
const minuteCol = ref<HTMLElement | null>(null)
const open = ref(false)
const doneLabel = computed(() => t('common.done'))

const parts = computed(() => {
  const [h, m] = (props.modelValue || '').split(':')
  return { hour: Number(h), minute: Number(m) }
})
const hour = computed(() => (Number.isFinite(parts.value.hour) ? parts.value.hour : 0))
const minute = computed(() => (Number.isFinite(parts.value.minute) ? parts.value.minute : 0))
const minuteOptions = computed(() => {
  const step = Math.max(1, Math.min(30, Math.floor(props.minuteStep || 5)))
  const out: number[] = []
  for (let m = 0; m < 60; m += step) out.push(m)
  return out
})

const pad = (n: number) => String(n).padStart(2, '0')
const value = computed(() => {
  if (!Number.isFinite(parts.value.hour) || !Number.isFinite(parts.value.minute)) return ''
  return `${pad(parts.value.hour)}:${pad(parts.value.minute)}`
})
const emitValue = (h: number, m: number) => emit('update:modelValue', `${pad(h)}:${pad(m)}`)
const pickHour = (h: number) => emitValue(h, minute.value)
const pickMinute = (m: number) => emitValue(hour.value, m)

const scrollToSelected = () => {
  // The popover mounts its panel after opening; wait for the columns to exist.
  nextTick(() => nextTick(() => {
    const hourItem = hourCol.value?.children[hour.value] as HTMLElement | undefined
    const index = Math.max(0, minuteOptions.value.indexOf(minute.value))
    const minuteItem = minuteCol.value?.children[index] as HTMLElement | undefined
    hourItem?.scrollIntoView({ block: 'center' })
    minuteItem?.scrollIntoView({ block: 'center' })
  }))
}

// The popover handles toggling, outside clicks and Escape.
watch(open, (value) => {
  if (value && props.disabled) {
    open.value = false
    return
  }
  if (value) scrollToSelected()
})
</script>
