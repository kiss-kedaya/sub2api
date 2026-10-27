<template>
  <BaseDialog
    :show="show"
    :title="t('admin.proxies.qualityReportTitle')"
    width="normal"
    @close="emit('close')"
  >
    <div v-if="report" class="space-y-4">
      <div class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-700">
        <div class="flex items-center justify-between gap-4">
          <div>
            <div class="text-sm text-gray-500 dark:text-gray-400">
              {{ name || '-' }}
            </div>
            <div class="mt-1 text-sm text-gray-700 dark:text-gray-200">
              {{ report.summary }}
            </div>
          </div>
          <div class="text-right">
            <div class="text-2xl font-semibold text-gray-900 dark:text-white">
              {{ report.score }}
            </div>
            <div class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.qualityGrade', { grade: report.grade }) }}
            </div>
          </div>
        </div>
        <div class="mt-3 grid grid-cols-2 gap-2 text-xs text-gray-600 dark:text-gray-300">
          <div>{{ t('admin.proxies.qualityExitIP') }}: {{ report.exit_ip || '-' }}</div>
          <div>{{ t('admin.proxies.qualityCountry') }}: {{ report.country || '-' }}</div>
          <div>
            {{ t('admin.proxies.qualityBaseLatency') }}:
            {{ typeof report.base_latency_ms === 'number' ? `${report.base_latency_ms}ms` : '-' }}
          </div>
          <div>{{ t('admin.proxies.qualityCheckedAt') }}: {{ new Date(report.checked_at * 1000).toLocaleString() }}</div>
        </div>
      </div>

      <div class="max-h-80 overflow-auto rounded-lg border border-gray-200 dark:border-dark-600">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-dark-400">
            <tr>
              <th class="whitespace-nowrap px-3 py-2 text-left">{{ t('admin.proxies.qualityTableTarget') }}</th>
              <th class="whitespace-nowrap px-3 py-2 text-left">{{ t('admin.proxies.qualityTableStatus') }}</th>
              <th class="whitespace-nowrap px-3 py-2 text-left">HTTP</th>
              <th class="whitespace-nowrap px-3 py-2 text-left">{{ t('admin.proxies.qualityTableLatency') }}</th>
              <th class="px-3 py-2 text-left">{{ t('admin.proxies.qualityTableMessage') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
            <tr v-for="item in report.items" :key="item.target">
              <td class="whitespace-nowrap px-3 py-2 text-gray-900 dark:text-white">{{ qualityTargetLabel(item.target) }}</td>
              <td class="whitespace-nowrap px-3 py-2">
                <span class="badge whitespace-nowrap" :class="qualityStatusClass(item.status)">{{ qualityStatusLabel(item.status) }}</span>
              </td>
              <td class="whitespace-nowrap px-3 py-2 text-gray-600 dark:text-gray-300">{{ item.http_status ?? '-' }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-gray-600 dark:text-gray-300">
                {{ typeof item.latency_ms === 'number' ? `${item.latency_ms}ms` : '-' }}
              </td>
              <td class="px-3 py-2 text-gray-600 dark:text-gray-300">
                <span>{{ item.message || '-' }}</span>
                <span v-if="item.cf_ray" class="ml-1 text-xs text-gray-400">(cf-ray: {{ item.cf_ray }})</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <button @click="emit('close')" class="btn btn-secondary">
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { ProxyQualityCheckResult } from '@/types'

defineProps<{
  show: boolean
  name?: string
  report: ProxyQualityCheckResult | null
}>()

const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()

const qualityStatusClass = (status: string) => {
  if (status === 'pass') return 'badge-success'
  if (status === 'warn') return 'badge-warning'
  if (status === 'challenge') return 'badge-danger'
  return 'badge-danger'
}

const qualityStatusLabel = (status: string) => {
  if (status === 'pass') return t('admin.proxies.qualityStatusPass')
  if (status === 'warn') return t('admin.proxies.qualityStatusWarn')
  if (status === 'challenge') return t('admin.proxies.qualityStatusChallenge')
  return t('admin.proxies.qualityStatusFail')
}

const qualityTargetLabel = (target: string) => {
  switch (target) {
    case 'base_connectivity':
      return t('admin.proxies.qualityTargetBase')
    case 'openai':
      return 'OpenAI'
    case 'anthropic':
      return 'Anthropic'
    case 'gemini':
      return 'Gemini'
    case 'grok':
      return 'Grok'
    case 'kimi':
      return 'Kimi'
    case 'zhipu':
      return 'Zhipu GLM'
    case 'deepseek':
      return 'DeepSeek'
    case 'minimax':
      return 'MiniMax'
    default:
      return target
  }
}
</script>
