<script setup lang="ts">
import { computed, provide, shallowReactive } from 'vue'
import SettingSummary from '@/components/common/SettingSummary.vue'
import { settingPanelKey, type SettingPanelEntry } from '@/components/common/settingPanel'

// A settings list with its "what is on" summary on top. Every SettingRow rendered inside (at any
// depth, through groups and v-if blocks) reports its name and on/off state here, so the page does not
// compute the summary itself.
withDefaults(defineProps<{
  /** Hide the summary bar, e.g. for a list too short to need one. */
  hideSummary?: boolean
}>(), { hideSummary: false })

const entries = shallowReactive(new Map<string, SettingPanelEntry>())

provide(settingPanelKey, {
  register: (entry) => { entries.set(entry.id, entry) },
  unregister: (id) => { entries.delete(id) },
})

// Registration order follows mount order, which is not page order once a v-if row reappears. Sort by
// where the row actually sits in the document.
const enabled = computed(() =>
  [...entries.values()]
    .filter((entry) => entry.on())
    .sort((a, b) => {
      const ea = a.el.value
      const eb = b.el.value
      if (!ea || !eb) return 0
      return ea.compareDocumentPosition(eb) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1
    })
    .map((entry) => entry.label()),
)
// A panel whose rows are all hidden by their own conditions has nothing to summarise, so the bar
// stays out of the way rather than announcing "0 enabled" over an empty list.
const hasRows = computed(() => entries.size > 0)
</script>

<template>
  <div class="setting-panel">
    <SettingSummary v-if="!hideSummary && hasRows" :items="enabled" />
    <slot />
  </div>
</template>

<style scoped>
.setting-panel {
  display: grid;
  /* One shrinkable column: an auto track would grow to the widest child (a long summary) and push the page sideways. */
  grid-template-columns: minmax(0, 1fr);
  gap: 18px;
  min-width: 0;
}
</style>
