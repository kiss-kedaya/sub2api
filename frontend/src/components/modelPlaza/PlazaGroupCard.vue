<template>
  <!-- One group in the plaza grid: badges, a short description and its models; the whole card
       links to the group's detail (prices), which opens in a slideover. -->
  <!-- `title` names the card link for screen readers; the #title slot shows the badges instead. -->
  <UPageCard
    :to="to"
    :title="group.name"
    variant="outline"
    class="h-full"
    :ui="{
      root: 'hover:ring-accented',
      container: 'flex flex-1 flex-col gap-3 p-4 sm:p-5',
      wrapper: 'flex-none',
      title: 'flex flex-wrap items-center gap-1.5 font-normal'
    }"
  >
    <template #title>
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
    </template>

    <p v-if="group.description" class="line-clamp-2 text-[13px] text-muted">{{ group.description }}</p>

    <div v-if="shownModels.length" class="flex flex-wrap gap-1.5">
      <UBadge
        v-for="m in shownModels"
        :key="`${m.platform}:${m.name}`"
        :label="m.name"
        color="neutral"
        variant="soft"
        class="font-mono font-normal"
      />
      <UBadge
        v-if="hiddenCount > 0"
        :label="t('modelPlaza.detail.moreModels', { count: hiddenCount })"
        color="neutral"
        variant="soft"
      />
    </div>

    <div class="mt-auto flex items-center justify-between gap-2 pt-1 text-xs text-muted">
      <span>{{ t('modelPlaza.detail.modelCount', { count: group.models.length }) }}</span>
      <UIcon name="i-lucide-chevron-right" class="size-4 text-dimmed" />
    </div>
  </UPageCard>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RouteLocationRaw } from 'vue-router'
import GroupBadge from '@/components/common/GroupBadge.vue'
import type { ModelPlazaGroup } from '@/api/modelPlaza'
import type { GroupPlatform, SubscriptionType } from '@/types'

const props = defineProps<{
  group: ModelPlazaGroup
  /** Where the card goes: the plaza with ?group=<id>, which opens the detail. */
  to: RouteLocationRaw
}>()

const { t } = useI18n()

/** A few model names on the card; the full list and prices are in the detail. */
const MAX_MODELS = 6
const shownModels = computed(() => props.group.models.slice(0, MAX_MODELS))
const hiddenCount = computed(() => Math.max(0, props.group.models.length - MAX_MODELS))
</script>
