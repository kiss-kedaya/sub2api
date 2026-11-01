<template>
  <UButton
    color="neutral"
    variant="ghost"
    square
    :icon="colorMode.isDark.value ? 'i-lucide-sun' : 'i-lucide-moon'"
    :aria-label="title"
    :title="title"
    data-testid="public-theme-toggle"
    @click="colorMode.toggle()"
  />
</template>

<script setup lang="ts">
// Light/dark toggle for the public pages. It reads and writes the shared site color mode
// (useSiteTheme), so "system" and the site default mode apply here as in the console.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSiteColorMode } from '@/composables/useSiteTheme'

const props = defineProps<{
  /** Label override (e.g. the docs page's own "theme" string). */
  label?: string
}>()

const { t } = useI18n()
const colorMode = useSiteColorMode()

const title = computed(() =>
  props.label || (colorMode.isDark.value ? t('home.switchToLight') : t('home.switchToDark'))
)
</script>
