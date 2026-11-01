<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useTemplateRef } from 'vue'

const props = withDefaults(defineProps<{
  content?: string
  trigger?: 'hover' | 'click'
  widthClass?: string
}>(), {
  trigger: 'hover',
  widthClass: 'w-64',
})

const show = ref(false)
const triggerRef = useTemplateRef<HTMLElement>('trigger')
const tooltipRef = useTemplateRef<HTMLElement>('tooltip')

function openTooltip() {
  show.value = true
}

function closeTooltip() {
  show.value = false
}

function onEnter() {
  if (props.trigger !== 'hover') return
  openTooltip()
}

function isInside(container: HTMLElement | null, target: EventTarget | null): boolean {
  return target instanceof Node && !!container?.contains(target)
}

// 悬停模式下指针在触发图标与提示框之间往返时保持打开，便于选中提示里的文字。
function onLeave(event: MouseEvent) {
  if (props.trigger !== 'hover') return
  if (isInside(tooltipRef.value, event.relatedTarget)) return
  closeTooltip()
}

function onTooltipLeave(event: MouseEvent) {
  if (props.trigger !== 'hover') return
  if (isInside(triggerRef.value, event.relatedTarget)) return
  closeTooltip()
}

function onClick(event: MouseEvent) {
  if (props.trigger !== 'click') return
  event.stopPropagation()
  if (show.value) {
    closeTooltip()
    return
  }
  openTooltip()
}

function onDocumentClick(event: MouseEvent) {
  if (props.trigger !== 'click' || !show.value) return
  const target = event.target as Node | null
  if (!target) return
  if (triggerRef.value?.contains(target) || tooltipRef.value?.contains(target)) return
  closeTooltip()
}

function onDocumentKeydown(event: KeyboardEvent) {
  if (props.trigger !== 'click') return
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
  // A help bubble must not steal focus from the field the user is typing in.
  onOpenAutoFocus: (event: Event) => event.preventDefault(),
  onCloseAutoFocus: (event: Event) => event.preventDefault(),
  onPointerDownOutside: (event: CustomEvent<{ originalEvent: PointerEvent }>) => {
    if (isInside(triggerRef.value, event.detail?.originalEvent?.target ?? null)) event.preventDefault()
  },
}

onMounted(() => {
  document.addEventListener('click', onDocumentClick, true)
  document.addEventListener('keydown', onDocumentKeydown)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', onDocumentClick, true)
  document.removeEventListener('keydown', onDocumentKeydown)
})
</script>

<template>
  <div
    ref="trigger"
    class="group relative ml-1 inline-flex items-center align-middle"
    @mouseenter="onEnter"
    @mouseleave="onLeave"
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
            <UIcon
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
            'relative p-3 text-xs leading-relaxed text-default before:absolute before:inset-x-0 before:top-full before:h-3',
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
