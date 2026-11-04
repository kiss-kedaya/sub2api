<template>
  <BaseDialog
    :show="show"
    :title="outcome ? t('admin.crypto.depositResultTitle') : t('admin.crypto.depositConfirmTitle')"
    @close="emit('close')"
  >
    <div v-if="!outcome" class="crypto-deposit-confirm space-y-4 text-sm">
      <dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5">
        <dt class="text-muted">{{ t('admin.crypto.depositConfirmCount') }}</dt>
        <dd data-testid="deposit-confirm-count" class="text-highlighted">{{ rows.length }}</dd>
        <dt class="text-muted">{{ t('admin.crypto.depositConfirmTotal') }}</dt>
        <dd data-testid="deposit-confirm-total" class="font-medium text-highlighted">{{ total }}</dd>
        <dt class="text-muted">{{ t('admin.crypto.depositConfirmChains') }}</dt>
        <dd data-testid="deposit-confirm-chains" class="text-highlighted">{{ chains.join(', ') }}</dd>
      </dl>
      <UAlert data-testid="deposit-confirm-warning" color="warning" variant="subtle" :description="t('admin.crypto.depositConfirmWarning')" />
      <UInput
        v-model="note"
        data-testid="deposit-confirm-note"
        maxlength="200"
        autocomplete="off"
        class="w-full"
        :placeholder="t('admin.crypto.depositConfirmNote')"
        :aria-label="t('admin.crypto.depositConfirmNote')"
      />
    </div>
    <div v-else class="crypto-deposit-result space-y-3 text-sm">
      <p data-testid="deposit-result-summary" class="text-toned">{{ t('admin.crypto.depositResultSummary', { credited: outcome.credited, failed: outcome.failed }) }}</p>
      <ul class="space-y-3">
        <li v-for="item in outcome.results" :key="item.id" data-testid="deposit-result-item" class="flex flex-col gap-0.5">
          <div class="flex items-center justify-between gap-3">
            <span class="font-mono text-xs text-muted">#{{ item.id }}<template v-if="detail(item.id)"> · {{ detail(item.id) }}</template></span>
            <UBadge :color="resultColor(item.status)" variant="subtle">{{ resultLabel(item.status) }}</UBadge>
          </div>
          <p v-if="item.message" class="text-xs text-muted wrap-anywhere">{{ item.message }}</p>
          <p v-if="item.order_id" class="text-xs text-muted">{{ t('admin.crypto.depositResultOrder', { id: item.order_id }) }}</p>
        </li>
      </ul>
    </div>
    <template #footer>
      <div class="flex justify-end gap-2">
        <template v-if="!outcome">
          <UButton color="neutral" variant="subtle" data-testid="deposit-confirm-cancel" @click="emit('close')">{{ t('common.cancel') }}</UButton>
          <UButton color="error" data-testid="deposit-confirm-submit" :disabled="!rows.length" @click="submit">{{ t('admin.crypto.depositConfirmSubmit') }}</UButton>
        </template>
        <UButton v-else data-testid="deposit-result-close" @click="emit('close')">{{ t('common.close') }}</UButton>
      </div>
    </template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { DepositConfirmOutcome, DepositRecord } from '@/api/admin/crypto'

// Presentational only. The parent snapshots the selected rows when it opens the
// dialog and runs the payment verification itself, so this component never
// touches the network or the 2FA credential.
const props = defineProps<{
  show: boolean
  rows: DepositRecord[]
  total: string
  outcome: DepositConfirmOutcome | null
}>()
const emit = defineEmits<{ close: []; confirm: [note: string] }>()
const { t } = useI18n()

const note = ref('')
const chains = computed(() => [...new Set(props.rows.map(row => row.chain))])
const rowById = computed(() => new Map(props.rows.map(row => [row.id, row])))

watch(() => props.show, (open) => { if (open) note.value = '' })

function detail(id: number): string {
  const row = rowById.value.get(id)
  return row ? `${row.chain} · ${row.amount}` : ''
}

// Keys are written out in full on purpose: the locale completeness check scans the source text for them.
function resultLabel(status: string): string {
  switch (status) {
    case 'credited': return t('admin.crypto.depositResultCredited')
    case 'already_credited': return t('admin.crypto.depositResultAlready')
    case 'rejected': return t('admin.crypto.depositResultRejected')
    case 'error': return t('admin.crypto.depositResultError')
    default: return status
  }
}

function resultColor(status: string): 'success' | 'warning' | 'error' | 'neutral' {
  if (status === 'credited') return 'success'
  if (status === 'rejected') return 'warning'
  if (status === 'error') return 'error'
  return 'neutral'
}

function submit() {
  emit('confirm', note.value.trim())
}
</script>
