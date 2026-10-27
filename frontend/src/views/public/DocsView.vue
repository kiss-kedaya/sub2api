<template>
  <div class="api-docs min-h-screen bg-white text-slate-900 dark:bg-dark-950 dark:text-slate-100">
    <a href="#docs-content" class="sr-only z-[60] rounded-md bg-blue-600 px-4 py-3 text-sm text-white focus:not-sr-only focus:fixed focus:left-4 focus:top-4">{{ t('docs.skipToContent') }}</a>
    <header class="docs-header sticky top-0 z-40 border-b border-slate-200 bg-white/95 backdrop-blur dark:border-dark-700 dark:bg-dark-950/95">
      <div class="flex h-14 items-center justify-between gap-4 px-5 sm:px-7">
        <router-link to="/home" class="flex min-w-0 items-center gap-2.5 font-semibold tracking-tight">
          <img :src="siteLogo || '/logo.svg'" alt="" class="h-7 w-7 shrink-0 object-contain" />
          <span class="truncate">{{ siteName }}</span>
        </router-link>
        <nav class="flex shrink-0 items-center gap-4 text-sm" :aria-label="t('docs.title')">
          <router-link :to="consolePath" class="hidden text-slate-500 hover:text-slate-900 dark:hover:text-white sm:block">{{ t('docs.api') }}</router-link>
          <router-link v-if="showModels" to="/model-plaza" class="hidden text-slate-500 hover:text-slate-900 dark:hover:text-white sm:block">{{ t('docs.models') }}</router-link>
          <span class="hidden font-medium sm:block" aria-current="page">{{ t('nav.docs') }}</span>
          <LocaleSwitcher />
          <button type="button" class="rounded-md p-2 text-slate-500 hover:bg-slate-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-500 dark:hover:bg-dark-800" :aria-label="t('docs.theme')" @click="toggleTheme">
            <Icon :name="isDark ? 'sun' : 'moon'" size="sm" />
          </button>
        </nav>
      </div>
      <div class="flex h-12 items-center gap-5 overflow-x-auto border-t border-slate-100 px-5 text-sm dark:border-dark-800 sm:px-7">
        <button type="button" class="inline-flex shrink-0 items-center gap-2 font-semibold lg:pointer-events-none" :aria-expanded="mobileOpen" aria-controls="docs-sidebar" :aria-label="mobileOpen ? t('docs.closeNavigation') : t('docs.openNavigation')" @click="mobileOpen = !mobileOpen">
          <Icon name="menu" size="sm" class="lg:hidden" />
          <Icon name="book" size="sm" class="hidden text-blue-600 lg:block" />
          <span>{{ t('docs.title') }}</span>
        </button>
        <router-link v-for="category in categories" :key="category" :to="docLocation(firstInCategory(category))" class="docs-category flex h-full shrink-0 items-center border-b-2 px-0.5" :class="page.category === category ? 'border-blue-600 font-medium text-blue-600 dark:text-blue-400' : 'border-transparent text-slate-500 hover:text-slate-900 dark:hover:text-white'" :aria-current="page.category === category ? 'page' : undefined">
          {{ t(`docs.category.${category}`) }}
        </router-link>
      </div>
    </header>

    <button v-if="mobileOpen" class="fixed inset-0 z-40 bg-slate-900/30 lg:hidden" :aria-label="t('docs.closeNavigation')" @click="mobileOpen = false"></button>
    <aside id="docs-sidebar" class="docs-sidebar fixed bottom-0 left-0 top-[105px] z-50 flex w-[260px] flex-col border-r border-slate-200 bg-white px-4 py-5 transition-transform dark:border-dark-700 dark:bg-dark-950 lg:z-30 lg:translate-x-0" :class="mobileOpen ? 'translate-x-0' : '-translate-x-full'" :inert="!mobileOpen && !wideLayout" :aria-label="t('docs.navigation')">
      <label class="relative block">
        <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-2.5 text-slate-400" />
        <input v-model="search" data-testid="docs-search" type="search" class="w-full rounded-xl border border-slate-200 bg-white py-2 pl-9 pr-3 text-sm outline-none transition focus:border-blue-500 focus:ring-2 focus:ring-blue-100 dark:border-dark-700 dark:bg-dark-900 dark:focus:ring-blue-900/30" :placeholder="t('docs.search')" :aria-label="t('docs.search')" />
      </label>
      <nav class="mt-6 flex-1 space-y-7 overflow-y-auto pr-1">
        <div v-for="group in navigationGroups" :key="group.category">
          <h2 class="mb-2 px-2 text-xs font-semibold tracking-wide text-slate-900 dark:text-slate-100">{{ t(`docs.category.${group.category}`) }}</h2>
          <router-link v-for="entry in group.pages" :key="entry.id" :to="docLocation(entry.id)" class="mb-0.5 flex min-h-9 items-center rounded-lg px-2.5 py-2 text-[13px] leading-5" :class="entry.id === page.id ? 'bg-blue-50 font-medium text-blue-600 dark:bg-blue-500/10 dark:text-blue-400' : 'text-slate-500 hover:bg-slate-50 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-dark-900 dark:hover:text-white'" :aria-current="entry.id === page.id ? 'page' : undefined" @click="mobileOpen = false">
            {{ entry.title }}
          </router-link>
        </div>
        <p v-if="!navigationGroups.length" class="px-2 text-sm text-slate-500">{{ t('docs.noResults') }}</p>
      </nav>
      <div class="mt-5 flex items-center gap-2.5 border-t border-slate-100 pt-4 text-[11px] leading-5 text-slate-500 dark:border-dark-800">
        <img :src="siteLogo || '/logo.svg'" alt="" class="h-8 w-8 object-contain" />
        <span>{{ t('docs.sidebarNote') }}</span>
      </div>
    </aside>

    <div class="lg:pl-[260px]">
      <main id="docs-content" class="mx-auto grid max-w-[1330px] grid-cols-1 gap-12 px-6 py-9 sm:px-12 sm:py-11 xl:grid-cols-[minmax(0,780px)_170px] xl:gap-14 xl:px-12">
        <article :key="page.id" class="min-w-0 max-w-[780px]" data-testid="docs-article">
          <div class="mb-8">
            <p class="mb-3 text-xs font-medium tracking-wide text-blue-600 dark:text-blue-400">{{ t(`docs.category.${page.category}`) }}</p>
            <h1 class="text-[30px] font-semibold leading-tight tracking-tight sm:text-[34px]">{{ page.title }}</h1>
            <p class="mt-5 text-[15px] leading-8 text-slate-600 dark:text-slate-300">{{ page.description }}</p>
          </div>

          <section v-for="section in page.sections" :id="section.id" :key="section.id" class="docs-section mb-10 scroll-mt-36">
            <h2 class="mb-4 text-xl font-semibold tracking-tight">
              <a :href="`#${section.id}`" class="group" @click.prevent="jumpTo(section.id)">{{ section.title }}<span class="ml-2 text-blue-400 opacity-0 transition group-hover:opacity-100" aria-hidden="true">#</span></a>
            </h2>
            <p v-for="paragraph in section.paragraphs" :key="paragraph" class="my-3 text-sm leading-8 text-slate-600 dark:text-slate-300">{{ paragraph }}</p>
            <div v-if="section.note" class="my-5 flex gap-3 rounded-xl border border-blue-200 bg-blue-50/70 px-4 py-3 text-sm leading-7 text-blue-700 dark:border-blue-800 dark:bg-blue-500/10 dark:text-blue-300">
              <Icon name="infoCircle" size="sm" class="mt-1 shrink-0" />
              <p>{{ section.note }}</p>
            </div>

            <div v-if="section.fields" class="my-5 overflow-x-auto rounded-xl border border-slate-200 dark:border-dark-700">
              <table class="w-full border-collapse text-left text-[13px]">
                <thead class="bg-slate-50 text-xs text-slate-500 dark:bg-dark-900"><tr><th class="w-1/4 px-4 py-3 font-medium">{{ t('docs.field') }}</th><th class="px-4 py-3 font-medium">{{ t('docs.description') }}</th></tr></thead>
                <tbody>
                  <tr v-for="field in section.fields" :key="field.name" class="border-t border-slate-200 align-top dark:border-dark-700">
                    <td class="px-4 py-4"><code class="rounded bg-slate-100 px-1.5 py-1 font-mono text-[12px] dark:bg-dark-800">{{ field.name }}</code><div class="mt-2 text-[11px] text-slate-400">{{ field.type }}<span v-if="field.required" class="ml-1.5 text-rose-500">{{ t('docs.required') }}</span></div></td>
                    <td class="break-words px-4 py-4 leading-7 text-slate-600 dark:text-slate-300">{{ field.description }}</td>
                  </tr>
                </tbody>
              </table>
            </div>

            <div v-if="section.examples" class="docs-code my-5 overflow-hidden rounded-xl border border-slate-200 bg-[#f8f9fb] dark:border-dark-700 dark:bg-dark-900">
              <div class="flex min-h-11 items-center justify-between gap-3 border-b border-slate-200 px-3 dark:border-dark-700">
                <div class="flex min-w-0 gap-1 overflow-x-auto" role="group" :aria-label="section.title">
                  <button v-for="language in languages(section)" :key="language" type="button" class="rounded-md px-2.5 py-1.5 font-mono text-[11px] transition" :class="selectedLanguage(section) === language ? 'bg-white font-medium text-blue-600 shadow-sm dark:bg-dark-800 dark:text-blue-400' : 'text-slate-500 hover:text-slate-900 dark:hover:text-white'" :aria-pressed="selectedLanguage(section) === language" @click="selectedLanguages[section.id] = language">{{ languageLabels[language] }}</button>
                </div>
                <button type="button" :data-testid="`docs-copy-${section.id}`" class="inline-flex shrink-0 items-center gap-1.5 rounded-md px-2 py-1.5 text-xs text-slate-500 hover:bg-white hover:text-slate-900 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-500 dark:hover:bg-dark-800 dark:hover:text-white" :title="t('docs.copy')" :aria-label="t('docs.copy')" @click="copyExample(section)">
                  <Icon :name="copiedSection === section.id ? 'check' : 'copy'" size="sm" />
                  <span class="hidden sm:inline">{{ copiedSection === section.id ? t('docs.copied') : t('docs.copy') }}</span>
                </button>
              </div>
              <pre class="overflow-x-auto p-5 text-[12px] leading-7 text-slate-700 dark:text-slate-200"><code>{{ example(section) }}</code></pre>
            </div>

            <a v-for="link in section.links" :key="link.href" :href="link.href" target="_blank" rel="noopener noreferrer" class="mt-3 inline-flex items-center gap-1.5 text-sm text-blue-600 underline decoration-blue-200 underline-offset-4 hover:decoration-blue-600 dark:text-blue-400">{{ link.label }}<Icon name="externalLink" size="xs" /></a>
          </section>

          <nav class="mt-12 grid grid-cols-2 gap-4 border-t border-slate-200 pt-6 dark:border-dark-700" :aria-label="t('docs.navigation')">
            <router-link v-if="previousPage" :to="docLocation(previousPage.id)" class="rounded-xl border border-slate-200 p-4 transition hover:border-blue-300 hover:bg-blue-50/30 dark:border-dark-700 dark:hover:bg-blue-500/5"><span class="mb-1 block text-[11px] text-slate-400">{{ t('docs.previous') }}</span><span class="text-sm font-medium">{{ previousPage.title }}</span></router-link><span v-else></span>
            <router-link v-if="nextPage" :to="docLocation(nextPage.id)" class="rounded-xl border border-slate-200 p-4 text-right transition hover:border-blue-300 hover:bg-blue-50/30 dark:border-dark-700 dark:hover:bg-blue-500/5"><span class="mb-1 block text-[11px] text-slate-400">{{ t('docs.next') }}</span><span class="text-sm font-medium">{{ nextPage.title }}</span></router-link>
          </nav>
          <footer class="mt-10 flex flex-wrap items-center justify-between gap-3 pb-4 text-[11px] text-slate-400"><span>{{ siteName }} · {{ t('docs.title') }}</span><span class="font-mono">{{ origin }}</span></footer>
        </article>

        <aside class="hidden xl:block">
          <nav class="sticky top-36 text-xs" :aria-label="t('docs.onThisPage')">
            <p class="mb-4 font-medium text-slate-500">{{ t('docs.onThisPage') }}</p>
            <div class="space-y-3 border-l border-slate-200 py-1 pl-4 dark:border-dark-700"><a v-for="section in page.sections" :key="section.id" :href="`#${section.id}`" class="block leading-5 text-slate-500 hover:text-blue-600 dark:text-slate-400 dark:hover:text-blue-400" @click.prevent="jumpTo(section.id)">{{ section.title }}</a></div>
          </nav>
        </aside>
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useClipboard } from '@/composables/useClipboard'
import { sanitizeUrl } from '@/utils/url'
import { getApiDocs, resolveDocsOrigin, resolveDocsPage, type DocsCategory, type DocsLanguage, type DocsSection } from '@/data/apiDocs'

const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const authStore = useAuthStore()
const { t, locale } = useI18n()
const { copyToClipboard } = useClipboard()
const origin = resolveDocsOrigin(window.location.origin)
const pages = computed(() => getApiDocs(locale.value, origin))
const page = computed(() => resolveDocsPage(pages.value, route.query.page))
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const showModels = computed(() => appStore.cachedPublicSettings?.model_plaza_enabled === true)
const consolePath = computed(() => !authStore.isAuthenticated ? '/login' : authStore.isAdminObserver ? '/admin/usage' : authStore.isAdmin ? '/admin/dashboard' : '/dashboard')
const categories: DocsCategory[] = ['start', 'guides', 'integrations', 'reference']
const search = ref('')
const mobileOpen = ref(false)
const wideQuery = window.matchMedia('(min-width: 1024px)')
const wideLayout = ref(wideQuery.matches)
const isDark = ref(document.documentElement.classList.contains('dark'))
const selectedLanguages = reactive<Record<string, DocsLanguage>>({})
const copiedSection = ref('')
let copiedTimer: ReturnType<typeof setTimeout> | undefined
const languageLabels: Record<DocsLanguage, string> = { curl: 'cURL', python: 'Python', node: 'Node.js', json: 'JSON', toml: 'TOML', shell: 'Shell' }

const navigationGroups = computed(() => categories.map(category => ({
  category,
  pages: pages.value.filter(entry => entry.category === category && (!search.value.trim() || [entry.title, entry.description, ...entry.sections.flatMap(section => [section.title, ...(section.paragraphs || [])])].join(' ').toLowerCase().includes(search.value.trim().toLowerCase()))),
})).filter(group => group.pages.length))
const pageIndex = computed(() => pages.value.findIndex(entry => entry.id === page.value.id))
const previousPage = computed(() => pages.value[pageIndex.value - 1])
const nextPage = computed(() => pages.value[pageIndex.value + 1])
const docLocation = (id: string) => ({ path: '/docs', query: { page: id } })
const firstInCategory = (category: DocsCategory) => pages.value.find(entry => entry.category === category)?.id || 'quick-start'
const languages = (section: DocsSection) => Object.keys(section.examples || {}) as DocsLanguage[]
const selectedLanguage = (section: DocsSection) => languages(section).includes(selectedLanguages[section.id]) ? selectedLanguages[section.id] : languages(section)[0]
const example = (section: DocsSection) => section.examples?.[selectedLanguage(section)] || ''

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

function syncWideLayout(event: MediaQueryListEvent) { wideLayout.value = event.matches }
function closeOnEscape(event: KeyboardEvent) { if (event.key === 'Escape') mobileOpen.value = false }

async function copyExample(section: DocsSection) {
  if (!await copyToClipboard(example(section), t('docs.copied'))) return
  copiedSection.value = section.id
  if (copiedTimer) clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => { copiedSection.value = '' }, 2000)
}

async function jumpTo(id: string) {
  await router.replace({ path: '/docs', query: { page: page.value.id }, hash: `#${id}` })
  document.getElementById(id)?.scrollIntoView({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'start' })
}

watch(() => route.query.page, async () => {
  mobileOpen.value = false
  copiedSection.value = ''
  await nextTick()
  if (route.hash) document.getElementById(route.hash.slice(1))?.scrollIntoView({ block: 'start' })
})
watch([() => page.value.title, siteName], () => { document.title = `${page.value.title} · ${t('docs.title')} - ${siteName.value}` }, { immediate: true })
onMounted(() => {
  wideQuery.addEventListener?.('change', syncWideLayout)
  document.addEventListener('keydown', closeOnEscape)
  if (!appStore.publicSettingsLoaded) void appStore.fetchPublicSettings().catch(() => {})
  if (route.hash) document.getElementById(route.hash.slice(1))?.scrollIntoView({ block: 'start' })
})
onBeforeUnmount(() => {
  if (copiedTimer) clearTimeout(copiedTimer)
  wideQuery.removeEventListener?.('change', syncWideLayout)
  document.removeEventListener('keydown', closeOnEscape)
})
</script>

<style scoped>
.api-docs { font-family: ui-sans-serif, system-ui, sans-serif; }
.docs-code pre { tab-size: 2; }
.docs-code code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
@media (prefers-reduced-motion: reduce) { .docs-sidebar { transition: none; } }
</style>
