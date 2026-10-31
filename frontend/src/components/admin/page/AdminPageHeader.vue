<template>
  <header class="admin-page-header">
    <div class="admin-page-header__text">
      <h2 class="admin-page-header__title">
        <slot name="title">{{ title }}</slot>
      </h2>
      <p v-if="description || $slots.description" class="admin-page-header__description">
        <slot name="description">{{ description }}</slot>
      </p>
    </div>
    <div v-if="$slots.actions" class="admin-page-header__actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<script setup lang="ts">
// Page header for admin pages in the Nuxt UI dashboard manner: title and the page's
// existing description on the left, primary actions on the right. Pages pass their own
// strings (no router lookup: several specs mock vue-router without useRoute).
import './admin-page.css'

defineProps<{
  title?: string
  description?: string
}>()
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
