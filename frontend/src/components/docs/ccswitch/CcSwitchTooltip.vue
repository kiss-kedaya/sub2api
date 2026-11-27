<template>
  <Teleport to="body">
    <div
      v-if="tooltip"
      role="tooltip"
      class="ccswitch-app-demo pointer-events-none fixed z-50"
      :data-ccs-theme="themeAttr"
      :style="{
        left: `${tooltip.x}px`,
        top: `${tooltip.y}px`,
        transform: `translate(${tooltip.left ? 'calc(-100% - 12px)' : '12px'}, ${tooltip.up ? 'calc(-100% - 12px)' : '12px'})`,
      }"
    >
      <div
        class="w-max max-w-[min(18rem,calc(100vw-2rem))] whitespace-pre-line rounded-lg border border-[var(--ccs-border)] bg-[var(--app-surface)] px-3 py-2 text-xs font-medium leading-relaxed text-[var(--ccs-fg)] shadow-lg"
      >
        <span v-if="tooltip.title" class="mb-0.5 block font-semibold">{{ tooltip.title }}</span>
        <span>{{ tooltip.text }}</span>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import type { CcsTooltipPayload } from './ccswitchContext'

defineProps<{
  tooltip: CcsTooltipPayload | null
  themeAttr?: 'light' | 'dark'
}>()
</script>
