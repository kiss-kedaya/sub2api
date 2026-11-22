<template>
  <div class="deposit-amount" :class="{ 'deposit-amount--error': check.blocked }">
    <label class="deposit-amount__label" for="deposit-amount-input">{{ t('deposit.amountFieldLabel') }}</label>
    <div class="deposit-amount__field">
      <input id="deposit-amount-input" class="deposit-amount__input" type="text" inputmode="decimal"
        autocomplete="off" :value="modelValue" :disabled="disabled" :aria-invalid="check.blocked"
        aria-describedby="deposit-amount-message" :placeholder="t('deposit.amountPlaceholder')"
        data-testid="deposit-amount-input" @input="onInput" />
      <span v-if="unit" class="deposit-amount__unit">{{ unit }}</span>
    </div>
    <p v-if="check.blocked" id="deposit-amount-message" class="deposit-amount__message deposit-amount__message--error"
      role="alert" data-testid="deposit-amount-error">{{ errorText }}</p>
    <p v-else id="deposit-amount-message" class="deposit-amount__message" data-testid="deposit-amount-hint">{{ hintText }}</p>
    <p v-if="estimateText" class="deposit-amount__estimate" data-testid="deposit-amount-credit">{{ estimateText }}</p>
    <p v-else-if="notionalText" class="deposit-amount__estimate" data-testid="deposit-amount-notional">{{ notionalText }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { estimatedDepositCredit, type DepositAmountCheck } from '@/utils/depositAmount'

// Presentational amount field: the deposit page owns the state and passes the
// already-validated snapshot in, so the red error, the disabled transfer action
// and the estimated credit can never disagree.
const props = defineProps<{
  modelValue: string
  check: DepositAmountCheck
  /** Unit of the amount the user types (the coin being sent). */
  unit: string
  /** Unit the backend thresholds use: the token, or USD for a market-priced coin. */
  limitUnit: string
  /** Fixed credit rate, if the asset has one. */
  rate: number | null
  disabled?: boolean
}>()

const emit = defineEmits<{ (event: 'update:modelValue', value: string): void }>()
const { t } = useI18n()

function onInput(event: Event) {
  emit('update:modelValue', (event.target as HTMLInputElement).value)
}

// Up to 8 decimals, trailing zeros trimmed: thresholds like 0.001 must not
// render as "0.00" and hide the actual limit.
function fmt(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return '—'
  return Number(value.toFixed(8)).toString()
}

// Market-priced assets are compared by USD notional, so their limits are
// announced in USD; everyone else is compared in the coin itself.
const unitOfLimits = computed(() => props.limitUnit)

const errorText = computed(() => {
  const { limits, state } = props.check
  switch (state) {
    case 'invalid':
      return t('deposit.amountInvalid')
    case 'below_min':
      return t('deposit.amountBelowMin', { min: fmt(limits.min), unit: unitOfLimits.value })
    case 'above_max':
      return t('deposit.amountAboveMax', { max: fmt(limits.max), unit: unitOfLimits.value })
    default:
      return ''
  }
})

const hintText = computed(() => {
  const { limits, state } = props.check
  if (state === 'empty') {
    if (limits.min !== null && limits.max !== null) {
      return t('deposit.amountHintRange', { min: fmt(limits.min), max: fmt(limits.max), unit: unitOfLimits.value })
    }
    if (limits.min !== null) return t('deposit.amountHintMin', { min: fmt(limits.min), unit: unitOfLimits.value })
    if (limits.max !== null) return t('deposit.amountHintMax', { max: fmt(limits.max), unit: unitOfLimits.value })
    // No thresholds on this deployment: say so instead of inventing a range.
    return t('deposit.amountNoLimit')
  }
  if (state === 'unverifiable') return t('deposit.amountUnverifiable')
  return t('deposit.amountWithinRange')
})

// Only a fixed-rate asset can predict the balance credit. A market-priced coin
// settles at the credit-time price, so it only gets a reference notional and
// never a made-up credit number.
const estimateText = computed(() => {
  if (props.check.state !== 'ok') return ''
  const credit = estimatedDepositCredit(props.check.amount, props.rate)
  return credit === null ? '' : t('deposit.amountEstimateCredit', { credit: fmt(credit) })
})

const notionalText = computed(() => {
  if (props.check.state !== 'ok') return ''
  if (props.check.notional === null) return ''
  return t('deposit.amountEstimateMarket', { usd: fmt(props.check.notional) })
})
</script>

<style scoped>
.deposit-amount { display: grid; gap: .4rem; max-width: 24rem; margin-top: 1rem; }
.deposit-amount__label { font-size: .75rem; color: var(--signal-muted, #8c99a6); }
.deposit-amount__field { position: relative; display: flex; align-items: center; }
.deposit-amount__input {
  width: 100%; min-height: 2.75rem; padding: .55rem 3.5rem .55rem .75rem;
  border: 1px solid var(--ui-border-accented); border-radius: 6px;
  background: var(--ui-bg, transparent); color: inherit;
  font-size: .9375rem; font-variant-numeric: tabular-nums;
}
.deposit-amount__input:focus-visible { outline: 2px solid var(--signal-accent, #0891b2); outline-offset: 1px; }
.deposit-amount--error .deposit-amount__input { border-color: #dc2626; }
.deposit-amount__unit {
  position: absolute; inset-inline-end: .75rem; pointer-events: none;
  color: var(--signal-muted, #8c99a6); font-size: .75rem; font-weight: 600;
}
.deposit-amount__message { margin: 0; font-size: .75rem; line-height: 1.6; color: var(--signal-muted, #8c99a6); }
.deposit-amount__message--error { color: #dc2626; }
.deposit-amount__estimate { margin: 0; font-size: .75rem; line-height: 1.6; color: #10b981; font-variant-numeric: tabular-nums; }
</style>
