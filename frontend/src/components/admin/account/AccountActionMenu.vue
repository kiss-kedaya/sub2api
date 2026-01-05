<template>
  <Teleport to="body">
    <div v-if="show && position" class="action-menu-content fixed z-[9999] w-52 overflow-hidden rounded-xl bg-white shadow-lg ring-1 ring-black/5 dark:bg-dark-800" :style="{ top: position.top + 'px', left: position.left + 'px' }">
      <div class="py-1">
        <template v-if="account">
          <button @click="$emit('test', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 dark:hover:bg-dark-700"><span class="text-green-500">▶</span> {{ t('admin.accounts.testConnection') }}</button>
          <button @click="$emit('stats', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 dark:hover:bg-dark-700"><span class="text-indigo-500">📊</span> {{ t('admin.accounts.viewStats') }}</button>
          <template v-if="account.type === 'oauth' || account.type === 'setup-token'">
            <button @click="$emit('reauth', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 dark:hover:bg-dark-700 text-blue-600">🔗 {{ t('admin.accounts.reAuthorize') }}</button>
            <button @click="$emit('refresh-token', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 dark:hover:bg-dark-700 text-purple-600">🔄 {{ t('admin.accounts.refreshToken') }}</button>
          </template>
          <div v-if="account.status === 'error' || isRateLimited || isOverloaded" class="my-1 border-t border-gray-100 dark:border-dark-700"></div>
          <button v-if="account.status === 'error'" @click="$emit('reset-status', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 dark:hover:bg-dark-700 text-yellow-600">🔃 {{ t('admin.accounts.resetStatus') }}</button>
          <button v-if="isRateLimited || isOverloaded" @click="$emit('clear-rate-limit', account); $emit('close')" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 dark:hover:bg-dark-700 text-amber-600">⏱️ {{ t('admin.accounts.clearRateLimit') }}</button>
        </template>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
const props = defineProps<{ show: boolean; account: Account | null; position: { top: number; left: number } | null }>()
defineEmits(['close', 'test', 'stats', 'reauth', 'refresh-token', 'reset-status', 'clear-rate-limit'])
const { t } = useI18n()
const isRateLimited = computed(() => props.account?.rate_limit_reset_at && new Date(props.account.rate_limit_reset_at) > new Date())
const isOverloaded = computed(() => props.account?.overload_until && new Date(props.account.overload_until) > new Date())
</script>