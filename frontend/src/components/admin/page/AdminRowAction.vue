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
  transition: color 140ms cubic-bezier(0.23, 1, 0.32, 1), background-color 140ms cubic-bezier(0.23, 1, 0.32, 1), transform 120ms cubic-bezier(0.23, 1, 0.32, 1);
}

.admin-row-action:active:not(:disabled) {
  transform: scale(0.94);
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

.admin-row-action--primary:hover:not(:disabled) {
  background: color-mix(in oklab, var(--ui-primary) 12%, transparent);
  color: var(--ui-primary);
}

.admin-row-action--danger:hover:not(:disabled) {
  background: color-mix(in oklab, var(--ui-error) 12%, transparent);
  color: var(--ui-error);
}

.admin-row-action--success:hover:not(:disabled) {
  background: color-mix(in oklab, var(--ui-success) 12%, transparent);
  color: var(--ui-success);
}

.admin-row-action--warning:hover:not(:disabled) {
  background: color-mix(in oklab, var(--ui-warning) 14%, transparent);
  color: #b45309;
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

<style>
html.dark .admin-row-action--warning:hover:not(:disabled) {
  color: var(--ui-warning);
}
</style>
