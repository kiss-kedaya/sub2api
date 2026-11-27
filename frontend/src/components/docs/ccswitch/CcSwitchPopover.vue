<template>
  <Teleport to="body">
    <div
      v-if="open && coords"
      ref="panelRef"
      class="ccswitch-app-demo fixed z-50 text-[var(--ccs-fg)]"
      :data-ccs-theme="themeAttr"
      :style="{
        left: `${coords.x}px`,
        top: `${coords.y}px`,
        transform: `translate(${align === 'end' ? '-100%' : '0'}, ${coords.up ? '-100%' : '0'})`,
      }"
      @mousedown.stop
      @click.stop
    >
      <div
        class="rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)] p-1 shadow-[0_4px_12px_rgba(16,24,40,0.12),0_1px_3px_rgba(16,24,40,0.08)]"
        :class="panelClass"
      >
        <slot />
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useCcSwitchWorkbench } from './ccswitchContext'

const props = withDefaults(
  defineProps<{
    anchor: HTMLElement | null
    open: boolean
    align?: 'start' | 'end'
    panelClass?: string
  }>(),
  {
    align: 'end',
    panelClass: 'w-[220px]',
  },
)

const emit = defineEmits<{
  close: []
}>()

const { effectiveThemeAttr } = useCcSwitchWorkbench()
const themeAttr = effectiveThemeAttr
const panelRef = ref<HTMLElement | null>(null)
const coords = ref<{ x: number; y: number; up: boolean } | null>(null)

function updatePosition() {
  if (!props.open || !props.anchor) {
    coords.value = null
    return
  }
  const rect = props.anchor.getBoundingClientRect()
  const up = rect.bottom + 320 > window.innerHeight && rect.top > 320
  coords.value = {
    x: props.align === 'end' ? rect.right : rect.left,
    y: up ? rect.top - 4 : rect.bottom + 4,
    up,
  }
  nextTick(() => {
    if (!panelRef.value || !coords.value) return
    const pRect = panelRef.value.getBoundingClientRect()
    let dx = 0
    if (pRect.left < 8) dx = 8 - pRect.left
    else if (pRect.right > window.innerWidth - 8) dx = window.innerWidth - 8 - pRect.right
    if (Math.round(dx) !== 0) {
      coords.value = { ...coords.value, x: coords.value.x + dx }
    }
  })
}

function handleDocMouseDown(e: MouseEvent) {
  const target = e.target as Node | null
  if (!target) return
  if (panelRef.value?.contains(target) || props.anchor?.contains(target)) return
  emit('close')
}

function handleKeyDown(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

function handleScroll(e: Event) {
  const target = e.target as Node | null
  if (target && panelRef.value?.contains(target)) return
  emit('close')
}

function handleResize() {
  emit('close')
}

let listenersBound = false
function bindListeners() {
  if (listenersBound || typeof document === 'undefined') return
  listenersBound = true
  document.addEventListener('mousedown', handleDocMouseDown)
  document.addEventListener('keydown', handleKeyDown)
  window.addEventListener('scroll', handleScroll, true)
  window.addEventListener('resize', handleResize)
}

function unbindListeners() {
  if (!listenersBound || typeof document === 'undefined') return
  listenersBound = false
  document.removeEventListener('mousedown', handleDocMouseDown)
  document.removeEventListener('keydown', handleKeyDown)
  window.removeEventListener('scroll', handleScroll, true)
  window.removeEventListener('resize', handleResize)
}

watch(
  [() => props.open, () => props.anchor, () => props.align],
  ([isOpen]) => {
    if (isOpen) {
      updatePosition()
      bindListeners()
    } else {
      coords.value = null
      unbindListeners()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  unbindListeners()
})
</script>
