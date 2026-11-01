<template>
  <div
    class="table-pagination flex items-center justify-between border-t border-default bg-default px-4 py-3 dark:border-dark-700 dark:bg-dark-800 sm:px-6"
  >
    <div class="flex flex-1 items-center justify-between sm:hidden">
      <!-- Mobile pagination -->
      <UButton
        color="neutral"
        variant="outline"
        class="disabled:opacity-50"
        :label="t('pagination.previous')"
        :disabled="page === 1"
        @click="goToPage(page - 1)"
      />
      <span class="text-sm text-default dark:text-gray-300">
        {{ t('pagination.pageOf', { page, total: totalPages }) }}
      </span>
      <UButton
        color="neutral"
        variant="outline"
        class="ml-3 disabled:opacity-50"
        :label="t('pagination.next')"
        :disabled="page === totalPages"
        @click="goToPage(page + 1)"
      />
    </div>

    <div class="hidden sm:flex sm:flex-1 sm:items-center sm:justify-between">
      <!-- Desktop pagination info -->
      <div class="flex items-center space-x-4">
        <p class="text-sm text-muted dark:text-gray-300">
          {{ t('pagination.showing') }}
          <span class="font-medium text-highlighted dark:text-gray-300">{{ fromItem }}</span>
          {{ t('pagination.to') }}
          <span class="font-medium text-highlighted dark:text-gray-300">{{ toItem }}</span>
          {{ t('pagination.of') }}
          <span class="font-medium text-highlighted dark:text-gray-300">{{ total }}</span>
          {{ t('pagination.results') }}
        </p>

        <!-- Page size selector -->
        <div v-if="showPageSizeSelector" class="flex items-center space-x-2">
          <span class="text-sm text-muted dark:text-gray-300"
            >{{ t('pagination.perPage') }}:</span
          >
          <div class="page-size-select w-20">
            <Select
              :model-value="pageSize"
              :options="pageSizeSelectOptions"
              @update:model-value="handlePageSizeChange"
            />
          </div>
        </div>

        <div v-if="showJump" class="flex items-center space-x-2">
          <span class="text-sm text-muted dark:text-gray-300">{{ t('pagination.jumpTo') }}</span>
          <UInput
            :model-value="jumpPage"
            type="number"
            min="1"
            :max="totalPages"
            color="neutral"
            class="w-20"
            :placeholder="t('pagination.jumpPlaceholder')"
            @update:model-value="jumpPage = String($event ?? '')"
            @keyup.enter="submitJump"
          />
          <UButton
            color="neutral"
            variant="ghost"
            size="sm"
            :label="t('pagination.jumpAction')"
            data-test="pagination-jump"
            @click="submitJump"
          />
        </div>
      </div>

      <!-- Desktop pagination buttons -->
      <UPagination
        :page="page"
        :total="total"
        :items-per-page="pageSize"
        :sibling-count="1"
        show-edges
        :show-controls="false"
        aria-label="Pagination"
        @update:page="goToPage"
      >
        <template #prev>
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-chevron-left"
            square
            class="disabled:opacity-50"
            :aria-label="t('pagination.previous')"
          />
        </template>
        <template #item="{ item }">
          <UButton
            :color="pageOf(item) === page ? 'primary' : 'neutral'"
            :variant="pageOf(item) === page ? 'solid' : 'outline'"
            :label="String(pageOf(item))"
            :aria-label="t('pagination.goToPage', { page: pageOf(item) })"
            class="min-w-8 justify-center tabular-nums"
          />
        </template>
        <template #ellipsis>
          <span class="inline-flex min-w-8 items-center justify-center text-sm text-muted" aria-hidden="true">...</span>
        </template>
        <template #next>
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-chevron-right"
            square
            class="disabled:opacity-50"
            :aria-label="t('pagination.next')"
          />
        </template>
      </UPagination>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from './Select.vue'
import { getConfiguredTablePageSizeOptions, normalizeTablePageSize } from '@/utils/tablePreferences'
import { setPersistedPageSize } from '@/composables/usePersistedPageSize'

const { t } = useI18n()

interface Props {
  total: number
  page: number
  pageSize: number
  pageSizeOptions?: number[]
  showPageSizeSelector?: boolean
  showJump?: boolean
}

interface Emits {
  (e: 'update:page', page: number): void
  (e: 'update:pageSize', pageSize: number): void
}

const props = withDefaults(defineProps<Props>(), {
  pageSizeOptions: () => getConfiguredTablePageSizeOptions(),
  showPageSizeSelector: true,
  showJump: false
})

const emit = defineEmits<Emits>()

const totalPages = computed(() => Math.ceil(props.total / props.pageSize))

const fromItem = computed(() => {
  if (props.total === 0) return 0
  return (props.page - 1) * props.pageSize + 1
})

const toItem = computed(() => {
  const to = props.page * props.pageSize
  return to > props.total ? props.total : to
})

const pageSizeSelectOptions = computed(() => {
  const options = Array.from(
    new Set([
      ...getConfiguredTablePageSizeOptions(),
      normalizeTablePageSize(props.pageSize)
    ])
  ).sort((a, b) => a - b)

  return options.map((size) => ({
    value: size,
    label: String(size)
  }))
})

const jumpPage = ref('')

// UPagination only renders the item slot for page entries; the slot type also covers ellipses.
const pageOf = (item: { type: string; value?: number }) => (item.type === 'page' ? item.value ?? 0 : 0)

const goToPage = (newPage: number) => {
  if (newPage >= 1 && newPage <= totalPages.value && newPage !== props.page) {
    emit('update:page', newPage)
  }
}

const handlePageSizeChange = (value: string | number | boolean | null) => {
  if (value === null || typeof value === 'boolean') return
  const newPageSize = normalizeTablePageSize(typeof value === 'string' ? parseInt(value, 10) : value)
  setPersistedPageSize(newPageSize)
  emit('update:pageSize', newPageSize)
}

const submitJump = () => {
  const value = String(jumpPage.value).trim()
  if (!value) return
  const pageNum = Number.parseInt(value, 10)
  if (Number.isNaN(pageNum)) return
  const nextPage = Math.min(Math.max(pageNum, 1), totalPages.value)
  jumpPage.value = ''
  goToPage(nextPage)
}
</script>

<style scoped>
@reference "../../style.css";
.page-size-select :deep(.select-trigger) {
  @apply px-3 py-1.5 text-sm;
}
</style>
