<template>
  <div class="mt-3 space-y-2">
    <p class="text-sm text-gray-600 dark:text-gray-400">{{ text('订阅、动态代理、地区规则与节点统一在 IP 管理中维护。', 'Manage subscriptions, dynamic proxies, region rules and nodes in IP Management.') }}</p>
    <div class="flex flex-wrap gap-2">
      <router-link to="/admin/proxies#subscriptions" class="btn btn-secondary">{{ text('前往 IP 管理', 'Open IP Management') }}</router-link>
      <button type="button" class="btn btn-secondary" :disabled="pending" @click="select">{{ text('设为打票代理', 'Use for ticket harvesting') }}</button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiClient } from '@/api/client'
const emit = defineEmits<{ ready: [endpoint: string] }>()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const pending = ref(false)
const error = ref('')
async function select() {
  pending.value = true
  error.value = ''
  try {
    const { data } = await apiClient.get<{ running: boolean; busy: boolean; nodes: number; endpoint: string }>('/admin/system/mihomo')
    if (!data.running || data.busy || !data.nodes || data.endpoint !== 'http://127.0.0.1:3101') {
      error.value = text('内核尚未就绪，请先在 IP 管理中配置并启动。', 'The kernel is not ready. Configure and start it in IP Management first.')
      return
    }
    emit('ready', data.endpoint)
  } catch {
    error.value = text('无法读取内核状态，请稍后重试。', 'Cannot read kernel status. Please retry.')
  } finally { pending.value = false }
}
</script>
