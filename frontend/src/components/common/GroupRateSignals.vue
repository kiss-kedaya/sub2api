<template>
  <span
    v-if="items.length > 0"
    class="inline-flex shrink-0 items-center gap-1"
    data-group-signals
    data-testid="group-rate-signals"
  >
    <template v-for="item in items" :key="item.kind">
      <UPopover
        v-if="interactive"
        mode="hover"
        enable-touch
        :open-delay="120"
        :close-delay="120"
        :content="popoverContent"
        :ui="popoverUi"
      >
        <button
          type="button"
          :class="[chipClass, item.tone, interactiveClass]"
          :aria-label="item.aria"
          :data-testid="item.testid"
          :data-direction="item.direction"
        >
          <UIcon v-if="item.icon" :name="item.icon" class="size-3" aria-hidden="true" />
          <span aria-hidden="true">{{ item.label }}</span>
        </button>
        <template #content>
          <!-- The trigger's aria-label already carries this sentence; keep the bubble visual only. -->
          <div
            class="w-max max-w-56 space-y-0.5 px-2.5 py-2 text-xs leading-snug"
            aria-hidden="true"
            :data-testid="`${item.testid}-detail`"
          >
            <p class="font-medium text-highlighted">{{ item.title }}</p>
            <p class="text-muted">{{ item.detail }}</p>
          </div>
        </template>
      </UPopover>
      <!-- Inside another button or option a nested control is invalid, so this stays a labelled badge. -->
      <span
        v-else
        role="img"
        :class="[chipClass, item.tone]"
        :aria-label="item.aria"
        :title="`${item.title} · ${item.detail}`"
        :data-testid="item.testid"
        :data-direction="item.direction"
      >
        <UIcon v-if="item.icon" :name="item.icon" class="size-3" aria-hidden="true" />
        <span aria-hidden="true">{{ item.label }}</span>
      </span>
    </template>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GroupSignalSource } from '@/utils/groupRateSignals'
import { formatRateValue, formatSignalTime } from '@/utils/groupRateSignals'

/**
 * Compact hints next to a group's rate: a small arrow with the size of a recent
 * base rate change, and a NEW pill for a group launched in the last 24 hours.
 * Detail opens on hover or keyboard focus (pointer devices) and on tap (touch).
 */
const props = withDefaults(defineProps<{
  /** Usually the group payload from GET /groups/available. */
  signals?: GroupSignalSource | null
  /** False when the host shows no per-token rate (subscription groups): the arrow would have nothing to point at. */
  showRateChange?: boolean
  /** False inside a button or option, where a nested control is not allowed. */
  interactive?: boolean
}>(), {
  signals: null,
  showRateChange: true,
  interactive: true,
})

interface SignalItem {
  kind: 'rate' | 'new'
  testid: string
  direction?: 'up' | 'down'
  icon?: string
  label: string
  tone: string
  title: string
  detail: string
  aria: string
}

const { t, locale } = useI18n()

const chipClass =
  'inline-flex min-h-4 min-w-4 items-center justify-center gap-0.5 rounded-md px-1.5 py-0.5 font-mono text-[10px] font-semibold leading-none tabular-nums'
// before:-inset-1.5 widens the tap target past the 16px chip without changing its look.
const interactiveClass =
  'relative cursor-help touch-manipulation transition-colors before:absolute before:-inset-1.5 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-current motion-reduce:transition-none'

const popoverContent = { side: 'top' as const, align: 'center' as const, sideOffset: 6, collisionPadding: 8 }
const popoverUi = {
  content:
    'z-[99999] max-w-[calc(100vw-1rem)] motion-reduce:data-[state=open]:animate-none motion-reduce:data-[state=closed]:animate-none',
}

const items = computed<SignalItem[]>(() => {
  const source = props.signals
  if (!source) return []
  const result: SignalItem[] = []
  const lang = locale?.value

  const change = props.showRateChange ? source.recent_rate_change : null
  if (change) {
    const up = change.direction === 'up'
    const params = {
      delta: formatRateValue(change.delta),
      previous: formatRateValue(change.previous_rate),
      time: formatSignalTime(change.changed_at, lang),
    }
    result.push({
      kind: 'rate',
      testid: 'group-rate-signal-rate',
      direction: change.direction,
      icon: up ? 'i-lucide-arrow-up' : 'i-lucide-arrow-down',
      label: params.delta,
      // 700 shade in light mode keeps 10px text legible on the tint; the dark theme's pastel token is already tuned for dark surfaces.
      tone: up
        ? 'bg-warning/15 text-warning-700 dark:text-warning'
        : 'bg-success/15 text-success-700 dark:text-success',
      title: up ? t('groupSignals.rateUpTitle', params) : t('groupSignals.rateDownTitle', params),
      detail: t('groupSignals.changedAt', params),
      aria: up ? t('groupSignals.ariaRateUp', params) : t('groupSignals.ariaRateDown', params),
    })
  }

  if (source.is_new) {
    const time = formatSignalTime(source.launched_at, lang)
    const rate = typeof source.launch_rate === 'number' ? formatRateValue(source.launch_rate) : null
    result.push({
      kind: 'new',
      testid: 'group-rate-signal-new',
      label: t('groupSignals.newBadge'),
      // The hairline ring keeps it apart from the neighbouring user-rate chip, which shares the primary hue.
      tone: 'bg-primary/10 text-primary-700 ring-1 ring-inset ring-primary/25 dark:text-primary',
      title: t('groupSignals.newTitle'),
      detail: rate === null
        ? t('groupSignals.launchedAt', { time })
        : t('groupSignals.launchedAtRate', { time, rate }),
      aria: rate === null
        ? t('groupSignals.ariaNew', { time })
        : t('groupSignals.ariaNewRate', { time, rate }),
    })
  }
  return result
})
</script>
