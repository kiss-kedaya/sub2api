<template>
  <div>
    <!-- Window stats (above the bar): plain muted figures -->
    <div
      v-if="windowStats && (windowStats.requests > 0 || windowStats.tokens > 0)"
      class="mb-0.5 flex flex-wrap items-center gap-x-2 text-xs tabular-nums text-muted"
    >
      <span>{{ formatRequests }} req</span>
      <span>{{ formatTokens }}</span>
      <span :title="t('usage.accountBilled')">A ${{ formatAccountCost }}</span>
      <span v-if="windowStats?.user_cost != null" :title="t('usage.userBilled')">U ${{ formatUserCost }}</span>
      <span
        v-if="estimatedTotalCost != null"
        data-test="estimated-total-cost"
        :title="t('admin.accounts.usageWindow.estimatedTotalCostTooltip')"
      >
        {{ t('admin.accounts.usageWindow.estimatedTotalCost', { cost: estimatedTotalCost.toFixed(2) }) }}
      </span>
    </div>

    <!-- Bar row: label, thin bar, percent, reset time -->
    <div class="flex items-center gap-1">
      <!-- label-width: fixed = 定宽居中（账号页纵向对齐）; auto = 限宽截断左对齐 -->
      <span :class="[labelSizeClass, 'text-muted']">
        {{ label }}
      </span>

      <UProgress
        :model-value="utilization"
        :max="100"
        :color="barColor"
        size="sm"
        class="usage-bar-track w-10 shrink-0"
      />

      <span :class="['w-[32px] shrink-0 text-right text-xs tabular-nums', textClass]">
        {{ displayPercent }}
      </span>

      <span v-if="shouldShowResetTime" class="shrink-0 text-xs tabular-nums text-dimmed">
        {{ formatResetTime }}
      </span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import type { WindowStats } from '@/types'
import { formatCompactNumber } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    label: string
    utilization: number // Percentage (0-100+)
    resetsAt?: string | null
    color: 'indigo' | 'emerald' | 'purple' | 'amber'
    windowStats?: WindowStats | null
    estimatedTotalCost?: number | null
    showNowWhenIdle?: boolean
    remainingCapacity?: boolean
    /** fixed: 定宽居中徽章（账号页纵向对齐）；auto: 限宽截断左对齐（监控页组合标签） */
    labelWidth?: 'fixed' | 'auto'
  }>(),
  { labelWidth: 'fixed' }
)

const { t } = useI18n()

// Reactive clock for countdown — only runs when a reset time is shown,
// to avoid creating many idle timers across large account lists.
const now = ref(new Date())
const { pause: pauseClock, resume: resumeClock } = useIntervalFn(
  () => {
    now.value = new Date()
  },
  60_000,
  { immediate: false },
)
if (props.resetsAt) resumeClock()
watch(
  () => props.resetsAt,
  (val) => {
    if (val) {
      now.value = new Date()
      resumeClock()
    } else {
      pauseClock()
    }
  },
)

// Label width mode: fixed 定宽保证账号页纵向对齐；auto 限宽截断适配
// 监控页「Pro/7 天」类组合标签。百分比列在两种模式下保持不变。
// The label is plain muted text; `color` stays accepted for callers but no longer paints a chip.
const labelSizeClass = computed(() =>
  props.labelWidth === 'auto'
    ? 'max-w-[72px] shrink-0 truncate text-left text-xs font-medium'
    : 'w-[32px] shrink-0 text-center text-xs font-medium'
)

// Usage level from the thresholds: high = error, elevated = warning, otherwise normal.
// Remaining-capacity bars count down, so their thresholds are reversed.
const level = computed<'error' | 'warning' | 'normal'>(() => {
  if (props.remainingCapacity) {
    if (props.utilization <= 20) return 'error'
    if (props.utilization <= 50) return 'warning'
    return 'normal'
  }
  if (props.utilization >= 90) return 'error'
  if (props.utilization >= 75) return 'warning'
  return 'normal'
})

// UProgress clamps the value to 0..100, so an over-limit window still shows a full bar.
const barColor = computed(() => (level.value === 'normal' ? 'primary' : level.value))

const textClass = computed(() => {
  if (level.value === 'error') return 'text-error'
  if (level.value === 'warning') return 'text-warning'
  return 'text-muted'
})

// Display percentage (cap at 999% for readability)
const displayPercent = computed(() => {
  const percent = Math.round(
    props.remainingCapacity
      ? Math.min(Math.max(props.utilization, 0), 100)
      : props.utilization
  )
  return percent > 999 ? '>999%' : `${percent}%`
})

const shouldShowResetTime = computed(() => {
  if (props.resetsAt) return true
  return Boolean(props.showNowWhenIdle && props.utilization <= 0)
})

// Format reset time
const formatResetTime = computed(() => {
  // For rolling windows, when utilization is 0%, treat as immediately available.
  if (props.showNowWhenIdle && props.utilization <= 0) {
    return t('usage.resetNow')
  }

  if (!props.resetsAt) return '-'

  const date = new Date(props.resetsAt)
  const diffMs = date.getTime() - now.value.getTime()

  // resetsAt 已过期：utilization>0 说明后端窗口数据还没刷新（active poll 没回写），
  // 显示「待刷新」以区别于真正可用的「现在」。
  if (diffMs <= 0) {
    return props.utilization > 0 ? t('usage.resetPending') : t('usage.resetNow')
  }

  const diffHours = Math.floor(diffMs / (1000 * 60 * 60))
  const diffMins = Math.floor((diffMs % (1000 * 60 * 60)) / (1000 * 60))

  if (diffHours >= 24) {
    const days = Math.floor(diffHours / 24)
    return `${days}d ${diffHours % 24}h`
  } else if (diffHours > 0) {
    return `${diffHours}h ${diffMins}m`
  } else {
    return `${diffMins}m`
  }
})

// Window stats formatters
const formatRequests = computed(() => {
  if (!props.windowStats) return ''
  return formatCompactNumber(props.windowStats.requests, { allowBillions: false })
})

const formatTokens = computed(() => {
  if (!props.windowStats) return ''
  return formatCompactNumber(props.windowStats.tokens)
})

const formatAccountCost = computed(() => {
  if (!props.windowStats) return '0.00'
  return props.windowStats.cost.toFixed(2)
})

const formatUserCost = computed(() => {
  if (!props.windowStats || props.windowStats.user_cost == null) return '0.00'
  return props.windowStats.user_cost.toFixed(2)
})

</script>
