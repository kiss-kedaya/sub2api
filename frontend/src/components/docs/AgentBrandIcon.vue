<template>
  <PlatformIcon v-if="platform" :platform="platform" :size="platformSize" />
  <!-- OpenCode mark -->
  <svg
    v-else-if="iconKey === 'opencode'"
    :class="[sizeClass, 'text-slate-900 dark:text-slate-100']"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    aria-hidden="true"
  >
    <rect x="3" y="4" width="18" height="16" rx="3.5" />
    <path stroke-linecap="round" stroke-linejoin="round" d="m8.5 9.5 3 2.5-3 2.5m4.5 0h3" />
  </svg>
  <!-- Cursor mark -->
  <svg
    v-else-if="iconKey === 'cursor'"
    :class="[sizeClass, 'text-slate-900 dark:text-slate-100']"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.8"
    aria-hidden="true"
  >
    <path stroke-linejoin="round" d="M12 2.5 20.5 7.4v9.2L12 21.5 3.5 16.6V7.4L12 2.5Z" />
    <path stroke-linejoin="round" d="M12 2.5V12m0 0 8.5-4.6M12 12l-8.5-4.6M12 12v9.5" opacity="0.55" />
    <path fill="currentColor" stroke="none" d="M12 6.8 16.5 12 12 17.2 9.8 12 12 6.8Z" />
  </svg>
  <!-- VS Code mark -->
  <svg
    v-else-if="iconKey === 'vscode'"
    :class="[sizeClass, 'text-sky-600 dark:text-sky-400']"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.9"
    aria-hidden="true"
  >
    <path stroke-linecap="round" stroke-linejoin="round" d="M16.5 3.5 21 5.6v12.8l-4.5 2.1L6.2 12l10.3-8.5Z" />
    <path stroke-linecap="round" stroke-linejoin="round" d="M3.5 8.5 6.2 12l-2.7 3.5L16.5 20.5V3.5L3.5 8.5Z" />
  </svg>
  <!-- OpenClaw mark -->
  <svg
    v-else-if="iconKey === 'openclaw'"
    :class="[sizeClass, 'text-rose-600 dark:text-rose-400']"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.9"
    aria-hidden="true"
  >
    <path stroke-linecap="round" stroke-linejoin="round" d="M6.5 16.5c-2.2-2.2-2.5-6-.6-8.7 1.3-1.9 3.4-3.1 6.1-3.3-1.4 2-1.4 4.1 0 5.5 1.4-1.4 1.4-3.5 0-5.5 2.7.2 4.8 1.4 6.1 3.3 1.9 2.7 1.6 6.5-.6 8.7L12 20.5l-5.5-4Z" />
    <circle cx="9.5" cy="13.5" r="1" fill="currentColor" stroke="none" />
    <circle cx="14.5" cy="13.5" r="1" fill="currentColor" stroke="none" />
  </svg>
  <!-- CC Switch mark -->
  <svg
    v-else-if="iconKey === 'ccswitch'"
    :class="[sizeClass, 'text-indigo-600 dark:text-indigo-400']"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.9"
    aria-hidden="true"
  >
    <rect x="2.75" y="5" width="18.5" height="6.25" rx="3.125" />
    <circle cx="15.8" cy="8.125" r="1.6" fill="currentColor" stroke="none" />
    <rect x="2.75" y="12.75" width="18.5" height="6.25" rx="3.125" />
    <circle cx="8.2" cy="15.875" r="1.6" fill="currentColor" stroke="none" />
  </svg>
  <!-- Fallback terminal mark -->
  <svg
    v-else
    :class="[sizeClass, 'text-slate-600 dark:text-slate-300']"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.9"
    aria-hidden="true"
  >
    <path stroke-linecap="round" stroke-linejoin="round" d="m6.75 7.5 3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0 0 21 17.25V6.75A2.25 2.25 0 0 0 18.75 4.5H5.25A2.25 2.25 0 0 0 3 6.75v10.5A2.25 2.25 0 0 0 5.25 20.25Z" />
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { GroupPlatform } from '@/types'

const props = withDefaults(
  defineProps<{
    iconKey: string
    platform?: GroupPlatform
    size?: 'sm' | 'md' | 'lg'
  }>(),
  {
    size: 'md',
  },
)

const platformSize = computed(() => (props.size === 'lg' ? 'lg' : props.size === 'sm' ? 'sm' : 'md'))

const sizeClass = computed(() => {
  switch (props.size) {
    case 'sm':
      return 'h-4 w-4 shrink-0'
    case 'lg':
      return 'h-6 w-6 shrink-0'
    default:
      return 'h-5 w-5 shrink-0'
  }
})
</script>
