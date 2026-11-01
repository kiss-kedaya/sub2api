<template>
  <!-- Same header as Nuxt UI's UAuthForm: optional icon, title, one muted line under it. -->
  <div class="flex flex-col text-center">
    <div v-if="icon" class="mb-2">
      <UIcon :name="icon" class="inline-block size-8 shrink-0 text-default" />
    </div>
    <h1 class="text-xl font-semibold text-pretty text-highlighted">{{ title }}</h1>
    <div v-if="$slots.default" class="mt-1 text-base text-pretty text-muted">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  title: string
  icon?: string
}>()
</script>
