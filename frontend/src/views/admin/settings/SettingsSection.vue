<template>
  <!--
    One settings group in the Nuxt UI dashboard manner (template settings/*.vue):
    a naked header with title, short description and the section's own actions (save / test / add),
    then one subtle card whose direct children are rows separated by a hairline.
    Fallthrough attrs (data-testid, id) land on the <section> root.
  -->
  <section class="settings-section">
    <UPageCard
      :title="title"
      :description="description"
      :icon="icon"
      variant="naked"
      :orientation="$slots.actions ? 'horizontal' : 'vertical'"
      :ui="{ container: $slots.actions ? 'lg:flex lg:flex-row lg:items-center lg:justify-between' : undefined, wrapper: 'min-w-0 flex-1' }"
      class="mb-4"
    >
      <template v-if="$slots.description" #description>
        <slot name="description" />
      </template>
      <div v-if="$slots.actions" class="flex w-fit shrink-0 flex-wrap items-center gap-2 lg:ms-auto">
        <slot name="actions" />
      </div>
    </UPageCard>

    <UPageCard
      v-if="$slots.default || loading"
      variant="subtle"
      :ui="{ container: 'gap-y-4 divide-y divide-default *:not-last:pb-4' }"
    >
      <div v-if="loading" class="flex items-center gap-2 text-sm text-muted">
        <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
        {{ t('common.loading') }}
      </div>
      <slot v-else />
    </UPageCard>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

defineProps<{
  title?: string
  description?: string
  /** Optional Nuxt UI icon used to make long settings pages scannable. */
  icon?: string
  /** Shows a loading row instead of the body (sections that fetch their own endpoint). */
  loading?: boolean
}>()

const { t } = useI18n()
</script>
