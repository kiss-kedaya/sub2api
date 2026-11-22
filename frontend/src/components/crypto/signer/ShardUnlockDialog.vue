<template>
  <BaseDialog
    :show="show"
    :title="mode === 'verify' ? t('admin.crypto.signer.unlock.titleVerify') : t('admin.crypto.signer.unlock.titleSweep')"
    :close-on-escape="!running"
    :show-close-button="!running"
    @close="requestClose"
  >
    <div class="shard-unlock space-y-4 text-sm" data-testid="shard-unlock">
      <UAlert
        v-if="locked"
        color="warning"
        variant="subtle"
        icon="i-lucide-lock"
        :title="t('admin.crypto.signer.locked')"
        :description="t('admin.crypto.signer.unlock.lockedNotice')"
      />

      <template v-if="stage === 'input'">
        <p class="text-xs leading-relaxed text-muted">{{ t('admin.crypto.signer.unlock.hint') }}</p>
        <ShareInputPanel
          v-model="shareText"
          :crypto="crypto"
          :label="t('admin.crypto.signer.unlock.shareLabel')"
          :hint="t('admin.crypto.signer.unlock.hint')"
          input-id="shard-unlock-input"
          :disabled="running"
          @parsed="parsedShare = $event"
        />
        <div class="space-y-2">
          <label for="shard-unlock-totp" class="block font-medium text-default">{{ t('admin.crypto.signer.unlock.totpLabel') }}</label>
          <TotpCodeInput
            id="shard-unlock-totp"
            v-model="totpCode"
            :label="t('admin.crypto.signer.unlock.totpLabel')"
            :invalid="!!errorMessage"
            :disabled="running"
            :autofocus="false"
          />
          <p class="text-xs text-muted">{{ t('admin.crypto.signer.unlock.totpHint') }}</p>
        </div>
        <UAlert v-if="rateLimited" role="alert" color="error" variant="subtle" icon="i-lucide-timer-off" :title="t('admin.crypto.signer.unlock.rateLimited')" />
        <UAlert v-else-if="errorMessage" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="errorMessage" />
      </template>

      <p v-else-if="stage === 'running'" role="status" class="flex items-center gap-2 text-muted" data-testid="shard-unlock-running">
        <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
        {{ mode === 'verify' ? t('admin.crypto.signer.unlock.verifying') : t('admin.crypto.signer.unlock.unlocking') }}
      </p>

      <template v-else-if="stage === 'audit'">
        <UAlert
          v-if="mode === 'verify'"
          color="success"
          variant="subtle"
          icon="i-lucide-circle-check"
          :title="t('admin.crypto.signer.unlock.verifySuccess')"
          data-testid="shard-unlock-verified"
        />
        <template v-else>
          <UAlert
            color="success"
            variant="subtle"
            icon="i-lucide-circle-check"
            :title="t('admin.crypto.signer.unlock.auditFinished')"
            :description="audit?.dryRun ? t('admin.crypto.signer.unlock.auditDryRun') : undefined"
          />
          <dl class="grid gap-2 sm:grid-cols-[9rem_minmax(0,1fr)]" data-testid="shard-unlock-audit">
            <dt class="text-muted">{{ t('admin.crypto.signer.unlock.auditAddresses') }}</dt>
            <dd class="text-default">{{ audit?.swept ?? 0 }} / {{ audit?.planned ?? 0 }}</dd>
            <dt class="text-muted">{{ t('admin.crypto.signer.unlock.auditAmount') }}</dt>
            <dd class="text-default">{{ audit?.amountUsd == null ? '—' : audit.amountUsd.toFixed(2) }}</dd>
            <dt class="text-muted">{{ t('admin.crypto.signer.unlock.auditStatus') }}</dt>
            <dd class="text-default">{{ audit?.status ?? '—' }}<span v-if="(audit?.failed ?? 0) > 0" class="text-error"> · failed {{ audit?.failed }}</span></dd>
            <dt class="text-muted">{{ t('admin.crypto.signer.unlock.auditTx') }}</dt>
            <dd class="min-w-0 space-y-1">
              <p v-if="!audit?.txHashes.length" class="text-muted">{{ t('admin.crypto.signer.unlock.auditNoTx') }}</p>
              <p
                v-for="hash in audit?.txHashes.slice(0, 12)"
                :key="hash"
                class="font-mono text-xs break-all text-default"
              >{{ hash }}</p>
            </dd>
          </dl>
        </template>
      </template>
    </div>
    <template #footer>
      <div class="flex flex-wrap justify-end gap-2">
        <UButton
          v-if="stage === 'input'"
          color="neutral"
          variant="subtle"
          :disabled="running"
          data-testid="shard-unlock-cancel"
          @click="requestClose"
        >{{ t('common.cancel') }}</UButton>
        <UButton
          v-if="stage === 'input'"
          :disabled="running || !parsedShare || totpCode.length !== 6"
          data-testid="shard-unlock-submit"
          @click="submit"
        >{{ mode === 'verify' ? t('admin.crypto.signer.unlock.submitVerify') : t('admin.crypto.signer.unlock.submitSweep') }}</UButton>
        <UButton v-if="stage === 'audit'" data-testid="shard-unlock-audit-close" @click="closeNow">
          {{ t('admin.crypto.signer.unlock.auditClose') }}
        </UButton>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'
import ShareInputPanel from '@/components/crypto/signer/ShareInputPanel.vue'
import type { SignerCrypto } from '@/utils/signer/cryptoProvider'
import type { ShardShare } from '@/utils/signer/shardFormat'
import { isRateLimitedError, signerErrorI18nKey } from '@/utils/signer/errors'
import { verifySignerShare } from '@/api/admin/cryptoSigner'
import type { SweepAuditInfo } from '@/utils/signer/audit'

const props = withDefaults(defineProps<{
  show: boolean
  crypto: SignerCrypto | null
  mode?: 'sweep' | 'verify'
  locked?: boolean
  /** Sweep executor supplied by the page; resolves with the finished audit. */
  runSweep?: (share: string, totpCode: string) => Promise<SweepAuditInfo>
}>(), {
  mode: 'sweep',
  locked: false,
  runSweep: undefined
})

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'completed', audit: SweepAuditInfo): void
}>()

const { t } = useI18n()
const stage = ref<'input' | 'running' | 'audit'>('input')
const shareText = ref('')
const totpCode = ref('')
const parsedShare = ref<ShardShare | null>(null)
const errorMessage = ref('')
const rateLimited = ref(false)
const running = ref(false)
const audit = ref<SweepAuditInfo | null>(null)

watch(() => props.show, show => {
  if (!show) return
  stage.value = 'input'
  shareText.value = ''
  totpCode.value = ''
  parsedShare.value = null
  errorMessage.value = ''
  rateLimited.value = false
  running.value = false
  audit.value = null
})

async function submit(): Promise<void> {
  if (!parsedShare.value || totpCode.value.length !== 6 || running.value) return
  errorMessage.value = ''
  rateLimited.value = false
  running.value = true
  stage.value = 'running'
  try {
    if (props.mode === 'verify') {
      await verifySignerShare(shareText.value, totpCode.value)
      audit.value = {
        addresses: 0, planned: 0, swept: 0, failed: 0, amountUsd: null,
        txHashes: [], status: 'verified', dryRun: false
      }
      stage.value = 'audit'
    } else if (props.runSweep) {
      audit.value = await props.runSweep(shareText.value, totpCode.value)
      stage.value = 'audit'
      emit('completed', audit.value)
    } else {
      throw new Error(t('admin.crypto.signer.unlock.failed'))
    }
    shareText.value = ''
    totpCode.value = ''
  } catch (error) {
    rateLimited.value = isRateLimitedError(error)
    errorMessage.value = signerErrorI18nKey(error) ?? t('admin.crypto.signer.unlock.failed')
    stage.value = 'input'
  } finally {
    running.value = false
  }
}

function requestClose(): void {
  if (running.value) return
  emit('close')
}

function closeNow(): void {
  shareText.value = ''
  totpCode.value = ''
  emit('close')
}
</script>
