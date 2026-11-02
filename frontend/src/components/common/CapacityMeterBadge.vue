<script setup lang="ts">
import { computed } from 'vue'

// One capacity line in a table: icon + "used / max" inside a small soft badge.
//
// The badge owns its explanation. `title` is the hover channel: a native tooltip is
// drawn by the OS, so it is never clipped by the table's scroll container (an absolutely
// positioned panel would be). `aria-label` carries the same sentence to keyboard focus
// and screen readers, and the focus ring keeps that state visible without animating the
// badge's box.
type Tone = 'full' | 'active' | 'idle' | 'unknown'

const props = withDefaults(defineProps<{
  /** Short name of the meter, e.g. "concurrency". Part of the tooltip sentence. */
  label: string
  /** What the two numbers mean and how they are counted. */
  hint: string
  used: number | string | null | undefined
  max?: number | string | null
  icon?: string
  suffix?: string
  showMax?: boolean
  /** Explicit state, for callers with their own thresholds. Auto-derived when omitted. */
  tone?: Tone | null
}>(), {
  max: undefined,
  icon: 'i-lucide-gauge',
  suffix: '',
  showMax: true,
  tone: null,
})

const isUnknown = computed(() => props.used === null || props.used === undefined || props.used === '')
const usedText = computed(() => (isUnknown.value ? '—' : String(props.used)))
const maxText = computed(() => {
  if (props.max === null || props.max === undefined || props.max === '') return '—'
  return String(props.max)
})

// Colour only signals state: full = error, in use = warning, idle = neutral.
// Unknown stays neutral so a failed counter never reads as "0 in use".
const autoTone = computed<Tone>(() => {
  if (isUnknown.value) return 'unknown'
  const used = Number(props.used)
  if (!Number.isFinite(used)) return 'unknown'
  const max = Number(props.max)
  if (Number.isFinite(max) && max > 0 && used >= max) return 'full'
  if (used > 0) return 'active'
  return 'idle'
})

const resolvedTone = computed<Tone>(() => props.tone ?? autoTone.value)

const toneClass = computed(() => {
  switch (resolvedTone.value) {
    case 'full':
      return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
    case 'active':
      return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
    case 'unknown':
      return 'bg-elevated text-dimmed dark:bg-gray-800 dark:text-gray-500'
    default:
      return 'bg-elevated text-muted dark:bg-gray-800 dark:text-gray-400'
  }
})

const description = computed(() => `${props.label} · ${props.hint}`)
</script>

<template>
  <span
    class="inline-flex max-w-full items-center gap-1 whitespace-nowrap rounded-md px-2 py-0.5 text-xs font-medium tabular-nums transition-colors focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-current motion-reduce:transition-none"
    :class="toneClass"
    :title="description"
    :aria-label="description"
    :data-tone="resolvedTone"
    tabindex="0"
  >
    <UIcon v-if="icon" :name="icon" class="size-3 shrink-0" aria-hidden="true" />
    <span class="font-mono">{{ usedText }}</span>
    <template v-if="showMax">
      <span class="opacity-60">/</span>
      <span class="font-mono">{{ maxText }}</span>
    </template>
    <span v-if="suffix" class="opacity-70">{{ suffix }}</span>
  </span>
</template>
