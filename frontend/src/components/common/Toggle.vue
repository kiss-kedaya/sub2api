<template>
  <!-- Fallthrough attrs (data-testid, aria-label, disabled) land on the switch button. -->
  <USwitch
    :model-value="modelValue"
    color="primary"
    size="md"
    :ui="{
      base: 'cursor-pointer dark:data-[state=checked]:bg-primary-600 dark:data-[state=unchecked]:bg-dark-600',
      thumb: 'dark:bg-white'
    }"
    @update:model-value="toggle"
  />
</template>

<script setup lang="ts">
const props = defineProps<{
  modelValue: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
}>()

function toggle() {
  emit('update:modelValue', !props.modelValue)
}
</script>
