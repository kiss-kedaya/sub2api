<template>
  <div
    v-if="visible"
    class="flex flex-wrap items-center gap-3 rounded-lg border border-blue-200 bg-blue-50 px-4 py-3 dark:border-blue-800 dark:bg-blue-900/20"
    data-test="two-factor-suggestion"
  >
    <UIcon name="i-lucide-shield-check" class="size-5 shrink-0 text-blue-500" />
    <div class="min-w-0 flex-1">
      <p class="text-sm font-medium text-blue-900 dark:text-blue-100">
        {{ t('twoFactorSuggestion.title') }}
      </p>
      <p class="mt-0.5 text-xs text-blue-800/80 dark:text-blue-200/80">
        {{ t('twoFactorSuggestion.body') }}
      </p>
    </div>
    <RouterLink :to="{ name: 'Profile', query: { tab: 'security' } }" class="btn btn-primary btn-sm shrink-0">
      {{ t('twoFactorSuggestion.enable') }}
    </RouterLink>
    <button
      type="button"
      class="shrink-0 rounded-md p-1 text-blue-700/70 transition-colors hover:bg-blue-100 hover:text-blue-900 dark:text-blue-200/70 dark:hover:bg-blue-800/40 dark:hover:text-blue-100"
      :title="t('twoFactorSuggestion.dismiss')"
      :aria-label="t('twoFactorSuggestion.dismiss')"
      @click="dismiss"
    >
      <UIcon name="i-lucide-x" class="size-4" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { totpAPI } from '@/api'
import type { TotpStatus } from '@/types'

// 「建议开启 2FA」提示：只在全局功能已开启、当前用户未开启时出现，
// 可关闭（记住本页面的关闭状态），不强制、不阻塞任何操作。
const props = defineProps<{ storageKey: string }>()

const { t } = useI18n()
const status = ref<TotpStatus | null>(null)
const dismissed = ref(false)

const visible = computed(
  () => Boolean(status.value?.feature_enabled) && !status.value?.enabled && !dismissed.value,
)

const storageKey = computed(() => `totp-suggestion-dismissed:${props.storageKey}`)

const dismiss = () => {
  dismissed.value = true
  try {
    window.localStorage.setItem(storageKey.value, '1')
  } catch {
    // 隐私模式下 localStorage 可能不可写；本次会话内隐藏即可。
  }
}

onMounted(async () => {
  try {
    dismissed.value = window.localStorage.getItem(storageKey.value) === '1'
  } catch {
    dismissed.value = false
  }
  try {
    status.value = await totpAPI.getStatus()
  } catch {
    // 状态拿不到时保持静默：这是建议提示，不是安全门控。
    status.value = null
  }
})
</script>
