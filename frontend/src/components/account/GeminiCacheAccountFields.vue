<template>
  <section class="space-y-4">
    <UCheckbox
      :model-value="modelValue.enabled"
      :label="copy.title"
      :description="copy.hint"
      data-testid="gemini-explicit-cache-upstream"
      @update:model-value="setEnabled($event === true)"
    />
    <UFormField v-if="modelValue.enabled" :label="copy.ttl" :help="copy.ttlHint">
      <UInput
        :id="ttlID"
        type="number"
        min="0"
        max="86400"
        step="1"
        class="w-full"
        :model-value="String(modelValue.maxTTLSeconds)"
        data-testid="gemini-explicit-cache-max-ttl"
        @update:model-value="setTTL"
      />
    </UFormField>
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
function setEnabled(enabled: boolean) {
  emit('update:modelValue', { ...props.modelValue, enabled })
}
// UInput type=number emits a number (or '' when cleared).
function setTTL(raw: unknown) {
  const value = Number(raw)
  emit('update:modelValue', {
    ...props.modelValue,
    maxTTLSeconds: Number.isSafeInteger(value) && value > 0 ? Math.min(value, 86400) : 0
  })
}
</script>
