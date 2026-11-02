<template>
  <div v-if="groups && groups.length > 0" class="max-w-56">
    <!-- 分组容器：固定最大宽度，最多显示2行 -->
    <div class="flex max-h-14 flex-wrap gap-1 overflow-hidden">
      <GroupBadge
        v-for="group in displayGroups"
        :key="group.id"
        :name="group.name"
        :platform="group.platform"
        :subscription-type="group.subscription_type"
        :rate-multiplier="group.rate_multiplier"
        :show-rate="false"
        class="max-w-24"
      />
      <!-- 更多数量：点开查看完整列表 -->
      <UPopover v-if="hiddenCount > 0" :content="{ side: 'bottom', align: 'start' }">
        <UButton
          color="neutral"
          variant="subtle"
          size="xs"
          class="px-1.5 py-0.5 tabular-nums"
          :label="`+${hiddenCount}`"
          :aria-label="t('admin.accounts.groupCountTotal', { count: groups.length })"
        />
        <template #content="{ close }">
          <div class="w-max min-w-48 max-w-96 p-3">
            <div class="mb-2 flex items-center justify-between gap-2">
              <span class="text-xs font-medium text-muted">
                {{ t('admin.accounts.groupCountTotal', { count: groups.length }) }}
              </span>
              <UButton
                color="neutral"
                variant="ghost"
                size="xs"
                icon="i-lucide-x"
                square
                :aria-label="t('common.close')"
                @click="close"
              />
            </div>
            <div class="flex max-h-64 flex-wrap gap-1.5 overflow-y-auto">
              <GroupBadge
                v-for="group in groups"
                :key="group.id"
                :name="group.name"
                :platform="group.platform"
                :subscription-type="group.subscription_type"
                :rate-multiplier="group.rate_multiplier"
                :show-rate="false"
              />
            </div>
          </div>
        </template>
      </UPopover>
    </div>
  </div>
  <span v-else class="text-sm text-dimmed">-</span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupBadge from '@/components/common/GroupBadge.vue'
import type { Group } from '@/types'

interface Props {
  groups: Group[] | null | undefined
  maxDisplay?: number
}

const props = withDefaults(defineProps<Props>(), {
  maxDisplay: 4
})

const { t } = useI18n()

// 显示的分组（最多显示 maxDisplay 个）
const displayGroups = computed(() => {
  if (!props.groups) return []
  if (props.groups.length <= props.maxDisplay) {
    return props.groups
  }
  // 留一个位置给 +N 按钮
  return props.groups.slice(0, props.maxDisplay - 1)
})

// 隐藏的数量
const hiddenCount = computed(() => {
  if (!props.groups) return 0
  if (props.groups.length <= props.maxDisplay) return 0
  return props.groups.length - (props.maxDisplay - 1)
})
</script>
