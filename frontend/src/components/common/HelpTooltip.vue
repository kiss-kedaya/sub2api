<script lang="ts">
// Shared across every HelpTooltip on the page: only one help bubble should be held open at a time, otherwise a
// touch user ends up with two pinned bubbles after tapping two icons.
const autoHintClosers = new Set<() => void>()

function closeOtherAutoHints(self: () => void) {
  for (const close of [...autoHintClosers]) {
    if (close !== self) close()
  }
}
</script>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useTemplateRef } from 'vue'

// trigger:
// - 'hover' (default): mouse enter / leave only. Kept unchanged for the existing call sites.
// - 'click': click toggles, outside click / Escape closes.
// - 'auto': hover opens, tap / click pins it open, keyboard focus opens, Escape / outside click / blur closes.
//   This is the one form fields use, because a hover-only bubble cannot be opened on a touch screen.
const props = withDefaults(defineProps<{
  content?: string
  trigger?: 'hover' | 'click' | 'auto'
  widthClass?: string
  ariaLabel?: string
}>(), {
  trigger: 'hover',
  widthClass: 'w-64',
  ariaLabel: undefined,
})

const show = ref(false)
// 'auto' only: true once the user tapped / clicked / pressed the trigger, so moving the pointer away keeps it
// open. That is what makes the bubble usable on touch, where there is no hover to hold it.
const pinned = ref(false)
const triggerRef = useTemplateRef<HTMLElement>('trigger')
const tooltipRef = useTemplateRef<HTMLElement>('tooltip')

function openTooltip() {
  show.value = true
}

function closeTooltip() {
  show.value = false
  pinned.value = false
}

// 'auto' opens are exclusive: another bubble closing is not a reason for this one to stay open.
function openAutoTooltip() {
  closeOtherAutoHints(closeTooltip)
  openTooltip()
}

function isInside(container: HTMLElement | null, target: EventTarget | null): boolean {
  return target instanceof Node && !!container?.contains(target)
}

function onEnter() {
  if (props.trigger !== 'hover') return
  openTooltip()
}

// 悬停模式下指针在触发图标与提示框之间往返时保持打开，便于选中提示里的文字。
function onLeave(event: MouseEvent) {
  if (props.trigger !== 'hover') return
  if (isInside(tooltipRef.value, event.relatedTarget)) return
  closeTooltip()
}

function onTriggerEnter() {
  if (props.trigger === 'auto') {
    // Touch also emits an emulated mouse enter right before the click that follows it; opening here is what
    // makes the tap feel immediate, and the click then pins it.
    openAutoTooltip()
    return
  }
  onEnter()
}

function onTriggerLeave(event: MouseEvent) {
  if (props.trigger === 'auto') {
    if (pinned.value || isInside(tooltipRef.value, event.relatedTarget)) return
    closeTooltip()
    return
  }
  onLeave(event)
}

function onTooltipLeave(event: MouseEvent) {
  if (props.trigger === 'click') return
  if (props.trigger === 'auto' && pinned.value) return
  if (isInside(triggerRef.value, event.relatedTarget)) return
  closeTooltip()
}

function onFocusIn() {
  if (props.trigger !== 'auto') return
  openAutoTooltip()
}

function onFocusOut(event: FocusEvent) {
  if (props.trigger !== 'auto') return
  if (isInside(triggerRef.value, event.relatedTarget) || isInside(tooltipRef.value, event.relatedTarget)) return
  closeTooltip()
}

function onClick(event: MouseEvent) {
  if (props.trigger === 'hover') return
  event.stopPropagation()
  if (props.trigger === 'auto') {
    // Already open from hover or focus: the click pins it, a second click closes it.
    if (show.value && !pinned.value) {
      pinned.value = true
      return
    }
    if (show.value) {
      closeTooltip()
      return
    }
    openAutoTooltip()
    pinned.value = true
    return
  }
  if (show.value) {
    closeTooltip()
    return
  }
  openTooltip()
}

function onDocumentClick(event: MouseEvent) {
  if (props.trigger === 'hover' || !show.value) return
  const target = event.target as Node | null
  if (!target) return
  if (triggerRef.value?.contains(target) || tooltipRef.value?.contains(target)) return
  closeTooltip()
}

function onDocumentKeydown(event: KeyboardEvent) {
  if (props.trigger === 'hover' || !show.value) return
  if (event.key === 'Escape') {
    closeTooltip()
  }
}

// The popover closes itself on outside pointerdown / Escape; the trigger toggles on its own click.
function onOpenChange(open: boolean) {
  if (!open) closeTooltip()
}

const popoverContent = {
  side: 'top' as const,
  align: 'center' as const,
  sideOffset: 8,
  // Keeps the bubble inside the viewport on a 390px phone instead of running off the edge.
  collisionPadding: 8,
  // A help bubble must not steal focus from the field the user is typing in.
  onOpenAutoFocus: (event: Event) => event.preventDefault(),
  onCloseAutoFocus: (event: Event) => event.preventDefault(),
  onPointerDownOutside: (event: CustomEvent<{ originalEvent: PointerEvent }>) => {
    if (isInside(triggerRef.value, event.detail?.originalEvent?.target ?? null)) event.preventDefault()
  },
  // The bubble is rendered outside the trigger, so while the trigger holds keyboard focus the
  // popover sees that focus as "outside" and closes itself the moment it opens. Focus on our
  // own trigger is what opened it; only focus that goes somewhere else should dismiss.
  // jsdom does not raise this event, so HintFormField.spec.ts cannot cover it: it was found and
  // checked in a real browser (Tab onto the icon, the bubble must still be there 500ms later).
  onFocusOutside: (event: CustomEvent<{ originalEvent: FocusEvent }>) => {
    if (isInside(triggerRef.value, event.detail?.originalEvent?.target ?? null)) event.preventDefault()
  },
}

onMounted(() => {
  autoHintClosers.add(closeTooltip)
  document.addEventListener('click', onDocumentClick, true)
  document.addEventListener('keydown', onDocumentKeydown)
})

onBeforeUnmount(() => {
  autoHintClosers.delete(closeTooltip)
  document.removeEventListener('click', onDocumentClick, true)
  document.removeEventListener('keydown', onDocumentKeydown)
})
</script>

<template>
  <div
    ref="trigger"
    class="group relative ml-1 inline-flex items-center align-middle"
    @mouseenter="onTriggerEnter"
    @mouseleave="onTriggerLeave"
    @focusin="onFocusIn"
    @focusout="onFocusOut"
    @click="onClick"
  >
    <UPopover
      :open="show"
      :content="popoverContent"
      :ui="{ content: 'z-[99999] max-w-[calc(100vw-1rem)]' }"
      @update:open="onOpenChange"
    >
      <template #anchor>
        <span class="inline-flex items-center">
          <!-- Trigger Icon -->
          <slot name="trigger">
            <button
              v-if="props.trigger === 'auto'"
              type="button"
              data-hint-trigger
              class="inline-flex size-5 cursor-help items-center justify-center rounded-full text-dimmed transition-colors hover:text-highlighted focus-visible:text-highlighted focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary"
              :aria-label="props.ariaLabel"
              :aria-expanded="show"
            >
              <UIcon name="i-lucide-info" class="size-4" />
            </button>
            <UIcon
              v-else
              name="i-lucide-info"
              class="size-4 cursor-help text-dimmed transition-colors hover:text-highlighted"
            />
          </slot>
        </span>
      </template>

      <template #content>
        <!-- before: 向下延伸一段透明区域，盖住提示框与触发图标之间的空隙，让指针能连续移入提示框。 -->
        <div
          ref="tooltip"
          role="tooltip"
          :class="[
            'relative select-text p-3 text-xs leading-relaxed text-default before:absolute before:inset-x-0 before:top-full before:h-3',
            props.trigger === 'click' && 'pr-8',
            props.widthClass,
          ]"
          @mouseleave="onTooltipLeave"
        >
          <UButton
            v-if="props.trigger === 'click'"
            icon="i-lucide-x"
            color="neutral"
            variant="ghost"
            size="xs"
            class="absolute right-1 top-1"
            aria-label="Close"
            @click.stop="closeTooltip"
          />
          <slot>{{ content }}</slot>
        </div>
      </template>
    </UPopover>
  </div>
</template>
