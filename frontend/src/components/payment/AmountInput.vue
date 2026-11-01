<template>
  <div class="payment-amount-input grid gap-5">
    <UInput
      :model-value="customText"
      type="text"
      inputmode="decimal"
      size="xl"
      class="w-full"
      :placeholder="placeholderText"
      :aria-label="t('payment.customAmount')"
      :ui="{ base: 'h-16 ps-[4.5rem] text-[26px] font-medium tabular-nums max-[479px]:ps-16 max-[479px]:text-[22px]', leading: 'ps-4' }"
      @input="handleInput"
    >
      <template #leading>
        <span class="payment-amount-input__currency max-w-12 text-xs font-semibold text-muted">{{ currency }}</span>
      </template>
    </UInput>
    <div>
      <p class="mb-2 text-sm font-medium text-default">{{ t('payment.quickAmounts') }}</p>
      <div class="payment-amount-input__grid grid grid-cols-3 gap-2 max-[374px]:grid-cols-2">
        <UButton
          v-for="amt in filteredAmounts"
          :key="amt"
          color="neutral"
          size="lg"
          :variant="modelValue === amt ? 'subtle' : 'outline'"
          :aria-pressed="modelValue === amt"
          :class="[
            'payment-amount-input__option min-h-11 min-w-0 justify-center text-base font-semibold tabular-nums',
            modelValue === amt ? 'text-highlighted ring-2 ring-inset ring-primary' : 'text-default'
          ]"
          @click="selectAmount(amt)"
        >
          {{ amt }}
        </UButton>
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
