<template>
  <div class="totp-code-input relative mx-auto grid w-full max-w-70 grid-cols-6 gap-2" dir="ltr">
    <input
      v-bind="$attrs"
      ref="input"
      :value="modelValue"
      type="text"
      inputmode="numeric"
      enterkeyhint="done"
      autocomplete="one-time-code"
      autocapitalize="off"
      :spellcheck="false"
      maxlength="6"
      pattern="[0-9]{6}"
      :aria-label="label"
      :aria-invalid="invalid || undefined"
      :disabled="disabled"
      class="absolute inset-0 z-10 h-full w-full cursor-text bg-transparent text-transparent caret-transparent opacity-100 disabled:cursor-not-allowed"
      @input="handleInput"
      @paste="handlePaste"
      @focus="syncSelection"
      @blur="focused = false"
      @select="syncSelection"
      @keyup="syncSelection"
      @pointerdown="handlePointerDown"
    />
    <span
      v-for="index in 6"
      :key="index"
      aria-hidden="true"
      class="flex h-12 min-w-0 items-center justify-center rounded-md bg-default text-center text-lg font-semibold text-highlighted tabular-nums ring ring-inset transition-colors"
      :class="[
        disabled ? 'opacity-75' : '',
        invalid ? 'ring-error' : 'ring-accented',
        focused && activeIndex === index - 1 ? (invalid ? 'outline-3 outline-error/25' : 'ring-2 ring-primary outline-3 outline-primary/25') : ''
      ]"
    >{{ modelValue[index - 1] || '\u200b' }}</span>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue'
import { normalizeOtpDigits } from '@/utils/otp'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  modelValue: string
  label: string
  disabled?: boolean
  invalid?: boolean
  autofocus?: boolean
}>(), { disabled: false, invalid: false, autofocus: true })

const emit = defineEmits<{
  'update:modelValue': [value: string]
  complete: [value: string]
}>()
const input = ref<HTMLInputElement | null>(null)
const focused = ref(false)
const activeIndex = ref(0)

function digits(value: string) {
  // Shared with the email verification field: full-width digits fold to ASCII.
  return normalizeOtpDigits(value)
}

function syncSelection() {
  focused.value = document.activeElement === input.value
  activeIndex.value = Math.min(input.value?.selectionStart ?? props.modelValue.length, 5)
}

function focus(index?: number) {
  if (props.disabled || !input.value) return
  input.value.focus()
  const position = Math.min(index ?? props.modelValue.length, props.modelValue.length)
  input.value.setSelectionRange(position, index === undefined ? position : Math.min(position + 1, props.modelValue.length))
  syncSelection()
}

function update(value: string) {
  if (props.disabled) return
  const normalized = digits(value)
  // Reject an overlong code instead of silently verifying its first six digits.
  const next = normalized.length <= 6 ? normalized : ''
  if (input.value) input.value.value = next
  emit('update:modelValue', next)
  // Completion comes from an actual edit/autofill/paste, not a retained or
  // programmatically supplied code. The parent v-model is updated first.
  if (next.length === 6 && next !== props.modelValue) emit('complete', next)
  nextTick(syncSelection)
}

function handleInput(event: Event) {
  update((event.target as HTMLInputElement).value)
}

function handlePaste(event: ClipboardEvent) {
  event.preventDefault()
  if (props.disabled) return
  update(event.clipboardData?.getData('text') || '')
  nextTick(() => focus())
}

function handlePointerDown(event: PointerEvent) {
  if (props.disabled || event.button !== 0) return
  // Never preventDefault here: Mobile Safari treats a prevented pointer/touch
  // default as "no user gesture" and immediately dismisses the keyboard, which
  // makes the six-cell field impossible to type into on iOS. Let the native
  // tap focus the field, then align the caret with the tapped cell afterwards.
  if (!input.value) return
  const bounds = input.value.getBoundingClientRect()
  const target = document.activeElement === input.value
  nextTick(() => {
    if (!input.value || !bounds.width) return
    if (!target) return
    const index = Math.max(0, Math.min(5, Math.floor((event.clientX - bounds.left) / (bounds.width / 6))))
    const position = Math.min(index, props.modelValue.length)
    input.value.setSelectionRange(position, position)
    syncSelection()
  })
}

watch(() => props.modelValue, () => nextTick(syncSelection))
watch(() => props.disabled, disabled => {
  if (!disabled && props.autofocus) nextTick(() => focus())
})
onMounted(() => {
  if (props.autofocus) nextTick(() => focus())
})

defineExpose({ focus })
</script>
