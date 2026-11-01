<template>
  <!-- Looks like UTabs (pill). Kept as its own tablist because pages link their panels to the
       `${id}-tab-${key}` ids, which UTabs generates itself. -->
  <div
    ref="tablist"
    class="console-tabs relative inline-flex max-w-full flex-wrap gap-1 rounded-lg bg-elevated p-1"
    role="tablist"
    :aria-label="label"
  >
    <UButton
      v-for="(item, index) in items"
      :id="`${id}-tab-${item.key}`"
      :key="item.key"
      type="button"
      role="tab"
      :color="modelValue === item.key ? 'primary' : 'neutral'"
      :variant="modelValue === item.key ? 'solid' : 'ghost'"
      size="md"
      :class="[
        'min-w-0 justify-center rounded-md px-3 py-1.5 font-medium',
        modelValue !== item.key && 'text-muted hover:bg-transparent hover:text-default'
      ]"
      :aria-selected="modelValue === item.key ? 'true' : 'false'"
      :aria-controls="`${id}-panel-${item.key}`"
      :tabindex="modelValue === item.key ? 0 : -1"
      @click="emit('update:modelValue', item.key)"
      @keydown="onKeydown($event, index)"
    >
      <span class="truncate">{{ item.label }}</span>
    </UButton>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref } from 'vue'

const props = defineProps<{
  id: string
  label: string
  modelValue: string
  items: { key: string; label: string }[]
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const tablist = ref<HTMLElement | null>(null)

function onKeydown(event: KeyboardEvent, index: number) {
  const last = props.items.length - 1
  const next = event.key === 'ArrowRight' ? (index + 1) % props.items.length
    : event.key === 'ArrowLeft' ? (index + last) % props.items.length
      : event.key === 'Home' ? 0 : event.key === 'End' ? last : -1
  if (next < 0) return
  event.preventDefault()
  emit('update:modelValue', props.items[next].key)
  nextTick(() => tablist.value?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus())
}
</script>
