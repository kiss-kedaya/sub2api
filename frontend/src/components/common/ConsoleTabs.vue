<template>
  <div ref="tablist" class="console-tabs" role="tablist" :aria-label="label">
    <button
      v-for="(item, index) in items"
      :id="`${id}-tab-${item.key}`"
      :key="item.key"
      type="button"
      role="tab"
      :aria-selected="modelValue === item.key"
      :aria-controls="`${id}-panel-${item.key}`"
      :tabindex="modelValue === item.key ? 0 : -1"
      @click="emit('update:modelValue', item.key)"
      @keydown="onKeydown($event, index)"
    >
      {{ item.label }}
    </button>
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

<style scoped>
.console-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 0 24px;
  min-width: 0;
  border-bottom: 1px solid var(--signal-line);
}
.console-tabs button {
  min-height: 46px;
  max-width: 100%;
  padding: 12px 2px;
  border-bottom: 2px solid transparent;
  color: var(--signal-muted);
  font-size: 13px;
  font-weight: 600;
  text-align: left;
  overflow-wrap: anywhere;
  transition: color 140ms ease, border-color 140ms ease;
}
.console-tabs button:hover { color: var(--signal-text); }
.console-tabs button[aria-selected='true'] { color: var(--signal-text); border-color: var(--signal-accent); }
.console-tabs button:focus-visible { outline: 2px solid var(--signal-accent); outline-offset: -5px; border-radius: 4px; }
@media (prefers-reduced-motion: reduce) {
  .console-tabs button { transition: none; }
}
</style>
