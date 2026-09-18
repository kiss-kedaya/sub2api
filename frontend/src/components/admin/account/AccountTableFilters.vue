<template>
  <div class="account-table-filters">
    <div class="account-filter-toolbar">
      <div class="account-filter-search-row">
        <SearchInput
          :model-value="searchQuery"
          :placeholder="t('admin.accounts.searchAccounts')"
          class="account-filter-search"
          @update:model-value="$emit('update:searchQuery', $event)"
        />
        <button
          ref="filterToggle"
          type="button"
          class="account-filter-toggle"
          :class="{ 'account-filter-toggle-active': filtersExpanded || activeFilterCount > 0 }"
          :aria-expanded="filtersExpanded"
          aria-controls="account-advanced-filters"
          :aria-label="`${filtersExpanded ? t('common.collapse') : t('common.expand')} ${t('common.filter')}`"
          @click="filtersExpanded = !filtersExpanded"
        >
          <Icon name="filter" size="sm" />
          <span>{{ t('common.filter') }}</span>
          <span v-if="activeFilterCount" class="account-filter-count">{{ activeFilterCount }}</span>
          <Icon :name="filtersExpanded ? 'chevronUp' : 'chevronDown'" size="xs" />
        </button>
      </div>
      <slot name="actions"></slot>
    </div>

    <div
      v-show="filtersExpanded"
      id="account-advanced-filters"
      class="account-filter-panel"
      role="group"
      :aria-label="t('common.filter')"
    >
      <Select
        :model-value="filters.platform"
        :options="pOpts"
        :aria-label="t('admin.accounts.columns.platform')"
        @update:model-value="updatePlatform"
        @change="$emit('change')"
      />
      <Select
        :model-value="filters.type"
        :options="tOpts"
        :aria-label="t('admin.accounts.columns.type')"
        @update:model-value="updateType"
        @change="$emit('change')"
      />
      <Select
        :model-value="filters.status"
        :options="sOpts"
        :aria-label="t('admin.accounts.columns.status')"
        @update:model-value="updateStatus"
        @change="$emit('change')"
      />
      <Select
        :model-value="filters.privacy_mode"
        :options="privacyOpts"
        :aria-label="t('admin.accounts.setPrivacy')"
        @update:model-value="updatePrivacyMode"
        @change="$emit('change')"
      />
      <Select
        :model-value="filters.group"
        :options="gOpts"
        :aria-label="t('admin.accounts.columns.groups')"
        @update:model-value="updateGroup"
        @change="$emit('change')"
      />
    </div>

    <div v-if="activeFilters.length" class="account-filter-summary" aria-live="polite">
      <span class="account-filter-summary-label">{{ t('common.filter') }}</span>
      <div ref="filterTags" class="account-filter-tags">
        <button
          v-for="filter in activeFilters"
          :key="filter.key"
          type="button"
          class="account-filter-tag"
          :data-filter-key="filter.key"
          :title="`${filter.label}: ${filter.valueLabel}`"
          :aria-label="`${t('common.remove')} ${filter.label}: ${filter.valueLabel}`"
          @click="clearFilter(filter.key)"
        >
          <span class="account-filter-tag-label">{{ filter.label }}</span>
          <span class="account-filter-tag-value">{{ filter.valueLabel }}</span>
          <Icon name="x" size="xs" aria-hidden="true" />
        </button>
      </div>
      <button
        type="button"
        class="account-filter-reset"
        data-test="reset-account-filters"
        :aria-label="`${t('common.reset')} ${t('common.all')} ${t('common.filter')}`"
        @click="resetFilters"
      >
        {{ t('common.reset') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import type { AdminGroup } from '@/types'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'

const FILTER_KEYS = ['platform', 'type', 'status', 'privacy_mode', 'group'] as const
type FilterKey = (typeof FILTER_KEYS)[number]

const props = defineProps<{
  searchQuery: string
  filters: Record<string, any>
  groups?: AdminGroup[]
}>()

const emit = defineEmits(['update:searchQuery', 'update:filters', 'change'])
const { t } = useI18n()
const filtersExpanded = ref(false)
const filterToggle = ref<HTMLButtonElement | null>(null)
const filterTags = ref<HTMLElement | null>(null)

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

const filterDefinitions = computed(() => ({
  platform: { label: t('admin.accounts.columns.platform'), options: pOpts.value },
  type: { label: t('admin.accounts.columns.type'), options: tOpts.value },
  status: { label: t('admin.accounts.columns.status'), options: sOpts.value },
  privacy_mode: { label: t('admin.accounts.setPrivacy'), options: privacyOpts.value },
  group: { label: t('admin.accounts.columns.groups'), options: gOpts.value }
}))

const activeFilters = computed(() => FILTER_KEYS.flatMap((key) => {
  const value = props.filters[key]
  if (value === '' || value === null || value === undefined) return []
  const definition = filterDefinitions.value[key]
  const option = definition.options.find((item) => item.value === value)
  return [{ key, label: definition.label, valueLabel: option?.label ?? String(value) }]
}))

const activeFilterCount = computed(() => activeFilters.value.length)

const updateFilter = (key: FilterKey, value: string | number | boolean | null) => {
  emit('update:filters', { ...props.filters, [key]: value })
}

const updatePlatform = (value: string | number | boolean | null) => updateFilter('platform', value)
const updateType = (value: string | number | boolean | null) => updateFilter('type', value)
const updateStatus = (value: string | number | boolean | null) => updateFilter('status', value)
const updatePrivacyMode = (value: string | number | boolean | null) => updateFilter('privacy_mode', value)
const updateGroup = (value: string | number | boolean | null) => updateFilter('group', value)

const clearFilter = (key: FilterKey) => {
  const index = activeFilters.value.findIndex(filter => filter.key === key)
  updateFilter(key, '')
  emit('change')
  nextTick(() => {
    const buttons = filterTags.value?.querySelectorAll<HTMLButtonElement>('button')
    const next = buttons?.[Math.min(index, buttons.length - 1)]
    const target = next ?? filterToggle.value
    target?.focus()
  })
}

const resetFilters = () => {
  emit('update:filters', {
    ...props.filters,
    ...Object.fromEntries(FILTER_KEYS.map((key) => [key, '']))
  })
  emit('change')
  nextTick(() => filterToggle.value?.focus())
}
</script>

<style scoped>
.account-table-filters {
  min-width: 0;
}

.account-filter-toolbar {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}

.account-filter-search-row {
  display: flex;
  min-width: 240px;
  flex: 1 1 360px;
  align-items: center;
  gap: 8px;
}

.account-filter-search {
  min-width: 0;
  width: 100%;
}

.account-filter-toggle {
  display: inline-flex;
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

.account-filter-toggle:focus-visible,
.account-filter-tag:focus-visible,
.account-filter-reset:focus-visible {
  outline: 2px solid var(--signal-accent);
  outline-offset: 2px;
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

.account-filter-panel {
  display: grid;
  grid-template-columns: repeat(5, minmax(120px, 1fr));
  gap: 8px;
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--signal-line);
}

.account-filter-panel > * {
  min-width: 0;
  width: 100%;
}

.account-filter-summary {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 7px;
  margin-top: 8px;
}

.account-filter-summary-label {
  flex: 0 0 auto;
  color: var(--signal-muted);
  font-size: 11px;
  font-weight: 600;
}

.account-filter-tags {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  flex-wrap: wrap;
  gap: 6px;
}

.account-filter-tag {
  display: inline-flex;
  min-height: 30px;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 5px;
  padding: 4px 7px;
  border: 1px solid var(--signal-line);
  border-radius: 6px;
  background: var(--signal-surface);
  color: var(--signal-text);
  font-size: 11px;
  transition: color 140ms ease, border-color 140ms ease, background-color 140ms ease;
}

.account-filter-tag:hover {
  border-color: var(--signal-accent);
  background: var(--signal-accent-soft);
  color: var(--signal-accent);
}

.account-filter-tag-label {
  color: var(--signal-muted);
}

.account-filter-tag-value {
  max-width: 160px;
  overflow: hidden;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.account-filter-reset {
  min-height: 30px;
  flex: 0 0 auto;
  padding-inline: 6px;
  color: var(--signal-accent);
  font-size: 11px;
  font-weight: 600;
}

.account-filter-reset:hover {
  text-decoration: underline;
}

@media (max-width: 1100px) {
  .account-filter-toolbar {
    align-items: stretch;
    flex-wrap: wrap;
  }

  .account-filter-search-row {
    flex-basis: 100%;
  }

  .account-filter-panel {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 767px) {
  .account-filter-search-row {
    min-width: 0;
  }

  .account-filter-toggle {
    min-height: 44px;
  }

  .account-filter-panel {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .account-filter-summary {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .account-filter-summary-label {
    width: 100%;
  }

  .account-filter-tag,
  .account-filter-reset {
    min-height: 40px;
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

@media (prefers-reduced-motion: reduce) {
  .account-filter-toggle,
  .account-filter-tag {
    transition: none;
  }
}
</style>
