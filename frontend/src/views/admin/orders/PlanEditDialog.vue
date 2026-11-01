<template>
  <BaseDialog :show="show" :title="plan ? t('payment.admin.editPlan') : t('payment.admin.createPlan')" width="wide" @close="emit('close')">
    <UForm id="plan-form" :state="planForm" class="space-y-4" @submit="handleSavePlan">
      <div class="grid grid-cols-2 gap-4">
        <UFormField :label="t('payment.admin.planName')" required>
          <UInput v-model="planForm.name" required class="w-full" />
        </UFormField>
        <UFormField :label="t('payment.admin.group')" required>
          <Select v-model="planForm.group_id" :options="groupOptions" :placeholder="t('payment.admin.selectGroup')" class="w-full">
            <template #selected="{ option }">
              <span v-if="option?.platform" :class="platformTextClass(String(option.platform))">{{ option.label }}</span>
              <span v-else>{{ option?.label || t('payment.admin.selectGroup') }}</span>
            </template>
            <template #option="{ option, selected }">
              <span class="flex-1 truncate text-left" :class="option.platform ? platformTextClass(String(option.platform)) : ''">{{ option.label }}</span>
              <UIcon v-if="selected" name="i-lucide-check" class="size-4 text-primary" />
            </template>
          </Select>
        </UFormField>
      </div>

      <!-- Group info preview: the group's limits, no box -->
      <div v-if="selectedGroupInfo" class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
        <GroupBadge :name="selectedGroupInfo.name" :platform="selectedGroupInfo.platform" :rate-multiplier="selectedGroupInfo.rate_multiplier" />
        <span><span class="text-muted">{{ t('payment.admin.dailyLimit') }}:</span> <span class="ml-1 font-medium text-default">{{ selectedGroupInfo.daily_limit_usd != null ? '$' + selectedGroupInfo.daily_limit_usd : t('payment.admin.unlimited') }}</span></span>
        <span><span class="text-muted">{{ t('payment.admin.weeklyLimit') }}:</span> <span class="ml-1 font-medium text-default">{{ selectedGroupInfo.weekly_limit_usd != null ? '$' + selectedGroupInfo.weekly_limit_usd : t('payment.admin.unlimited') }}</span></span>
        <span><span class="text-muted">{{ t('payment.admin.monthlyLimit') }}:</span> <span class="ml-1 font-medium text-default">{{ selectedGroupInfo.monthly_limit_usd != null ? '$' + selectedGroupInfo.monthly_limit_usd : t('payment.admin.unlimited') }}</span></span>
      </div>

      <UFormField :label="t('payment.admin.planDescription')" required>
        <UTextarea v-model="planForm.description" :rows="2" required class="w-full" />
      </UFormField>
      <div class="grid grid-cols-2 gap-4">
        <UFormField :label="t('payment.admin.price')" required>
          <UInput v-model.number="planForm.price" type="number" step="0.01" min="0.01" required class="w-full" />
          <p v-if="subscriptionCnyPreview" class="mt-1 text-xs font-medium text-primary">
            {{ t('payment.admin.subscriptionCnyPayPreview', { amount: subscriptionCnyPreview.amount }) }}
            <span v-if="subscriptionCnyPreview.feeRate > 0">
              {{ t('payment.admin.subscriptionCnyPayPreviewWithFee', { feeRate: subscriptionCnyPreview.feeRate, total: subscriptionCnyPreview.total }) }}
            </span>
          </p>
        </UFormField>
        <UFormField :label="t('payment.admin.forSale')">
          <USwitch v-model="planForm.for_sale" class="mt-1.5" />
        </UFormField>
      </div>
      <div class="grid grid-cols-2 gap-4">
        <UFormField :label="t('payment.admin.validity')" required>
          <UInput v-model.number="planForm.validity_days" type="number" min="1" required class="w-full" />
        </UFormField>
        <UFormField :label="t('payment.admin.validityUnit')" required>
          <Select v-model="planForm.validity_unit" :options="validityUnitOptions" />
        </UFormField>
      </div>

      <!-- Rarely changed fields -->
      <UCollapsible :unmount-on-hide="false" class="flex flex-col gap-4">
        <UButton
          color="neutral"
          variant="link"
          size="sm"
          class="self-start px-0"
          trailing-icon="i-lucide-chevron-down"
          :label="t('payment.admin.advancedOptions')"
          :ui="{ trailingIcon: 'group-data-[state=open]:rotate-180 transition-transform' }"
        />
        <template #content>
          <div class="space-y-4">
            <div class="grid grid-cols-2 gap-4">
              <UFormField :label="t('payment.admin.originalPrice')">
                <UInput v-model.number="planForm.original_price" type="number" step="0.01" min="0" class="w-full" />
              </UFormField>
              <UFormField :label="t('payment.admin.sortOrder')">
                <UInput v-model.number="planForm.sort_order" type="number" min="0" class="w-full" />
              </UFormField>
            </div>
            <UFormField :label="t('payment.admin.currency')" :help="t('payment.admin.currencyHint')">
              <UInput v-model="planForm.currency" maxlength="3" class="w-full" :ui="{ base: 'uppercase' }" :placeholder="t('payment.admin.currencyPlaceholder')" />
            </UFormField>
            <UFormField :label="t('payment.admin.features')" :help="t('payment.admin.featuresHint')">
              <UTextarea v-model="planFeaturesText" :rows="3" class="w-full" :placeholder="t('payment.admin.featuresPlaceholder')" />
            </UFormField>
          </div>
        </template>
      </UCollapsible>
    </UForm>
    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="emit('close')" />
        <UButton type="submit" form="plan-form" :loading="saving" :label="saving ? t('common.saving') : t('common.save')" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { AdminPaymentConfig } from '@/api/admin/payment'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatPaymentAmount } from '@/components/payment/currency'
import type { SubscriptionPlan } from '@/types/payment'
import type { AdminGroup } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import { platformTextClass } from '@/utils/platformColors'

const props = defineProps<{
  show: boolean
  plan: SubscriptionPlan | null
  groups: AdminGroup[]
  paymentConfig?: AdminPaymentConfig | null
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const saving = ref(false)
const planForm = reactive({ name: '', group_id: null as number | null, description: '', price: 0, original_price: 0, currency: '', validity_days: 30, validity_unit: 'days', sort_order: 0, for_sale: true })
const planFeaturesText = ref('')

const validityUnitOptions = computed(() => [
  { value: 'days', label: t('payment.admin.days') },
  { value: 'weeks', label: t('payment.admin.weeks') },
  { value: 'months', label: t('payment.admin.months') },
])

const groupOptions = computed(() =>
  props.groups
    .filter(g => g.subscription_type === 'subscription')
    .map(g => ({
      value: g.id,
      label: `${g.name} — ${g.platform} (${g.rate_multiplier}x)`,
      platform: g.platform,
    })),
)

const selectedGroupInfo = computed(() => {
  if (!planForm.group_id) return null
  return props.groups.find(g => g.id === planForm.group_id) || null
})

function roundCnyAmount(value: number): number {
  return Math.round(value * 100) / 100
}

function ceilCnyAmount(value: number): number {
  return Math.ceil(value * 100) / 100
}

const subscriptionCnyPreview = computed(() => {
  const price = Number(planForm.price) || 0
  const rate = Number(props.paymentConfig?.subscription_usd_to_cny_rate) || 0
  if (price <= 0 || rate <= 0) return null

  const amount = roundCnyAmount(price * rate)
  const feeRate = Number(props.paymentConfig?.recharge_fee_rate) || 0
  const fee = feeRate > 0 ? ceilCnyAmount((amount * feeRate) / 100) : 0
  const total = feeRate > 0 ? roundCnyAmount(amount + fee) : amount

  return {
    amount: formatPaymentAmount(amount, 'CNY'),
    feeRate,
    total: formatPaymentAmount(total, 'CNY'),
  }
})

// Reset form when dialog opens
watch(() => props.show, (visible) => {
  if (!visible) return
  if (props.plan) {
    Object.assign(planForm, { name: props.plan.name, group_id: props.plan.group_id, description: props.plan.description, price: props.plan.price, original_price: props.plan.original_price || 0, currency: props.plan.currency || '', validity_days: props.plan.validity_days, validity_unit: props.plan.validity_unit || 'days', sort_order: props.plan.sort_order || 0, for_sale: props.plan.for_sale })
    planFeaturesText.value = (props.plan.features || []).join('\n')
  } else {
    Object.assign(planForm, { name: '', group_id: null, description: '', price: 0, original_price: 0, currency: '', validity_days: 30, validity_unit: 'days', sort_order: 0, for_sale: true })
    planFeaturesText.value = ''
  }
})

/** Build request payload with snake_case keys matching backend JSON tags */
function buildPlanPayload() {
  const features = planFeaturesText.value.split('\n').map(f => f.trim()).filter(Boolean).join('\n')
  return {
    name: planForm.name,
    group_id: planForm.group_id,
    description: planForm.description,
    price: planForm.price,
    original_price: planForm.original_price || 0,
    currency: planForm.currency.trim().toUpperCase(),
    validity_days: planForm.validity_days,
    validity_unit: planForm.validity_unit,
    sort_order: planForm.sort_order,
    for_sale: planForm.for_sale,
    features,
  }
}

async function handleSavePlan() {
  if (!planForm.group_id) {
    appStore.showError(t('payment.admin.groupRequired'))
    return
  }
  if (!planForm.price || planForm.price <= 0) {
    appStore.showError(t('payment.admin.priceRequired'))
    return
  }
  if (!planForm.validity_days || planForm.validity_days < 1) {
    appStore.showError(t('payment.admin.validityRequired'))
    return
  }
  saving.value = true
  try {
    const data = buildPlanPayload()
    if (props.plan) { await adminPaymentAPI.updatePlan(props.plan.id, data) }
    else { await adminPaymentAPI.createPlan(data) }
    appStore.showSuccess(t('common.saved'))
    emit('close')
    emit('saved')
  } catch (err: unknown) { appStore.showError(extractApiErrorMessage(err, t('common.error'))) }
  finally { saving.value = false }
}
</script>
