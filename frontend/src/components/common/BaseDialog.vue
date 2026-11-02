<template>
  <BaseDialogPortal>
    <!-- The veil and stacking layer. It keeps the `modal-overlay` class: the console theme scopes
         its colour variables to `body > .modal-overlay`, and dialog content relies on them.
         It stays mounted until the panel's closing animation has finished. -->
    <div
      v-if="rendered"
      ref="overlayRef"
      class="modal-overlay base-dialog"
      :data-state="show ? 'open' : 'closed'"
      :style="zIndexStyle"
      :aria-hidden="!isTopmost || undefined"
      :inert="!isTopmost || undefined"
      @click.self="handleClose"
    >
      <!-- Nuxt UI modal rendered in place: the layer above owns stacking, Escape and outside
           clicks, so legacy popups teleported to <body> stay usable while a dialog is open. -->
      <UModal
        :open="show"
        :title="title"
        :description="description"
        :portal="false"
        :overlay="false"
        :modal="false"
        :dismissible="false"
        :close="showCloseButton ? closeButtonProps : false"
        :content="contentProps"
        :ui="modalUi"
        @after:leave="onPanelLeft"
      >
        <template #title>
          <span :id="dialogId">{{ title }}</span>
        </template>
        <template v-if="$slots.description" #description>
          <slot name="description"></slot>
        </template>
        <template #body>
          <slot></slot>
        </template>
        <template v-if="$slots.footer" #footer>
          <slot name="footer"></slot>
        </template>
      </UModal>

      <!-- Portal target of Nuxt UI menus, popovers and tooltips opened inside the dialog, so they
           stack above the panel and share its inert state. -->
      <div ref="popoverHostRef" class="base-dialog-portal absolute" data-base-dialog-portal></div>
    </div>
  </BaseDialogPortal>
</template>

<script lang="ts">
let dialogIdCounter = 0
</script>

<script setup lang="ts">
import { computed, watch, onMounted, onUnmounted, provide, ref, nextTick, type Ref } from 'vue'
import { portalTargetInjectionKey } from '@nuxt/ui/composables/usePortal'
import { useDialogStack } from '@/composables/useDialogStack'
import BaseDialogPortal from './BaseDialogPortal'

// 生成唯一ID以避免多个对话框时ID冲突；同一实例重开时保持不变
const dialogId = `modal-title-${++dialogIdCounter}`

const overlayRef = ref<HTMLElement | null>(null)
const popoverHostRef = ref<HTMLElement | null>(null)
let previousActiveElement: HTMLElement | null = null

// The layer is mounted while the dialog is open and while the panel plays its closing animation;
// the panel (reka-ui Presence) reports the end of that animation through `after:leave`.
const rendered = ref(false)
let leaveFallbackTimer: ReturnType<typeof setTimeout> | null = null
const clearLeaveFallback = () => {
  if (leaveFallbackTimer) clearTimeout(leaveFallbackTimer)
  leaveFallbackTimer = null
}

type DialogWidth = 'narrow' | 'normal' | 'wide' | 'extra-wide' | 'full'

interface Props {
  show: boolean
  title: string
  /** Optional line under the title, as in Nuxt UI's modal header. */
  description?: string
  width?: DialogWidth
  closeOnEscape?: boolean
  closeOnClickOutside?: boolean
  showCloseButton?: boolean
  zIndex?: number
}

interface Emits {
  (e: 'close'): void
}

const props = withDefaults(defineProps<Props>(), {
  width: 'normal',
  closeOnEscape: true,
  closeOnClickOutside: false,
  showCloseButton: true,
  zIndex: 50
})

const emit = defineEmits<Emits>()

const { isTopmost, zIndex, open: openLayer, close: closeLayer } = useDialogStack(() => props.zIndex)
const zIndexStyle = computed(() => ({ zIndex: zIndex.value }))

// Nuxt UI overlays (menus, popovers, tooltips) opened from the dialog teleport into the layer
// instead of <body>: a body-level popover would sit under this z-indexed layer.
provide(
  portalTargetInjectionKey,
  computed(() => popoverHostRef.value ?? overlayRef.value ?? 'body') as Ref<string | HTMLElement>
)

// Width guidance: narrow=confirm/short prompts, normal=standard forms,
// wide=multi-section forms or rich content, extra-wide=analytics/tables,
// full=full-screen or very dense layouts.
const widthClasses: Record<DialogWidth, string> = {
  narrow: 'max-w-md',
  normal: 'max-w-lg',
  wide: 'max-w-none sm:max-w-2xl md:max-w-3xl lg:max-w-4xl',
  'extra-wide': 'max-w-none sm:max-w-3xl md:max-w-4xl lg:max-w-5xl xl:max-w-6xl',
  full: 'max-w-none sm:max-w-4xl md:max-w-5xl lg:max-w-6xl xl:max-w-7xl'
}

// The panel is laid out by the veil (flex, centred) instead of Nuxt UI's fixed + translate
// centring: a resting transform would make the panel the containing block of `position: fixed`
// content inside it.
const modalUi = computed(() => ({
  content: `base-dialog-panel relative inset-auto translate-none w-full max-h-[95vh] sm:max-h-[90vh] ${widthClasses[props.width]}`,
  header: ['base-dialog-header', props.showCloseButton && 'pe-14 sm:pe-14'].filter(Boolean).join(' '),
  body: 'base-dialog-body',
  footer: 'base-dialog-footer justify-end gap-2'
}))

const contentProps = computed(() => ({
  'aria-labelledby': dialogId,
  'aria-modal': isTopmost.value ? ('true' as const) : undefined,
  // Focus is placed and restored below, in step with the dialog stack.
  onOpenAutoFocus: (event: Event) => event.preventDefault(),
  onCloseAutoFocus: (event: Event) => event.preventDefault()
}))

const panelElement = () => overlayRef.value?.querySelector<HTMLElement>('.base-dialog-panel') ?? null

// Only the close button, Escape and the veil close the dialog. UModal's own `update:open` is not
// used: reka also reports Escape and outside interaction through it, which this layer owns.
const closeButtonProps = { onClick: () => emit('close') }

const onPanelLeft = () => {
  if (props.show) return
  clearLeaveFallback()
  rendered.value = false
}

const handleClose = () => {
  if (isTopmost.value && props.closeOnClickOutside) {
    emit('close')
  }
}

const focusableSelector =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

const handleEscape = (event: KeyboardEvent) => {
  if (!props.show || !isTopmost.value) return
  const panel = panelElement()
  // An open Nuxt UI layer inside the dialog (menu, popover, tooltip) takes this Escape first.
  if (event.key === 'Escape') {
    const layers = overlayRef.value?.querySelectorAll('[data-dismissable-layer]') ?? []
    if (Array.from(layers).some(layer => layer !== panel)) return
  }
  if (props.closeOnEscape && event.key === 'Escape') {
    event.preventDefault()
    emit('close')
  }
  if (event.key === 'Tab' && panel) {
    const focusable = Array.from(panel.querySelectorAll<HTMLElement>(focusableSelector))
      .filter(element => !element.hidden && element.getAttribute('aria-hidden') !== 'true')
    const first = focusable[0]
    const last = focusable.at(-1)
    if (first && last && (event.shiftKey ? document.activeElement === first : document.activeElement === last)) {
      event.preventDefault()
      ;(event.shiftKey ? last : first).focus()
    }
  }
}

// Prevent body scroll when modal is open and manage focus
watch(
  () => props.show,
  async (isOpen) => {
    if (isOpen) {
      clearLeaveFallback()
      rendered.value = true
      // 保存当前焦点元素
      previousActiveElement = document.activeElement as HTMLElement
      openLayer()

      // 等待DOM更新后设置焦点到对话框
      await nextTick()
      const panel = panelElement()
      const body = panel?.querySelector<HTMLElement>('.base-dialog-body')
      if (body) body.scrollTop = 0
      if (panel && isTopmost.value) {
        // Focus moves into the dialog without drawing a focus ring; the next Tab shows it.
        panel.querySelector<HTMLElement>(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
        )?.focus()
      }
    } else {
      // Safety net if the panel never reports the end of its closing animation.
      if (rendered.value) {
        clearLeaveFallback()
        leaveFallbackTimer = setTimeout(() => {
          leaveFallbackTimer = null
          if (!props.show) rendered.value = false
        }, 400)
      }
      const wasTopmost = isTopmost.value
      closeLayer()
      // 恢复之前的焦点
      if (wasTopmost && previousActiveElement?.isConnected && typeof previousActiveElement.focus === 'function') {
        previousActiveElement.focus()
      }
      previousActiveElement = null
    }
  },
  { immediate: true }
)

onMounted(() => {
  document.addEventListener('keydown', handleEscape)
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleEscape)
  clearLeaveFallback()
  // 确保组件卸载时移除滚动锁定
  closeLayer()
})
</script>

<style>
/* The veil without the console theme: a flat neutral wash, no blur. The theme's own overlay
   rules are more specific and still win while it is loaded. */
.base-dialog.modal-overlay {
  background-color: color-mix(in oklab, var(--ui-bg-elevated) 75%, transparent);
  backdrop-filter: none;
  -webkit-backdrop-filter: none;
}

/* Motion: Nuxt UI's modal scales the panel in and out; the veil fades along with it. While the
   panel closes, the layer no longer takes clicks. */
@keyframes base-dialog-veil-in {
  from { opacity: 0; }
}

@keyframes base-dialog-veil-out {
  to { opacity: 0; }
}

.base-dialog.modal-overlay[data-state='open'] {
  animation: base-dialog-veil-in 200ms cubic-bezier(0.2, 0, 0, 1);
}

.base-dialog.modal-overlay[data-state='closed'] {
  animation: base-dialog-veil-out 200ms cubic-bezier(0.2, 0, 0, 1) forwards;
  pointer-events: none;
}

@media (prefers-reduced-motion: reduce) {
  .base-dialog.modal-overlay,
  .base-dialog .base-dialog-panel {
    animation: none !important;
  }
}
</style>
