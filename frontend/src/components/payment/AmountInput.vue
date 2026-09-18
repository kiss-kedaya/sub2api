<template>
  <div class="payment-amount-input">
    <label class="payment-amount-input__entry">
      <span class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('payment.amountLabel') }}</span>
      <span class="payment-amount-input__field">
        <span class="payment-amount-input__currency">{{ currency }}</span>
        <input
          type="text"
          inputmode="decimal"
          :value="customText"
          :placeholder="placeholderText"
          :aria-label="t('payment.customAmount')"
          class="input w-full"
          @input="handleInput"
        />
      </span>
    </label>
    <!-- Quick Amount Buttons -->
    <div>
      <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
        {{ t('payment.quickAmounts') }}
      </label>
      <div class="payment-amount-input__grid grid grid-cols-3 gap-2">
        <button
          v-for="amt in filteredAmounts"
          :key="amt"
          type="button"
          :aria-pressed="modelValue === amt"
          :class="[
            'payment-amount-input__option rounded-lg border-2 px-4 py-3 text-center font-medium transition-colors',
            modelValue === amt
              ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-400 dark:bg-primary-900/40 dark:text-primary-300'
              : 'border-gray-200 bg-white text-gray-700 hover:border-gray-300 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200 dark:hover:border-dark-500',
          ]"
          @click="selectAmount(amt)"
        >
          {{ amt }}
        </button>
      </div>
    </div>

  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = withDefaults(defineProps<{
  amounts?: number[]
  modelValue: number | null
  min?: number
  max?: number
  currency?: string
}>(), {
  amounts: () => [10, 20, 50, 100, 200, 500, 1000, 2000, 5000],
  min: 0,
  max: 0,
  currency: 'USD',
})

const emit = defineEmits<{
  'update:modelValue': [value: number | null]
}>()

const { t } = useI18n()

const customText = ref('')

// 0 = no limit
const filteredAmounts = computed(() =>
  props.amounts.filter((a) => (props.min <= 0 || a >= props.min) && (props.max <= 0 || a <= props.max))
)

const placeholderText = computed(() => {
  if (props.min > 0 && props.max > 0) return `${props.min} - ${props.max}`
  if (props.min > 0) return `≥ ${props.min}`
  if (props.max > 0) return `≤ ${props.max}`
  return t('payment.enterAmount')
})

const AMOUNT_PATTERN = /^\d*(\.\d{0,2})?$/

function selectAmount(amt: number) {
  customText.value = String(amt)
  emit('update:modelValue', amt)
}

function handleInput(e: Event) {
  const input = e.target as HTMLInputElement
  const val = input.value
  if (!AMOUNT_PATTERN.test(val)) {
    input.value = customText.value
    return
  }
  customText.value = val
  if (val === '') {
    emit('update:modelValue', null)
    return
  }
  const num = parseFloat(val)
  if (!isNaN(num) && num > 0) {
    emit('update:modelValue', num)
  } else {
    emit('update:modelValue', null)
  }
}

watch(() => props.modelValue, (v) => {
  if (v === null) {
    if (Number(customText.value) > 0) customText.value = ''
  } else if (String(v) !== customText.value) customText.value = String(v)
}, { immediate: true })
</script>

<style scoped>
.payment-amount-input { display: grid; gap: 20px; }
.payment-amount-input__entry { display: block; min-width: 0; }
.payment-amount-input__field { position: relative; display: block; }
.payment-amount-input__field .input { min-height: 64px; padding: 12px 16px 12px 72px; font-size: 26px; font-variant-numeric: tabular-nums; }
.payment-amount-input__currency { position: absolute; inset: 0 auto 0 16px; display: flex; align-items: center; max-width: 48px; font-size: 12px; font-weight: 600; color: var(--signal-muted, #6b7280); }
@media (max-width: 479px) {
  .payment-amount-input__field .input { font-size: 22px; padding-left: 64px; }
}
</style>
