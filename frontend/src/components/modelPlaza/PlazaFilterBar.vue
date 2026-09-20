<template>
  <div class="plaza-filters">
    <div class="plaza-toolbar">
      <div class="plaza-search">
        <Icon name="search" size="sm" class="plaza-search-icon" />
        <input
          :value="search"
          type="text"
          :placeholder="t('modelPlaza.filters.searchPlaceholder')"
          :aria-label="t('modelPlaza.filters.modelLabel')"
          class="input plaza-search-input"
          @input="$emit('update:search', ($event.target as HTMLInputElement).value)"
        />
        <button
          v-if="search"
          type="button"
          class="plaza-search-clear"
          :aria-label="t('common.clear')"
          @click="$emit('update:search', '')"
        >
          <Icon name="x" size="xs" class="h-3.5 w-3.5" />
        </button>
      </div>
      <Select
        :model-value="groupId"
        :options="groupSelectOptions"
        :aria-label="t('modelPlaza.filters.groupLabel')"
        class="plaza-select"
        @update:model-value="onGroupChange"
      />
      <Select
        :model-value="rate"
        :options="rateSelectOptions"
        :aria-label="t('modelPlaza.filters.rateLabel')"
        class="plaza-select plaza-select-rate"
        @update:model-value="onRateChange"
      />
    </div>

    <div class="plaza-seg-wrap">
      <div
        class="plaza-seg"
        role="radiogroup"
        :aria-label="t('modelPlaza.filters.platformLabel')"
      >
        <button
          v-for="p in platformChoices"
          :key="`platform-${p}`"
          type="button"
          role="radio"
          class="plaza-seg-item"
          :aria-checked="platform === p"
          :disabled="p !== 'all' && !platformEnabled(p)"
          @click="$emit('update:platform', p)"
        >
          <PlatformIcon v-if="p !== 'all'" :platform="p as GroupPlatform" size="xs" />
          {{ p === 'all' ? t('modelPlaza.filters.all') : p }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
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

const platformChoices = computed(() => ['all', ...props.platforms])

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

<style scoped>
.plaza-filters {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
}

.plaza-toolbar {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(168px, 220px) minmax(112px, 150px);
  gap: 8px;
  align-items: stretch;
}

.plaza-search {
  position: relative;
  min-width: 0;
}

.plaza-search-icon {
  position: absolute;
  top: 50%;
  left: 12px;
  z-index: 1;
  color: var(--signal-muted, #63636c);
  transform: translateY(-50%);
  pointer-events: none;
}

.plaza-search-input {
  width: 100%;
  min-height: 36px;
  padding: 8px 36px 8px 36px;
  border-radius: 8px;
}

.plaza-search-clear {
  position: absolute;
  top: 50%;
  right: 8px;
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--signal-muted, #63636c);
  transform: translateY(-50%);
}

.plaza-search-clear:hover {
  color: var(--signal-text, #1c1c1e);
}

.plaza-select {
  min-width: 0;
}

.plaza-seg-wrap {
  min-width: 0;
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
}

.plaza-seg {
  display: inline-flex;
  min-height: 36px;
  padding: 2px 0;
  border-bottom: 1px solid var(--signal-line, #dde2e4);
  gap: 6px;
}

.plaza-seg-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 32px;
  padding: 0 12px;
  border: 0;
  border-bottom: 2px solid transparent;
  border-radius: 0;
  background: transparent;
  color: var(--signal-muted, #63636c);
  font-size: 13px;
  line-height: 18px;
  white-space: nowrap;
}

.plaza-seg-item[aria-checked='true'] {
  border-bottom-color: var(--signal-accent, #007db5);
  color: var(--signal-text, #202426);
}

.plaza-seg-item:disabled {
  cursor: not-allowed;
  opacity: 0.4;
}

@media (max-width: 767px) {
  .plaza-toolbar {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  }

  .plaza-search {
    grid-column: 1 / -1;
  }

  .plaza-seg-item {
    min-height: 40px;
  }
}
</style>
