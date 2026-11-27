<template>
  <div class="docs-code my-6 overflow-hidden rounded-2xl border border-slate-200/95 bg-[#f8f9fb] shadow-2xs dark:border-dark-700/95 dark:bg-[#0e1117]">
    <!-- Code Group Header (VitePress / Rspress / Starlight style) -->
    <div class="flex min-h-11 items-center justify-between gap-2 border-b border-slate-200/90 bg-slate-100/65 px-3 dark:border-dark-700/90 dark:bg-dark-900/90">
      <div class="flex min-w-0 items-center gap-0.5 overflow-x-auto" role="tablist" :aria-label="title">
        <template v-if="languages.length > 1">
          <button
            v-for="lang in languages"
            :key="lang"
            type="button"
            role="tab"
            :aria-selected="activeLanguage === lang"
            :aria-pressed="activeLanguage === lang"
            class="relative inline-flex min-h-11 shrink-0 items-center px-3 font-mono text-[12.5px] transition-colors"
            :class="activeLanguage === lang
              ? 'font-semibold text-blue-600 dark:text-blue-400'
              : 'text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-100'"
            @click="emit('select-language', lang)"
          >{{ languageLabels[lang] }}<span
            v-if="activeLanguage === lang"
            class="absolute inset-x-2 bottom-0 h-[2px] rounded-full bg-blue-600 dark:bg-blue-400"
            aria-hidden="true"
          /></button>
        </template>
        <div v-else class="flex items-center gap-2 px-1.5 py-2">
          <span class="h-2 w-2 rounded-full bg-slate-300 dark:bg-dark-600" aria-hidden="true" />
          <span class="font-mono text-[12px] font-medium text-slate-600 dark:text-slate-300">
            {{ filename || languageLabels[activeLanguage] }}
          </span>
        </div>
      </div>

      <div class="flex shrink-0 items-center gap-1">
        <span
          v-if="synced && languages.length > 1"
          class="hidden items-center gap-1 rounded-full bg-blue-500/10 px-2 py-0.5 text-[10.5px] font-medium text-blue-600 dark:bg-blue-400/12 dark:text-blue-300 md:inline-flex"
          :title="t('docs.codeTabsSyncedHint')"
        >
          <Icon name="sync" size="xs" />
          <span>{{ t('docs.codeTabsSynced') }}</span>
        </span>

        <button
          type="button"
          class="inline-flex h-8 w-8 items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-white/90 hover:text-slate-700 focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-blue-500 dark:text-slate-500 dark:hover:bg-dark-800 dark:hover:text-slate-200"
          :class="wrapLines ? 'bg-white text-blue-600 shadow-2xs dark:bg-dark-800 dark:text-blue-400' : ''"
          :title="t('docs.toggleWrap')"
          :aria-label="t('docs.toggleWrap')"
          :aria-pressed="wrapLines"
          @click="wrapLines = !wrapLines"
        >
          <svg class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true">
            <path stroke-linecap="round" stroke-linejoin="round" d="M3 6h18M3 12h15a3 3 0 110 6h-4m0 0l2-2m-2 2l2 2M3 18h7" />
          </svg>
        </button>

        <button
          type="button"
          :data-testid="`docs-copy-${sectionId}`"
          class="inline-flex min-h-8 items-center justify-center gap-1.5 rounded-lg px-2.5 py-1 text-xs font-medium transition-colors focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-blue-500"
          :class="copied
            ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
            : 'text-slate-500 hover:bg-white hover:text-slate-900 dark:text-slate-400 dark:hover:bg-dark-800 dark:hover:text-white'"
          :title="t('docs.copy')"
          :aria-label="t('docs.copy')"
          @click="emit('copy')"
        >
          <Icon :name="copied ? 'check' : 'copy'" size="xs" />
          <span class="hidden sm:inline">{{ copied ? t('docs.copied') : t('docs.copy') }}</span>
        </button>
      </div>
    </div>

    <!-- Tokenized Syntax-Highlighted Code Area (zero v-html, optional line numbers) -->
    <div class="overflow-x-auto">
      <pre
        class="docs-code-pre m-0 p-4.5 font-mono text-[13px] leading-[1.72] text-slate-800 dark:text-slate-200"
        :class="wrapLines ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'"
      ><code><span
        v-for="(lineTokens, lineIdx) in tokenizedLines"
        :key="lineIdx"
        class="docs-code-line block"
      ><span
        v-if="showLineNumbers"
        class="mr-4 inline-block w-6 select-none text-right text-[11.5px] tabular-nums text-slate-400/75 dark:text-slate-600"
        aria-hidden="true"
      >{{ lineIdx + 1 }}</span><span
        v-for="(tok, tokIdx) in lineTokens"
        :key="tokIdx"
        :class="tokenClass(tok.kind)"
      >{{ tok.text }}</span>{{ lineIdx < tokenizedLines.length - 1 ? '\n' : '' }}</span></code></pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { DocsLanguage } from '@/data/apiDocs'
import { tokenizeDocsCode, type DocsCodeTokenKind } from '@/utils/docsHighlight'

const props = withDefaults(
  defineProps<{
    sectionId: string
    title: string
    languages: DocsLanguage[]
    activeLanguage: DocsLanguage
    code: string
    copied?: boolean
    synced?: boolean
    filename?: string
  }>(),
  {
    copied: false,
    synced: false,
    filename: '',
  },
)

const emit = defineEmits<{
  'select-language': [language: DocsLanguage]
  copy: []
}>()

const { t } = useI18n()
const wrapLines = ref(false)

const languageLabels: Record<DocsLanguage, string> = {
  curl: 'cURL',
  python: 'Python',
  node: 'Node.js',
  json: 'JSON',
  toml: 'TOML',
  shell: 'Shell',
}

const tokenizedLines = computed(() => tokenizeDocsCode(props.code, props.activeLanguage))
const showLineNumbers = computed(() => tokenizedLines.value.length >= 4)

function tokenClass(kind: DocsCodeTokenKind): string {
  switch (kind) {
    case 'comment':
      return 'italic text-slate-400 dark:text-slate-500'
    case 'keyword':
      return 'font-medium text-violet-700 dark:text-violet-400'
    case 'string':
      return 'text-emerald-700 dark:text-emerald-400'
    case 'url':
      return 'text-blue-700 dark:text-sky-400'
    case 'number':
      return 'text-amber-700 dark:text-amber-300'
    case 'key':
      return 'text-sky-700 dark:text-sky-300'
    case 'flag':
      return 'text-indigo-700 dark:text-indigo-300'
    case 'var':
      return 'text-rose-700 dark:text-rose-300'
    case 'plain':
    default:
      return ''
  }
}
</script>
