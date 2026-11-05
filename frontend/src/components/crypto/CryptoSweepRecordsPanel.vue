<template>
  <section class="crypto-sweep-records space-y-3" data-testid="crypto-sweep-records">
    <p v-if="!records.length" class="py-10 text-center text-sm text-muted" role="status" data-testid="crypto-sweep-empty">
      {{ loading ? t('common.loading') : t('common.noData') }}
    </p>

    <template v-else>
      <div class="flex flex-wrap items-center justify-between gap-1.5" data-testid="crypto-sweep-filters">
        <div class="flex flex-wrap items-center gap-1.5">
          <USelect v-model="statusFilter" :items="statusItems" class="min-w-32" :aria-label="t('common.status')" data-testid="crypto-sweep-status-filter" />
          <USelect v-model="chainFilter" :items="chainItems" class="min-w-32" :aria-label="t('admin.crypto.chain')" data-testid="crypto-sweep-chain-filter" />
          <UInput
            v-model="search"
            icon="i-lucide-search"
            class="w-80 max-w-full"
            :placeholder="t('admin.crypto.sweepView.searchPlaceholder')"
            :aria-label="t('admin.crypto.sweepView.searchPlaceholder')"
            data-testid="crypto-sweep-search"
          />
        </div>
        <UButton color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :loading="loading" :aria-label="t('common.refresh')" data-testid="crypto-sweep-refresh" @click="emit('refresh')" />
      </div>

      <UTable v-if="filtered.length" :data="filtered" :columns="columns" :loading="loading" :ui="tableUi">
        <template #time-cell="{ row }">
          <span class="whitespace-normal text-muted" :title="timeTitle(row.original)" data-testid="crypto-sweep-time">{{ formatSweepTime(row.original.updated_at || row.original.created_at, localeTag) }}</span>
        </template>
        <template #owner-cell="{ row }"><CryptoOwnerCell :owner="ownerOf(row.original.from_address)" /></template>
        <template #from_address-cell="{ row }">
          <div class="flex items-center gap-0.5">
            <span class="font-mono" :title="row.original.from_address" data-testid="crypto-sweep-address">{{ shortValue(row.original.from_address) }}</span>
            <UButton color="neutral" variant="ghost" size="xs" class="p-0.5" icon="i-lucide-copy" :aria-label="t('admin.crypto.sweepView.copyAddress')" data-testid="crypto-sweep-copy" @click="copyToClipboard(row.original.from_address)" />
            <UButton
              v-if="addressLink(row.original)"
              color="neutral"
              variant="ghost"
              size="xs"
              class="p-0.5"
              icon="i-lucide-external-link"
              :to="addressLink(row.original)"
              target="_blank"
              rel="noopener noreferrer"
              :aria-label="t('admin.crypto.sweepView.openAddress')"
              data-testid="crypto-sweep-address-link"
            />
          </div>
        </template>
        <template #amount-cell="{ row }">
          <span class="whitespace-normal tabular-nums" data-testid="crypto-sweep-amount">{{ amountText(row.original) }}</span>
        </template>
        <template #status-cell="{ row }">
          <div class="flex max-w-52 flex-col items-start gap-1">
            <UBadge :color="sweepStatusColor(row.original.status)" variant="subtle" data-testid="crypto-sweep-status">{{ statusText(row.original.status) }}</UBadge>
            <span v-if="errorText(row.original)" class="line-clamp-2 text-xs break-words whitespace-normal text-muted" :title="errorText(row.original)" data-testid="crypto-sweep-error">{{ errorText(row.original) }}</span>
          </div>
        </template>
        <template #tx-cell="{ row }">
          <div class="flex flex-col items-start leading-tight">
            <ULink
              v-if="txLink(row.original)"
              class="font-mono text-primary hover:underline"
              :to="txLink(row.original)"
              target="_blank"
              rel="noopener noreferrer"
              :title="row.original.sweep_tx_hash"
              data-testid="crypto-sweep-tx"
            >{{ shortValue(row.original.sweep_tx_hash) }}</ULink>
            <span v-else class="font-mono" data-testid="crypto-sweep-tx">{{ shortValue(row.original.sweep_tx_hash) }}</span>
            <ULink
              v-if="gasLink(row.original)"
              class="text-xs text-muted hover:underline"
              :to="gasLink(row.original)"
              target="_blank"
              rel="noopener noreferrer"
              :title="row.original.gas_tx_hash"
              data-testid="crypto-sweep-gas-link"
            >{{ t('admin.crypto.sweepView.gasTx') }}</ULink>
          </div>
        </template>
      </UTable>
      <p v-else class="py-8 text-center text-sm text-muted" role="status" data-testid="crypto-sweep-no-match">
        {{ t(hasMore ? 'admin.crypto.sweepView.noMatchMore' : 'admin.crypto.sweepView.noMatch') }}
      </p>

      <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default pt-3">
        <span class="text-sm text-muted" data-testid="crypto-sweep-loaded">{{ t('admin.crypto.sweepView.loaded', { count: records.length }) }}</span>
        <UButton v-if="hasMore" color="neutral" variant="outline" size="sm" :loading="loadingMore" :disabled="loadingMore" data-testid="crypto-sweep-load-more" @click="emit('load-more')">
          {{ t('admin.crypto.sweepView.loadMore') }}
        </UButton>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import type { SweepRecord } from '@/api/admin/crypto'
import CryptoOwnerCell from '@/components/crypto/CryptoOwnerCell.vue'
import { useAddressOwners } from '@/composables/useAddressOwners'
import { useClipboard } from '@/composables/useClipboard'
import { explorerAddressURL, explorerTxURL } from '@/utils/cryptoExplorer'
import {
  SWEEP_FILTER_ALL,
  formatSweepFullTime,
  formatSweepTime,
  redactSweepError,
  shortValue,
  sweepRecordMatches,
  sweepStatusColor,
  sweepStatusLabelKey,
  sweepStatusMatches
} from '@/utils/cryptoSweepView'

// Shows the sweep history it is given. Loading stays with the page: this panel
// only filters what is already there and asks for more (`load-more`) or a fresh
// list (`refresh`).
const props = withDefaults(defineProps<{
  records: SweepRecord[]
  hasMore?: boolean
  loading?: boolean
  loadingMore?: boolean
}>(), { hasMore: false, loading: false, loadingMore: false })
const emit = defineEmits<{ (e: 'load-more'): void; (e: 'refresh'): void }>()

const { t, locale } = useI18n()
const { copyToClipboard } = useClipboard()
const localeTag = computed(() => locale?.value || undefined)

const { ownerOf } = useAddressOwners(() => props.records.map(record => record.from_address))

const statusFilter = ref<string>(SWEEP_FILTER_ALL)
const chainFilter = ref<string>(SWEEP_FILTER_ALL)
const search = ref('')

const statusItems = computed(() => [
  { label: t('admin.crypto.allStatuses'), value: SWEEP_FILTER_ALL },
  { label: t('admin.crypto.statusSwept'), value: 'swept' },
  { label: t('admin.crypto.sweepView.statusRunning'), value: 'running' },
  { label: t('admin.crypto.failed'), value: 'failed' },
  { label: t('admin.crypto.sweepView.statusQueued'), value: 'pending' }
])
const chainItems = computed(() => [
  { label: t('admin.crypto.allChains'), value: SWEEP_FILTER_ALL },
  ...[...new Set(props.records.map(record => record.chain))].sort().map(chain => ({ label: chain, value: chain }))
])

const filtered = computed(() => props.records.filter(record =>
  sweepStatusMatches(statusFilter.value, record.status)
  && (chainFilter.value === SWEEP_FILTER_ALL || record.chain === chainFilter.value)
  && sweepRecordMatches(record, ownerOf(record.from_address), search.value)
))

const columns = computed<TableColumn<SweepRecord>[]>(() => [
  { id: 'time', header: t('admin.crypto.colTime') },
  { accessorKey: 'chain', header: t('admin.crypto.chain') },
  { id: 'owner', header: t('admin.crypto.sweepView.colUser') },
  { id: 'from_address', header: t('admin.crypto.sweepView.colDepositAddress') },
  { id: 'amount', header: t('admin.crypto.amount') },
  { accessorKey: 'status', header: t('common.status') },
  { id: 'tx', header: 'TX' }
])

// Same table look as the other tables on the crypto page.
const tableUi = {
  base: 'border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'px-2 py-2 first:ps-3 last:pe-3 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'px-2 py-2.5 first:ps-3 last:pe-3 border-b border-default',
  separator: 'h-0'
}

function statusText(status: string): string {
  const key = sweepStatusLabelKey(status)
  return key ? t(key) : status
}
function amountText(record: SweepRecord): string {
  return record.token_symbol ? `${record.amount} ${record.token_symbol}` : record.amount
}
function errorText(record: SweepRecord): string {
  return redactSweepError(record.error)
}
function timeTitle(record: SweepRecord): string {
  return t('admin.crypto.sweepView.timeTitle', { id: record.id, time: formatSweepFullTime(record.created_at) })
}
function addressLink(record: SweepRecord): string {
  return explorerAddressURL(record.chain, record.from_address)
}
function txLink(record: SweepRecord): string {
  return explorerTxURL(record.chain, record.sweep_tx_hash)
}
function gasLink(record: SweepRecord): string {
  return explorerTxURL(record.chain, record.gas_tx_hash)
}
</script>
