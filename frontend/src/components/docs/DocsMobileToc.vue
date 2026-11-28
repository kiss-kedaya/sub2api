<template>
  <div
    v-if="sections.length > 1"
    data-testid="docs-mobile-toc"
    class="sticky top-[105px] z-20 border-b border-slate-200/90 bg-white/92 backdrop-blur-md dark:border-dark-700/90 dark:bg-dark-950/92 xl:hidden"
  >
    <div class="mx-auto max-w-[1720px] px-5 sm:px-10">
      <button
        type="button"
        data-testid="docs-mobile-toc-toggle"
        :aria-expanded="expanded"
        aria-controls="docs-mobile-toc-list"
        class="flex min-h-11 w-full items-center justify-between gap-3 py-2 text-left text-[13px]"
        @click="expanded = !expanded"
      >
        <span class="flex min-w-0 items-center gap-2">
          <span class="shrink-0 font-semibold text-slate-700 dark:text-slate-200">{{ t('docs.onThisPage') }}</span>
          <span v-if="activeTitle" class="text-slate-300 dark:text-dark-600" aria-hidden="true">/</span>
          <span v-if="activeTitle" class="truncate text-blue-600 dark:text-blue-400">{{ activeTitle }}</span>
        </span>
        <Icon
          name="chevronDown"
          size="xs"
          class="shrink-0 text-slate-400 transition-transform duration-150"
          :class="{ 'rotate-180': expanded }"
        />
      </button>

      <nav
        v-show="expanded"
        id="docs-mobile-toc-list"
        :aria-label="t('docs.mobileOnThisPage')"
        class="max-h-[52vh] overflow-y-auto border-t border-slate-100 pb-3 pt-2 dark:border-dark-800"
      >
        <ul class="space-y-0.5">
          <li v-for="item in sections" :key="item.id">
            <a
              :href="`#${item.id}`"
              class="block rounded-lg px-3 py-2 text-[13.5px] transition-colors"
              :class="activeId === item.id
                ? 'bg-blue-50/90 font-medium text-blue-600 dark:bg-blue-500/12 dark:text-blue-400'
                : 'text-slate-600 hover:bg-slate-50 hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-900 dark:hover:text-white'"
              :aria-current="activeId === item.id ? 'location' : undefined"
              @click.prevent="onSelect(item.id)"
            >
              {{ item.title }}
            </a>
          </li>
        </ul>
      </nav>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

export interface MobileTocSection {
  id: string
  title: string
}

const props = defineProps<{
  sections: MobileTocSection[]
  activeId: string
}>()

const emit = defineEmits<{
  select: [id: string]
}>()

const { t } = useI18n()
const expanded = ref(false)

const activeTitle = computed(() => {
  const hit = props.sections.find((s) => s.id === props.activeId)
  return hit?.title ?? props.sections[0]?.title ?? ''
})

function onSelect(id: string) {
  expanded.value = false
  emit('select', id)
}

watch(
  () => props.sections,
  () => {
    expanded.value = false
  },
)
</script>
