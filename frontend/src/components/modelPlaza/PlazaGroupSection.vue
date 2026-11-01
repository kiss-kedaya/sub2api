<template>
  <!-- 分组头部:名称/平台/倍率徽章/专属/订阅徽章 + 描述;卡片与详情(plain)共用 -->
  <DefineHeader>
    <div class="flex flex-wrap items-center gap-2">
      <GroupBadge
        :name="group.name"
        :platform="group.platform as GroupPlatform"
        :subscription-type="(group.subscription_type || 'standard') as SubscriptionType"
        :rate-multiplier="group.rate_multiplier"
        :user-rate-multiplier="group.user_rate_multiplier ?? null"
        :peak-rate-enabled="group.peak_rate_enabled"
        :peak-start="group.peak_start"
        :peak-end="group.peak_end"
        :peak-rate-multiplier="group.peak_rate_multiplier"
        always-show-rate
      />
      <UBadge
        v-if="group.is_exclusive"
        icon="i-lucide-shield"
        :label="t('modelPlaza.badges.exclusive')"
        color="neutral"
        variant="subtle"
      />
      <UBadge
        v-if="group.subscription_type === 'subscription'"
        :label="t('modelPlaza.badges.subscription')"
        color="neutral"
        variant="subtle"
      />
      <UBadge
        v-if="group.quality_status"
        icon="i-lucide-circle-check"
        :label="t(`modelPlaza.badges.quality.${group.quality_status}`)"
        :color="group.quality_status === 'suspect' ? 'warning' : 'success'"
        variant="subtle"
      />
    </div>
    <p v-if="group.description" class="mt-2 text-[13px] text-muted">
      {{ group.description }}
    </p>
    <p v-if="peakNote" class="mt-1.5 inline-flex items-center gap-1 text-xs text-warning">
      <UIcon name="i-lucide-clock" class="size-3" />
      {{ peakNote }}
    </p>
    <p v-if="longContextNote" class="mt-1.5 flex items-center gap-1 text-xs text-muted">
      <UIcon name="i-lucide-info" class="size-3" />
      {{ longContextNote }}
    </p>
  </DefineHeader>

  <!-- 模型价格表:整行顶到边缘,左右留白由表格首列/末列的 padding 提供 -->
  <DefineTable>
    <PlazaModelPricingTable
      v-if="group.models.length > 0"
      :models="group.models"
      :platform="group.platform"
      :rate-multiplier="group.rate_multiplier"
      :user-rate-multiplier="group.user_rate_multiplier ?? null"
      :image-rate-independent="group.image_rate_independent"
      :image-rate-multiplier="group.image_rate_multiplier"
      :peak-window="peakWindow"
      :peak-rate-multiplier="group.peak_rate_multiplier"
    />
    <p v-else class="px-5 py-4 text-center text-sm text-dimmed">
      {{ t('modelPlaza.detail.noModels') }}
    </p>
  </DefineTable>

  <!-- 详情(侧滑面板)里不再套卡片 -->
  <section v-if="plain" class="min-w-0 space-y-4">
    <div><ReuseHeader /></div>
    <div class="overflow-hidden rounded-lg border border-default"><ReuseTable /></div>
  </section>

  <UCard
    v-else
    as="section"
    variant="outline"
    class="plaza-group-card min-w-0"
    :ui="{ header: 'px-4 py-4 sm:px-5', body: 'p-0 sm:p-0' }"
  >
    <template #header><ReuseHeader /></template>
    <ReuseTable />
  </UCard>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { createReusableTemplate } from '@vueuse/core'
import GroupBadge from '@/components/common/GroupBadge.vue'
import PlazaModelPricingTable from './PlazaModelPricingTable.vue'
import type { ModelPlazaGroup } from '@/api/modelPlaza'
import type { GroupPlatform, SubscriptionType } from '@/types'
import { hasPeakRate, formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { useAppStore } from '@/stores/app'

const props = defineProps<{
  group: ModelPlazaGroup
  /** Detail view (inside the plaza slideover): header and table without the card frame. */
  plain?: boolean
}>()

const [DefineHeader, ReuseHeader] = createReusableTemplate()
const [DefineTable, ReuseTable] = createReusableTemplate()

const { t } = useI18n()
const appStore = useAppStore()

/** 高峰窗口描述(含倍率与服务器时区标注);分组未启用高峰为空串。 */
const peakWindow = computed(() => {
  if (!hasPeakRate(props.group)) return ''
  return formatPeakRateWindow(
    props.group,
    serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset)
  )
})

const peakNote = computed(() => {
  if (!peakWindow.value) return ''
  return t('modelPlaza.detail.peakNote', {
    window: peakWindow.value,
    multiplier: props.group.peak_rate_multiplier
  })
})

/**
 * 分组关闭了长上下文阶梯、但组内有模型官方带阶梯时提示:实付列只展示基础档,
 * 官方阶梯仅供参考。字段缺失(旧后端)不提示。
 */
const longContextNote = computed(() => {
  if (props.group.long_context_pricing_enabled !== false) return ''
  const hasOfficialLadder = props.group.models.some(
    (m) => (m.official_pricing?.intervals?.length ?? 0) > 1
  )
  return hasOfficialLadder ? t('modelPlaza.detail.longContextDisabledNote') : ''
})
</script>
