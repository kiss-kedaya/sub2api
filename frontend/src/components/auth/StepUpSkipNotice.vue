<template>
  <Transition
    enter-active-class="transition duration-200 ease-out"
    enter-from-class="translate-y-2 opacity-0"
    enter-to-class="translate-y-0 opacity-100"
    leave-active-class="transition duration-150 ease-in"
    leave-from-class="translate-y-0 opacity-100"
    leave-to-class="translate-y-2 opacity-0"
  >
    <div
      v-if="visible"
      class="pointer-events-none fixed bottom-4 right-4 z-[60]"
      role="status"
      aria-live="polite"
      data-testid="step-up-skip-notice"
    >
      <div
        class="pointer-events-auto flex items-center gap-2 rounded-full border border-amber-300/70 bg-amber-50 px-3 py-1.5 text-xs font-medium text-amber-900 shadow-lg dark:border-amber-500/40 dark:bg-amber-950/85 dark:text-amber-200"
      >
        <UIcon name="i-lucide-shield-check" class="size-4 shrink-0" />
        <span data-testid="step-up-skip-notice-text">
          {{ t('stepUp.skipNotice', { time: formattedRemaining }) }}
        </span>
      </div>
    </div>
  </Transition>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useStepUpSkip } from '@/composables/useStepUpSkip'

const { t } = useI18n()
const { visible, formattedRemaining } = useStepUpSkip()
</script>
