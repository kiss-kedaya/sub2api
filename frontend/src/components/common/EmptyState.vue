<template>
  <UEmpty
    variant="naked"
    data-slot="empty-state"
    :ui="{ root: 'gap-5 px-4 py-12 sm:p-12', header: 'gap-1.5' }"
  >
    <template #header>
      <!-- Icon -->
      <div
        class="mb-2 flex size-12 items-center justify-center rounded-lg bg-elevated text-dimmed ring ring-default dark:bg-dark-800 dark:text-dark-600 dark:ring-0"
      >
        <slot name="icon">
          <component v-if="icon" :is="icon" class="size-6" aria-hidden="true" />
          <svg
            v-else
            class="size-6"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            stroke-width="1.5"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4"
            />
          </svg>
        </slot>
      </div>

      <!-- Title (console themes recolour these hook classes) -->
      <h3 class="empty-state-title">
        {{ displayTitle }}
      </h3>

      <!-- Description -->
      <p class="empty-state-description text-balance">
        {{ description }}
      </p>
    </template>

    <!-- Action -->
    <template v-if="actionText || $slots.action" #actions>
      <slot name="action">
        <UButton
          v-if="actionText"
          color="primary"
          variant="solid"
          size="lg"
          :to="actionTo"
          :icon="actionIcon ? 'i-lucide-plus' : undefined"
          :label="actionText"
          @click="!actionTo && $emit('action')"
        />
      </slot>
    </template>
  </UEmpty>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Component } from 'vue'

const { t } = useI18n()

interface Props {
  icon?: Component | string
  title?: string
  description?: string
  actionText?: string
  actionTo?: string | object
  actionIcon?: boolean
  message?: string
}

const props = withDefaults(defineProps<Props>(), {
  description: '',
  actionIcon: true
})

const displayTitle = computed(() => props.title || t('common.noData'))

defineEmits(['action'])
</script>

<style scoped>
/* Nuxt UI empty-state type scale; colours stay with the shared `empty-state-*` utilities. */
.empty-state-title {
  margin: 0;
  font-size: 1rem;
  line-height: 1.5rem;
}
</style>
