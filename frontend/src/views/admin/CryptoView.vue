<template>
  <AppLayout>
    <div class="crypto-admin">
      <div class="crypto-admin__tabs">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          class="crypto-admin__tab"
          :class="{ 'crypto-admin__tab--active': activeTab === tab.id }"
          @click="switchTab(tab.id)"
        >
          {{ tab.label }}
        </button>
      </div>

      <!-- Config -->
      <section v-if="activeTab === 'config'" class="crypto-admin__panel">
        <div v-if="loadingConfig" class="crypto-admin__loading">{{ t('common.loading') }}</div>
        <form v-else-if="config" class="crypto-admin__form" @submit.prevent="saveConfig">
          <label class="crypto-admin__row">
            <span>{{ t('admin.crypto.enabled') }}</span>
            <input v-model="config.enabled" type="checkbox" />
          </label>

          <label class="crypto-admin__row">
            <span>{{ t('admin.crypto.chains') }}</span>
            <input
              :value="config.chains.join(',')"
              class="input"
              placeholder="bsc,polygon,plasma,solana"
              @change="(e) => (config!.chains = splitList((e.target as HTMLInputElement).value))"
            />
          </label>

          <label v-for="chain in config.chains" :key="`rpc-${chain}`" class="crypto-admin__row">
            <span>{{ t('admin.crypto.rpc') }} · {{ chain }}</span>
            <input
              :value="config.rpc[chain] ?? ''"
              class="input"
              :placeholder="t('admin.crypto.rpcPlaceholder')"
              @change="(e) => setRPC(chain, (e.target as HTMLInputElement).value)"
            />
          </label>

          <label class="crypto-admin__row">
            <span>{{ t('admin.crypto.globalThreshold') }}</span>
            <input v-model.number="config.sweep_global_threshold" type="number" step="1" class="input" />
          </label>

          <label class="crypto-admin__row">
            <span>{{ t('admin.crypto.hotWalletEvm') }}</span>
            <input v-model="config.hot_wallet_evm" class="input" placeholder="0x…" />
          </label>

          <label class="crypto-admin__row">
            <span>{{ t('admin.crypto.hotWalletSolana') }}</span>
            <input v-model="config.hot_wallet_solana" class="input" placeholder="…" />
          </label>

          <label class="crypto-admin__row">
            <span>{{ t('admin.crypto.signerSocket') }}</span>
            <input v-model="config.signer_socket" class="input" />
          </label>

          <div class="crypto-admin__actions">
            <button type="submit" class="btn btn-primary" :disabled="savingConfig">
              {{ savingConfig ? t('common.saving') : t('common.save') }}
            </button>
            <span v-if="configSaved" class="crypto-admin__saved">{{ t('common.saved') }}</span>
          </div>
        </form>
      </section>

      <!-- Addresses -->
      <section v-if="activeTab === 'addresses'" class="crypto-admin__panel">
        <div class="crypto-admin__filters">
          <input
            v-model.number="addressUserId"
            type="number"
            class="input"
            :placeholder="t('admin.crypto.userId')"
            @keyup.enter="loadAddresses"
          />
          <button class="btn btn-secondary" @click="loadAddresses">{{ t('common.search') }}</button>
        </div>
        <table v-if="addresses.length" class="crypto-admin__table">
          <thead>
            <tr>
              <th>ID</th>
              <th>{{ t('admin.crypto.family') }}</th>
              <th>{{ t('admin.crypto.address') }}</th>
              <th>{{ t('common.status') }}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in addresses" :key="row.id">
              <td>{{ row.id }}</td>
              <td>{{ row.family }}</td>
              <td class="crypto-admin__mono">{{ row.address }}</td>
              <td>{{ row.status }}</td>
              <td>
                <button
                  class="btn btn-secondary btn-sm"
                  @click="toggleAddress(row)"
                >
                  {{ row.status === 'active' ? t('admin.crypto.disable') : t('admin.crypto.enable') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else class="crypto-admin__empty">{{ t('common.noData') }}</div>
      </section>

      <!-- Sweep -->
      <section v-if="activeTab === 'sweep'" class="crypto-admin__panel">
        <div class="crypto-admin__filters">
          <button class="btn btn-secondary" @click="loadSweepPlan">{{ t('admin.crypto.loadPlan') }}</button>
          <button class="btn btn-secondary" @click="loadSweepRecords">{{ t('common.refresh') }}</button>
        </div>
        <div v-if="sweepPlan" class="crypto-admin__plan">
          <p>
            {{ t('admin.crypto.totalToSweep') }}: <strong>{{ sweepPlan.TotalUSD.toFixed(2) }} USDT</strong>
            <span v-if="sweepPlan.DryRun" class="crypto-admin__badge">dry-run</span>
          </p>
          <table v-if="sweepPlan.Entries?.length" class="crypto-admin__table">
            <thead>
              <tr>
                <th>{{ t('admin.crypto.chain') }}</th>
                <th>{{ t('admin.crypto.address') }}</th>
                <th>{{ t('admin.crypto.amount') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(e, i) in sweepPlan.Entries" :key="i">
                <td>{{ e.Chain }}</td>
                <td class="crypto-admin__mono">{{ e.Address }}</td>
                <td>{{ e.AmountDecimal.toFixed(2) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <table v-if="sweepRecords.length" class="crypto-admin__table">
          <thead>
            <tr>
              <th>{{ t('admin.crypto.chain') }}</th>
              <th>{{ t('admin.crypto.from') }}</th>
              <th>{{ t('admin.crypto.amount') }}</th>
              <th>{{ t('common.status') }}</th>
              <th>TX</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in sweepRecords" :key="row.id">
              <td>{{ row.chain }}</td>
              <td class="crypto-admin__mono">{{ short(row.from_address) }}</td>
              <td>{{ row.amount }}</td>
              <td>{{ row.status }}</td>
              <td class="crypto-admin__mono">{{ short(row.sweep_tx_hash) }}</td>
            </tr>
          </tbody>
        </table>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import {
  getCryptoConfig,
  getSweepPlan,
  listDepositAddresses,
  listSweepRecords,
  setAddressStatus,
  updateCryptoConfig,
  type CryptoConfig,
  type DepositAddress,
  type SweepPlan,
  type SweepRecord
} from '@/api/admin/crypto'

const { t } = useI18n()

const tabs = [
  { id: 'config', label: t('admin.crypto.tabConfig') },
  { id: 'addresses', label: t('admin.crypto.tabAddresses') },
  { id: 'sweep', label: t('admin.crypto.tabSweep') }
]
const activeTab = ref('config')

const config = ref<CryptoConfig | null>(null)
const loadingConfig = ref(false)
const savingConfig = ref(false)
const configSaved = ref(false)

const addressUserId = ref<number | null>(null)
const addresses = ref<DepositAddress[]>([])

const sweepPlan = ref<SweepPlan | null>(null)
const sweepRecords = ref<SweepRecord[]>([])

function splitList(value: string): string[] {
  return value
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

function setRPC(chain: string, value: string) {
  if (config.value) config.value.rpc[chain] = value.trim()
}

function short(value: string): string {
  if (!value) return '—'
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value
}

function switchTab(id: string) {
  activeTab.value = id
  if (id === 'sweep') {
    void loadSweepRecords()
  }
}

async function loadConfig() {
  loadingConfig.value = true
  try {
    config.value = await getCryptoConfig()
  } finally {
    loadingConfig.value = false
  }
}

async function saveConfig() {
  if (!config.value) return
  savingConfig.value = true
  configSaved.value = false
  try {
    await updateCryptoConfig(config.value)
    configSaved.value = true
  } finally {
    savingConfig.value = false
  }
}

async function loadAddresses() {
  if (!addressUserId.value) return
  addresses.value = await listDepositAddresses(addressUserId.value)
}

async function toggleAddress(row: DepositAddress) {
  const next = row.status === 'active' ? 'disabled' : 'active'
  await setAddressStatus(row.id, next)
  await loadAddresses()
}

async function loadSweepPlan() {
  sweepPlan.value = await getSweepPlan()
}

async function loadSweepRecords() {
  sweepRecords.value = await listSweepRecords()
}

onMounted(loadConfig)
</script>

<style scoped>
.crypto-admin {
  padding: 1rem;
}
.crypto-admin__tabs {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 1rem;
}
.crypto-admin__tab {
  padding: 0.4rem 0.9rem;
  border-radius: 0.4rem;
  border: 1px solid transparent;
  background: transparent;
  cursor: pointer;
}
.crypto-admin__tab--active {
  border-color: var(--color-primary, #6366f1);
  font-weight: 600;
}
.crypto-admin__form {
  display: grid;
  gap: 0.75rem;
  max-width: 42rem;
}
.crypto-admin__row {
  display: grid;
  grid-template-columns: 12rem 1fr;
  align-items: center;
  gap: 0.5rem;
}
.crypto-admin__filters {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 0.75rem;
}
.crypto-admin__table {
  width: 100%;
  border-collapse: collapse;
}
.crypto-admin__table th,
.crypto-admin__table td {
  padding: 0.4rem 0.6rem;
  border-bottom: 1px solid rgba(127, 127, 127, 0.2);
  text-align: left;
  font-size: 0.875rem;
}
.crypto-admin__mono {
  font-family: ui-monospace, monospace;
}
.crypto-admin__empty,
.crypto-admin__loading {
  padding: 2rem;
  text-align: center;
  opacity: 0.6;
}
.crypto-admin__actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-top: 0.5rem;
}
.crypto-admin__saved {
  color: var(--color-success, #22c55e);
}
.crypto-admin__badge {
  margin-left: 0.5rem;
  padding: 0.1rem 0.4rem;
  border-radius: 0.25rem;
  background: rgba(99, 102, 241, 0.15);
  font-size: 0.75rem;
}
</style>
