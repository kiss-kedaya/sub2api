<template>
  <section class="shard-reveal rounded-lg p-4 ring ring-default" :class="`shard-reveal--${shardSlot.toLowerCase()}`">
    <div class="flex items-start justify-between gap-3 max-sm:flex-col">
      <div class="flex min-w-0 items-start gap-3">
        <UBadge color="warning" variant="outline" class="mt-0.5 h-6 shrink-0 font-mono">{{ shardSlot }}</UBadge>
        <div class="min-w-0">
          <h4 class="font-semibold text-highlighted">{{ title }}</h4>
          <p class="mt-1 text-xs leading-relaxed text-muted">{{ description }}</p>
        </div>
      </div>
      <UBadge color="error" variant="subtle" class="shrink-0">{{ t('admin.crypto.signer.wizard.revealOnce') }}</UBadge>
    </div>

    <div v-if="!revealed" class="mt-3 flex flex-wrap items-center gap-2">
      <UButton
        color="neutral"
        variant="outline"
        size="sm"
        icon="i-lucide-eye"
        data-testid="shard-reveal-toggle"
        @click="reveal"
      >{{ t('admin.crypto.signer.wizard.revealShow') }}</UButton>
      <span class="font-mono text-xs text-muted">{{ masked }}</span>
    </div>

    <div v-else class="mt-3 space-y-3" data-testid="shard-reveal-body">
      <p class="shard-reveal__text rounded-md bg-elevated/60 p-3 font-mono text-sm leading-relaxed break-all text-highlighted">{{ shareText }}</p>

      <div class="flex flex-wrap items-center gap-2">
        <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-copy" data-testid="shard-copy" @click="copy">{{ t('admin.crypto.signer.wizard.revealCopy') }}</UButton>
        <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-download" data-testid="shard-download" @click="download">{{ t('admin.crypto.signer.wizard.revealDownload') }}</UButton>
        <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-printer" data-testid="shard-print" @click="print">{{ t('admin.crypto.signer.wizard.revealPrint') }}</UButton>
        <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-eye-off" data-testid="shard-hide" @click="hide">{{ t('admin.crypto.signer.wizard.revealHide') }}</UButton>
      </div>

      <div class="shard-reveal__qr flex flex-col items-center gap-2 rounded-md bg-white p-3">
        <img v-if="qrDataUrl" :src="qrDataUrl" :alt="t('admin.crypto.signer.wizard.revealQrAlt')" width="208" height="208" class="size-52" />
        <span class="text-[11px] text-gray-500">{{ t('admin.crypto.signer.wizard.revealQrAlt') }}</span>
      </div>

      <p v-if="statusKey" role="status" class="text-xs" :class="statusError ? 'text-error' : 'text-success'">{{ t(statusKey) }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { copySensitiveText } from '@/utils/signer/clipboard'
import { buildShareSheetHtml, downloadTextFile, openSharePrintWindow, shareFileName } from '@/utils/signer/download'
import { shareQrDataUrl } from '@/utils/signer/qr'

const props = defineProps<{
  shareText: string
  shardSlot: 'B' | 'C'
  title: string
  description: string
  createdAt: string
}>()

const { t } = useI18n()
const revealed = ref(false)
const qrDataUrl = ref('')
const statusKey = ref('')
const statusError = ref(false)
let cancelClear: (() => void) | null = null

const masked = computed(() => `${props.shareText.slice(0, 5)}-••••-••••-••••`)

async function reveal(): Promise<void> {
  revealed.value = true
  statusKey.value = ''
  try {
    qrDataUrl.value = await shareQrDataUrl(props.shareText)
  } catch {
    qrDataUrl.value = ''
  }
}

function hide(): void {
  revealed.value = false
  qrDataUrl.value = ''
}

async function copy(): Promise<void> {
  const handle = await copySensitiveText(props.shareText)
  if (!handle.result.ok) {
    statusError.value = true
    statusKey.value = 'admin.crypto.signer.wizard.revealCopyManual'
    return
  }
  cancelClear = handle.scheduleClear(30_000)
  statusError.value = false
  statusKey.value = 'admin.crypto.signer.wizard.revealCopied'
}

function download(): void {
  const ok = downloadTextFile(props.shareText, shareFileName(props.shardSlot))
  statusError.value = !ok
  statusKey.value = ok
    ? 'admin.crypto.signer.wizard.revealDownloaded'
    : 'admin.crypto.signer.wizard.revealDownloadFailed'
}

function print(): void {
  const html = buildShareSheetHtml({
    shareText: props.shareText,
    slotLabel: props.shardSlot,
    shareIndex: props.shardSlot === 'B' ? 2 : 3,
    createdAt: props.createdAt,
    qrDataUrl: qrDataUrl.value || undefined,
    labels: {
      title: t('admin.crypto.signer.wizard.printTitle'),
      warning: t('admin.crypto.signer.wizard.printWarning'),
      shareLabel: t('admin.crypto.signer.wizard.printShareLabel'),
      createdAtLabel: t('admin.crypto.signer.createdAt'),
      recoveryTitle: t('admin.crypto.signer.wizard.printRecoveryTitle'),
      recoverySteps: [
        t('admin.crypto.signer.wizard.printStep1'),
        t('admin.crypto.signer.wizard.printStep2'),
        t('admin.crypto.signer.wizard.printStep3'),
        t('admin.crypto.signer.wizard.printStep4')
      ],
      footer: t('admin.crypto.signer.wizard.printFooter')
    }
  })
  const ok = openSharePrintWindow(html)
  statusError.value = !ok
  statusKey.value = ok
    ? 'admin.crypto.signer.wizard.revealPrinted'
    : 'admin.crypto.signer.wizard.revealPrintFailed'
}

onBeforeUnmount(() => {
  cancelClear?.()
  hide()
})
</script>
