<template>
  <Teleport to="body">
    <div
      v-if="open"
      data-testid="docs-search-modal"
      class="docs-search-enter fixed inset-0 z-50 flex items-start justify-center bg-slate-950/45 px-4 pt-[10vh] backdrop-blur-xs dark:bg-black/65"
      role="dialog"
      aria-modal="true"
      :aria-label="t('docs.searchModalTitle')"
      @click.self="emit('close')"
    >
        <div
          class="flex max-h-[74vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-slate-200/90 bg-white shadow-2xl dark:border-dark-700 dark:bg-dark-900"
          @keydown.down.prevent="moveSelection(1)"
          @keydown.up.prevent="moveSelection(-1)"
          @keydown.enter.prevent="openSelected"
          @keydown.escape.prevent="emit('close')"
        >
          <!-- Search input bar -->
          <div class="flex items-center gap-3 border-b border-slate-200/90 px-4 py-3.5 dark:border-dark-700/90">
            <Icon name="search" size="sm" class="shrink-0 text-blue-600 dark:text-blue-400" />
            <input
              ref="inputRef"
              v-model="query"
              data-testid="docs-search-modal-input"
              type="search"
              name="docs-modal-search"
              autocomplete="off"
              autocorrect="off"
              autocapitalize="off"
              spellcheck="false"
              data-lpignore="true"
              data-1p-ignore="true"
              data-bwignore="true"
              data-form-type="other"
              :placeholder="t('docs.searchModalPlaceholder')"
              :aria-label="t('docs.searchModalPlaceholder')"
              class="min-w-0 flex-1 bg-transparent text-[15px] text-slate-900 outline-hidden placeholder:text-slate-400 dark:text-slate-100 dark:placeholder:text-slate-500"
            />
            <button
              v-if="query"
              type="button"
              class="rounded-md px-2 py-1 text-xs text-slate-400 transition hover:bg-slate-100 hover:text-slate-700 dark:hover:bg-dark-800 dark:hover:text-slate-200"
              @click="query = ''"
            >
              {{ t('docs.searchClear') }}
            </button>
            <button
              type="button"
              data-testid="docs-search-modal-close"
              class="rounded-md border border-slate-200 bg-slate-50 px-2 py-0.5 font-mono text-[11px] font-medium text-slate-500 transition hover:bg-slate-100 dark:border-dark-700 dark:bg-dark-800 dark:text-slate-400"
              @click="emit('close')"
            >
              ESC
            </button>
          </div>

          <!-- Results list -->
          <div ref="listRef" class="min-h-0 flex-1 overflow-y-auto p-2.5">
            <div v-if="!query.trim()" class="px-2 pb-1.5 pt-1 text-[11.5px] font-semibold uppercase tracking-wide text-slate-400 dark:text-slate-500">
              {{ t('docs.searchQuickLinks') }}
            </div>
            <div v-else-if="results.length" class="px-2 pb-1.5 pt-1 text-[11.5px] font-medium text-slate-400 dark:text-slate-500">
              {{ t('docs.searchMatchCount', { count: results.length }) }}
            </div>

            <ul v-if="results.length" role="listbox" class="space-y-1">
              <li v-for="(item, idx) in results" :key="item.key" role="option" :aria-selected="idx === selectedIndex">
                <button
                  type="button"
                  :data-testid="`docs-search-result-${idx}`"
                  class="flex w-full items-start gap-3 rounded-xl px-3.5 py-2.5 text-left transition-colors"
                  :class="idx === selectedIndex
                    ? 'bg-blue-50/95 text-slate-950 ring-1 ring-inset ring-blue-200/80 dark:bg-blue-500/15 dark:text-white dark:ring-blue-400/30'
                    : 'text-slate-700 hover:bg-slate-50 dark:text-slate-300 dark:hover:bg-dark-800/70'"
                  @mouseenter="selectedIndex = idx"
                  @click="selectItem(item)"
                >
                  <span
                    class="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg border text-xs"
                    :class="idx === selectedIndex
                      ? 'border-blue-200 bg-white text-blue-600 dark:border-blue-400/30 dark:bg-dark-900 dark:text-blue-400'
                      : 'border-slate-200/80 bg-slate-50 text-slate-400 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-500'"
                  >
                    <Icon :name="item.sectionId ? 'link' : 'document'" size="xs" />
                  </span>
                  <div class="min-w-0 flex-1">
                    <div class="flex flex-wrap items-center gap-1.5 text-[11.5px] text-slate-400 dark:text-slate-500">
                      <span>{{ t(`docs.category.${item.category}`) }}</span>
                      <span aria-hidden="true">›</span>
                      <span class="truncate">{{ item.pageTitle }}</span>
                      <span
                        v-if="item.endpoint"
                        class="ml-auto shrink-0 rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[10.5px] text-slate-600 dark:bg-dark-800 dark:text-slate-300"
                      >
                        {{ item.endpoint }}
                      </span>
                    </div>
                    <p class="mt-0.5 truncate text-[14px] font-semibold text-slate-900 dark:text-slate-100">
                      <template v-for="(seg, sIdx) in splitHighlightSegments(item.heading, query)" :key="sIdx">
                        <mark v-if="seg.match" class="rounded bg-blue-100 px-0.5 text-blue-900 dark:bg-blue-500/30 dark:text-blue-200">{{ seg.text }}</mark>
                        <template v-else>{{ seg.text }}</template>
                      </template>
                    </p>
                    <p v-if="item.excerpt" class="mt-0.5 line-clamp-2 text-[12.5px] leading-5 text-slate-500 dark:text-slate-400">
                      <template v-for="(seg, sIdx) in splitHighlightSegments(item.excerpt, query)" :key="sIdx">
                        <mark v-if="seg.match" class="rounded bg-blue-100 px-0.5 text-blue-900 dark:bg-blue-500/30 dark:text-blue-200">{{ seg.text }}</mark>
                        <template v-else>{{ seg.text }}</template>
                      </template>
                    </p>
                  </div>
                  <Icon
                    name="arrowRight"
                    size="xs"
                    class="mt-2 shrink-0 transition-opacity"
                    :class="idx === selectedIndex ? 'text-blue-600 opacity-100 dark:text-blue-400' : 'opacity-0'"
                  />
                </button>
              </li>
            </ul>

            <div v-else class="flex flex-col items-center justify-center py-12 text-center">
              <Icon name="search" size="lg" class="text-slate-300 dark:text-dark-600" />
              <p class="mt-3 text-sm font-medium text-slate-600 dark:text-slate-300">{{ t('docs.noResults') }}</p>
              <p class="mt-1 text-xs text-slate-400 dark:text-slate-500">{{ t('docs.searchEmptyHint') }}</p>
            </div>
          </div>

          <!-- Footer keyboard legend (VitePress / DocSearch style) -->
          <div class="flex flex-wrap items-center justify-between gap-3 border-t border-slate-200/80 bg-slate-50/75 px-4 py-2.5 text-[11.5px] text-slate-500 dark:border-dark-700/80 dark:bg-dark-950/70 dark:text-slate-400">
            <div class="flex items-center gap-3.5">
              <span class="inline-flex items-center gap-1">
                <kbd class="rounded border border-slate-200 bg-white px-1.5 py-0.5 font-mono text-[10px] dark:border-dark-700 dark:bg-dark-900">↑</kbd>
                <kbd class="rounded border border-slate-200 bg-white px-1.5 py-0.5 font-mono text-[10px] dark:border-dark-700 dark:bg-dark-900">↓</kbd>
                <span>{{ t('docs.searchKeyNavigate') }}</span>
              </span>
              <span class="inline-flex items-center gap-1">
                <kbd class="rounded border border-slate-200 bg-white px-1.5 py-0.5 font-mono text-[10px] dark:border-dark-700 dark:bg-dark-900">↵</kbd>
                <span>{{ t('docs.searchKeyOpen') }}</span>
              </span>
              <span class="inline-flex items-center gap-1">
                <kbd class="rounded border border-slate-200 bg-white px-1.5 py-0.5 font-mono text-[10px] dark:border-dark-700 dark:bg-dark-900">ESC</kbd>
                <span>{{ t('docs.searchKeyClose') }}</span>
              </span>
            </div>
          </div>
        </div>
      </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { DocsCategory, DocsPage } from '@/data/apiDocs'
import { splitHighlightSegments } from '@/utils/docsHighlight'

export interface DocsSearchHit {
  key: string
  pageId: string
  sectionId?: string
  category: DocsCategory
  pageTitle: string
  heading: string
  excerpt: string
  endpoint?: string
  score: number
}

const props = defineProps<{
  open: boolean
  pages: DocsPage[]
}>()

const emit = defineEmits<{
  close: []
  select: [payload: { pageId: string; sectionId?: string }]
}>()

const { t } = useI18n()
const inputRef = ref<HTMLInputElement | null>(null)
const listRef = ref<HTMLElement | null>(null)
const query = ref('')
const selectedIndex = ref(0)

const QUICK_LINK_IDS = ['quick-start', 'agent-setup', 'authentication', 'streaming', 'image-generation', 'key-self-service']

function buildExcerpt(haystack: string, terms: string[]): string {
  const clean = haystack.replace(/\s+/g, ' ').trim()
  if (!clean) return ''
  const lower = clean.toLowerCase()
  const idx = terms.map((term) => lower.indexOf(term)).filter((i) => i >= 0).sort((a, b) => a - b)[0] ?? -1
  if (idx < 0 || clean.length <= 130) return clean.slice(0, 140)
  const start = Math.max(0, idx - 36)
  const end = Math.min(clean.length, start + 132)
  return `${start > 0 ? '…' : ''}${clean.slice(start, end)}${end < clean.length ? '…' : ''}`
}

const results = computed<DocsSearchHit[]>(() => {
  const trimmed = query.value.trim().toLowerCase()
  if (!trimmed) {
    const picked: DocsSearchHit[] = []
    for (const id of QUICK_LINK_IDS) {
      const p = props.pages.find((entry) => entry.id === id)
      if (!p) continue
      picked.push({
        key: `quick-${p.id}`,
        pageId: p.id,
        category: p.category,
        pageTitle: p.title,
        heading: p.title,
        excerpt: p.description,
        score: 100,
      })
    }
    return picked.length ? picked : props.pages.slice(0, 6).map((p) => ({
      key: `quick-${p.id}`,
      pageId: p.id,
      category: p.category,
      pageTitle: p.title,
      heading: p.title,
      excerpt: p.description,
      score: 100,
    }))
  }

  const terms = trimmed.split(/\s+/).filter(Boolean)
  const hits: DocsSearchHit[] = []

  for (const p of props.pages) {
    const pageText = `${p.title} ${p.description} ${(p.summaryCards || []).map((c) => `${c.title} ${c.subtitle} ${c.path || ''}`).join(' ')}`.toLowerCase()
    if (terms.every((term) => pageText.includes(term))) {
      const titleLower = p.title.toLowerCase()
      const titleHit = terms.every((term) => titleLower.includes(term))
      hits.push({
        key: `page-${p.id}`,
        pageId: p.id,
        category: p.category,
        pageTitle: p.title,
        heading: p.title,
        excerpt: buildExcerpt(p.description, terms),
        score: titleHit ? 120 : 80,
      })
    }

    for (const s of p.sections) {
      const endpointText = s.endpoint ? `${s.endpoint.method} ${s.endpoint.path}` : ''
      const fieldsText = (s.fields || []).map((f) => `${f.name} ${f.type} ${f.description}`).join(' ')
      const bodyText = [
        ...(s.paragraphs || []),
        s.note || '',
        fieldsText,
        ...(s.links || []).map((l) => l.label),
      ].join(' ')
      const fullLower = `${p.title} ${s.title} ${endpointText} ${bodyText}`.toLowerCase()
      if (!terms.every((term) => fullLower.includes(term))) continue

      const sectionTitleLower = s.title.toLowerCase()
      const endpointLower = endpointText.toLowerCase()
      let score = 40
      if (terms.every((term) => endpointLower.includes(term)) && endpointText) score = 115
      else if (terms.every((term) => sectionTitleLower.includes(term))) score = 100
      else if (terms.some((term) => fieldsText.toLowerCase().includes(term))) score = 70

      hits.push({
        key: `sec-${p.id}-${s.id}`,
        pageId: p.id,
        sectionId: s.id,
        category: p.category,
        pageTitle: p.title,
        heading: s.title,
        excerpt: buildExcerpt(bodyText || p.description, terms),
        endpoint: endpointText || undefined,
        score,
      })
    }
  }

  return hits.sort((a, b) => b.score - a.score).slice(0, 14)
})

watch(query, () => {
  selectedIndex.value = 0
})

watch(
  () => props.open,
  async (isOpen) => {
    if (isOpen) {
      selectedIndex.value = 0
      await nextTick()
      inputRef.value?.focus()
      inputRef.value?.select()
    }
  },
)

function moveSelection(delta: number) {
  if (!results.value.length) return
  selectedIndex.value = (selectedIndex.value + delta + results.value.length) % results.value.length
  void nextTick(() => {
    const el = listRef.value?.querySelector(`[data-testid="docs-search-result-${selectedIndex.value}"]`)
    if (el instanceof HTMLElement && typeof el.scrollIntoView === 'function') {
      el.scrollIntoView({ block: 'nearest' })
    }
  })
}

function selectItem(item: DocsSearchHit) {
  emit('select', { pageId: item.pageId, sectionId: item.sectionId })
  emit('close')
}

function openSelected() {
  const item = results.value[selectedIndex.value]
  if (item) selectItem(item)
}
</script>

<style scoped>
.docs-search-enter {
  animation: docs-search-in 130ms ease-out both;
}
@keyframes docs-search-in {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}
@media (prefers-reduced-motion: reduce) {
  .docs-search-enter {
    animation: none;
  }
}
</style>
