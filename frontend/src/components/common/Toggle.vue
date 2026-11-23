<template>
  <!-- Fallthrough attrs (data-testid, aria-label, disabled) land on the switch button. -->
  <USwitch
    :model-value="modelValue"
    color="primary"
    :size="size"
    :ui="{ base: 'cursor-pointer' }"
    @update:model-value="toggle"
  />
</template>

<script setup lang="ts">
const props = withDefaults(defineProps<{
  modelValue: boolean
  /**
   * Nuxt UI switch scale. `xl` (w-11 h-6) is the headline toggle of a card,
   * `md` (w-9 h-5) the default for form rows and inline lists.
   */
  size?: 'sm' | 'md' | 'lg' | 'xl'
}>(), { size: 'md' })

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
}>()

function toggle() {
  emit('update:modelValue', !props.modelValue)
}
</script>
