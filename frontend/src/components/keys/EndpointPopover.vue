<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useClipboard } from '@/composables/useClipboard'
import type { CustomEndpoint } from '@/types'

const props = defineProps<{
  apiBaseUrl: string
  customEndpoints: CustomEndpoint[]
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const copiedEndpoint = ref<string | null>(null)
const activeIndex = ref<number | null>(null)
const activeTriggerEl = ref<HTMLElement | null>(null)
const popoverRef = ref<HTMLElement | null>(null)
const popoverStyle = ref<Record<string, string>>({})
const arrowLeft = ref('50%')
const placeBelow = ref(false)

let copiedResetTimer: number | undefined

const allEndpoints = computed(() => {
  const items: Array<{ name: string; endpoint: string; description: string; isDefault: boolean }> = []
  if (props.apiBaseUrl) {
    items.push({
      name: t('keys.endpoints.title'),
      endpoint: props.apiBaseUrl,
      description: '',
      isDefault: true,
    })
  }
  for (const ep of props.customEndpoints) {
    items.push({ ...ep, isDefault: false })
  }
  return items
})

const activeItem = computed(() =>
  activeIndex.value !== null ? (allEndpoints.value[activeIndex.value] ?? null) : null
)

function updatePosition() {
  const trigger = activeTriggerEl.value
  if (!trigger || typeof window === 'undefined') return
  const rect = trigger.getBoundingClientRect()
  const vw = window.innerWidth || 1024
  const vh = window.innerHeight || 768
  const margin = 12
  const popRect = popoverRef.value?.getBoundingClientRect()
  const popWidth = popRect?.width && popRect.width > 0 ? popRect.width : Math.min(384, vw - margin * 2)
  const popHeight = popRect?.height && popRect.height > 0 ? popRect.height : 72
  const centerX = rect.left + rect.width / 2
  const left = Math.max(margin, Math.min(centerX - popWidth / 2, vw - margin - popWidth))
  const shouldPlaceBelow = rect.top - popHeight - 10 < margin && rect.bottom + popHeight + 10 <= vh - margin
  placeBelow.value = shouldPlaceBelow
  const top = shouldPlaceBelow ? rect.bottom + 8 : Math.max(margin, rect.top - popHeight - 8)
  const arrowX = Math.max(14, Math.min(centerX - left, popWidth - 14))
  arrowLeft.value = `${arrowX}px`
  popoverStyle.value = {
    left: `${left}px`,
    top: `${top}px`,
    '--transform-origin': shouldPlaceBelow ? 'top center' : 'bottom center',
  }
}

function attachViewportListeners() {
  window.addEventListener('scroll', updatePosition, true)
  window.addEventListener('resize', updatePosition)
}

function detachViewportListeners() {
  window.removeEventListener('scroll', updatePosition, true)
  window.removeEventListener('resize', updatePosition)
}

async function openTooltip(index: number, el: HTMLElement | null) {
  if (!el) return
  activeIndex.value = index
  activeTriggerEl.value = el
  updatePosition()
  attachViewportListeners()
  await nextTick()
  if (activeIndex.value === index) {
    updatePosition()
  }
}

function onTriggerMouseEnter(index: number, event: MouseEvent) {
  if (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    !window.matchMedia('(hover: hover) and (pointer: fine)').matches
  ) {
    return
  }
  void openTooltip(index, event.currentTarget as HTMLElement | null)
}

function closeTooltip(index: number) {
  if (activeIndex.value !== index) return
  activeIndex.value = null
  activeTriggerEl.value = null
  detachViewportListeners()
}

function onTriggerFocusOut(index: number, event: FocusEvent) {
  const related = event.relatedTarget as Node | null
  const current = event.currentTarget as HTMLElement | null
  if (related && current?.contains(related)) return
  closeTooltip(index)
}

async function copy(url: string) {
  const success = await copyToClipboard(url, t('keys.endpoints.copied'))
  if (!success) return

  copiedEndpoint.value = url
  if (copiedResetTimer !== undefined) {
    window.clearTimeout(copiedResetTimer)
  }
  copiedResetTimer = window.setTimeout(() => {
    if (copiedEndpoint.value === url) {
      copiedEndpoint.value = null
    }
  }, 1800)
}

function tooltipHint(endpoint: string): string {
  return copiedEndpoint.value === endpoint
    ? t('keys.endpoints.copiedHint')
    : t('keys.endpoints.clickToCopy')
}

function speedTestUrl(endpoint: string): string {
  return `https://www.tcptest.cn/http/${encodeURIComponent(endpoint)}`
}

onBeforeUnmount(() => {
  if (copiedResetTimer !== undefined) {
    window.clearTimeout(copiedResetTimer)
  }
  detachViewportListeners()
})
</script>

<template>
  <div v-if="allEndpoints.length > 0" class="flex flex-wrap gap-2">
    <div
      v-for="(item, index) in allEndpoints"
      :key="index"
      class="flex items-center gap-1.5 rounded-md border border-default bg-default px-2.5 py-1.5 text-xs shadow-xs transition-colors [@media(hover:hover)_and_(pointer:fine)]:hover:border-accented"
    >
      <span class="font-medium text-toned">{{ item.name }}</span>
      <span
        v-if="item.isDefault"
        class="rounded-sm bg-elevated px-1 py-px text-[10px] font-medium leading-tight text-primary"
      >{{ t('keys.endpoints.default') }}</span>

      <span class="text-dimmed">|</span>

      <div
        class="relative flex items-center gap-1.5"
        data-test="endpoint-trigger"
        @mouseenter="onTriggerMouseEnter(index, $event)"
        @mouseleave="closeTooltip(index)"
        @focusin="openTooltip(index, $event.currentTarget as HTMLElement | null)"
        @focusout="onTriggerFocusOut(index, $event)"
      >
        <code
          class="cursor-pointer rounded-xs font-mono text-muted decoration-muted/60 decoration-dashed underline-offset-2 focus:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(hover:hover)_and_(pointer:fine)]:hover:text-primary [@media(hover:hover)_and_(pointer:fine)]:hover:underline"
          role="button"
          tabindex="0"
          @click="copy(item.endpoint)"
          @keydown.enter.prevent="copy(item.endpoint)"
          @keydown.space.prevent="copy(item.endpoint)"
        >{{ item.endpoint }}</code>

        <button
          type="button"
          class="inline-flex items-center justify-center rounded-sm p-0.5 transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(pointer:coarse)]:min-h-11 [@media(pointer:coarse)]:min-w-11"
          :class="copiedEndpoint === item.endpoint
            ? 'text-success'
            : 'text-dimmed [@media(hover:hover)_and_(pointer:fine)]:hover:text-highlighted'"
          :aria-label="tooltipHint(item.endpoint)"
          @click="copy(item.endpoint)"
        >
          <svg v-if="copiedEndpoint === item.endpoint" class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
          </svg>
          <svg v-else class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
          </svg>
        </button>

        <a
          :href="speedTestUrl(item.endpoint)"
          target="_blank"
          rel="noopener noreferrer"
          class="inline-flex items-center justify-center rounded-sm p-0.5 text-dimmed transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(hover:hover)_and_(pointer:fine)]:hover:text-warning [@media(pointer:coarse)]:min-h-11 [@media(pointer:coarse)]:min-w-11"
          :title="t('keys.endpoints.speedTest')"
        >
          <svg class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
          </svg>
        </a>
      </div>
    </div>

    <Teleport to="body">
      <div
        v-if="activeItem"
        ref="popoverRef"
        role="tooltip"
        :style="popoverStyle"
        data-test="endpoint-popover-tooltip"
        class="pointer-events-none fixed z-50 w-max max-w-[min(24rem,calc(100vw-24px))] origin-(--transform-origin) rounded-lg border border-default bg-default px-3 py-2.5 text-left shadow-(--app-shadow-popover) ring-1 ring-default transition-[opacity,transform] duration-150 ease-[cubic-bezier(0.23,1,0.32,1)] motion-reduce:transition-none"
      >
        <p
          v-if="activeItem.description"
          class="max-w-[24rem] wrap-anywhere text-xs leading-5 text-toned"
        >
          {{ activeItem.description }}
        </p>
        <p
          class="flex items-center gap-1.5 text-[11px] leading-4 text-primary"
          :class="activeItem.description ? 'mt-1.5' : ''"
        >
          <span class="h-1.5 w-1.5 rounded-full bg-primary"></span>
          {{ tooltipHint(activeItem.endpoint) }}
        </p>
        <div
          :style="{ left: arrowLeft }"
          :class="placeBelow ? '-top-1.5 border-l border-t' : '-bottom-1.5 border-b border-r'"
          class="absolute h-3 w-3 -translate-x-1/2 rotate-45 border-default bg-default"
        ></div>
      </div>
    </Teleport>
  </div>
</template>
