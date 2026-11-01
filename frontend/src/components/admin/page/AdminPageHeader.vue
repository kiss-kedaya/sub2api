<template>
  <!-- In the console frame the title names the page in the navbar and the actions join the
       navbar (wide screens) or the toolbar row under it; the description line is dropped.
       Only #meta stays in the page. Without the frame (specs, public pages) it renders here. -->
  <template v-if="frame">
    <Teleport v-if="$slots.title" defer :to="selector(FRAME_TARGETS.title)">
      <slot name="title">{{ title }}</slot>
    </Teleport>
    <Teleport v-if="$slots.actions" defer :to="actionsTarget">
      <slot name="actions" />
    </Teleport>
    <div v-if="$slots.meta" v-bind="$attrs" class="admin-page-header__meta admin-page-header__meta--framed">
      <slot name="meta" />
    </div>
  </template>

  <header v-else v-bind="$attrs" class="admin-page-header">
    <div class="admin-page-header__text">
      <h2 class="admin-page-header__title">
        <slot name="title">{{ title }}</slot>
      </h2>
      <p v-if="description || $slots.description" class="admin-page-header__description">
        <slot name="description">{{ description }}</slot>
      </p>
      <div v-if="$slots.meta" class="admin-page-header__meta">
        <slot name="meta" />
      </div>
    </div>
    <div v-if="$slots.actions" class="admin-page-header__actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<script setup lang="ts">
// Page header for admin pages in the Nuxt UI dashboard manner. Pages pass their own strings
// (no router lookup: several specs mock vue-router without useRoute).
import { computed, useSlots } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import {
  FRAME_TARGETS,
  NAVBAR_ACTIONS_QUERY,
  claimFrameTitle,
  selector,
  useDashboardFrame
} from '@/components/layout/dashboardFrame'
import './admin-page.css'

defineOptions({ inheritAttrs: false })

const props = defineProps<{
  title?: string
  description?: string
}>()

const slots = useSlots()
const frame = useDashboardFrame()
const wide = useMediaQuery(NAVBAR_ACTIONS_QUERY)
const actionsTarget = computed(() => selector(wide.value ? FRAME_TARGETS.actions : FRAME_TARGETS.toolbarEnd))

claimFrameTitle(frame, () => props.title ?? '', () => !!slots.title)
</script>

<style scoped>
.admin-page-header {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px 16px;
}

.admin-page-header__text {
  min-width: 0;
  flex: 1 1 320px;
}

.admin-page-header__title {
  margin: 0;
  overflow: hidden;
  color: var(--ui-text-highlighted);
  font-size: 20px;
  font-weight: 600;
  line-height: 28px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.admin-page-header__description {
  margin-top: 2px;
  color: var(--ui-text-muted);
  font-size: 13px;
  line-height: 20px;
}

.admin-page-header__meta {
  margin-top: 10px;
}

.admin-page-header__meta--framed {
  margin-top: 0;
}

.admin-page-header__actions {
  display: flex;
  min-width: 0;
  flex: 0 0 auto;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

@media (max-width: 639px) {
  .admin-page-header {
    align-items: stretch;
  }

  .admin-page-header__title {
    font-size: 18px;
    line-height: 26px;
  }

  .admin-page-header__actions {
    width: 100%;
    justify-content: flex-start;
  }
}
</style>
