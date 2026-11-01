<template>
  <div class="api-docs min-h-screen bg-default text-default">
    <a href="#docs-content" class="sr-only z-60 rounded-md bg-inverted px-4 py-3 text-sm text-inverted focus:not-sr-only focus:fixed focus:left-4 focus:top-4">{{ t('docs.skipToContent') }}</a>

    <PublicHeader :site-name="siteName" :logo="siteLogo" brand-to="/home" :section="t('docs.title')">
      <template #leading>
        <UButton
          icon="i-lucide-menu"
          color="neutral"
          variant="ghost"
          square
          class="-ms-1.5 lg:hidden"
          aria-controls="docs-sidebar"
          :aria-expanded="mobileOpen"
          :aria-label="mobileOpen ? t('docs.closeNavigation') : t('docs.openNavigation')"
          @click="mobileOpen = !mobileOpen"
        />
      </template>

      <UNavigationMenu :items="categoryItems" variant="link" :aria-label="t('docs.title')" />

      <template #actions>
        <UButton :to="consolePath" :label="t('docs.api')" color="neutral" variant="ghost" class="hidden sm:inline-flex" />
        <UButton v-if="showModels" to="/model-plaza" :label="t('docs.models')" color="neutral" variant="ghost" class="hidden sm:inline-flex" />
        <LocaleSwitcher />
        <ThemeToggleButton :label="t('docs.theme')" />
      </template>
    </PublicHeader>

    <div v-if="mobileOpen" class="fixed inset-0 top-16 z-40 bg-elevated/75 lg:hidden" aria-hidden="true" @click="mobileOpen = false"></div>

    <UContainer>
      <UPage>
        <template #left>
          <aside
            id="docs-sidebar"
            class="docs-sidebar fixed bottom-0 start-0 top-16 z-50 flex w-72 flex-col border-e border-default bg-default px-4 py-5 transition-transform lg:sticky lg:top-16 lg:bottom-auto lg:z-auto lg:h-[calc(100vh-4rem)] lg:w-auto lg:translate-x-0 lg:border-e-0 lg:bg-transparent lg:px-0 lg:py-8"
            :class="mobileOpen ? 'translate-x-0' : '-translate-x-full'"
            :inert="!mobileOpen && !wideLayout"
            :aria-label="t('docs.navigation')"
          >
            <UInput
              v-model="search"
              data-testid="docs-search"
              type="search"
              icon="i-lucide-search"
              :placeholder="t('docs.search')"
              :aria-label="t('docs.search')"
              class="w-full"
            />
            <div class="mt-5 min-h-0 flex-1 overflow-y-auto pe-1">
              <UNavigationMenu
                v-if="navigationGroups.length"
                :items="sidebarItems"
                orientation="vertical"
                :ui="{ separator: 'my-2 bg-transparent', label: 'px-2.5 pt-3 pb-1 text-xs' }"
              />
              <p v-else class="px-2 text-sm text-muted">{{ t('docs.noResults') }}</p>
            </div>
            <div class="mt-4 flex items-center gap-2.5 border-t border-default pt-4 text-xs leading-5 text-muted">
              <img :src="siteLogo || '/logo.svg'" alt="" class="size-7 shrink-0 object-contain" />
              <span>{{ t('docs.sidebarNote') }}</span>
            </div>
          </aside>
        </template>

        <main id="docs-content">
          <article :key="page.id" class="min-w-0" data-testid="docs-article">
            <UPageHeader
              :headline="t(`docs.category.${page.category}`)"
              :title="page.title"
              :description="page.description"
              :ui="{ description: 'text-base leading-7' }"
            />

            <UPageBody class="space-y-10">
              <section v-for="section in page.sections" :id="section.id" :key="section.id" class="docs-section scroll-mt-24">
                <h2 class="mb-3 text-xl font-semibold tracking-tight text-highlighted">
                  <a :href="`#${section.id}`" class="group" @click.prevent="jumpTo(section.id)">{{ section.title }}<span class="ms-2 text-primary opacity-0 transition group-hover:opacity-100" aria-hidden="true">#</span></a>
                </h2>
                <p v-for="paragraph in section.paragraphs" :key="paragraph" class="my-3 text-sm leading-7 text-default">{{ paragraph }}</p>

                <UAlert
                  v-if="section.note"
                  :description="section.note"
                  icon="i-lucide-info"
                  color="neutral"
                  variant="subtle"
                  class="my-5"
                />

                <UTable
                  v-if="section.fields"
                  :data="section.fields"
                  :columns="fieldColumns"
                  class="my-5 rounded-lg border border-default"
                  :ui="{ thead: 'bg-elevated/50', th: 'py-3 text-xs font-medium text-muted', td: 'whitespace-normal align-top py-4' }"
                >
                  <template #name-cell="{ row }">
                    <code class="rounded-sm bg-elevated px-1.5 py-1 font-mono text-xs text-highlighted">{{ row.original.name }}</code>
                    <div class="mt-2 text-xs text-dimmed">
                      {{ row.original.type }}<UBadge v-if="row.original.required" :label="t('docs.required')" color="error" variant="subtle" size="sm" class="ms-1.5" />
                    </div>
                  </template>
                  <template #description-cell="{ row }">
                    <span class="wrap-break-word leading-6 text-default">{{ row.original.description }}</span>
                  </template>
                </UTable>

                <div v-if="section.examples" class="docs-code my-5 overflow-hidden rounded-lg border border-default bg-muted">
                  <div class="flex min-h-11 items-center justify-between gap-3 border-b border-default px-2">
                    <div class="flex min-w-0 gap-1 overflow-x-auto" role="group" :aria-label="section.title">
                      <UButton
                        v-for="language in languages(section)"
                        :key="language"
                        :label="languageLabels[language]"
                        :color="selectedLanguage(section) === language ? 'primary' : 'neutral'"
                        :variant="selectedLanguage(section) === language ? 'soft' : 'ghost'"
                        size="xs"
                        class="font-mono"
                        :aria-pressed="selectedLanguage(section) === language"
                        @click="selectedLanguages[section.id] = language"
                      />
                    </div>
                    <UButton
                      :data-testid="`docs-copy-${section.id}`"
                      :icon="copiedSection === section.id ? 'i-lucide-check' : 'i-lucide-copy'"
                      :label="copiedSection === section.id ? t('docs.copied') : t('docs.copy')"
                      color="neutral"
                      variant="ghost"
                      size="xs"
                      :title="t('docs.copy')"
                      :aria-label="t('docs.copy')"
                      :ui="{ label: 'hidden sm:inline' }"
                      @click="copyExample(section)"
                    />
                  </div>
                  <pre class="overflow-x-auto p-5 text-xs leading-6 text-default"><code>{{ example(section) }}</code></pre>
                </div>

                <UButton
                  v-for="link in section.links"
                  :key="link.href"
                  :href="link.href"
                  external
                  target="_blank"
                  :label="link.label"
                  trailing-icon="i-lucide-external-link"
                  color="primary"
                  variant="link"
                  class="mt-2 px-0"
                />
              </section>
            </UPageBody>

            <USeparator class="mt-12" />
            <nav class="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2" :aria-label="t('docs.navigation')">
              <UPageCard
                v-if="previousPage"
                :to="docLocation(previousPage.id)"
                :title="previousPage.title"
                :description="t('docs.previous')"
                icon="i-lucide-arrow-left"
                variant="outline"
                :ui="{ leadingIcon: 'size-4 text-muted', title: 'text-sm', description: 'text-xs' }"
              />
              <span v-else class="hidden sm:block"></span>
              <UPageCard
                v-if="nextPage"
                :to="docLocation(nextPage.id)"
                :title="nextPage.title"
                :description="t('docs.next')"
                icon="i-lucide-arrow-right"
                variant="outline"
                class="text-right"
                :ui="{ wrapper: 'items-end', leadingIcon: 'size-4 text-muted', title: 'text-sm', description: 'text-xs' }"
              />
            </nav>
            <footer class="mt-10 flex flex-wrap items-center justify-between gap-3 pb-10 text-xs text-dimmed">
              <span>{{ siteName }} · {{ t('docs.title') }}</span>
              <span class="font-mono">{{ origin }}</span>
            </footer>
          </article>
        </main>

        <template #right>
          <nav class="sticky top-16 hidden self-start py-8 text-sm lg:block" :aria-label="t('docs.onThisPage')">
            <p class="mb-3 text-sm font-semibold text-highlighted">{{ t('docs.onThisPage') }}</p>
            <div class="space-y-1 border-s border-default">
              <a
                v-for="section in page.sections"
                :key="section.id"
                :href="`#${section.id}`"
                class="-ms-px block border-s border-transparent py-1 ps-4 text-muted transition-colors hover:border-accented hover:text-default"
                @click.prevent="jumpTo(section.id)"
              >{{ section.title }}</a>
            </div>
          </nav>
        </template>
      </UPage>
    </UContainer>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import type { NavigationMenuItem, TableColumn } from '@nuxt/ui'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import PublicHeader from '@/views/public/components/PublicHeader.vue'
import ThemeToggleButton from '@/views/public/components/ThemeToggleButton.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useClipboard } from '@/composables/useClipboard'
import { sanitizeUrl } from '@/utils/url'
import { getApiDocs, resolveDocsOrigin, resolveDocsPage, type DocsCategory, type DocsField, type DocsLanguage, type DocsSection } from '@/data/apiDocs'

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

// Every docs link shares the /docs path, so the current page and category are marked explicitly.
const categoryItems = computed<NavigationMenuItem[]>(() => categories.map(category => ({
  label: t(`docs.category.${category}`),
  to: docLocation(firstInCategory(category)),
  active: page.value.category === category,
})))

const sidebarItems = computed<NavigationMenuItem[][]>(() => navigationGroups.value.map(group => [
  { label: t(`docs.category.${group.category}`), type: 'label' as const },
  ...group.pages.map(entry => ({
    label: entry.title,
    to: docLocation(entry.id),
    active: entry.id === page.value.id,
    onSelect: () => { mobileOpen.value = false },
  })),
]))

const fieldColumns: TableColumn<DocsField>[] = [
  { id: 'name', header: () => t('docs.field'), meta: { class: { th: 'w-1/4', td: 'w-1/4' } } },
  { id: 'description', header: () => t('docs.description') },
]

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
  // Light/dark follows the shared site theme (main.ts + ThemeToggleButton), including "system".
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
.docs-code pre { tab-size: 2; }
.docs-code code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
@media (prefers-reduced-motion: reduce) { .docs-sidebar { transition: none; } }
</style>
