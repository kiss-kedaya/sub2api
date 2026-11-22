<template>
  <section
    class="rounded-xl border border-amber-300 bg-amber-50 p-5 text-amber-950 shadow-sm sm:p-6 dark:border-amber-400/40 dark:bg-amber-400/10 dark:text-amber-50"
    role="alert"
    data-testid="key-rotation-gate-notice"
  >
    <div class="flex items-start gap-4">
      <div class="flex size-11 shrink-0 items-center justify-center rounded-full bg-amber-100 dark:bg-amber-400/20">
        <UIcon name="i-lucide-shield-alert" class="size-6 text-amber-600 dark:text-amber-300" aria-hidden="true" />
      </div>
      <div class="min-w-0 space-y-3">
        <div class="space-y-1">
          <h2 class="text-base font-semibold">{{ t('keyRotationGate.title') }}</h2>
          <p class="text-sm leading-6">{{ t('keyRotationGate.description') }}</p>
        </div>

        <dl class="flex flex-wrap gap-x-8 gap-y-2 text-xs">
          <div v-if="activeCount >= 0">
            <dt class="font-medium text-amber-700 dark:text-amber-200">{{ t('keyRotationGate.activeKeysLabel') }}</dt>
            <dd class="mt-0.5 tabular-nums">{{ activeCount }}</dd>
          </div>
          <div v-if="pendingCount > 0">
            <dt class="font-medium text-amber-700 dark:text-amber-200">{{ t('keyRotationGate.pendingKeysLabel') }}</dt>
            <dd class="mt-0.5 tabular-nums">{{ pendingCount }}</dd>
          </div>
        </dl>

        <p class="text-xs leading-5 text-amber-800 dark:text-amber-100/80">
          {{ t('keyRotationGate.hint') }}
        </p>

        <div class="flex flex-wrap items-center gap-2">
          <UButton
            :to="remediationPath"
            size="lg"
            color="warning"
            icon="i-lucide-key-round"
            data-testid="key-rotation-gate-action"
          >
            {{ t('keyRotationGate.action') }}
          </UButton>
          <!-- 次级动作：显式风险确认。故意用 ghost/neutral，不做成和整改按钮一样醒目。 -->
          <UButton
            v-if="riskAckEnabled"
            size="sm"
            color="neutral"
            variant="ghost"
            :loading="submitting"
            :disabled="submitting"
            data-testid="key-rotation-gate-risk-ack-action"
            @click="acknowledgeRisk"
          >
            {{ submitting ? t('keyRotationGate.riskAckLoading') : t('keyRotationGate.riskAckAction') }}
          </UButton>
        </div>

        <p v-if="riskAckError" class="text-xs leading-5 text-red-700 dark:text-red-300" role="alert" data-testid="key-rotation-gate-risk-ack-error">
          {{ riskAckError }}
        </p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { keyRotationGateAPI, type KeyRotationGateStatus } from '@/api/keyRotationGate'
import { KEY_ROTATION_REMEDIATION_PATH } from '@/api/keyRotationGateCodes'
import { confirm } from '@/composables/useConfirmation'
import { ensureKeyRotationGate } from '@/composables/useKeyRotationGate'

const props = defineProps<{
  status?: KeyRotationGateStatus | null
}>()

const { t } = useI18n()

const remediationPath = computed(() => props.status?.remediation_path || KEY_ROTATION_REMEDIATION_PATH)

const activeCount = computed(() => props.status?.active_key_count ?? -1)
const pendingCount = computed(() => props.status?.pending_key_count ?? 0)

// 后端开关关闭（security.key_rotation_risk_ack_enabled=false）时不展示次级按钮。
const riskAckEnabled = computed(() => props.status?.risk_ack_enabled === true)
const submitting = ref(false)
const riskAckError = ref('')

/**
 * 显式风险确认：先弹一次确认（说明 key 泄漏后他人可直接盗刷余额），
 * 确认后才写确认记录并刷新全局门禁状态，页面随之恢复充值/兑换表单。
 */
async function acknowledgeRisk(): Promise<void> {
  if (submitting.value) return
  riskAckError.value = ''
  const confirmed = await confirm({
    title: t('keyRotationGate.riskAckConfirmTitle'),
    message: t('keyRotationGate.riskAckConfirmMessage'),
    confirmText: t('keyRotationGate.riskAckConfirm'),
    cancelText: t('keyRotationGate.riskAckCancel'),
    danger: true
  })
  if (!confirmed) return

  submitting.value = true
  try {
    await keyRotationGateAPI.acknowledgeRisk()
    await ensureKeyRotationGate(true)
  } catch {
    // 失败（含功能被关闭的 403）保持原拦截状态，并允许用户重试或去整改。
    riskAckError.value = t('keyRotationGate.riskAckFailed')
  } finally {
    submitting.value = false
  }
}
</script>
