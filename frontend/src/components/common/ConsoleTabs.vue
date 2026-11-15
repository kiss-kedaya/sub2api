<template>
  <!-- Looks like UTabs (pill). Kept as its own tablist because pages link their panels to the
       `${id}-tab-${key}` ids, which UTabs generates itself. -->
  <div
    ref="tablist"
    class="relative inline-flex max-w-full flex-wrap gap-1 rounded-xl bg-elevated/80 p-1 ring-1 ring-black/5 backdrop-blur dark:bg-white/5 dark:ring-white/10"
    data-test="console-tabs"
    role="tablist"
    :aria-label="label"
  >
    <div
      v-for="(item, index) in items"
      :key="item.key"
      class="group relative inline-flex"
      :draggable="reorderable"
      :data-tab-key="item.key"
      @dragstart="onDragStart($event, item.key)"
      @dragover="onDragOver($event, item.key)"
      @drop="onDrop($event, item.key)"
      @dragend="resetDrag"
    >
      <UButton
        :id="`${id}-tab-${item.key}`"
        type="button"
        role="tab"
        :color="'neutral'"
        :variant="'ghost'"
        size="md"
        :class="[
          'min-w-0 justify-center rounded-lg px-3 py-1.5 font-medium transition-all duration-200 ease-out',
          modelValue === item.key
            ? 'bg-white text-highlighted shadow-sm ring-1 ring-black/5 dark:bg-white/15 dark:text-white dark:ring-white/15 dark:shadow-[0_1px_2px_rgba(0,0,0,0.4)]'
            : 'text-muted hover:bg-transparent hover:text-default',
          reorderable && 'cursor-grab active:cursor-grabbing',
          dragOverKey === item.key && draggingKey !== item.key && 'ring-2 ring-primary-400',
          draggingKey === item.key && 'opacity-50'
        ]"
        :aria-selected="modelValue === item.key ? 'true' : 'false'"
        :aria-controls="`${id}-panel-${item.key}`"
        :tabindex="modelValue === item.key ? 0 : -1"
        :draggable="reorderable ? 'true' : undefined"
        @click="emit('update:modelValue', item.key)"
        @keydown="onKeydown($event, index)"
      >
        <span class="flex min-w-0 items-center justify-center gap-1">
          <!-- The star marks the landing tab. It has to read on both the light
               selected pill and the dark track, so the colour follows the state
               instead of a single amber that disappears on the pink fill. -->
          <span
            v-if="defaultKey === item.key"
            class="flex shrink-0 items-center"
            data-test="console-tabs-default-star"
            :aria-label="defaultLabel"
            :title="defaultLabel"
          >
            <UIcon
              name="i-lucide-star"
              class="size-3.5 fill-current"
              :class="modelValue === item.key ? 'text-amber-500 dark:text-amber-300' : 'text-amber-400'"
            />
          </span>
          <span class="truncate">{{ item.label }}</span>
        </span>
      </UButton>
      <!-- Every user sees the star on the default tab; an administrator can
           hover any other tab to promote it. -->
      <button
        v-if="reorderable && defaultKey !== item.key"
        type="button"
        data-test="console-tabs-set-default"
        :data-default-tab-key="item.key"
        class="absolute -right-1 -top-1 flex size-4 items-center justify-center rounded-full bg-default text-muted opacity-0 shadow-sm ring-1 ring-black/10 transition-opacity hover:text-default focus-visible:opacity-100 group-hover:opacity-100 dark:bg-white/15 dark:text-white/80 dark:ring-white/20"
        :aria-label="setDefaultLabel"
        :title="setDefaultLabel"
        aria-pressed="false"
        @click.stop="onSetDefault(item.key)"
      >
        <UIcon name="i-lucide-star-off" class="size-3" />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref } from 'vue'

const props = defineProps<{
  id: string
  label: string
  modelValue: string
  items: { key: string; label: string }[]
  /** Enables HTML5 drag reordering and the interactive default-star overlay. */
  reorderable?: boolean
  /** Key of the tab marked as the default landing tab. */
  defaultKey?: string
  defaultLabel?: string
  setDefaultLabel?: string
}>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
  reorder: [keys: string[]]
  'set-default': [key: string]
}>()
const tablist = ref<HTMLElement | null>(null)
const draggingKey = ref<string | null>(null)
const dragOverKey = ref<string | null>(null)

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

function onDragStart(event: DragEvent, key: string) {
  if (!props.reorderable) return
  draggingKey.value = key
  dragOverKey.value = null
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    // Firefox refuses to start a drag unless some data is attached.
    event.dataTransfer.setData('text/plain', key)
  }
}

function onDragOver(event: DragEvent, key: string) {
  if (!props.reorderable || draggingKey.value === null) return
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
  dragOverKey.value = key
}

function onDrop(event: DragEvent, targetKey: string) {
  if (!props.reorderable || draggingKey.value === null) return
  event.preventDefault()
  const from = props.items.findIndex((item) => item.key === draggingKey.value)
  const to = props.items.findIndex((item) => item.key === targetKey)
  resetDrag()
  if (from < 0 || to < 0 || from === to) return
  const keys = props.items.map((item) => item.key)
  const [moved] = keys.splice(from, 1)
  keys.splice(to, 0, moved)
  emit('reorder', keys)
}

function onSetDefault(key: string) {
  if (!props.reorderable) return
  emit('set-default', key)
}

function resetDrag() {
  draggingKey.value = null
  dragOverKey.value = null
}
</script>
