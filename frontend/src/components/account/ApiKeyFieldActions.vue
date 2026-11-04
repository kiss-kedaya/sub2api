<template>
  <div class="flex items-center gap-0.5">
    <UButton
      type="button"
      color="neutral"
      variant="ghost"
      size="xs"
      :icon="visible ? 'i-lucide-eye-off' : 'i-lucide-eye'"
      :loading="revealing"
      :disabled="disabled || revealing || copying"
      :aria-label="visible ? t('admin.accounts.apiKeyHide') : t('admin.accounts.apiKeyReveal')"
      :title="visible ? t('admin.accounts.apiKeyHide') : t('admin.accounts.apiKeyReveal')"
      data-testid="edit-api-key-visibility"
      @click="$emit('toggle')"
    />
    <UButton
      type="button"
      color="neutral"
      variant="ghost"
      size="xs"
      icon="i-lucide-copy"
      :loading="copying"
      :disabled="disabled || revealing || copying"
      :aria-label="t('admin.accounts.apiKeyCopy')"
      :title="t('admin.accounts.apiKeyCopy')"
      data-testid="edit-api-key-copy"
      @click="$emit('copy')"
    />
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

defineProps<{
  visible: boolean
  revealing: boolean
  copying: boolean
  disabled: boolean
}>()

defineEmits<{
  toggle: []
  copy: []
}>()
</script>
