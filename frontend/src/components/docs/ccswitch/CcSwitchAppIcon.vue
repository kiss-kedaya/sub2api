<template>
  <span
    class="relative inline-flex shrink-0 items-center justify-center"
    :style="{ width: `${size}px`, height: `${size}px` }"
  >
    <span
      v-if="app.iconSvg"
      class="inline-flex h-full w-full shrink-0 items-center justify-center [&>svg]:block [&>svg]:h-full [&>svg]:w-full"
      :aria-label="app.label"
      role="img"
      v-html="app.iconSvg"
    />
    <img
      v-else
      :src="app.icon"
      :alt="app.label"
      class="h-full w-full object-contain"
    />
    <span
      v-if="app.badge"
      aria-hidden="true"
      class="absolute -bottom-[3px] -right-[3px] flex items-center justify-center rounded-[3px] border text-[var(--ccs-fg)]"
      :class="badgeClass"
      :style="{ width: `${size * 0.62}px`, height: `${size * 0.62}px` }"
    >
      <CcSwitchLucideIcon
        :name="app.badge === 'terminal' ? 'terminal' : 'monitor'"
        :stroke-width="2.5"
        :class-name="'block'"
        :style="{ width: `${size * 0.48}px`, height: `${size * 0.48}px` }"
      />
    </span>
  </span>
</template>

<script setup lang="ts">
import CcSwitchLucideIcon from './CcSwitchLucideIcon.vue'
import type { CcsAppSpec } from './ccswitchTypes'

withDefaults(
  defineProps<{
    app: CcsAppSpec
    size?: number
    badgeClass?: string
  }>(),
  {
    size: 16,
    badgeClass: 'border-[var(--app-sidebar)] bg-[var(--app-sidebar)]',
  },
)
</script>
