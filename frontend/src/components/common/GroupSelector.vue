<template>
  <div>
    <UFormField :label="t('admin.users.groups')" :hint="t('common.selectedCount', { count: modelValue.length })">
      <USelectMenu
        :model-value="selectedVisibleIds"
        :items="items"
        value-key="value"
        multiple
        :search-input="isSearchable ? { placeholder: t('common.searchPlaceholder'), icon: 'i-lucide-search' } : false"
        :filter-fields="['label', 'searchText']"
        :placeholder="t('common.selectedCount', { count: 0 })"
        class="w-full"
        :ui="{ base: 'min-h-9 py-1', content: 'min-w-72' }"
        data-test="group-selector"
        @update:model-value="handleSelect"
      >
        <template #default>
          <span v-if="selectedGroups.length" class="flex min-w-0 flex-wrap items-center gap-1">
            <GroupBadge
              v-for="group in selectedGroups.slice(0, maxTriggerBadges)"
              :key="group.id"
              :name="group.name"
              :platform="group.platform"
              :subscription-type="group.subscription_type || undefined"
              :rate-multiplier="group.rate_multiplier == null ? undefined : group.rate_multiplier"
            />
            <UBadge
              v-if="selectedGroups.length > maxTriggerBadges"
              color="neutral"
              variant="subtle"
              size="sm"
              :label="`+${selectedGroups.length - maxTriggerBadges}`"
            />
          </span>
          <span v-else class="truncate text-dimmed">{{ t('common.selectedCount', { count: 0 }) }}</span>
        </template>

        <template #item-label="{ item }">
          <span
            class="flex min-w-0 flex-1 items-center gap-2"
            :title="titleOf((item as GroupItem).group)"
          >
            <GroupBadge
              :name="(item as GroupItem).group.name"
              :platform="(item as GroupItem).group.platform"
              :subscription-type="(item as GroupItem).group.subscription_type || undefined"
              :rate-multiplier="(item as GroupItem).group.rate_multiplier == null ? undefined : (item as GroupItem).group.rate_multiplier"
              class="min-w-0"
            />
            <span class="ml-auto shrink-0 text-xs text-dimmed tabular-nums">{{ (item as GroupItem).group.account_count || 0 }}</span>
          </span>
        </template>

        <template #empty>
          <span class="text-sm text-muted">{{ t('common.noGroupsAvailable') }}</span>
        </template>
      </USelectMenu>
    </UFormField>
  </div>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupBadge from './GroupBadge.vue'
import type { Group, GroupPlatform } from '@/types'
import { useAuthStore } from '@/stores'

const { t } = useI18n()
const authStore = useAuthStore()

interface Props {
  modelValue: number[]
  groups: (Group & { account_count?: number })[]
  platform?: GroupPlatform // Optional platform filter
  mixedScheduling?: boolean // For antigravity accounts: allow anthropic/gemini groups
  searchable?: boolean | 'auto'
}

const props = withDefaults(defineProps<Props>(), {
  searchable: 'auto'
})
const emit = defineEmits<{
  'update:modelValue': [value: number[]]
}>()

const isSearchable = computed(() => {
  if (props.searchable === 'auto') return props.groups.length > 5
  return props.searchable
})

// Filter groups by platform if specified
const filteredGroups = computed(() => {
  let result = authStore.isSimpleMode
    ? props.groups.filter((g) => g.platform !== 'composite')
    : props.groups
  if (props.platform) {
    // antigravity 账户启用混合调度后，可选择 anthropic/gemini 分组
    if (props.platform === 'antigravity' && props.mixedScheduling) {
      result = result.filter(
        (g) => g.platform === 'antigravity' || g.platform === 'anthropic' || g.platform === 'gemini' || g.platform === 'composite'
      )
    } else if (
      props.platform === 'openai' ||
      props.platform === 'grok' ||
      props.platform === 'kimi' ||
      props.platform === 'zhipu' ||
      props.platform === 'deepseek'
    ) {
      // OpenAI-compatible custom accounts (including Responses URLs) can join
      // Gemini-labeled groups. Native Gemini OAuth accounts stay on Gemini groups.
      result = result.filter(
        (g) => g.platform === props.platform || g.platform === 'gemini' || g.platform === 'composite'
      )
    } else {
      result = result.filter((g) => g.platform === props.platform || g.platform === 'composite')
    }
  }
  return result
})

type SelectableGroup = Group & { account_count?: number }
type GroupItem = { value: number; label: string; searchText: string; group: SelectableGroup }

// The menu searches by name and description, like the old inline filter.
const items = computed<GroupItem[]>(() =>
  filteredGroups.value.map((group) => ({
    value: group.id,
    label: group.name,
    searchText: group.description || '',
    group
  }))
)

const maxTriggerBadges = 3
const selectedGroups = computed(() => {
  const byId = new Map(filteredGroups.value.map((group) => [group.id, group]))
  return props.modelValue.map((id) => byId.get(id)).filter((group): group is SelectableGroup => !!group)
})
// Ids the menu can show; selections outside the current filter stay in modelValue untouched.
const selectedVisibleIds = computed(() => selectedGroups.value.map((group) => group.id))

const titleOf = (group: SelectableGroup) =>
  group.rate_multiplier == null
    ? group.name
    : t('admin.groups.rateAndAccounts', { rate: group.rate_multiplier, count: group.account_count || 0 })

// The menu reports the full visible selection; toggle the difference so hidden ids are kept.
const handleSelect = (value: unknown) => {
  const next = new Set(Array.isArray(value) ? (value as number[]) : [])
  const current = new Set(selectedVisibleIds.value)
  for (const id of current) if (!next.has(id)) handleChange(id, false)
  for (const id of next) if (!current.has(id)) handleChange(id, true)
}

watch(
  () => [authStore.isSimpleMode, props.groups, props.modelValue] as const,
  () => {
    if (!authStore.isSimpleMode || props.groups.length === 0) return
    const visibleIDs = new Set(props.groups.filter((group) => group.platform !== 'composite').map((group) => group.id))
    const cleaned = props.modelValue.filter((id) => visibleIDs.has(id))
    if (cleaned.length !== props.modelValue.length) emit('update:modelValue', cleaned)
  },
  { immediate: true, deep: true }
)

const handleChange = (groupId: number, checked: boolean) => {
  const newValue = checked
    ? [...props.modelValue, groupId]
    : props.modelValue.filter((id) => id !== groupId)
  emit('update:modelValue', newValue)
}
</script>
