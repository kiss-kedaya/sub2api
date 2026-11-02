<template>
  <UDropdownMenu :items="items" :content="{ align: 'end' }" :ui="{ content: 'w-44' }">
    <UButton
      color="neutral"
      variant="outline"
      size="sm"
      icon="i-lucide-refresh-cw"
      :ui="{ leadingIcon: enabled ? 'animate-spin' : '' }"
      :title="t('common.autoRefresh.title')"
      :label="enabled ? t('common.autoRefresh.countdown', { seconds: countdown }) : t('common.autoRefresh.title')"
      class="tabular-nums"
    />
  </UDropdownMenu>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem } from '@nuxt/ui'

const props = defineProps<{
  enabled: boolean
  intervalSeconds: number
  countdown: number
  intervals: readonly number[]
}>()

const emit = defineEmits<{
  (e: 'update:enabled', value: boolean): void
  (e: 'update:interval', value: number): void
}>()

const { t } = useI18n()

// Picking an option keeps the menu open (as before), so the interval can be set right after enabling.
const keepOpen = (event: Event) => event.preventDefault()

const items = computed<DropdownMenuItem[][]>(() => [
  [{
    label: t('common.autoRefresh.enable'),
    type: 'checkbox',
    checked: props.enabled,
    onSelect: (event: Event) => {
      keepOpen(event)
      emit('update:enabled', !props.enabled)
    }
  }],
  props.intervals.map((sec) => ({
    label: t('common.autoRefresh.seconds', { n: sec }),
    type: 'checkbox' as const,
    checked: props.intervalSeconds === sec,
    onSelect: (event: Event) => {
      keepOpen(event)
      emit('update:interval', sec)
    }
  }))
])
</script>
