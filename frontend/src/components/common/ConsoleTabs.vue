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

<style>
/* Light theme: Nuxt UI's pill tabs, a neutral track with a raised white segment and no
   underline. The night theme keeps the underline tabs above. */
html:not(.dark) .console-tabs.console-tabs {
  display: inline-flex;
  gap: 4px;
  padding: 4px;
  border: 0;
  border-radius: calc(var(--ui-radius) * 2);
  background: var(--ui-bg-elevated);
}
html:not(.dark) .console-tabs.console-tabs button {
  min-height: 34px;
  padding: 6px 14px;
  border: 0;
  border-radius: calc(var(--ui-radius) * 1.5);
  color: var(--ui-text-muted);
  transition: background-color 150ms ease, color 150ms ease, box-shadow 150ms ease;
}
html:not(.dark) .console-tabs.console-tabs button:hover { color: var(--ui-text-highlighted); }
html:not(.dark) .console-tabs.console-tabs button[aria-selected='true'] {
  background: var(--ui-bg);
  color: var(--ui-text-highlighted);
  box-shadow: 0 1px 3px rgb(10 10 10 / 0.06), 0 1px 2px -1px rgb(10 10 10 / 0.06), 0 0 0 1px rgb(10 10 10 / 0.04);
}
html:not(.dark) .console-tabs.console-tabs button:focus-visible { outline-color: var(--ui-border-inverted); }
</style>
