<template>
  <USwitch
    :model-value="checked"
    :label="label"
    size="sm"
    :ui="{ root: 'items-center', label: 'whitespace-nowrap text-xs font-normal text-muted' }"
    @update:model-value="emit('toggle')"
  />
</template>

<script setup lang="ts">
defineProps<{ label: string; checked: boolean }>()
const emit = defineEmits<{ toggle: [] }>()
</script>
