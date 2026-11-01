<template>
  <UEmpty
    variant="naked"
    data-slot="empty-state"
    :title="displayTitle"
    :description="description || undefined"
    :ui="{ root: 'gap-5 px-4 py-12 sm:p-12', header: 'gap-1.5', title: 'text-base', description: 'text-balance' }"
  >
    <template #leading>
      <div class="mb-2 flex size-12 items-center justify-center rounded-lg bg-elevated text-dimmed ring ring-default">
        <slot name="icon">
          <component v-if="icon && typeof icon !== 'string'" :is="icon" class="size-6" aria-hidden="true" />
          <UIcon v-else :name="typeof icon === 'string' && icon.startsWith('i-') ? icon : 'i-lucide-inbox'" class="size-6" />
        </slot>
      </div>
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
