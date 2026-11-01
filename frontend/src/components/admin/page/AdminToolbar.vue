<template>
  <div class="admin-toolbar">
    <div class="admin-toolbar__start">
      <slot />
    </div>
    <div v-if="$slots.end" class="admin-toolbar__end">
      <slot name="end" />
    </div>
  </div>
</template>

<script setup lang="ts">
// Toolbar row under the page header: search and filters on the left, view and
// secondary actions on the right. Wraps on narrow screens.
</script>

<style scoped>
.admin-toolbar {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.admin-toolbar__start {
  display: flex;
  min-width: 0;
  flex: 1 1 480px;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.admin-toolbar__end {
  display: flex;
  min-width: 0;
  flex: 0 0 auto;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin-left: auto;
}

@media (max-width: 767px) {
  .admin-toolbar__start {
    flex-basis: 100%;
  }

  .admin-toolbar__end {
    width: 100%;
    margin-left: 0;
    justify-content: flex-start;
  }
}
</style>
