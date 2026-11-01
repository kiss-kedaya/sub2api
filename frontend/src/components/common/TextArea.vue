<template>
  <div class="w-full">
    <DefineControl>
      <UTextarea
        :id="id"
        ref="textAreaRef"
        :model-value="modelValue ?? ''"
        :disabled="disabled"
        :required="required"
        :placeholder="placeholderText"
        :readonly="readonly"
        :rows="rowCount"
        class="w-full"
        :ui="{ base: 'min-h-20 resize-y' }"
        @update:model-value="onInput"
        @change="$emit('change', ($event.target as HTMLTextAreaElement).value)"
        @blur="$emit('blur', $event)"
        @focus="$emit('focus', $event)"
      />
    </DefineControl>

    <!-- A field wrapper only when this component shows its own label / hint / error. -->
    <UFormField v-if="label || error || hint" :label="label" :required="required" :error="error || false" :help="hint">
      <ReuseControl />
    </UFormField>
    <ReuseControl v-else />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { createReusableTemplate } from '@vueuse/core'

interface Props {
  modelValue: string | null | undefined
  label?: string
  placeholder?: string
  disabled?: boolean
  required?: boolean
  readonly?: boolean
  error?: string
  hint?: string
  id?: string
  rows?: number | string
}

const props = withDefaults(defineProps<Props>(), {
  disabled: false,
  required: false,
  readonly: false,
  rows: 3
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'change', value: string): void
  (e: 'blur', event: FocusEvent): void
  (e: 'focus', event: FocusEvent): void
}>()

const [DefineControl, ReuseControl] = createReusableTemplate()

const textAreaRef = ref<{ textareaRef?: HTMLTextAreaElement | null } | null>(null)
const placeholderText = computed(() => props.placeholder || '')
const rowCount = computed(() => {
  const rows = Number(props.rows)
  return Number.isFinite(rows) && rows > 0 ? rows : 3
})

const onInput = (value: string | null | undefined) => {
  emit('update:modelValue', value == null ? '' : String(value))
}

// Expose focus method
defineExpose({
  focus: () => textAreaRef.value?.textareaRef?.focus(),
  select: () => textAreaRef.value?.textareaRef?.select()
})
</script>
