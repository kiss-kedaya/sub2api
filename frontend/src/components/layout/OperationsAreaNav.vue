<template>
  <nav v-if="area && area.id !== 'overview'" class="operations-area-nav min-w-0" :aria-label="t(area.labelKey)">
    <div class="mb-2 flex items-center gap-2 text-xs text-muted">
      <RouterLink to="/admin/ops-center" class="hover:text-highlighted">{{ t('opsCenter.title') }}</RouterLink>
      <span aria-hidden="true">/</span>
      <span>{{ t(area.labelKey) }}</span>
    </div>
    <div ref="tabs" class="flex min-w-0 gap-1 overflow-x-auto border-b border-default pb-2">
      <UButton
        v-for="item in area.items"
        :key="item.path"
        :to="item.path"
        color="neutral"
        :variant="isOperationsPageActive(item.path, route.path) ? 'soft' : 'ghost'"
        :aria-current="isOperationsPageActive(item.path, route.path) ? 'page' : undefined"
        class="shrink-0"
        :label="t(item.labelKey)"
      />
    </div>
  </nav>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { isOperationsPageActive, useOperationsNavigation } from '@/components/admin/operations/opsNavigation'

const { t } = useI18n()
const route = useRoute()
const areas = useOperationsNavigation()
const tabs = ref<HTMLElement | null>(null)
const area = computed(() => areas.value.find(candidate => candidate.items.some(item => isOperationsPageActive(item.path, route.path))))
watch([() => route.path, area], async () => {
  await nextTick()
  tabs.value?.querySelector<HTMLElement>('[aria-current="page"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
}, { immediate: true, flush: 'post' })
</script>
