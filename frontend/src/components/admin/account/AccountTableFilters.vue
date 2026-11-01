<template>
  <div ref="rootEl" class="account-table-filters">
    <!-- In the console frame this row goes to the toolbar under the navbar (left: search and
         filters, right: actions); without the frame it renders here. -->
    <PageToolbar>
      <template #left>
      <div class="account-filter-search-row">
        <SearchInput
          :model-value="searchQuery"
          :placeholder="t('admin.accounts.searchAccounts')"
          class="account-filter-search"
          @update:model-value="$emit('update:searchQuery', $event)"
        />
        <!-- The two most used filters stay in the toolbar row, like the template's status select. -->
        <div class="account-filter-inline">
          <Select
            :model-value="filters.platform"
            :options="pOpts"
            :aria-label="t('admin.accounts.columns.platform')"
            @update:model-value="updatePlatform"
            @change="$emit('change')"
          />
        </div>
        <div class="account-filter-inline">
          <Select
            :model-value="filters.status"
            :options="sOpts"
            :aria-label="t('admin.accounts.columns.status')"
            @update:model-value="updateStatus"
            @change="$emit('change')"
          />
        </div>
        <UButton
          color="neutral"
          :variant="filtersExpanded || hiddenFilterCount > 0 ? 'soft' : 'outline'"
          icon="i-lucide-list-filter"
          :trailing-icon="filtersExpanded ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
          class="account-filter-toggle"
          :aria-expanded="filtersExpanded"
          aria-controls="account-advanced-filters"
          :aria-label="`${filtersExpanded ? t('common.collapse') : t('common.expand')} ${t('common.filter')}`"
          @click="filtersExpanded = !filtersExpanded"
        >
          <span class="hidden sm:inline">{{ t('common.filter') }}</span>
          <UBadge v-if="hiddenFilterCount" color="neutral" variant="outline" size="sm" class="tabular-nums">
            {{ hiddenFilterCount }}
          </UBadge>
        </UButton>
      </div>
      </template>
      <template v-if="$slots.actions" #right>
        <div class="account-filter-actions">
          <slot name="actions"></slot>
        </div>
      </template>
    </PageToolbar>

    <div
      v-show="filtersExpanded"
      id="account-advanced-filters"
      class="account-filter-panel"
      role="group"
      :aria-label="t('common.filter')"
    >
      <Select
        :model-value="filters.type"
        :options="tOpts"
        :aria-label="t('admin.accounts.columns.type')"
        @update:model-value="updateType"
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
      <div ref="filterTags" class="account-filter-tags">
        <UButton
          v-for="filter in activeFilters"
          :key="filter.key"
          color="neutral"
          variant="subtle"
          size="xs"
          trailing-icon="i-lucide-x"
          class="account-filter-tag"
          :data-filter-key="filter.key"
          :title="`${filter.label}: ${filter.valueLabel}`"
          :aria-label="`${t('common.remove')} ${filter.label}: ${filter.valueLabel}`"
          @click="clearFilter(filter.key)"
        >
          <span class="text-muted">{{ filter.label }}</span>
          <span class="account-filter-tag-value">{{ filter.valueLabel }}</span>
        </UButton>
      </div>
      <UButton
        color="neutral"
        variant="link"
        size="xs"
        class="account-filter-reset"
        data-test="reset-account-filters"
        :label="t('common.reset')"
        :aria-label="`${t('common.reset')} ${t('common.all')} ${t('common.filter')}`"
        @click="resetFilters"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import type { AdminGroup } from '@/types'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'

const FILTER_KEYS = ['platform', 'type', 'status', 'privacy_mode', 'group'] as const
type FilterKey = (typeof FILTER_KEYS)[number]
// Filters that live in the collapsible row (platform and status sit in the toolbar).
const PANEL_FILTER_KEYS: readonly FilterKey[] = ['type', 'privacy_mode', 'group']

const props = defineProps<{
  searchQuery: string
  filters: Record<string, any>
  groups?: AdminGroup[]
}>()

const emit = defineEmits(['update:searchQuery', 'update:filters', 'change'])
const { t } = useI18n()
const filtersExpanded = ref(false)
const rootEl = ref<HTMLElement | null>(null)
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

// The toggle counts the conditions hidden in its row; the toolbar selects show their own value.
const hiddenFilterCount = computed(() => activeFilters.value.filter(filter => PANEL_FILTER_KEYS.includes(filter.key)).length)

const updateFilter = (key: FilterKey, value: string | number | boolean | null) => {
  emit('update:filters', { ...props.filters, [key]: value })
}

const updatePlatform = (value: string | number | boolean | null) => updateFilter('platform', value)
const updateType = (value: string | number | boolean | null) => updateFilter('type', value)
const updateStatus = (value: string | number | boolean | null) => updateFilter('status', value)
const updatePrivacyMode = (value: string | number | boolean | null) => updateFilter('privacy_mode', value)
const updateGroup = (value: string | number | boolean | null) => updateFilter('group', value)

// The toggle is a UButton; focus the button element it renders. In the console frame the
// toolbar row is teleported out of this component, so look it up in the document.
const focusToggle = () =>
  (rootEl.value?.querySelector<HTMLElement>('.account-filter-toggle') ??
    document.querySelector<HTMLElement>('.account-filter-toggle'))?.focus()

const clearFilter = (key: FilterKey) => {
  const index = activeFilters.value.findIndex(filter => filter.key === key)
  updateFilter(key, '')
  emit('change')
  nextTick(() => {
    const buttons = filterTags.value?.querySelectorAll<HTMLButtonElement>('button')
    const next = buttons?.[Math.min(index, buttons.length - 1)]
    if (next) next.focus()
    else focusToggle()
  })
}

const resetFilters = () => {
  emit('update:filters', {
    ...props.filters,
    ...Object.fromEntries(FILTER_KEYS.map((key) => [key, '']))
  })
  emit('change')
  nextTick(focusToggle)
}
</script>

<style scoped>
.account-table-filters {
  min-width: 0;
}

.account-filter-toolbar {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}

.account-filter-search-row {
  display: flex;
  min-width: 0;
  flex: 1 1 520px;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.account-filter-search {
  width: 260px;
  min-width: 0;
  flex: 0 1 260px;
}

.account-filter-inline {
  width: 148px;
  flex: 0 0 auto;
}

.account-filter-actions {
  display: flex;
  min-width: 0;
  flex: 0 1 auto;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
}

.account-filter-panel {
  display: grid;
  grid-template-columns: repeat(3, minmax(140px, 200px));
  gap: 6px;
  margin-top: 8px;
}

.account-filter-panel > * {
  min-width: 0;
  width: 100%;
}

.account-filter-summary {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
}

.account-filter-tags {
  display: flex;
  min-width: 0;
  flex: 0 1 auto;
  flex-wrap: wrap;
  gap: 6px;
}

.account-filter-tag-value {
  max-width: 160px;
  overflow: hidden;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 767px) {
  .account-filter-search {
    width: 100%;
    flex-basis: 100%;
  }

  .account-filter-inline {
    width: auto;
    flex: 1 1 140px;
  }

  .account-filter-actions {
    width: 100%;
    justify-content: flex-start;
  }

  .account-filter-panel {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .account-filter-summary {
    align-items: flex-start;
    flex-wrap: wrap;
  }
}

@media (max-width: 359px) {
  .account-filter-panel {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
