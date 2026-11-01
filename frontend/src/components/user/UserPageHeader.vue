<template>
  <header class="user-page-header flex flex-wrap items-start justify-between gap-x-4 gap-y-3" :class="{ 'mb-6': !flush }">
    <div class="min-w-0 flex-1">
      <div class="flex min-w-0 flex-wrap items-center gap-2">
        <h1 class="text-xl font-semibold leading-8 text-highlighted text-pretty">
          <slot name="title">{{ title }}</slot>
        </h1>
        <slot name="badge" />
      </div>
      <p v-if="description || $slots.description" class="mt-1 text-sm text-muted text-pretty">
        <slot name="description">{{ description }}</slot>
      </p>
    </div>
    <div v-if="$slots.actions" class="flex shrink-0 flex-wrap items-center gap-2">
      <slot name="actions" />
    </div>
  </header>
</template>

<script setup lang="ts">
// Page header row shared by the user console pages: title (+ optional badge and
// description) on the left, page actions on the right. `flush` drops the bottom margin
// when the surrounding layout already spaces its sections.
defineProps<{
  title?: string
  description?: string
  flush?: boolean
}>()
</script>
