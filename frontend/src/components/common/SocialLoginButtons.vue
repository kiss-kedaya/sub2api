<template>
  <!--
    Social sign-in buttons, driven by a provider list.
    Presentation only: the page keeps the OAuth request/redirect logic and the
    icon slot supplies brand marks that are multi-colour SVG (Google/LinuxDo/...).

    Button shape follows the auth reference: full-width, quiet raised surface,
    left icon, centred label, 10px gaps, one shared radius. The surface and the
    control outline are painted from theme tokens (--ui-bg-elevated /
    --app-control-border) so light and ink themes both keep a visible edge.
    Dynamic slots: #icon-<provider id>, plus #footer below the buttons.
  -->
  <div class="social-login-buttons space-y-4">
    <div v-if="divider" class="social-login-buttons__divider flex items-center gap-3">
      <span class="h-px flex-1 bg-border" aria-hidden="true"></span>
      <span class="text-xs text-muted">{{ dividerLabel }}</span>
      <span class="h-px flex-1 bg-border" aria-hidden="true"></span>
    </div>

    <div v-if="providers.length" :class="gridClass">
      <UButton
        v-for="provider in providers"
        :key="provider.id"
        color="neutral"
        variant="outline"
        size="lg"
        block
        :disabled="disabled || provider.disabled"
        :aria-label="provider.ariaLabel || provider.label"
        :data-testid="provider.testId || `social-login-${provider.id}`"
        :ui="{ base: 'social-auth-button relative h-12 justify-center' }"
        @click="emit('select', provider.id)"
      >
        <template #leading>
          <span class="pointer-events-none absolute inset-y-0 start-4 flex items-center">
            <slot :name="`icon-${provider.id}`" :provider="provider">
              <UIcon v-if="provider.icon" :name="provider.icon" class="size-5" />
            </slot>
          </span>
        </template>
        <span class="w-full truncate text-center text-sm font-medium">{{ provider.label }}</span>
      </UButton>
    </div>

    <slot name="footer" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { SocialLoginProvider } from './socialLogin'

const props = withDefaults(defineProps<{
  providers: SocialLoginProvider[]
  disabled?: boolean
  /** Show the "or continue with" divider above the buttons. */
  divider?: boolean
  dividerLabel?: string
  /** 1 = stacked full-width buttons (auth reference), 2 = two columns on sm+. */
  columns?: 1 | 2
}>(), {
  disabled: false,
  divider: false,
  dividerLabel: '',
  columns: 1
})

const emit = defineEmits<{
  select: [id: string]
}>()

const gridClass = computed(() => [
  'grid gap-2.5',
  props.columns === 2 ? 'sm:grid-cols-2' : 'grid-cols-1'
])
</script>

<style scoped>
/* Higher specificity than UButton's own `bg-default`/`ring-accented` utilities:
   the raised surface and the visible edge are the whole point of this button. */
.social-login-buttons :deep(.social-auth-button) {
  background-color: var(--ui-bg-elevated);
  border-radius: calc(var(--ui-radius) * 2);
  --tw-ring-color: var(--app-control-border);
}

.social-login-buttons :deep(.social-auth-button:hover:not(:disabled)) {
  background-color: var(--ui-bg-accented);
}

@media (prefers-reduced-motion: reduce) {
  .social-login-buttons :deep(.social-auth-button) {
    transition: none;
  }
}
</style>
