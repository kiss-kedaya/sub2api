<script setup lang="ts">
// One capacity line in the accounts table: a muted icon and "current / max" in tabular
// figures. colorClass is a text token (text-muted normally, text-warning / text-error near
// or at the limit), so only the state is coloured, never the background.
defineProps<{
  colorClass: string
  tooltip?: string
  current: string | number
  max: string | number
  suffix?: string
}>()
</script>

<template>
  <span
    :class="['inline-flex items-center gap-1 text-xs leading-5 tabular-nums', colorClass]"
    :title="tooltip"
  >
    <span class="inline-flex text-dimmed"><slot /></span>
    <span>{{ current }}</span>
    <span class="text-dimmed">/</span>
    <span>{{ max }}</span>
    <span v-if="suffix" class="text-dimmed">{{ suffix }}</span>
  </span>
</template>
