<template>
  <section class="space-y-3">
    <UCheckbox
      :model-value="modelValue"
      label="GitHub Copilot SDK"
      :description="hint"
      @update:model-value="emit('update:modelValue', $event === true)"
    />
    <UAlert v-if="modelValue" color="warning" variant="subtle" :description="continuity" />
  </section>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const { locale } = useI18n()
const hint = computed(() => locale.value.startsWith('zh') ? '仅用于已独立配置的 Copilot SDK 适配器。上游地址填写适配器地址，API Key 填写适配器认证密钥。' : 'Use an independently configured Copilot SDK adapter. Enter its URL and its authentication key as upstream credentials.')
const continuity = computed(() => locale.value.startsWith('zh') ? '使用原生 Responses 和 HTTP；取消会终止当前 SDK 请求。会话与此密钥及原适配器绑定，失效时返回错误，不自动重放到其他账号。' : 'Uses native Responses over HTTP. Cancellation stops the SDK turn. Sessions stay bound to this key and adapter; unavailable sessions fail without replaying on another account.')
</script>
