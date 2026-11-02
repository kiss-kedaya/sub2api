<template>
  <div class="w-full">
    <DefineControl>
      <UInput
        :id="id"
        ref="inputRef"
        :type="type"
        :model-value="modelValue == null ? '' : String(modelValue)"
        :disabled="disabled"
        :required="required"
        :placeholder="placeholderText"
        :autocomplete="autocomplete"
        :readonly="readonly"
        class="w-full"
        @update:model-value="onInput"
        @change="$emit('change', ($event.target as HTMLInputElement).value)"
        @blur="$emit('blur', $event)"
        @focus="$emit('focus', $event)"
        @keyup.enter="$emit('enter', $event)"
      >
        <template v-if="$slots.prefix" #leading>
          <span class="pointer-events-none flex items-center text-dimmed">
            <slot name="prefix"></slot>
          </span>
        </template>
        <!-- Suffix slot (e.g. password toggle or clear button) stays clickable. -->
        <template v-if="$slots.suffix" #trailing>
          <slot name="suffix"></slot>
        </template>
      </UInput>
    </DefineControl>

    <!-- A field wrapper only when this component shows its own label / hint / error,
         so a page-level UFormField around <Input> keeps its own label link. -->
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
  modelValue: string | number | null | undefined
  type?: string
  label?: string
  placeholder?: string
  disabled?: boolean
  required?: boolean
  readonly?: boolean
  error?: string
  hint?: string
  id?: string
  autocomplete?: string
}

const props = withDefaults(defineProps<Props>(), {
  type: 'text',
  disabled: false,
  required: false,
  readonly: false
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'change', value: string): void
  (e: 'blur', event: FocusEvent): void
  (e: 'focus', event: FocusEvent): void
  (e: 'enter', event: KeyboardEvent): void
}>()

const [DefineControl, ReuseControl] = createReusableTemplate()

const inputRef = ref<{ inputRef?: HTMLInputElement | null } | null>(null)
const placeholderText = computed(() => props.placeholder || '')

const onInput = (value: string | null | undefined) => {
  emit('update:modelValue', value == null ? '' : String(value))
}

// Expose focus method
defineExpose({
  focus: () => inputRef.value?.inputRef?.focus(),
  select: () => inputRef.value?.inputRef?.select()
})
</script>
