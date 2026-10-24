<template>
  <section class="gas-wallet" aria-labelledby="gas-wallet-title" data-testid="crypto-gas-wallet">
    <div class="gas-wallet__heading">
      <div>
        <h2 id="gas-wallet-title">{{ t('admin.crypto.gasFundingTitle') }}</h2>
        <p class="gas-wallet__hint">{{ t('admin.crypto.gasFundingHint') }}</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || !available" @click="refresh">
        {{ loading ? t('common.loading') : t('common.refresh') }}
      </button>
    </div>
    <p v-if="!available" class="gas-wallet__hint" role="status">{{ t('admin.crypto.gasFundingUnavailable') }}</p>
    <p v-else-if="!networks.length" class="gas-wallet__hint" role="status">{{ t('admin.crypto.gasFundingNoNetworks') }}</p>
    <template v-else>
      <label class="gas-wallet__network">
        <span>{{ t('admin.crypto.gasFundingNetwork') }}</span>
        <select v-model="selectedChain" class="input" data-testid="crypto-gas-network">
          <option v-for="network in networks" :key="network.chain" :value="network.chain">{{ network.label }} · {{ network.symbol }}</option>
        </select>
      </label>
      <p v-if="addressError" class="gas-wallet__error" role="alert">{{ addressError }}</p>
      <div v-if="address" class="gas-wallet__content">
        <div class="gas-wallet__details">
          <p class="gas-wallet__hint">{{ t('admin.crypto.gasFundingAddress') }}</p>
          <code class="gas-wallet__address" data-testid="crypto-gas-address">{{ address }}</code>
          <button type="button" class="btn btn-secondary btn-sm" data-testid="crypto-gas-copy" @click="copyToClipboard(address)">{{ t('admin.crypto.gasFundingCopy') }}</button>
          <p class="gas-wallet__warning">{{ t('admin.crypto.gasFundingWarning', { network: selectedNetwork?.label, symbol: selectedNetwork?.symbol }) }}</p>
          <p class="gas-wallet__balance" role="status">
            {{ t('admin.crypto.gasFundingBalance') }}:
            <strong v-if="selectedBalance && !selectedBalance.error">{{ selectedBalance.balance }} {{ selectedNetwork?.symbol }}</strong>
            <span v-else>{{ t(balanceLoading ? 'common.loading' : selectedBalance?.error_code === 'RPC_DISCOVERY_PENDING' ? 'admin.crypto.rpcDiscovering' : 'admin.crypto.hotWalletUnreachable') }}</span>
          </p>
          <p v-if="selectedBalance && !selectedBalance.error && !selectedBalance.enough" class="gas-wallet__warning">{{ t('admin.crypto.gasFundingLowBalance') }}</p>
        </div>
        <div class="gas-wallet__qr">
          <img v-if="qrImage" :src="qrImage" :alt="t('admin.crypto.gasFundingQrAlt', { network: selectedNetwork?.label })" width="184" height="184" data-testid="crypto-gas-qr" />
          <p v-else-if="qrFailed" class="gas-wallet__error" role="status">{{ t('admin.crypto.gasFundingQrFailed') }}</p>
          <p v-else class="gas-wallet__hint" role="status">{{ t('common.loading') }}</p>
        </div>
      </div>
    </template>
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

<style scoped>
.gas-wallet { padding: 1.25rem; border: 1px solid var(--signal-line, #64748b40); border-radius: .875rem; }
.gas-wallet__heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 1rem; }
.gas-wallet__heading h2 { font-size: .9375rem; font-weight: 650; }
.gas-wallet__heading button { flex-shrink: 0; }
.gas-wallet__hint { margin-top: .4rem; font-size: .8125rem; line-height: 1.7; color: var(--signal-muted, #64748b); }
.gas-wallet__network { display: grid; gap: .5rem; max-width: 20rem; margin-top: 1rem; font-size: .8125rem; }
.gas-wallet__content { display: flex; align-items: center; justify-content: space-between; gap: 1.25rem; margin-top: .75rem; }
.gas-wallet__details { min-width: 0; }
.gas-wallet__address { display: block; margin: .5rem 0 .75rem; font-size: .875rem; line-height: 1.7; overflow-wrap: anywhere; user-select: all; }
.gas-wallet__warning { margin-top: .75rem; color: var(--signal-muted, #64748b); font-size: .8125rem; line-height: 1.7; }
.gas-wallet__balance { margin-top: .75rem; font-size: .8125rem; line-height: 1.7; }
.gas-wallet__qr { flex: 0 0 184px; min-height: 184px; display: grid; place-items: center; }
.gas-wallet__qr img { border-radius: .5rem; background: #fff; }
.gas-wallet__error { margin-top: .75rem; font-size: .8125rem; color: #dc2626; }
@media(max-width: 640px) { .gas-wallet__content { align-items: stretch; flex-direction: column; } .gas-wallet__qr { align-self: center; } }
</style>
