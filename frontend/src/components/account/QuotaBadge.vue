<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{
  used: number
  limit: number
  label?: string // 文字前缀，如 "D" / "W"；不传时显示 icon
}>()

const { t } = useI18n()

// Colour only signals state: exceeded = error, from 80% = warning, otherwise muted text.
const textClass = computed(() => {
  if (props.used >= props.limit) return 'text-error'
  if (props.used >= props.limit * 0.8) return 'text-warning'
  return 'text-muted'
})

const tooltip = computed(() => {
  if (props.used >= props.limit) {
    return t('admin.accounts.capacity.quota.exceeded')
  }
  return t('admin.accounts.capacity.quota.normal')
})

const fmt = (v: number) => v.toFixed(2)
</script>

<template>
  <span
    :class="['inline-flex items-center gap-1 text-xs leading-5 tabular-nums', textClass]"
    :title="tooltip"
  >
    <span v-if="label" class="font-medium text-dimmed">{{ label }}</span>
    <UIcon v-else name="i-lucide-wallet" class="size-3 text-dimmed" />
    <span>${{ fmt(used) }}</span>
    <span class="text-dimmed">/</span>
    <span>${{ fmt(limit) }}</span>
  </span>
</template>
