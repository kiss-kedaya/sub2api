<template>
  <UPageCard
    variant="subtle"
    :title="title"
    :ui="{
      container: 'gap-y-1.5',
      wrapper: 'items-start',
      leading: ['p-2.5 rounded-full ring ring-inset', iconClass],
      title: 'font-normal text-muted text-xs uppercase truncate'
    }"
    class="stat-card min-w-0"
  >
    <template v-if="icon" #leading>
      <component :is="icon" class="size-5" aria-hidden="true" />
    </template>
    <div class="flex min-w-0 items-center gap-2">
      <span class="truncate text-2xl font-semibold text-highlighted tabular-nums" :title="String(formattedValue)">
        {{ formattedValue }}
      </span>
      <UBadge
        v-if="change !== undefined"
        :color="trendColor"
        variant="subtle"
        size="sm"
        :icon="trendIcon"
        :label="formattedChange"
        class="stat-trend shrink-0 tabular-nums"
      />
    </div>
  </UPageCard>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { Component } from 'vue'

type ChangeType = 'up' | 'down' | 'neutral'
type IconVariant = 'primary' | 'success' | 'warning' | 'danger'

interface Props {
  title: string
  value: number | string
  icon?: Component
  iconVariant?: IconVariant
  change?: number
  changeType?: ChangeType
  formatValue?: (value: number | string) => string
}

const props = withDefaults(defineProps<Props>(), {
  changeType: 'neutral',
  iconVariant: 'primary'
})

const formattedValue = computed(() => {
  if (props.formatValue) {
    return props.formatValue(props.value)
  }
  if (typeof props.value === 'number') {
    return props.value.toLocaleString()
  }
  return props.value
})

const formattedChange = computed(() => {
  if (props.change === undefined) return ''
  const absChange = Math.abs(props.change)
  return `${absChange}%`
})

const iconClass = computed(() => {
  const classes: Record<IconVariant, string> = {
    primary: 'bg-primary/10 text-primary ring-primary/25',
    success: 'bg-success/10 text-success ring-success/25',
    warning: 'bg-warning/10 text-warning ring-warning/25',
    danger: 'bg-error/10 text-error ring-error/25'
  }
  return classes[props.iconVariant]
})

const trendColor = computed(() => {
  const colors: Record<ChangeType, 'success' | 'error' | 'neutral'> = {
    up: 'success',
    down: 'error',
    neutral: 'neutral'
  }
  return colors[props.changeType]
})

const trendIcon = computed(() => {
  if (props.changeType === 'up') return 'i-lucide-arrow-up'
  if (props.changeType === 'down') return 'i-lucide-arrow-down'
  return undefined
})
</script>
