<template>
  <div class="flex min-w-0 flex-col gap-3">
    <div class="grid grid-cols-2 gap-2 md:grid-cols-[minmax(0,1fr)_minmax(168px,220px)_minmax(112px,150px)]">
      <UInput
        :model-value="search"
        icon="i-lucide-search"
        :placeholder="t('modelPlaza.filters.searchPlaceholder')"
        :aria-label="t('modelPlaza.filters.modelLabel')"
        class="col-span-2 min-w-0 md:col-span-1"
        :ui="{ base: 'plaza-search-input', trailing: 'pe-1' }"
        @update:model-value="$emit('update:search', String($event ?? ''))"
      >
        <template v-if="search" #trailing>
          <UButton
            icon="i-lucide-x"
            color="neutral"
            variant="link"
            size="sm"
            :aria-label="t('common.clear')"
            @click="$emit('update:search', '')"
          />
        </template>
      </UInput>
      <Select
        :model-value="groupId"
        :options="groupSelectOptions"
        :aria-label="t('modelPlaza.filters.groupLabel')"
        class="min-w-0"
        @update:model-value="onGroupChange"
      />
      <Select
        :model-value="rate"
        :options="rateSelectOptions"
        :aria-label="t('modelPlaza.filters.rateLabel')"
        class="min-w-0"
        @update:model-value="onRateChange"
      />
    </div>

    <!-- pb-px keeps the tab list's bottom line and indicator inside the scroll box -->
    <div class="min-w-0 overflow-x-auto pb-px">
      <UTabs
        :model-value="platform"
        :items="platformItems"
        :content="false"
        variant="link"
        size="sm"
        :aria-label="t('modelPlaza.filters.platformLabel')"
        class="w-max min-w-full"
        :ui="{ trigger: 'plaza-seg-item' }"
        @update:model-value="$emit('update:platform', String($event))"
      >
        <template #leading="{ item }">
          <PlatformIcon v-if="item.value !== 'all'" :platform="item.value as GroupPlatform" size="xs" />
        </template>
      </UTabs>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import type { GroupPlatform } from '@/types'

const props = defineProps<{
  /** 数据中出现的平台(去重排序后)。 */
  platforms: string[]
  /** 全量分组(含平台与生效倍率),三个维度的置灰联动由此推导。 */
  groups: Array<{ id: number; name: string; platform: string; rate: number }>
  /** 全量生效倍率去重升序。 */
  rates: number[]
  platform: string
  groupId: number | 'all'
  rate: number | 'all'
  /** 模型名搜索词(纯前端过滤)。 */
  search: string
}>()

const emit = defineEmits<{
  'update:platform': [value: string]
  'update:groupId': [value: number | 'all']
  'update:rate': [value: number | 'all']
  'update:search': [value: string]
}>()

const { t } = useI18n()

/** 平台页签:「全部」恒可点,其余在另两维当前选择下无命中时置灰。 */
const platformItems = computed(() =>
  ['all', ...props.platforms].map((p) => ({
    value: p,
    label: p === 'all' ? t('modelPlaza.filters.all') : p,
    disabled: p !== 'all' && !platformEnabled(p)
  }))
)

const groupSelectOptions = computed<SelectOption[]>(() => [
  { value: 'all', label: t('modelPlaza.filters.groupLabel') },
  ...props.groups.map((g) => ({
    value: g.id,
    label: g.name,
    disabled: !groupEnabled(g)
  }))
])

const rateSelectOptions = computed<SelectOption[]>(() => [
  { value: 'all', label: t('modelPlaza.filters.rateLabel') },
  ...props.rates.map((r) => ({
    value: r,
    label: `${r}x`,
    disabled: !rateEnabled(r)
  }))
])

/**
 * 三个维度互为约束(faceted):某选项可点 ⟺ 在「其他两维」当前选择下仍有分组命中。
 * 「全部」永远可点,作为解除本维约束的出口;可点项组合恒有结果,无需选择修正。
 */
function platformEnabled(p: string): boolean {
  return props.groups.some(
    (g) =>
      g.platform === p &&
      (props.groupId === 'all' || g.id === props.groupId) &&
      (props.rate === 'all' || g.rate === props.rate)
  )
}

function groupEnabled(g: { platform: string; rate: number }): boolean {
  return (
    (props.platform === 'all' || g.platform === props.platform) &&
    (props.rate === 'all' || g.rate === props.rate)
  )
}

function rateEnabled(r: number): boolean {
  return props.groups.some(
    (g) =>
      g.rate === r &&
      (props.platform === 'all' || g.platform === props.platform) &&
      (props.groupId === 'all' || g.id === props.groupId)
  )
}

function onGroupChange(value: string | number | boolean | null) {
  emit('update:groupId', value === 'all' || value === null ? 'all' : Number(value))
}

function onRateChange(value: string | number | boolean | null) {
  emit('update:rate', value === 'all' || value === null ? 'all' : Number(value))
}
</script>
