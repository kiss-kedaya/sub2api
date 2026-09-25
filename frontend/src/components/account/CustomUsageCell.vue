<template>
  <div ref="root" class="min-w-[8rem]" :aria-busy="state?.loading || false" data-testid="custom-usage-cell">
    <span v-if="!eligible" class="text-sm text-gray-400">—</span>
    <template v-else>
      <div class="flex items-start gap-1.5">
        <div class="min-w-0">
          <CustomUsageResult v-if="state?.result?.configured" :result="state.result" />
          <span v-else-if="state?.loading" class="text-xs text-gray-400">{{ t('admin.accounts.customUsage.loading') }}</span>
          <button v-else-if="state?.result" type="button" class="text-xs text-gray-500 hover:text-primary-600 dark:text-gray-400" @click="emit('configure')">{{ t('admin.accounts.customUsage.notConfigured') }}</button>
          <span v-else class="text-xs text-gray-400">—</span>
          <div v-if="state?.result?.configured && !state.result.enabled" class="text-xs text-gray-400">{{ t('admin.accounts.customUsage.disabled') }}</div>
        </div>
        <button v-if="!state?.result || (state.result.enabled && state.result.configured)" type="button" class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-primary-600 disabled:cursor-wait dark:hover:bg-dark-700" :disabled="state?.loading" :aria-label="t('admin.accounts.customUsage.refresh')" :title="t('admin.accounts.customUsage.refresh')" data-testid="custom-usage-refresh" @click="emit('refresh')">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin motion-reduce:animate-none': state?.loading }" />
        </button>
      </div>
      <div v-if="state?.error" class="mt-1 text-xs text-amber-600 dark:text-amber-400" role="status">{{ t('admin.accounts.customUsage.loadFailed') }}</div>
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
import Icon from '@/components/icons/Icon.vue'
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
