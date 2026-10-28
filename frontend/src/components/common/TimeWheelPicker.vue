<template>
  <div ref="root" class="relative">
    <button
      type="button"
      class="input flex w-full items-center justify-between text-left"
      :disabled="disabled"
      :aria-label="label || undefined"
      @click="toggle"
    >
      <span :class="value ? '' : 'text-gray-400 dark:text-dark-500'">{{ value || '--:--' }}</span>
      <span class="text-gray-400 dark:text-dark-500" aria-hidden="true">⌄</span>
    </button>
    <div
      v-if="open"
      class="absolute z-30 mt-1 w-44 rounded-xl bg-white p-2 shadow-sm ring-1 ring-black/5 dark:bg-dark-800 dark:ring-white/5"
    >
      <div class="flex gap-1">
        <div ref="hourCol" class="max-h-40 flex-1 overflow-y-auto snap-y snap-mandatory">
          <button
            v-for="h in 24"
            :key="h"
            type="button"
            class="block w-full snap-center rounded-lg px-2 py-1 text-center text-sm"
            :class="h - 1 === hour ? 'bg-primary-50 text-primary-700 dark:bg-dark-700 dark:text-primary-300' : 'text-gray-600 dark:text-dark-300'"
            @click="pickHour(h - 1)"
          >
            {{ pad(h - 1) }}
          </button>
        </div>
        <div ref="minuteCol" class="max-h-40 flex-1 overflow-y-auto snap-y snap-mandatory">
          <button
            v-for="m in minuteOptions"
            :key="m"
            type="button"
            class="block w-full snap-center rounded-lg px-2 py-1 text-center text-sm"
            :class="m === minute ? 'bg-primary-50 text-primary-700 dark:bg-dark-700 dark:text-primary-300' : 'text-gray-600 dark:text-dark-300'"
            @click="pickMinute(m)"
          >
            {{ pad(m) }}
          </button>
        </div>
      </div>
      <div class="mt-2 flex justify-end">
        <button type="button" class="text-xs text-primary-600 hover:underline dark:text-primary-400" @click="open = false">
          {{ doneLabel }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
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
const root = ref<HTMLElement | null>(null)
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
  nextTick(() => {
    const hourItem = hourCol.value?.children[hour.value] as HTMLElement | undefined
    const index = Math.max(0, minuteOptions.value.indexOf(minute.value))
    const minuteItem = minuteCol.value?.children[index] as HTMLElement | undefined
    hourItem?.scrollIntoView({ block: 'center' })
    minuteItem?.scrollIntoView({ block: 'center' })
  })
}

const toggle = () => {
  if (props.disabled) return
  open.value = !open.value
  if (open.value) scrollToSelected()
}

const onDocumentClick = (event: MouseEvent) => {
  if (!open.value) return
  if (root.value && !root.value.contains(event.target as Node)) open.value = false
}
watch(open, (value) => {
  if (value) document.addEventListener('mousedown', onDocumentClick)
  else document.removeEventListener('mousedown', onDocumentClick)
})
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocumentClick))
</script>
