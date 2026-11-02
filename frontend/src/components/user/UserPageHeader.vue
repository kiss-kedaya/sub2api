<template>
  <!-- In the console frame: the title names the page in the navbar (the badge follows it), a
       live #description status sits next to the title (or in the toolbar row on narrow
       screens), actions join the navbar or the toolbar row, and a plain description prop is
       dropped. Without the frame (specs, the public model plaza) it renders here as before. -->
  <template v-if="frame">
    <Teleport v-if="$slots.title" defer :to="selector(FRAME_TARGETS.title)">
      <slot name="title">{{ title }}</slot>
    </Teleport>
    <Teleport v-if="$slots.badge" defer :to="selector(FRAME_TARGETS.titleTrailing)">
      <slot name="badge" />
    </Teleport>
    <Teleport v-if="$slots.description" defer :to="statusTarget">
      <span class="user-page-header__status">
        <slot name="description" />
      </span>
    </Teleport>
    <Teleport v-if="$slots.actions" defer :to="actionsTarget">
      <slot name="actions" />
    </Teleport>
  </template>

  <header v-else v-bind="$attrs" class="user-page-header flex flex-wrap items-start justify-between gap-x-4 gap-y-3" :class="{ 'mb-6': !flush }">
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
// description) and page actions. `flush` drops the bottom margin when the surrounding
// layout already spaces its sections (inline rendering only).
import { computed, useSlots } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import {
  FRAME_TARGETS,
  NAVBAR_ACTIONS_QUERY,
  NAVBAR_STATUS_QUERY,
  claimFrameTitle,
  selector,
  useDashboardFrame
} from '@/components/layout/dashboardFrame'

defineOptions({ inheritAttrs: false })

const props = defineProps<{
  title?: string
  description?: string
  flush?: boolean
}>()

const slots = useSlots()
const frame = useDashboardFrame()
const wide = useMediaQuery(NAVBAR_ACTIONS_QUERY)
const medium = useMediaQuery(NAVBAR_STATUS_QUERY)
const actionsTarget = computed(() => selector(wide.value ? FRAME_TARGETS.actions : FRAME_TARGETS.toolbarEnd))
const statusTarget = computed(() => selector(medium.value ? FRAME_TARGETS.titleTrailing : FRAME_TARGETS.toolbarStart))

claimFrameTitle(frame, () => props.title ?? '', () => !!slots.title)
</script>

<style scoped>
.user-page-header__status {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
  overflow: hidden;
  color: var(--ui-text-muted);
  font-size: 14px;
  font-weight: 400;
  white-space: nowrap;
}
</style>
