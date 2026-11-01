<template>
  <div class="relative w-full">
    <UInput
      :model-value="modelValue"
      type="text"
      icon="i-lucide-search"
      class="w-full"
      :placeholder="placeholder"
      @update:model-value="handleInput"
    />
  </div>
</template>

<script setup lang="ts">
import { useDebounceFn } from '@vueuse/core'

const props = withDefaults(defineProps<{
  modelValue: string
  placeholder?: string
  debounceMs?: number
}>(), {
  placeholder: 'Search...',
  debounceMs: 300
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'search', value: string): void
}>()

const debouncedEmitSearch = useDebounceFn((value: string) => {
  emit('search', value)
}, props.debounceMs)

const handleInput = (raw: string | number | null | undefined) => {
  const value = raw == null ? '' : String(raw)
  emit('update:modelValue', value)
  debouncedEmitSearch(value)
}
</script>
