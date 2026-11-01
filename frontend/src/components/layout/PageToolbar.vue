<template>
  <!-- UDashboardToolbar row under the navbar: filters on the left (default slot or #left),
       secondary actions on the right (#right). The row appears only while it has content.
       Outside the console frame the same row renders in place. -->
  <template v-if="frame">
    <Teleport v-if="$slots.default || $slots.left" defer :to="selector(FRAME_TARGETS.toolbarStart)">
      <slot name="left"><slot /></slot>
    </Teleport>
    <Teleport v-if="$slots.right" defer :to="selector(FRAME_TARGETS.toolbarEnd)">
      <slot name="right" />
    </Teleport>
  </template>
  <div v-else v-bind="$attrs" class="page-toolbar-inline">
    <div class="page-toolbar-inline__start">
      <slot name="left"><slot /></slot>
    </div>
    <div v-if="$slots.right" class="page-toolbar-inline__end">
      <slot name="right" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { FRAME_TARGETS, selector, useDashboardFrame } from './dashboardFrame'

defineOptions({ inheritAttrs: false })

const frame = useDashboardFrame()
</script>

<style scoped>
.page-toolbar-inline {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.page-toolbar-inline__start,
.page-toolbar-inline__end {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.page-toolbar-inline__end {
  margin-left: auto;
}
</style>
