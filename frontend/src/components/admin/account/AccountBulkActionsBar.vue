<template>
  <!-- Footer: selection summary and selection helpers, like the template's "n of m selected" line. -->
  <div v-if="placement === 'footer'" class="account-selection-footer">
    <span class="text-sm text-muted tabular-nums">
      {{
        allResultsSelected
          ? t('admin.accounts.bulkActions.selectedAll', { count: selectedIds.length })
          : t('admin.accounts.bulkActions.selected', { count: selectedIds.length })
      }}
    </span>
    <UButton
      v-if="selectedIds.length > 0"
      color="neutral"
      variant="link"
      size="xs"
      :label="t('admin.accounts.bulkActions.selectCurrentPage')"
      @click="$emit('select-page')"
    />
    <UButton
      v-if="!allResultsSelected && totalResults > selectedIds.length"
      color="neutral"
      variant="link"
      size="xs"
      :loading="selectingAll"
      :disabled="selectingAll"
      :label="selectingAll
        ? t('admin.accounts.bulkActions.selectingAll')
        : t('admin.accounts.bulkActions.selectAllResults', { count: totalResults })"
      @click="$emit('select-all-results')"
    />
    <UButton
      v-if="selectedIds.length > 0"
      color="neutral"
      variant="link"
      size="xs"
      :label="t('admin.accounts.bulkActions.clear')"
      @click="$emit('clear')"
    />
  </div>

  <!-- Toolbar: bulk actions with the selected count, like the template's "Delete [n]" button. -->
  <div v-else class="account-bulk-bar">
    <template v-if="selectedIds.length > 0">
      <UButton color="neutral" variant="outline" icon="i-lucide-pencil" @click="$emit('edit-selected')">
        {{ t('admin.accounts.bulkActions.edit') }}
        <template #trailing>
          <UBadge color="neutral" variant="subtle" size="sm" class="tabular-nums">{{ selectedIds.length }}</UBadge>
        </template>
      </UButton>
      <UButton
        color="error"
        variant="subtle"
        icon="i-lucide-trash-2"
        :label="t('admin.accounts.bulkActions.delete')"
        @click="$emit('delete')"
      />
      <UDropdownMenu :items="moreItems" :content="{ align: 'end' }" :ui="{ content: 'w-56' }">
        <UButton
          color="neutral"
          variant="outline"
          icon="i-lucide-ellipsis"
          square
          data-test="bulk-more"
          :title="t('common.more')"
          :aria-label="t('common.more')"
        />
      </UDropdownMenu>
    </template>
    <UButton
      v-else
      color="neutral"
      variant="outline"
      icon="i-lucide-list-checks"
      :label="t('admin.accounts.bulkEdit.submit')"
      @click="$emit('edit-filtered')"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem } from '@nuxt/ui'

withDefaults(defineProps<{
  selectedIds: number[]
  totalResults: number
  selectingAll: boolean
  allResultsSelected: boolean
  /** toolbar: bulk action buttons; footer: selected count and selection helpers. */
  placement?: 'toolbar' | 'footer'
}>(), {
  placement: 'toolbar'
})

const emit = defineEmits([
  'delete',
  'edit-selected',
  'edit-filtered',
  'clear',
  'select-page',
  'select-all-results',
  'toggle-schedulable',
  'reset-status',
  'refresh-token',
  'probe-upstream-billing',
  'test-models'
])

const { t } = useI18n()

// Less frequent bulk actions; edit and delete stay visible next to the selected count.
const moreItems = computed<DropdownMenuItem[][]>(() => [
  [
    { label: t('admin.accounts.bulkActions.testModels'), icon: 'i-lucide-flask-conical', onSelect: () => emit('test-models') },
    { label: t('admin.accounts.bulkActions.probeUpstreamBilling'), icon: 'i-lucide-radar', onSelect: () => emit('probe-upstream-billing') }
  ],
  [
    { label: t('admin.accounts.bulkActions.enableScheduling'), icon: 'i-lucide-play', onSelect: () => emit('toggle-schedulable', true) },
    { label: t('admin.accounts.bulkActions.disableScheduling'), icon: 'i-lucide-pause', onSelect: () => emit('toggle-schedulable', false) },
    { label: t('admin.accounts.bulkActions.resetStatus'), icon: 'i-lucide-rotate-ccw', onSelect: () => emit('reset-status') },
    { label: t('admin.accounts.bulkActions.refreshToken'), icon: 'i-lucide-key-round', onSelect: () => emit('refresh-token') }
  ],
  [
    { label: t('admin.accounts.bulkEdit.submit'), icon: 'i-lucide-list-checks', onSelect: () => emit('edit-filtered') }
  ]
])
</script>

<style scoped>
.account-bulk-bar,
.account-selection-footer {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.account-selection-footer {
  gap: 4px 10px;
}
</style>
