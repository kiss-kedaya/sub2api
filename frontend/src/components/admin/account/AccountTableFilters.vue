<template>
  <div class="account-table-filters">
    <div class="account-filter-search-row">
      <SearchInput
        :model-value="searchQuery"
        :placeholder="t('admin.accounts.searchAccounts')"
        class="account-filter-search"
        @update:model-value="$emit('update:searchQuery', $event)"
        @search="$emit('change')"
      />
      <button
        type="button"
        class="account-filter-toggle"
        :class="{ 'account-filter-toggle-active': filtersExpanded || activeFilterCount > 0 }"
        :aria-expanded="filtersExpanded"
        :title="t('common.filter')"
        @click="filtersExpanded = !filtersExpanded"
      >
        <Icon name="filter" size="sm" />
        <span>{{ t('common.filter') }}</span>
        <span v-if="activeFilterCount" class="account-filter-count">{{ activeFilterCount }}</span>
        <Icon :name="filtersExpanded ? 'chevronUp' : 'chevronDown'" size="xs" />
      </button>
    </div>

    <div
      class="account-filter-panel"
      :class="{ 'account-filter-panel-open': filtersExpanded }"
    >
      <Select :model-value="filters.platform" :options="pOpts" @update:model-value="updatePlatform" @change="$emit('change')" />
      <Select :model-value="filters.type" :options="tOpts" @update:model-value="updateType" @change="$emit('change')" />
      <Select :model-value="filters.status" :options="sOpts" @update:model-value="updateStatus" @change="$emit('change')" />
      <Select :model-value="filters.privacy_mode" :options="privacyOpts" @update:model-value="updatePrivacyMode" @change="$emit('change')" />
      <Select :model-value="filters.group" :options="gOpts" @update:model-value="updateGroup" @change="$emit('change')" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import type { AdminGroup } from '@/types'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'

const props = defineProps<{
  searchQuery: string
  filters: Record<string, any>
  groups?: AdminGroup[]
}>()

const emit = defineEmits(['update:searchQuery', 'update:filters', 'change'])
const { t } = useI18n()
const filtersExpanded = ref(false)

const activeFilterCount = computed(() =>
  ['platform', 'type', 'status', 'privacy_mode', 'group'].filter((key) => Boolean(props.filters[key])).length
)

const updatePlatform = (value: string | number | boolean | null) => {
  emit('update:filters', { ...props.filters, platform: value })
}
const updateType = (value: string | number | boolean | null) => {
  emit('update:filters', { ...props.filters, type: value })
}
const updateStatus = (value: string | number | boolean | null) => {
  emit('update:filters', { ...props.filters, status: value })
}
const updatePrivacyMode = (value: string | number | boolean | null) => {
  emit('update:filters', { ...props.filters, privacy_mode: value })
}
const updateGroup = (value: string | number | boolean | null) => {
  emit('update:filters', { ...props.filters, group: value })
}

const pOpts = computed(() => [
  { value: '', label: t('admin.accounts.allPlatforms') },
  ...CONCRETE_PLATFORM_OPTIONS
])
const tOpts = computed(() => [
  { value: '', label: t('admin.accounts.allTypes') },
  { value: 'oauth', label: t('admin.accounts.oauthType') },
  { value: 'setup-token', label: t('admin.accounts.setupToken') },
  { value: 'apikey', label: t('admin.accounts.apiKey') },
  { value: 'bedrock', label: 'AWS Bedrock' }
])
const sOpts = computed(() => [
  { value: '', label: t('admin.accounts.allStatus') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') },
  { value: 'error', label: t('admin.accounts.status.error') },
  { value: 'rate_limited', label: t('admin.accounts.status.rateLimited') },
  { value: 'temp_unschedulable', label: t('admin.accounts.status.tempUnschedulable') },
  { value: 'unschedulable', label: t('admin.accounts.status.unschedulable') }
])
const privacyOpts = computed(() => [
  { value: '', label: t('admin.accounts.allPrivacyModes') },
  { value: '__unset__', label: t('admin.accounts.privacyUnset') },
  { value: 'training_off', label: 'Privacy' },
  { value: 'training_set_cf_blocked', label: 'CF' },
  { value: 'training_set_failed', label: 'Fail' }
])
const gOpts = computed(() => [
  { value: '', label: t('admin.accounts.allGroups') },
  { value: 'ungrouped', label: t('admin.accounts.ungroupedGroup') },
  ...(props.groups || []).map((group) => ({ value: String(group.id), label: group.name }))
])
</script>

<style scoped>
.account-table-filters {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  align-items: center;
  gap: 8px;
}

.account-filter-search-row {
  display: flex;
  min-width: 220px;
  flex: 1 1 280px;
  align-items: center;
  gap: 8px;
}

.account-filter-search {
  min-width: 0;
  width: 100%;
}

.account-filter-panel {
  display: flex;
  min-width: 0;
  flex: 3 1 640px;
  align-items: center;
  gap: 8px;
}

.account-filter-panel > * {
  min-width: 128px;
  flex: 1 1 144px;
}

.account-filter-toggle {
  display: none;
  min-height: 40px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 0 10px;
  border: 1px solid var(--signal-control-line);
  border-radius: 6px;
  background: var(--signal-surface);
  color: var(--signal-muted);
  font-size: 12px;
  font-weight: 600;
  transition: color 140ms ease, background-color 140ms ease, border-color 140ms ease;
}

.account-filter-toggle:hover,
.account-filter-toggle-active {
  border-color: var(--signal-accent);
  background: var(--signal-accent-soft);
  color: var(--signal-accent);
}

.account-filter-count {
  display: inline-grid;
  min-width: 18px;
  height: 18px;
  place-items: center;
  border-radius: 6px;
  background: var(--signal-accent);
  color: var(--signal-on-accent);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}

@media (max-width: 1100px) {
  .account-table-filters {
    align-items: stretch;
    flex-direction: column;
  }

  .account-filter-search-row,
  .account-filter-panel {
    width: 100%;
    flex-basis: auto;
  }
}

@media (max-width: 767px) {
  .account-filter-search-row {
    min-width: 0;
  }

  .account-filter-toggle {
    display: inline-flex;
    min-height: 44px;
  }

  .account-filter-panel {
    display: none;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    padding-top: 2px;
  }

  .account-filter-panel-open {
    display: grid;
  }

  .account-filter-panel > * {
    width: 100%;
    min-width: 0;
  }
}

@media (max-width: 359px) {
  .account-filter-panel {
    grid-template-columns: minmax(0, 1fr);
  }

  .account-filter-toggle > span:first-of-type {
    display: none;
  }
}
</style>
