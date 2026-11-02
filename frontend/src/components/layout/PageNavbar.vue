<template>
  <!-- Puts page buttons on the navbar's right side (before notifications / theme / balance).
       Below 1280px they move to the toolbar row under the navbar, so the navbar stays short.
       Outside the console frame the content renders in place. -->
  <Teleport v-if="frame" defer :to="target">
    <slot />
  </Teleport>
  <div v-else v-bind="$attrs" class="page-navbar-inline">
    <slot />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { FRAME_TARGETS, NAVBAR_ACTIONS_QUERY, selector, useDashboardFrame } from './dashboardFrame'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  /** Keep the content in the navbar on every width (one or two icon buttons). */
  pinned?: boolean
}>(), { pinned: false })

const frame = useDashboardFrame()
const wide = useMediaQuery(NAVBAR_ACTIONS_QUERY)
const target = computed(() => selector(props.pinned || wide.value ? FRAME_TARGETS.actions : FRAME_TARGETS.toolbarEnd))
</script>

<style scoped>
.page-navbar-inline {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}
</style>
