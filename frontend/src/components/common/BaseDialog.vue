<template>
  <Teleport to="body">
    <Transition name="base-dialog">
      <!-- The overlay is also the portal target of Nuxt UI popovers opened inside the dialog,
           so they stack above the panel and share its inert state. -->
      <div
        v-if="show"
        ref="overlayRef"
        class="modal-overlay base-dialog"
        :style="zIndexStyle"
        :aria-labelledby="dialogId"
        role="dialog"
        :aria-modal="isTopmost || undefined"
        :aria-hidden="!isTopmost || undefined"
        :inert="!isTopmost || undefined"
        @click.self="handleClose"
      >
        <!-- Modal panel -->
        <div ref="dialogRef" :class="['modal-content', widthClasses]" @click.stop>
          <!-- Header -->
          <div class="modal-header">
            <h3 :id="dialogId" class="modal-title">
              {{ title }}
            </h3>
            <UButton
              v-if="showCloseButton"
              icon="i-lucide-x"
              color="neutral"
              variant="ghost"
              square
              class="-me-1.5 shrink-0 text-muted hover:bg-elevated hover:text-default dark:text-dark-500 dark:hover:bg-dark-700 dark:hover:text-dark-300"
              aria-label="Close modal"
              @click="emit('close')"
            />
          </div>

          <!-- Body -->
          <div ref="modalBodyRef" class="modal-body">
            <slot></slot>
          </div>

          <!-- Footer -->
          <div v-if="$slots.footer" class="modal-footer">
            <slot name="footer"></slot>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script lang="ts">
let dialogIdCounter = 0
</script>

<script setup lang="ts">
import { computed, watch, onMounted, onUnmounted, provide, ref, nextTick, type Ref } from 'vue'
import { portalTargetInjectionKey } from '@nuxt/ui/composables/usePortal'
import { useDialogStack } from '@/composables/useDialogStack'

// 生成唯一ID以避免多个对话框时ID冲突
const dialogId = `modal-title-${++dialogIdCounter}`

// 焦点管理
const overlayRef = ref<HTMLElement | null>(null)
const dialogRef = ref<HTMLElement | null>(null)
const modalBodyRef = ref<HTMLElement | null>(null)
let previousActiveElement: HTMLElement | null = null

type DialogWidth = 'narrow' | 'normal' | 'wide' | 'extra-wide' | 'full'

interface Props {
  show: boolean
  title: string
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

// Nuxt UI overlays (menus, popovers, tooltips) opened from the dialog teleport into its
// overlay instead of <body>: a body-level popover would sit under this z-indexed layer.
provide(portalTargetInjectionKey, computed(() => overlayRef.value ?? 'body') as Ref<string | HTMLElement>)

const widthClasses = computed(() => {
  // Width guidance: narrow=confirm/short prompts, normal=standard forms,
  // wide=multi-section forms or rich content, extra-wide=analytics/tables,
  // full=full-screen or very dense layouts.
  const widths: Record<DialogWidth, string> = {
    narrow: 'max-w-md',
    normal: 'max-w-lg',
    wide: 'w-full sm:max-w-2xl md:max-w-3xl lg:max-w-4xl',
    'extra-wide': 'w-full sm:max-w-3xl md:max-w-4xl lg:max-w-5xl xl:max-w-6xl',
    full: 'w-full sm:max-w-4xl md:max-w-5xl lg:max-w-6xl xl:max-w-7xl'
  }
  return widths[props.width]
})

const handleClose = () => {
  if (isTopmost.value && props.closeOnClickOutside) {
    emit('close')
  }
}

const handleEscape = (event: KeyboardEvent) => {
  if (!props.show || !isTopmost.value) return
  // An open Nuxt UI layer inside the dialog (menu, popover, tooltip) takes this Escape first.
  if (event.key === 'Escape' && overlayRef.value?.querySelector('[data-dismissable-layer]')) return
  if (props.closeOnEscape && event.key === 'Escape') {
    event.preventDefault()
    emit('close')
  }
  if (event.key === 'Tab' && dialogRef.value) {
    const focusable = Array.from(dialogRef.value.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )).filter(element => !element.hidden && element.getAttribute('aria-hidden') !== 'true')
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
      // 保存当前焦点元素
      previousActiveElement = document.activeElement as HTMLElement
      // 使用CSS类而不是直接操作style,更易于管理多个对话框
      openLayer()

      // 等待DOM更新后设置焦点到对话框
      await nextTick()
      if (modalBodyRef.value) {
        modalBodyRef.value.scrollTop = 0
      }
      if (dialogRef.value && isTopmost.value) {
        const firstFocusable = dialogRef.value.querySelector<HTMLElement>(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
        )
        firstFocusable?.focus()
      }
    } else {
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
  // 确保组件卸载时移除滚动锁定
  closeLayer()
})
</script>

<style>
/* Light theme in the manner of Nuxt UI's modal: a flat neutral veil (no blur), a white panel
   with a hairline ring, shadow-xl, 12px radius and solid hairline dividers. Unlayered, so it
   overrides the legacy `modal-*` utilities; the night theme keeps today's styling. */
html:not(.dark) .base-dialog.modal-overlay {
  background-color: color-mix(in oklab, var(--ui-bg-elevated) 75%, transparent);
  backdrop-filter: none;
  -webkit-backdrop-filter: none;
}

html:not(.dark) .base-dialog .modal-content {
  border-color: var(--ui-border);
  border-radius: calc(var(--ui-radius) * 3);
  background-color: var(--ui-bg);
  box-shadow: 0 20px 25px -5px rgb(10 10 10 / 0.1), 0 8px 10px -6px rgb(10 10 10 / 0.08);
}

html:not(.dark) .base-dialog .modal-header::after,
html:not(.dark) .base-dialog .modal-footer::before {
  background: var(--ui-border);
}

.base-dialog .modal-title {
  font-size: 1rem;
  line-height: 1.5rem;
  letter-spacing: 0;
}

/* Quick, quiet motion: fade the veil, lift the panel 4px. */
.base-dialog-enter-active,
.base-dialog-leave-active {
  transition: opacity 150ms cubic-bezier(0.2, 0, 0, 1);
}

.base-dialog-enter-active .modal-content,
.base-dialog-leave-active .modal-content {
  transition: opacity 150ms cubic-bezier(0.2, 0, 0, 1), transform 150ms cubic-bezier(0.2, 0, 0, 1);
}

.base-dialog-enter-from,
.base-dialog-leave-to {
  opacity: 0;
}

.base-dialog-enter-from .modal-content,
.base-dialog-leave-to .modal-content {
  opacity: 0;
  transform: translateY(4px) scale(0.98);
}

@media (prefers-reduced-motion: reduce) {
  .base-dialog-enter-active,
  .base-dialog-leave-active,
  .base-dialog-enter-active .modal-content,
  .base-dialog-leave-active .modal-content {
    transition-duration: 1ms;
  }

  .base-dialog-enter-from .modal-content,
  .base-dialog-leave-to .modal-content {
    transform: none;
  }
}
</style>
