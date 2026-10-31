<template>
  <section class="rounded-xl border border-gray-200 p-4 dark:border-dark-600">
    <label class="flex cursor-pointer items-start gap-3">
      <input
        type="checkbox"
        class="mt-1 h-4 w-4 rounded-sm border-gray-300 text-primary-600"
        :checked="modelValue.enabled"
        data-testid="gemini-explicit-cache-upstream"
        @change="updateEnabled"
      />
      <span>
        <span class="block text-sm font-medium text-gray-900 dark:text-white">{{ copy.title }}</span>
        <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ copy.hint }}</span>
      </span>
    </label>
    <div v-if="modelValue.enabled" class="mt-4">
      <label class="input-label" :for="ttlID">{{ copy.ttl }}</label>
      <input
        :id="ttlID"
        type="number"
        min="0"
        max="86400"
        step="1"
        class="input"
        :value="modelValue.maxTTLSeconds"
        data-testid="gemini-explicit-cache-max-ttl"
        @input="updateTTL"
      />
      <p class="input-hint">{{ copy.ttlHint }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GeminiCacheSettings } from './geminiCacheSettings'

const props = defineProps<{ modelValue: GeminiCacheSettings }>()
const emit = defineEmits<{ 'update:modelValue': [value: GeminiCacheSettings] }>()
const { locale } = useI18n()
const ttlID = `gemini-cache-ttl-${useId()}`
const copy = computed(() => locale.value.startsWith('zh') ? {
  title: '自定义上游支持 Gemini 显式缓存',
  hint: '仅在上游实现原生 /v1beta/cachedContents 接口时开启。缓存严格绑定创建它的上游账号；官方 Gemini 地址和 Vertex 服务账号自动识别。',
  ttl: '上游最长缓存有效期（秒）',
  ttlHint: '0 表示使用本站上限；最长 86400 秒。存储费用按模型价格与实际有效期计收。'
} : {
  title: 'Custom upstream supports Gemini explicit caching',
  hint: 'Enable only if the upstream implements native /v1beta/cachedContents. Caches stay bound to their original account. Official Gemini endpoints and Vertex service accounts are recognized automatically.',
  ttl: 'Maximum upstream cache TTL (seconds)',
  ttlHint: '0 uses the site limit; maximum 86400 seconds. Storage is charged by model price and actual lifetime.'
})
function updateEnabled(event: Event) {
  emit('update:modelValue', { ...props.modelValue, enabled: (event.target as HTMLInputElement).checked })
}
function updateTTL(event: Event) {
  const value = Number((event.target as HTMLInputElement).value)
  emit('update:modelValue', {
    ...props.modelValue,
    maxTTLSeconds: Number.isSafeInteger(value) && value > 0 ? Math.min(value, 86400) : 0
  })
}
</script>
