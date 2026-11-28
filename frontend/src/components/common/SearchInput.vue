<template>
  <div class="relative w-full">
    <!-- 列表搜索框不是登录表单：显式 off + 非凭据 name + 密码管理器忽略标记，
         避免浏览器把账号/密码自动填进搜索框（Key 页曾出现过这种串扰）。 -->
    <UInput
      :model-value="modelValue"
      type="search"
      name="list-search"
      autocomplete="off"
      autocorrect="off"
      autocapitalize="off"
      data-lpignore="true"
      data-1p-ignore="true"
      data-bwignore="true"
      data-form-type="other"
      spellcheck="false"
      icon="i-lucide-search"
      class="w-full"
      :placeholder="placeholder"
      @update:model-value="handleInput"
    />
  </div>
</template>

<script setup lang="ts">
import { useDebounceFn } from '@vueuse/core'

const props = withDefaults(defineProps<{
  modelValue: string
  placeholder?: string
  debounceMs?: number
}>(), {
  placeholder: 'Search...',
  debounceMs: 300
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'search', value: string): void
}>()

const debouncedEmitSearch = useDebounceFn((value: string) => {
  emit('search', value)
}, props.debounceMs)

const handleInput = (raw: string | number | null | undefined) => {
  const value = raw == null ? '' : String(raw)
  emit('update:modelValue', value)
  debouncedEmitSearch(value)
}
</script>
