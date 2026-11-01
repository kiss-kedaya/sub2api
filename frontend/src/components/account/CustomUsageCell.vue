<template>
  <div ref="root" class="min-w-28" :aria-busy="state?.loading || false" data-testid="custom-usage-cell">
    <span v-if="!eligible" class="text-sm text-dimmed">—</span>
    <template v-else>
      <div class="flex items-start gap-1.5">
        <div class="min-w-0">
          <CustomUsageResult v-if="state?.result?.configured" :result="state.result" />
          <span v-else-if="state?.loading" class="text-xs text-dimmed">{{ t('admin.accounts.customUsage.loading') }}</span>
          <UButton v-else-if="state?.result" color="neutral" variant="link" size="xs" class="p-0" :label="t('admin.accounts.customUsage.configure')" @click="emit('configure')" />
          <span v-else class="text-xs text-dimmed">—</span>
          <div v-if="state?.result?.configured && !state.result.enabled" class="text-xs text-dimmed">{{ t('admin.accounts.customUsage.disabled') }}</div>
        </div>
        <UButton
          v-if="!state?.result || (state.result.enabled && state.result.configured)"
          color="neutral"
          variant="ghost"
          size="xs"
          icon="i-lucide-refresh-cw"
          square
          :loading="state?.loading"
          :disabled="state?.loading"
          :aria-label="t('admin.accounts.customUsage.refresh')"
          :title="t('admin.accounts.customUsage.refresh')"
          data-testid="custom-usage-refresh"
          @click="emit('refresh')"
        />
      </div>
      <div v-if="state?.error" class="mt-1 text-xs text-warning" role="status">{{ t('admin.accounts.customUsage.loadFailed') }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountListItem } from '@/types'
import type { CustomUsageRowState } from '@/composables/useCustomUsage'
import { supportsCustomUsage } from '@/utils/customUsage'
import CustomUsageResult from './CustomUsageResult.vue'
const props = defineProps<{ account: AccountListItem; state?: CustomUsageRowState }>()
const emit = defineEmits<{ refresh: []; configure: []; visibility: [visible: boolean] }>()
const { t } = useI18n()
const eligible = computed(() => supportsCustomUsage(props.account))
const root = ref<HTMLElement | null>(null)
let observer: IntersectionObserver | undefined
let intersecting = false
onMounted(() => {
  if (typeof IntersectionObserver === 'undefined') { intersecting = true; emit('visibility', eligible.value); return }
  observer = new IntersectionObserver(entries => {
    intersecting = entries.some(entry => entry.isIntersecting)
    emit('visibility', intersecting && eligible.value)
  }, { threshold: 0 })
  if (root.value) observer.observe(root.value)
})
watch(eligible, value => emit('visibility', intersecting && value))
onUnmounted(() => { observer?.disconnect(); emit('visibility', false) })
</script>
