<script setup lang="ts">
// A titled block of SettingRow lines. The title is the small, quiet label above the list; the rows
// draw their own dividers, so the group only owns the spacing and the heading.
//
// Reusable outside the account dialogs: the group has no idea what the rows configure.
defineProps<{
  /** Already-translated group title. Omit to render the rows with no heading. */
  title?: string
  /** Already-translated line under the title, for what the whole group is about. */
  description?: string
}>()
</script>

<template>
  <section class="setting-group">
    <h4 v-if="title" class="setting-group__title">{{ title }}</h4>
    <p v-if="description" class="setting-group__description">{{ description }}</p>
    <div class="setting-group__rows">
      <slot />
    </div>
  </section>
</template>

<style scoped>
.setting-group {
  display: grid;
  gap: 4px;
  min-width: 0;
}

.setting-group__title {
  font-size: 11px;
  font-weight: 600;
  line-height: 16px;
  letter-spacing: 0.06em;
  color: var(--ui-text-dimmed);
}

.setting-group__description {
  max-width: 64ch;
  padding-block-end: 4px;
  font-size: 12px;
  line-height: 18px;
  color: var(--ui-text-muted);
  text-wrap: pretty;
}

/* Rows draw the line above themselves; the group closes the list with the line below the last one.
   Doing it here rather than with :last-child keeps working when a row sits inside a v-if wrapper. */
.setting-group__rows {
  display: grid;
  min-width: 0;
  border-bottom: 1px solid var(--ui-border);
}
</style>
