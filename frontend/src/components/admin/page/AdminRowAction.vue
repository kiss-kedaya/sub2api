<template>
  <button
    type="button"
    class="admin-row-action"
    :class="`admin-row-action--${tone}`"
    :title="label"
  >
    <slot name="icon">
      <Icon v-if="icon" :name="icon" size="sm" aria-hidden="true" />
    </slot>
    <span class="sr-only">{{ label }}</span>
  </button>
</template>

<script setup lang="ts">
// Icon-only ghost button for table rows. The label stays in the DOM (screen readers,
// specs that find the button by text) and shows as the native tooltip.
import Icon from '@/components/icons/Icon.vue'

type IconName = InstanceType<typeof Icon>['$props']['name']

withDefaults(defineProps<{
  label: string
  icon?: IconName
  tone?: 'neutral' | 'primary' | 'danger' | 'success' | 'warning'
}>(), {
  icon: undefined,
  tone: 'neutral'
})
</script>

<style scoped>
.admin-row-action {
  display: inline-flex;
  width: 30px;
  height: 30px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  color: var(--ui-text-muted);
  transition: color 140ms ease, background-color 140ms ease;
}

.admin-row-action:focus-visible {
  outline: 2px solid var(--ui-primary);
  outline-offset: 1px;
}

.admin-row-action:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.admin-row-action:hover:not(:disabled) {
  background: var(--ui-bg-elevated);
  color: var(--ui-text-highlighted);
}

.admin-row-action--danger:hover:not(:disabled) {
  background: color-mix(in srgb, var(--ui-error) 10%, transparent);
  color: var(--ui-error);
}

.admin-row-action--success:hover:not(:disabled) {
  background: color-mix(in srgb, var(--ui-success) 10%, transparent);
  color: var(--ui-success);
}

.admin-row-action--warning:hover:not(:disabled) {
  background: color-mix(in srgb, var(--ui-warning) 12%, transparent);
  color: #c2410c;
}

/* Night theme keeps the row buttons' current colours: muted text, tinted hover fill. */
.dark .admin-row-action:hover:not(:disabled) {
  background: #3f2f37;
  color: var(--ui-text-muted);
}

.dark .admin-row-action--danger:hover:not(:disabled) {
  background: rgb(127 29 29 / 0.2);
}

.dark .admin-row-action--success:hover:not(:disabled) {
  background: rgb(20 83 45 / 0.2);
}

.dark .admin-row-action--warning:hover:not(:disabled) {
  background: rgb(124 45 18 / 0.2);
}

@media (max-width: 767px) {
  .admin-row-action {
    width: 40px;
    height: 40px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .admin-row-action {
    transition: none;
  }
}
</style>
