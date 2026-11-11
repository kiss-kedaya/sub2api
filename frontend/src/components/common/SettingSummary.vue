<script setup lang="ts">
import { useI18n } from 'vue-i18n'

// The one-line "what is on" bar at the top of a settings list: a count, then one pill per enabled
// setting. With nothing on it says so, so an empty bar never reads as "still loading".
defineProps<{
  /** Already-translated names of the settings that are on, in page order. */
  items: string[]
}>()

const { t } = useI18n()
</script>

<template>
  <div class="setting-summary" data-testid="setting-summary">
    <span class="setting-summary__count" data-testid="setting-summary-count">
      {{ t('common.settingsSummary.enabled', { count: items.length }) }}
    </span>
    <span v-for="(name, index) in items" :key="`${index}-${name}`" class="setting-summary__chip" :title="name">{{ name }}</span>
    <span v-if="!items.length" class="setting-summary__none">{{ t('common.settingsSummary.allDefault') }}</span>
  </div>
</template>

<style scoped>
.setting-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-width: 0;
  padding: 10px 12px;
  border-radius: 10px;
  background: color-mix(in oklab, var(--ui-bg-elevated) 55%, transparent);
  box-shadow: 0 0 0 1px var(--ui-border);
}

.setting-summary__count {
  margin-inline-end: 4px;
  font-size: 12px;
  font-weight: 500;
  color: var(--ui-text-muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.setting-summary__chip {
  max-width: 100%;
  min-width: 0;
  box-sizing: border-box;
  height: 22px;
  padding: 0 8px;
  overflow: hidden;
  border-radius: 999px;
  background: color-mix(in oklab, var(--ui-primary) 14%, transparent);
  color: var(--ui-text-highlighted);
  font-size: 12px;
  line-height: 22px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.setting-summary__none {
  font-size: 12px;
  color: var(--ui-text-dimmed);
}
</style>
