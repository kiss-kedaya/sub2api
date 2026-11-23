<template>
  <!--
    Segmented one-time-code field (email verification, and reusable by any
    future code flow with another length).

    Layout: one input per digit, an optional separator dot between the halves,
    and an optional footer slot for "resend / countdown" actions.

    Behaviour:
    - digits only (full-width digits are folded to ASCII);
    - typing advances to the next cell, Backspace walks back and clears;
    - pasting / iOS one-time-code autofill distributes the whole code;
    - a complete code emits `complete`, and (when `autoSubmit` is on) `submit`
      exactly once per entered value so the page can verify immediately.

    The page owns sending/verifying; this component owns the code string.
  -->
  <div
    class="verification-code-input"
    role="group"
    :aria-label="label"
    :aria-describedby="hint ? hintId : undefined"
    :data-testid="testId"
    :data-value="modelValue"
    :data-length="length"
  >
    <div class="flex items-center justify-center gap-1.5 sm:gap-2" dir="ltr">
      <template v-for="cell in cells" :key="cell">
        <span
          v-if="separator && cell === separatorAfter"
          class="mx-0.5 h-1 w-1 shrink-0 rounded-full bg-[var(--ui-text-dimmed)]"
          aria-hidden="true"
          data-testid="verification-code-separator"
        ></span>
        <UInput
          :ref="(element: unknown) => setCellRef(cell, element)"
          :id="cell === 0 ? inputId : undefined"
          :model-value="digits[cell] ?? ''"
          type="text"
          inputmode="numeric"
          maxlength="1"
          :autocomplete="cell === 0 ? autocomplete : 'off'"
          autocapitalize="off"
          :spellcheck="false"
          :disabled="disabled"
          :aria-label="`${label} ${cell + 1}`"
          :aria-invalid="invalid || undefined"
          :data-testid="`verification-code-digit-${cell}`"
          :data-index="cell"
          :color="invalid ? 'error' : 'primary'"
          size="lg"
          :ui="{ root: 'min-w-0 flex-1', base: 'verification-code-input__digit' }"
          @update:model-value="onDigitInput(cell, $event)"
          @keydown="onKeydown(cell, $event)"
          @paste="onPaste(cell, $event)"
          @focus="selectCellContent"
        />
      </template>
    </div>

    <p v-if="errorText" class="mt-2 text-center text-xs text-error" role="alert">{{ errorText }}</p>
    <p v-else-if="hint" :id="hintId" class="mt-2 text-center text-xs text-muted">{{ hint }}</p>

    <div v-if="$slots.footer" class="mt-3 text-center">
      <slot name="footer" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { normalizeOtpDigits } from '@/utils/otp'

/** Only the inner input is needed to move focus between cells. */
interface CodeCellHandle {
  inputRef?: HTMLInputElement | null
}

const props = withDefaults(defineProps<{
  modelValue: string
  /** Digit count. 6 for email verification; the layout scales to any >= 2. */
  length?: number
  /** Draw the dot between the two halves. */
  separator?: boolean
  /** Cell index the separator is drawn before. Defaults to the middle. */
  separatorAfter?: number
  /** Emit `submit` once the code is complete, so the page can verify at once. */
  autoSubmit?: boolean
  disabled?: boolean
  invalid?: boolean
  /** Accessible label for the group and each digit ("Code 1"…). */
  label: string
  hint?: string
  errorText?: string
  autocomplete?: string
  /** Id put on the first cell, so labels/specs can target the field. */
  inputId?: string
  testId?: string
}>(), {
  length: 6,
  separator: true,
  autoSubmit: true,
  disabled: false,
  invalid: false,
  label: '',
  hint: '',
  errorText: '',
  autocomplete: 'one-time-code',
  inputId: 'verification-code',
  testId: 'verification-code-input'
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
  complete: [value: string]
  submit: [value: string]
}>()

const cells = computed(() => Array.from({ length: Math.max(2, props.length) }, (_, index) => index))
const separatorAfter = computed(() => {
  if (!props.separator || props.length < 2) return -1
  return props.separatorAfter ?? Math.floor(props.length / 2)
})
const hintId = computed(() => `${props.inputId}-hint`)

const digits = ref<string[]>(toDigits(props.modelValue))
const cellHandles: Array<CodeCellHandle | null> = []
/** Complete codes already submitted, so editing and re-completing submits again. */
let lastSubmitted = ''

function toDigits(value: string): string[] {
  const normalized = normalizeOtpDigits(value ?? '').slice(0, Math.max(2, props.length))
  return Array.from({ length: Math.max(2, props.length) }, (_, index) => normalized[index] ?? '')
}

function setCellRef(index: number, element: unknown): void {
  cellHandles[index] = (element as CodeCellHandle | null) ?? null
}

function innerInput(index: number): HTMLInputElement | null {
  return cellHandles[index]?.inputRef ?? null
}

function focusCell(index: number): void {
  const target = Math.max(0, Math.min(cells.value.length - 1, index))
  const input = innerInput(target)
  if (!input) return
  input.focus()
  input.select?.()
}

function selectCellContent(): void {
  const active = document.activeElement
  if (active instanceof HTMLInputElement) active.select?.()
}

function commit(next: string[], user = true): void {
  // Auto-verify only on the transition into a complete code. Editing an already
  // complete code in place must not fire a request per keystroke; the page's
  // submit button is still there for "verify this corrected code" retries.
  const wasComplete = digits.value.join('').length === cells.value.length
  digits.value = next
  const value = next.join('')
  emit('update:modelValue', value)
  if (!user) return
  if (value.length < cells.value.length) {
    lastSubmitted = ''
    return
  }
  if (wasComplete) return
  emit('complete', value)
  if (props.autoSubmit && value !== lastSubmitted) {
    lastSubmitted = value
    emit('submit', value)
  }
}

/** Writes `text` from `start`, then parks focus on the first empty cell. */
function distribute(text: string, start: number): void {
  const incoming = normalizeOtpDigits(text)
  if (!incoming) return
  const from = incoming.length >= cells.value.length ? 0 : start
  const next = [...digits.value]
  for (let offset = 0; offset < incoming.length; offset++) {
    const index = from + offset
    if (index >= cells.value.length) break
    next[index] = incoming[offset]
  }
  commit(next)
  const firstEmpty = next.findIndex((digit, index) => index >= from && !digit)
  nextTick(() => focusCell(firstEmpty === -1 ? Math.min(from + incoming.length, cells.value.length - 1) : firstEmpty))
}

function onDigitInput(index: number, value: unknown): void {
  if (props.disabled) return
  const raw = normalizeOtpDigits(typeof value === 'string' ? value : String(value ?? ''))
  if (raw.length > 1) {
    distribute(raw, index)
    return
  }
  const next = [...digits.value]
  next[index] = raw
  commit(next)
  if (raw) nextTick(() => focusCell(index + 1))
}

function onKeydown(index: number, event: KeyboardEvent): void {
  if (props.disabled) return
  const key = event.key
  if (key === 'Backspace') {
    event.preventDefault()
    const next = [...digits.value]
    if (next[index]) {
      next[index] = ''
      commit(next)
      return
    }
    if (index === 0) return
    next[index - 1] = ''
    commit(next)
    nextTick(() => focusCell(index - 1))
    return
  }
  if (key === 'Delete') {
    event.preventDefault()
    const next = [...digits.value]
    next[index] = ''
    commit(next)
    return
  }
  if (key === 'ArrowLeft') {
    event.preventDefault()
    focusCell(index - 1)
    return
  }
  if (key === 'ArrowRight') {
    event.preventDefault()
    focusCell(index + 1)
    return
  }
  if (key === 'Home') {
    event.preventDefault()
    focusCell(0)
    return
  }
  if (key === 'End') {
    event.preventDefault()
    focusCell(cells.value.length - 1)
  }
}

function onPaste(index: number, event: ClipboardEvent): void {
  event.preventDefault()
  if (props.disabled) return
  distribute(event.clipboardData?.getData('text') ?? '', index)
}

// External writes (resend clears the field, a saved draft restores it) must not
// emit complete/submit: only the person typing drives verification.
watch(() => props.modelValue, (value) => {
  const next = toDigits(value)
  if (next.join('') === digits.value.join('')) return
  digits.value = next
  lastSubmitted = next.join('').length === cells.value.length ? lastSubmitted : ''
})

onMounted(() => {
  if (!props.disabled) nextTick(() => focusCell(0))
})

defineExpose({
  focus: focusCell,
  clear: () => commit(Array.from({ length: cells.value.length }, () => ''), false)
})
</script>

<style scoped>
/* UInput owns the box; this block only gives the box the segmented-code shape.
   The ring is painted from --app-control-border (>=3:1 on the ink surface), the
   text never drops under 16px so iOS does not zoom the page on focus. */
.verification-code-input :deep(.verification-code-input__digit) {
  height: 3rem;
  padding-inline: 0;
  text-align: center;
  font-size: max(16px, 1.125rem);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  --tw-ring-color: var(--app-control-border);
}

@media (min-width: 640px) {
  .verification-code-input :deep(.verification-code-input__digit) {
    height: 3.5rem;
    font-size: max(16px, 1.25rem);
  }
}

/* Keep all six cells on one line on narrow phones. */
.verification-code-input {
  max-width: 24rem;
  margin-inline: auto;
}
</style>
