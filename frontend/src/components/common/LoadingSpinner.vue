<template>
  <div
    :class="['inline-flex shrink-0', sizeClasses, colorClass]"
    role="status"
    :aria-label="t('common.loading')"
  >
    <UIcon name="i-lucide-loader-circle" class="size-full animate-spin" />
    <span class="sr-only">{{ t('common.loading') }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

type SpinnerSize = 'sm' | 'md' | 'lg' | 'xl'
type SpinnerColor = 'primary' | 'secondary' | 'white' | 'gray'

interface Props {
  size?: SpinnerSize
  color?: SpinnerColor
}

const props = withDefaults(defineProps<Props>(), {
  size: 'md',
  color: 'primary'
})

const sizeClasses = computed(() => {
  const sizes: Record<SpinnerSize, string> = {
    sm: 'size-4',
    md: 'size-8',
    lg: 'size-12',
    xl: 'size-16'
  }
  return sizes[props.size]
})

const colorClass = computed(() => {
  const colors: Record<SpinnerColor, string> = {
    primary: 'text-primary-500',
    secondary: 'text-gray-500 dark:text-dark-400',
    white: 'text-white',
    gray: 'text-gray-400 dark:text-dark-500'
  }
  return colors[props.color]
})
</script>
