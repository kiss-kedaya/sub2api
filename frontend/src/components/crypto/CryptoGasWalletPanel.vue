<template>
  <section class="gas-wallet" aria-labelledby="gas-wallet-title" data-testid="crypto-gas-wallet">
    <UPageCard :description="t('admin.crypto.gasFundingHint')" variant="naked" orientation="horizontal" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }" class="mb-4">
      <template #title><h2 id="gas-wallet-title">{{ t('admin.crypto.gasFundingTitle') }}</h2></template>
      <UButton color="neutral" variant="outline" size="sm" class="w-fit lg:ms-auto" :disabled="loading || !available" @click="refresh">
        {{ loading ? t('common.loading') : t('common.refresh') }}
      </UButton>
    </UPageCard>
    <UPageCard variant="subtle">
      <p v-if="!available" class="text-sm text-muted" role="status">{{ t('admin.crypto.gasFundingUnavailable') }}</p>
      <p v-else-if="!networks.length" class="text-sm text-muted" role="status">{{ t('admin.crypto.gasFundingNoNetworks') }}</p>
      <template v-else>
        <UFormField :label="t('admin.crypto.gasFundingNetwork')" class="flex max-sm:flex-col justify-between sm:items-center gap-4">
          <USelect v-model="selectedChain" :items="networkItems" class="w-56" data-testid="crypto-gas-network" />
        </UFormField>
        <UAlert v-if="addressError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="addressError" />
        <template v-if="address">
          <USeparator />
          <div class="flex items-center justify-between gap-5 max-sm:flex-col max-sm:items-stretch">
            <div class="min-w-0 space-y-3">
              <div>
                <p class="text-sm font-medium text-highlighted">{{ t('admin.crypto.gasFundingAddress') }}</p>
                <code class="mt-1 block select-all font-mono text-sm leading-relaxed break-all text-default" data-testid="crypto-gas-address">{{ address }}</code>
              </div>
              <UButton color="neutral" variant="outline" size="xs" icon="i-lucide-copy" data-testid="crypto-gas-copy" @click="copyToClipboard(address)">{{ t('admin.crypto.gasFundingCopy') }}</UButton>
              <p class="gas-wallet__warning text-xs leading-relaxed text-muted">{{ t('admin.crypto.gasFundingWarning', { network: selectedNetwork?.label, symbol: selectedNetwork?.symbol }) }}</p>
              <p class="text-sm" role="status">
                <span class="text-muted">{{ t('admin.crypto.gasFundingBalance') }}:</span>
                <strong v-if="selectedBalance && !selectedBalance.error" class="ms-1 text-highlighted">{{ selectedBalance.balance }} {{ selectedNetwork?.symbol }}</strong>
                <span v-else class="ms-1 text-muted">{{ t(balanceLoading ? 'common.loading' : selectedBalance?.error_code === 'RPC_DISCOVERY_PENDING' ? 'admin.crypto.rpcDiscovering' : 'admin.crypto.hotWalletUnreachable') }}</span>
              </p>
              <p v-if="selectedBalance && !selectedBalance.error && !selectedBalance.enough" class="gas-wallet__warning text-xs text-warning">{{ t('admin.crypto.gasFundingLowBalance') }}</p>
            </div>
            <div class="grid size-46 shrink-0 place-items-center self-center">
              <img v-if="qrImage" :src="qrImage" :alt="t('admin.crypto.gasFundingQrAlt', { network: selectedNetwork?.label })" width="184" height="184" class="rounded-md bg-white" data-testid="crypto-gas-qr" />
              <p v-else-if="qrFailed" class="text-sm text-error" role="status">{{ t('admin.crypto.gasFundingQrFailed') }}</p>
              <p v-else class="text-sm text-muted" role="status">{{ t('common.loading') }}</p>
            </div>
          </div>
        </template>
      </template>
    </UPageCard>
  </section>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import { getHotWalletAddress, getHotWalletStatus, type HotWalletStatus } from '@/api/admin/crypto'
import { useClipboard } from '@/composables/useClipboard'

const props = withDefaults(defineProps<{ available: boolean; chains: string[]; revision?: number }>(), { revision: 0 })
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const supportedNetworks = [
  { chain: 'bsc', label: 'BSC (BEP20)', symbol: 'BNB' },
  { chain: 'ethereum', label: 'Ethereum', symbol: 'ETH' },
  { chain: 'polygon', label: 'Polygon', symbol: 'POL' },
  { chain: 'plasma', label: 'Plasma', symbol: 'XPL' },
  { chain: 'arbitrum', label: 'Arbitrum One', symbol: 'ETH' },
  { chain: 'base', label: 'Base', symbol: 'ETH' },
  { chain: 'xlayer', label: 'X Layer', symbol: 'OKB' }
]
const networks = computed(() => supportedNetworks.filter(network => props.chains.includes(network.chain)))
const networkItems = computed(() => networks.value.map(network => ({ label: `${network.label} · ${network.symbol}`, value: network.chain })))
const selectedChain = ref('')
const selectedNetwork = computed(() => networks.value.find(network => network.chain === selectedChain.value))
const address = ref('')
const balances = ref<HotWalletStatus[]>([])
const selectedBalance = computed(() => balances.value.find(wallet => wallet.chain === selectedChain.value && wallet.address.toLowerCase() === address.value.toLowerCase()))
const loading = ref(false)
const balanceLoading = ref(false)
const addressError = ref('')
const qrImage = ref('')
const qrFailed = ref(false)
let generation = 0

function validAddress(value: unknown): value is string {
  return typeof value === 'string' && /^0x[0-9a-fA-F]{40}$/.test(value) && !/^0x0{40}$/i.test(value)
}

async function refresh() {
  const current = ++generation
  // Clearing before reload prevents funding a stale Gas seed after replacement.
  address.value = ''
  balances.value = []
  addressError.value = ''
  loading.value = false
  balanceLoading.value = false
  if (!props.available || !networks.value.length) return
  loading.value = true
  balanceLoading.value = true
  await Promise.all([
    (async () => {
      try {
        const result = await getHotWalletAddress()
        if (current !== generation) return
        if (!validAddress(result.evm)) throw new Error('Invalid Gas wallet address')
        address.value = result.evm
      } catch {
        if (current === generation) addressError.value = t('admin.crypto.gasFundingLoadFailed')
      }
    })(),
    (async () => {
      try {
        const result = await getHotWalletStatus()
        if (current === generation) balances.value = result
      } catch {
        // A slow/unavailable RPC must not hide the public funding address.
        if (current === generation) balances.value = []
      } finally {
        if (current === generation) balanceLoading.value = false
      }
    })()
  ])
  if (current === generation) loading.value = false
}

watch(address, async (value, _, onCleanup) => {
  let stale = false
  onCleanup(() => { stale = true })
  qrImage.value = ''
  qrFailed.value = false
  if (!validAddress(value)) return
  try {
    // Encode the same public address shown/copied. Never contact a QR service.
    const image = await QRCode.toDataURL(value, { type: 'image/png', width: 184, margin: 2 })
    if (!stale) qrImage.value = image
  } catch { if (!stale) qrFailed.value = true }
})
watch(() => [props.available, props.revision, props.chains.join(',')], () => {
  if (!networks.value.some(network => network.chain === selectedChain.value)) selectedChain.value = networks.value[0]?.chain ?? ''
  void refresh()
}, { immediate: true })
onUnmounted(() => { generation++ })
</script>
