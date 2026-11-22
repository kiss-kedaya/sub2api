<template>
  <section
    v-if="expiresLabel"
    class="flex items-start gap-3 rounded-lg border border-primary-200 bg-primary-50 px-4 py-3 text-sm text-primary-900 dark:border-primary-400/30 dark:bg-primary-400/10 dark:text-primary-100"
    role="status"
    data-testid="key-rotation-risk-ack-banner"
  >
    <UIcon name="i-lucide-shield-check" class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
    <div class="min-w-0 space-y-1">
      <p class="font-medium leading-6">{{ t('keyRotationGate.riskAckExpiry', { time: expiresLabel }) }}</p>
      <p class="text-xs leading-5 opacity-90">{{ t('keyRotationGate.riskAckBanner' ) }}</p>
      <UButton
        v-if="remediationPath"
        :to="remediationPath"
        size="xs"
        color="neutral"
        variant="link"
        class="p-0"
        data-testid="key-rotation-risk-ack-banner-action"
      >
        {{ t('keyRotationGate.action') }}
      </UButton>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { KeyRotationGateStatus } from '@/api/keyRotationGate'
import { KEY_ROTATION_REMEDIATION_PATH } from '@/api/keyRotationGateCodes'

/**
 * 「风险确认仍在有效期内」的提示条。
 *
 * 用户确认风险后门禁会放行充值/兑换，整改卡片随之消失；这条轻提示负责让
 * 用户看到确认的失效时间（如「风险确认有效期至 14:30」），避免误以为永久豁免。
 */
const props = defineProps<{
  status?: KeyRotationGateStatus | null
}>()

const { t } = useI18n()

const remediationPath = computed(() => props.status?.remediation_path || KEY_ROTATION_REMEDIATION_PATH)

const expiresLabel = computed(() => {
  const raw = props.status?.risk_ack_expires_at
  if (!raw) return ''
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return ''
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  return `${hours}:${minutes}`
})
</script>
