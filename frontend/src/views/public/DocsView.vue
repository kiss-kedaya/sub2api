<template>
  <div class="api-docs min-h-screen bg-white text-slate-900 dark:bg-dark-950 dark:text-slate-100">
    <a href="#docs-content" class="sr-only z-60 rounded-md bg-blue-600 px-4 py-3 text-sm text-white focus:not-sr-only focus:fixed focus:left-4 focus:top-4">{{ t('docs.skipToContent') }}</a>
    <header class="docs-header sticky top-0 z-40 border-b border-slate-200/90 bg-white/88 backdrop-blur-md backdrop-saturate-150 dark:border-dark-700/90 dark:bg-dark-950/85">
      <div class="flex h-14 items-center justify-between gap-4 px-5 sm:px-7">
        <div class="flex min-w-0 items-center gap-4">
          <router-link to="/home" class="flex min-w-0 items-center gap-2.5 font-semibold tracking-tight">
            <img :src="siteLogo || '/logo.svg'" alt="" class="h-7 w-7 shrink-0 object-contain" />
            <span class="truncate">{{ siteName }}</span>
          </router-link>

          <!-- VitePress / Rspress style Cmd+K search trigger -->
          <button
            type="button"
            data-testid="docs-search-trigger"
            class="hidden h-9 min-w-[200px] items-center justify-between gap-3 rounded-xl border border-slate-200/90 bg-slate-50/85 px-3 text-[13px] text-slate-500 transition hover:border-slate-300 hover:bg-white hover:text-slate-800 focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-blue-500 dark:border-dark-700 dark:bg-dark-900/90 dark:text-slate-400 dark:hover:border-dark-600 dark:hover:text-slate-200 md:inline-flex"
            @click="searchModalOpen = true"
          >
            <span class="inline-flex items-center gap-2">
              <Icon name="search" size="xs" class="text-slate-400" />
              <span>{{ t('docs.searchTrigger') }}</span>
            </span>
            <kbd class="rounded-md border border-slate-200/90 bg-white px-1.5 py-0.5 font-mono text-[11px] font-medium text-slate-400 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-500">
              {{ shortcutLabel }}
            </kbd>
          </button>
        </div>

        <nav class="flex shrink-0 items-center gap-3.5 text-[14px] sm:gap-5" :aria-label="t('docs.title')">
          <button
            type="button"
            data-testid="docs-search-trigger-mobile"
            class="rounded-full p-2 text-slate-500 hover:bg-slate-100 focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-blue-500 dark:hover:bg-dark-800 md:hidden"
            :aria-label="t('docs.searchModalTitle')"
            @click="searchModalOpen = true"
          >
            <Icon name="search" size="sm" />
          </button>
          <router-link :to="consolePath" class="hidden text-slate-500 transition-colors hover:text-slate-900 dark:hover:text-white sm:block">{{ t('docs.api') }}</router-link>
          <router-link v-if="showModels" :to="modelsPath" class="hidden text-slate-500 transition-colors hover:text-slate-900 dark:hover:text-white sm:block">{{ t('docs.models') }}</router-link>
          <span class="hidden font-medium sm:block" aria-current="page">{{ t('nav.docs') }}</span>
          <LocaleSwitcher />
          <button type="button" class="rounded-full p-2 text-slate-500 hover:bg-slate-100 focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-blue-500 dark:hover:bg-dark-800" :aria-label="t('docs.theme')" @click="toggleTheme">
            <Icon :name="isDark ? 'sun' : 'moon'" size="sm" />
          </button>
        </nav>
      </div>
      <div class="flex h-12 items-center gap-6 overflow-x-auto border-t border-slate-100 px-5 text-[14px] dark:border-dark-800 sm:px-7">
        <button type="button" class="inline-flex shrink-0 items-center gap-2 font-semibold lg:pointer-events-none" :aria-expanded="mobileOpen" aria-controls="docs-sidebar" :aria-label="mobileOpen ? t('docs.closeNavigation') : t('docs.openNavigation')" @click="mobileOpen = !mobileOpen">
          <Icon name="menu" size="sm" class="lg:hidden" />
          <Icon name="book" size="sm" class="hidden text-blue-600 lg:block" />
          <span>{{ t('docs.title') }}</span>
        </button>
        <router-link v-for="category in categories" :key="category" :to="docLocation(firstInCategory(category))" class="docs-category flex h-full shrink-0 items-center border-b-2 px-0.5 transition-colors" :class="page.category === category ? 'border-blue-600 font-medium text-blue-600 dark:text-blue-400' : 'border-transparent text-slate-500 hover:text-slate-900 dark:hover:text-white'" :aria-current="page.category === category ? 'page' : undefined" @click="openCategoryForTab(category)">
          {{ t(`docs.category.${category}`) }}
        </router-link>
      </div>
    </header>

    <button v-if="mobileOpen" class="fixed inset-0 z-40 bg-slate-900/30 lg:hidden" :aria-label="t('docs.closeNavigation')" @click="mobileOpen = false"></button>
    <aside id="docs-sidebar" class="docs-sidebar fixed bottom-0 left-0 top-[105px] z-50 flex w-[272px] flex-col border-r border-slate-200 bg-white px-4 py-6 transition-transform dark:border-dark-700 dark:bg-dark-950 lg:z-30 lg:translate-x-0" :class="mobileOpen ? 'translate-x-0' : '-translate-x-full'" :inert="!mobileOpen && !wideLayout" :aria-label="t('docs.navigation')">
      <label class="relative block">
        <Icon name="search" size="sm" class="pointer-events-none absolute left-3.5 top-3 text-slate-400" />
        <input v-model="search" data-testid="docs-search" type="search" class="w-full rounded-xl border border-slate-200 bg-slate-50/70 py-2.5 pl-10 pr-4 text-[13.5px] outline-hidden transition focus:border-blue-500 focus:bg-white focus:ring-2 focus:ring-blue-100 dark:border-dark-700 dark:bg-dark-900 dark:focus:bg-dark-950 dark:focus:ring-blue-900/30" :placeholder="t('docs.search')" :aria-label="t('docs.search')" />
      </label>
      <div class="docs-nav-wrap relative mt-5 min-h-0 flex-1" :class="{ 'docs-nav-more': navHasMore }">
      <nav ref="navRef" class="h-full space-y-1.5 overflow-y-auto pr-1" @scroll.passive="updateNavCue">
        <div v-for="group in navigationGroups" :key="group.category" :data-testid="`docs-group-${group.category}`">
          <h2 class="m-0">
            <button
              type="button"
              :data-testid="`docs-group-toggle-${group.category}`"
              :aria-expanded="isCategoryExpanded(group.category)"
              :aria-controls="`docs-group-list-${group.category}`"
              class="docs-group-toggle flex w-full items-center justify-between gap-2 rounded-xl px-3 py-2 text-left text-[13px] font-semibold tracking-wide transition-colors focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-blue-500"
              :class="group.category === page.category
                ? 'text-slate-950 hover:bg-slate-100/80 dark:text-slate-100 dark:hover:bg-dark-900'
                : 'text-slate-600 hover:bg-slate-100/80 hover:text-slate-950 dark:text-slate-400 dark:hover:bg-dark-900 dark:hover:text-slate-100'"
              @click="toggleCategory(group.category)"
            >
              <span class="flex min-w-0 items-center gap-2">
                <span class="truncate">{{ t(`docs.category.${group.category}`) }}</span>
                <span
                  v-if="group.category === page.category && !isCategoryExpanded(group.category)"
                  class="h-1.5 w-1.5 shrink-0 rounded-full bg-blue-600 dark:bg-blue-400"
                  aria-hidden="true"
                />
              </span>
              <span class="flex shrink-0 items-center gap-1.5">
                <span class="text-[11px] font-medium tabular-nums text-slate-400 dark:text-slate-500">{{ group.pages.length }}</span>
                <Icon
                  name="chevronRight"
                  size="xs"
                  class="docs-group-chevron text-slate-400 dark:text-slate-500"
                  :class="{ 'rotate-90 text-blue-600 dark:text-blue-400': isCategoryExpanded(group.category) }"
                />
              </span>
            </button>
          </h2>
          <Transition
            :css="false"
            @enter="expandHeight"
            @leave="collapseHeight"
            @enter-cancelled="cancelCollapseMotion"
            @leave-cancelled="cancelCollapseMotion"
            @after-enter="onCategoryAfterEnter(group.category)"
            @after-leave="onCategoryAfterLeave"
          >
            <div
              v-show="isCategoryExpanded(group.category)"
              :id="`docs-group-list-${group.category}`"
              :data-testid="`docs-group-list-${group.category}`"
            >
              <div class="ml-3 mt-1 space-y-0.5 border-l border-slate-200/85 pb-2 pl-2.5 dark:border-dark-800">
                <router-link v-for="entry in group.pages" :key="entry.id" :to="docLocation(entry.id)" class="flex min-h-9 items-center rounded-xl px-3 py-2 text-[14px] leading-5 transition-colors" :class="entry.id === page.id ? 'bg-blue-50/90 font-medium text-blue-600 dark:bg-blue-500/12 dark:text-blue-400' : 'text-slate-600 hover:bg-slate-50 hover:text-slate-950 dark:text-slate-400 dark:hover:bg-dark-900 dark:hover:text-white'" :aria-current="entry.id === page.id ? 'page' : undefined" @click="mobileOpen = false">
                  {{ entry.title }}
                </router-link>
              </div>
            </div>
          </Transition>
        </div>
        <p v-if="!navigationGroups.length" class="px-3 text-sm text-slate-500">{{ t('docs.noResults') }}</p>
      </nav>
      </div>
    </aside>

    <div class="lg:pl-[272px]">
      <!-- Starlight / VitePress style sticky mobile & tablet On-This-Page bar (z-20 <= navbar z-40) -->
      <DocsMobileToc
        :sections="tocSections"
        :active-id="activeSection"
        @select="jumpTo"
      />

      <main
        id="docs-content"
        class="mx-auto grid max-w-[1720px] grid-cols-1 gap-10 px-6 py-9 sm:px-10 sm:py-12 xl:gap-14 xl:px-14"
        :class="isAgentSetupPage ? 'xl:grid-cols-[minmax(0,1fr)_216px]' : 'xl:grid-cols-[minmax(0,1040px)_216px]'"
      >
        <article
          :key="page.id"
          class="docs-article-enter w-full min-w-0"
          :class="isAgentSetupPage ? 'max-w-[1360px]' : 'max-w-[1040px]'"
          data-testid="docs-article"
        >
          <AgentSetupGuide
            v-if="isAgentSetupPage"
            :origin="agentSetupOrigin"
            :site-name="siteName"
            :is-authenticated="authStore.isAuthenticated"
            :selected-agent-id="selectedAgentId"
            @toc-change="onAgentTocChange"
          />

          <template v-else>
          <!-- Docusaurus / Rspress style breadcrumbs + reading time -->
          <DocsBreadcrumbs
            :home-location="docLocation(firstInCategory('start'))"
            :category-location="docLocation(firstInCategory(page.category))"
            :category-label="t(`docs.category.${page.category}`)"
            :page-title="page.title"
            :reading-minutes="estimatedReadingMinutes"
          />

          <div class="mb-10 border-b border-slate-200/85 pb-8 dark:border-dark-800">
            <h1 class="text-[32px] font-semibold leading-[1.16] tracking-[-0.028em] text-slate-950 dark:text-slate-50 sm:text-[38px]">{{ page.title }}</h1>
            <p class="mt-4 max-w-3xl text-[16px] leading-8 text-slate-600 dark:text-slate-300 sm:text-[17px]">{{ page.description }}</p>
            <div v-if="page.quickActions?.length" class="mt-6 flex flex-wrap items-center gap-3" data-testid="docs-quick-actions">
              <router-link
                v-for="(action, idx) in page.quickActions"
                :key="`${action.kind}-${action.page || idx}`"
                :to="quickActionTo(action)"
                :data-testid="quickActionTestId(action)"
                class="docs-quick-action inline-flex min-h-10 items-center gap-2 rounded-full text-[14px] font-medium transition focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-500"
                :class="quickActionClass(action.variant)"
              >
                <Icon v-if="action.kind === 'createKey'" name="plus" size="xs" />
                <Icon v-else-if="action.kind === 'manageKeys'" name="key" size="xs" />
                <span>{{ quickActionLabel(action) }}</span>
                <Icon v-if="action.kind === 'doc'" name="arrowRight" size="xs" />
              </router-link>
            </div>
          </div>

          <div v-if="page.summaryCards?.length" class="mb-12 grid grid-cols-1 gap-3.5 sm:grid-cols-2" data-testid="docs-summary-cards">
            <a
              v-for="card in page.summaryCards"
              :key="card.sectionId"
              :href="`#${card.sectionId}`"
              :data-testid="`docs-summary-card-${card.sectionId}`"
              class="docs-summary-card group flex flex-col justify-between rounded-2xl border border-slate-200/90 bg-slate-50/55 p-4.5 text-left transition hover:border-blue-300/90 hover:bg-white hover:shadow-xs focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-blue-500 dark:border-dark-700/90 dark:bg-dark-900/65 dark:hover:border-blue-500/45 dark:hover:bg-dark-900"
              @click.prevent="jumpTo(card.sectionId)"
            >
              <div>
                <div class="flex items-start justify-between gap-2.5">
                  <span class="text-[15px] font-semibold leading-snug text-slate-900 transition-colors group-hover:text-blue-600 dark:text-slate-100 dark:group-hover:text-blue-400">{{ card.title }}</span>
                  <span v-if="card.badge" class="inline-flex shrink-0 items-center rounded-full px-2.5 py-0.5 text-[11px] font-medium ring-1 ring-inset" :class="summaryBadgeClass(card.badgeTone)">{{ card.badge }}</span>
                </div>
                <div v-if="card.method || card.path" class="mt-2.5 flex items-center justify-between gap-2">
                  <div class="flex min-w-0 flex-1 items-center gap-1.5">
                    <span v-if="card.method" class="shrink-0 rounded-md px-2 py-0.5 font-mono text-[11px] font-semibold" :class="methodClass(card.method)">{{ card.method }}</span>
                    <code v-if="card.path" :title="card.path" class="min-w-0 truncate font-mono text-[12px] text-slate-600 dark:text-slate-300">{{ card.path }}</code>
                  </div>
                  <Icon name="arrowRight" size="xs" class="shrink-0 text-slate-400 transition-transform duration-150 group-hover:translate-x-0.5 group-hover:text-blue-600 dark:text-slate-500 dark:group-hover:text-blue-400" />
                </div>
                <p class="mt-2 text-[13.5px] leading-6 text-slate-600 dark:text-slate-400">{{ card.subtitle }}</p>
              </div>
            </a>
          </div>

          <div :class="page.stepped ? 'docs-steps relative space-y-11 border-l border-slate-200/90 pl-7 dark:border-dark-700/90 sm:pl-9' : 'space-y-12'">
          <section v-for="(section, sectionIdx) in page.sections" :id="section.id" :key="section.id" class="docs-section relative scroll-mt-36">
            <span
              v-if="page.stepped"
              aria-hidden="true"
              class="absolute -left-[43px] top-0.5 flex h-7 w-7 items-center justify-center rounded-full border border-blue-200 bg-blue-50 font-mono text-xs font-semibold text-blue-700 dark:border-blue-500/40 dark:bg-blue-500/15 dark:text-blue-300 sm:-left-[51px]"
            >
              {{ sectionIdx + 1 }}
            </span>

            <h2 class="mb-4 text-[21px] font-semibold tracking-[-0.02em] text-slate-950 dark:text-slate-50 sm:text-[23px]">
              <a :href="`#${section.id}`" class="group inline-flex items-center" @click.prevent="jumpTo(section.id)">{{ section.title }}<span class="ml-2 text-blue-400 opacity-0 transition group-hover:opacity-100" aria-hidden="true">#</span></a>
            </h2>

            <div v-if="section.endpoint" class="docs-endpoint mb-4 flex flex-wrap items-center gap-2 rounded-xl border border-slate-200/85 bg-slate-50/75 px-3 py-2 dark:border-dark-700/85 dark:bg-dark-900/75" :data-testid="`docs-endpoint-${section.id}`">
              <span class="rounded-md px-2.5 py-1 font-mono text-xs font-semibold" :class="methodClass(section.endpoint.method)">{{ section.endpoint.method }}</span>
              <code class="min-w-0 break-all rounded-md bg-white px-2.5 py-1 font-mono text-[13px] text-slate-800 ring-1 ring-inset ring-slate-200/80 dark:bg-dark-950 dark:text-slate-100 dark:ring-dark-700">{{ section.endpoint.path }}</code>
              <span
                class="inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium ring-1 ring-inset"
                :class="section.endpoint.auth === 'apiKey'
                  ? 'bg-emerald-50/80 text-emerald-700 ring-emerald-200/80 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-500/25'
                  : 'bg-amber-50/80 text-amber-800 ring-amber-200/80 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/25'"
              >
                <Icon :name="section.endpoint.auth === 'apiKey' ? 'key' : 'user'" size="xs" />{{ section.endpoint.auth === 'apiKey' ? t('docs.authApiKey') : t('docs.authSession') }}
              </span>
              <button
                type="button"
                :data-testid="`docs-copy-endpoint-${section.id}`"
                class="ml-auto inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-slate-400 transition hover:bg-white hover:text-slate-900 focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-blue-500 dark:text-slate-500 dark:hover:bg-dark-800 dark:hover:text-white"
                :title="t('docs.copyEndpoint')"
                :aria-label="t('docs.copyEndpoint')"
                @click="copyEndpoint(section)"
              >
                <Icon :name="copiedEndpoint === section.id ? 'check' : 'copy'" size="xs" />
              </button>
            </div>

            <p v-for="paragraph in section.paragraphs" :key="paragraph" class="my-3.5 text-[15px] leading-8 text-slate-600 dark:text-slate-300">{{ paragraph }}</p>

            <!-- Starlight / VitePress / Docusaurus style semantic callout -->
            <DocsCallout
              v-if="section.note"
              :tone="section.noteTone || 'info'"
              :title="section.noteTitle"
              :test-id="`docs-callout-${section.id}`"
            >
              <p>{{ section.note }}</p>
            </DocsCallout>

            <div
              v-if="section.action"
              :data-testid="`docs-section-action-card-${section.id}`"
              class="my-6 flex flex-col justify-between gap-4 rounded-2xl border border-blue-200/85 bg-linear-to-br from-blue-50/75 to-white p-5 dark:border-blue-500/25 dark:from-blue-500/10 dark:to-dark-900/80 sm:flex-row sm:items-center"
            >
              <div class="min-w-0">
                <p class="text-[15px] font-semibold text-slate-900 dark:text-slate-100">{{ section.action.title }}</p>
                <p class="mt-1 text-[14px] leading-6 text-slate-600 dark:text-slate-300">{{ section.action.description }}</p>
              </div>
              <router-link
                :to="quickActionTo(section.action)"
                :data-testid="`docs-section-action-${section.id}`"
                class="docs-quick-action inline-flex min-h-10 shrink-0 items-center justify-center gap-2 rounded-full bg-blue-600 px-5 py-2 text-[14px] font-medium text-white shadow-xs transition hover:bg-blue-700 focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-500 dark:bg-blue-500 dark:hover:bg-blue-400"
              >
                <Icon v-if="section.action.kind === 'createKey'" name="plus" size="xs" />
                <Icon v-else-if="section.action.kind === 'manageKeys'" name="key" size="xs" />
                <span>{{ quickActionLabel(section.action) }}</span>
              </router-link>
            </div>

            <div v-if="section.fields" class="my-6 overflow-x-auto rounded-2xl border border-slate-200 dark:border-dark-700">
              <table class="w-full border-collapse text-left text-[14px]">
                <thead class="bg-slate-50/90 text-xs text-slate-500 dark:bg-dark-900/90"><tr><th class="min-w-[168px] px-5 py-3.5 font-medium sm:w-[30%]">{{ t('docs.field') }}</th><th class="px-5 py-3.5 font-medium">{{ t('docs.description') }}</th></tr></thead>
                <tbody>
                  <tr v-for="field in section.fields" :key="field.name" class="group border-t border-slate-200 align-top transition-colors hover:bg-slate-50/60 dark:border-dark-700 dark:hover:bg-dark-900/50">
                    <td class="px-5 py-4">
                      <button
                        type="button"
                        class="inline-flex max-w-full items-center gap-1.5 rounded-md bg-slate-100 px-2 py-1 text-left font-mono text-[13px] leading-5 tabular-nums text-slate-800 transition hover:bg-blue-50 hover:text-blue-700 dark:bg-dark-800 dark:text-slate-100 dark:hover:bg-blue-500/15 dark:hover:text-blue-300"
                        :title="t('docs.copyFieldName')"
                        @click="copyFieldName(`${section.id}:${field.name}`, field.name)"
                      >
                        <code class="[overflow-wrap:anywhere]">{{ field.name }}</code>
                        <Icon
                          :name="copiedField === `${section.id}:${field.name}` ? 'check' : 'copy'"
                          size="xs"
                          class="shrink-0 transition-opacity"
                          :class="copiedField === `${section.id}:${field.name}` ? 'text-emerald-600 opacity-100 dark:text-emerald-400' : 'text-slate-400 opacity-0 group-hover:opacity-100'"
                        />
                      </button>
                      <div class="mt-2 text-xs text-slate-400">{{ field.type }}<span v-if="field.required" class="ml-1.5 font-medium text-rose-500">{{ t('docs.required') }}</span></div>
                    </td>
                    <td class="wrap-break-word px-5 py-4 leading-7 text-slate-600 dark:text-slate-300">{{ field.description }}</td>
                  </tr>
                </tbody>
              </table>
            </div>

            <!-- Rspress / VitePress / Starlight style synced & highlighted code group -->
            <DocsCodeBlock
              v-if="section.examples"
              :section-id="section.id"
              :title="section.title"
              :languages="languages(section)"
              :active-language="selectedLanguage(section)"
              :code="example(section)"
              :copied="copiedSection === section.id"
              :synced="Boolean(syncedHttpLang && languages(section).includes(syncedHttpLang))"
              @select-language="onSelectSectionLanguage(section, $event)"
              @copy="copyExample(section)"
            />

            <div v-if="section.links?.length" class="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2">
              <template v-for="(link, index) in section.links" :key="link.page || link.href || index">
                <router-link v-if="link.page" :to="docLocation(link.page)" class="inline-flex items-center gap-1.5 text-[15px] text-blue-600 underline decoration-blue-200 underline-offset-4 hover:decoration-blue-600 dark:text-blue-400">{{ link.label }}<Icon name="arrowRight" size="xs" /></router-link>
                <a v-else-if="link.href" :href="link.href" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-1.5 text-[15px] text-blue-600 underline decoration-blue-200 underline-offset-4 hover:decoration-blue-600 dark:text-blue-400">{{ link.label }}<Icon name="externalLink" size="xs" /></a>
              </template>
            </div>
          </section>
          </div>
          </template>

          <!-- VitePress / Starlight style Previous / Next pagination cards -->
          <nav class="mt-14 grid grid-cols-1 gap-4 border-t border-slate-200 pt-7 dark:border-dark-700 sm:grid-cols-2" :aria-label="t('docs.navigation')">
            <router-link v-if="previousPage" :to="docLocation(previousPage.id)" class="group flex flex-col justify-between rounded-2xl border border-slate-200/95 p-5 transition hover:border-blue-300 hover:bg-blue-50/35 dark:border-dark-700 dark:hover:border-blue-500/45 dark:hover:bg-blue-500/[0.06]">
              <span class="mb-1.5 inline-flex items-center gap-1 text-xs text-slate-400">
                <Icon name="arrowLeft" size="xs" class="transition-transform group-hover:-translate-x-0.5" />
                <span>{{ t('docs.previous') }}</span>
              </span>
              <span class="text-[15px] font-semibold text-slate-900 transition-colors group-hover:text-blue-600 dark:text-slate-100 dark:group-hover:text-blue-400">{{ previousPage.title }}</span>
            </router-link><span v-else></span>
            <router-link v-if="nextPage" :to="docLocation(nextPage.id)" class="group flex flex-col items-end justify-between rounded-2xl border border-slate-200/95 p-5 text-right transition hover:border-blue-300 hover:bg-blue-50/35 dark:border-dark-700 dark:hover:border-blue-500/45 dark:hover:bg-blue-500/[0.06]">
              <span class="mb-1.5 inline-flex items-center gap-1 text-xs text-slate-400">
                <span>{{ t('docs.next') }}</span>
                <Icon name="arrowRight" size="xs" class="transition-transform group-hover:translate-x-0.5" />
              </span>
              <span class="text-[15px] font-semibold text-slate-900 transition-colors group-hover:text-blue-600 dark:text-slate-100 dark:group-hover:text-blue-400">{{ nextPage.title }}</span>
            </router-link>
          </nav>
          <footer class="mt-10 flex flex-wrap items-center justify-between gap-3 pb-4 text-xs text-slate-400"><span>{{ siteName }} · {{ t('docs.title') }}</span><span class="font-mono">{{ agentSetupOrigin }}</span></footer>
        </article>

        <!-- VitePress style desktop On-This-Page outline with active left indicator & Back to top -->
        <aside class="hidden xl:block">
          <nav class="sticky top-36 text-[13px]" :aria-label="t('docs.onThisPage')">
            <p class="mb-3.5 text-[12px] font-semibold uppercase tracking-[0.05em] text-slate-400 dark:text-slate-500">{{ t('docs.onThisPage') }}</p>
            <div class="relative border-l border-slate-200 dark:border-dark-700">
              <a
                v-for="section in tocSections"
                :key="section.id"
                :href="`#${section.id}`"
                class="-ml-px block border-l-2 py-1.5 pl-4 leading-5.5 transition-colors"
                :class="activeSection === section.id
                  ? 'border-blue-600 font-medium text-blue-600 dark:border-blue-400 dark:text-blue-400'
                  : 'border-transparent text-slate-500 hover:border-slate-300 hover:text-blue-600 dark:text-slate-400 dark:hover:border-dark-600 dark:hover:text-blue-400'"
                :aria-current="activeSection === section.id ? 'location' : undefined"
                @click.prevent="jumpTo(section.id)"
              >{{ section.title }}</a>
            </div>
            <button
              v-if="showBackToTop"
              type="button"
              data-testid="docs-back-to-top"
              class="mt-6 inline-flex items-center gap-1.5 rounded-full border border-slate-200/90 bg-slate-50/75 px-3 py-1.5 text-xs font-medium text-slate-500 transition hover:border-slate-300 hover:bg-white hover:text-slate-900 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-400 dark:hover:text-slate-100"
              @click="scrollToTop"
            >
              <Icon name="arrowUp" size="xs" />
              <span>{{ t('docs.backToTop') }}</span>
            </button>
          </nav>
        </aside>
      </main>
    </div>

    <!-- Full-text Cmd+K Command Search Modal -->
    <DocsSearchModal
      :open="searchModalOpen"
      :pages="pages"
      @close="searchModalOpen = false"
      @select="onSearchModalSelect"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import AgentSetupGuide, { type AgentTocItem } from '@/components/docs/AgentSetupGuide.vue'
import DocsBreadcrumbs from '@/components/docs/DocsBreadcrumbs.vue'
import DocsCallout from '@/components/docs/DocsCallout.vue'
import DocsCodeBlock from '@/components/docs/DocsCodeBlock.vue'
import DocsMobileToc from '@/components/docs/DocsMobileToc.vue'
import DocsSearchModal from '@/components/docs/DocsSearchModal.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useClipboard } from '@/composables/useClipboard'
import { useDocsSyncTabs } from '@/composables/useDocsSyncTabs'
import { toggleColorMode, useSiteColorMode } from '@/composables/useSiteTheme'
import { cancelCollapseMotion, collapseHeight, expandHeight } from '@/utils/collapseMotion'
import { sanitizeUrl } from '@/utils/url'
import {
  getApiDocs,
  resolveDocsOrigin,
  resolveDocsPage,
  type DocsCategory,
  type DocsLanguage,
  type DocsPage,
  type DocsQuickAction,
  type DocsSection,
  type DocsSummaryCard,
} from '@/data/apiDocs'
import { resolveAgentGatewayOrigin } from '@/data/agentSetupGuides'

const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const authStore = useAuthStore()
const { t, locale } = useI18n()
const { copyToClipboard } = useClipboard()
const { syncedHttpLang, resolveLanguage, selectLanguage } = useDocsSyncTabs()

const origin = resolveDocsOrigin(typeof window !== 'undefined' ? window.location.origin : '')
const agentSetupOrigin = computed(() => resolveAgentGatewayOrigin(appStore.apiBaseUrl || appStore.cachedPublicSettings?.api_base_url, origin))
const pages = computed(() => getApiDocs(locale.value, origin))
const page = computed(() => resolveDocsPage(pages.value, route.query.page))
const isAgentSetupPage = computed(() => page.value.id === 'agent-setup')
const selectedAgentId = computed(() => (typeof route.query.agent === 'string' ? route.query.agent : ''))
const agentToc = ref<AgentTocItem[]>([])
const tocSections = computed<AgentTocItem[]>(() => (
  isAgentSetupPage.value && agentToc.value.length
    ? agentToc.value
    : page.value.sections.map(section => ({ id: section.id, title: section.title }))
))
function onAgentTocChange(items: AgentTocItem[]) {
  agentToc.value = items
}
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const showModels = computed(() => appStore.cachedPublicSettings?.model_plaza_enabled === true)
const modelsPath = computed(() => (authStore.isAuthenticated ? { path: '/model-plaza', query: { embedded: '1' } } : '/model-plaza'))
const consolePath = computed(() => !authStore.isAuthenticated ? '/login' : authStore.isAdminObserver ? '/admin/usage' : authStore.isAdmin ? '/admin/dashboard' : '/dashboard')
const categories: DocsCategory[] = ['start', 'guides', 'media', 'integrations', 'account', 'reference']
const search = ref('')
const searchModalOpen = ref(false)
const activeSection = ref('')
const showBackToTop = ref(false)
const mobileOpen = ref(false)
const navRef = ref<HTMLElement | null>(null)
const navHasMore = ref(false)
const wideQuery = typeof window !== 'undefined' ? window.matchMedia('(min-width: 1024px)') : null
const wideLayout = ref(wideQuery?.matches ?? true)
const isDark = ref(typeof document !== 'undefined' && document.documentElement.classList.contains('dark'))
const copiedSection = ref('')
const copiedEndpoint = ref('')
const copiedField = ref('')
let copiedTimer: ReturnType<typeof setTimeout> | undefined
let copiedEndpointTimer: ReturnType<typeof setTimeout> | undefined
let copiedFieldTimer: ReturnType<typeof setTimeout> | undefined
let sectionObserver: IntersectionObserver | undefined
const visibleSectionIds = new Set<string>()

const isMac = typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent || '')
const shortcutLabel = isMac ? '⌘K' : 'Ctrl K'

const estimatedReadingMinutes = computed(() => {
  if (isAgentSetupPage.value) return 0
  const textLen = [
    page.value.description,
    ...page.value.sections.flatMap((s) => [
      s.title,
      ...(s.paragraphs || []),
      s.note || '',
      ...(s.fields || []).map((f) => f.description),
    ]),
  ].join(' ').length
  return Math.max(1, Math.round(textLen / 700))
})

// Sidebar category disclosure: only the current page's category starts expanded.
// Categories a visitor opens explicitly stay open (pinned) so navigation never fights user choices;
// collapsing any category (including the active one) is also preserved until navigating to a different category.
const openCategories = reactive(new Set<DocsCategory>([page.value.category]))
const pinnedOpenCategories = new Set<DocsCategory>()
const searchCollapsedCategories = reactive(new Set<DocsCategory>())
const hasSearchQuery = computed(() => search.value.trim().length > 0)
let pendingRevealCategory: DocsCategory | null = null
let pendingRevealAwaitsEnter = false

function isCategoryExpanded(category: DocsCategory): boolean {
  if (hasSearchQuery.value) return !searchCollapsedCategories.has(category)
  return openCategories.has(category)
}

function toggleCategory(category: DocsCategory) {
  // Never hijack sidebar scroll position when a user explicitly toggles a disclosure group.
  pendingRevealCategory = null
  pendingRevealAwaitsEnter = false
  if (hasSearchQuery.value) {
    if (searchCollapsedCategories.has(category)) searchCollapsedCategories.delete(category)
    else searchCollapsedCategories.add(category)
  } else if (openCategories.has(category)) {
    openCategories.delete(category)
    pinnedOpenCategories.delete(category)
  } else {
    openCategories.add(category)
    if (category !== page.value.category) pinnedOpenCategories.add(category)
  }
  void nextTick(updateNavCue)
}

function openCategoryForTab(category: DocsCategory) {
  const clearedSearch = search.value.length > 0
  if (clearedSearch) {
    search.value = ''
    searchCollapsedCategories.clear()
  }
  const wasExpanded = openCategories.has(category)
  openCategories.add(category)
  pendingRevealCategory = category
  pendingRevealAwaitsEnter = !wasExpanded && !clearedSearch
  if (category === page.value.category || clearedSearch) {
    void nextTick(revealActiveEntry)
  }
}

function onCategoryAfterEnter(category: DocsCategory) {
  if (pendingRevealCategory === category) {
    pendingRevealCategory = null
    pendingRevealAwaitsEnter = false
    revealActiveEntry()
    return
  }
  updateNavCue()
}

function onCategoryAfterLeave() {
  if (pendingRevealCategory && isCategoryExpanded(pendingRevealCategory)) {
    const waitingForEnter = pendingRevealAwaitsEnter
    if (!waitingForEnter) pendingRevealCategory = null
    revealActiveEntry()
    return
  }
  updateNavCue()
}

function matchesSearch(entry: DocsPage, query: string): boolean {
  if (!query) return true
  const searchable = [
    entry.title,
    entry.description,
    ...(entry.summaryCards || []).flatMap(card => [card.title, card.subtitle, card.path || '']),
    ...entry.sections.flatMap(section => [
      section.title,
      section.endpoint?.path || '',
      ...(section.paragraphs || []),
      section.note || '',
      ...(section.fields || []).flatMap(field => [field.name, field.type, field.description]),
      ...(section.links || []).map(link => link.label),
    ]),
  ]
  return searchable.join(' ').toLowerCase().includes(query)
}

const navigationGroups = computed(() => {
  const query = search.value.trim().toLowerCase()
  return categories.map(category => ({
    category,
    pages: pages.value.filter(entry => entry.category === category && matchesSearch(entry, query)),
  })).filter(group => group.pages.length)
})
const pageIndex = computed(() => pages.value.findIndex(entry => entry.id === page.value.id))
const previousPage = computed(() => pages.value[pageIndex.value - 1])
const nextPage = computed(() => pages.value[pageIndex.value + 1])
const docLocation = (id: string) => ({ path: '/docs', query: { page: id } })
const firstInCategory = (category: DocsCategory) => pages.value.find(entry => entry.category === category)?.id || 'quick-start'
const languages = (section: DocsSection) => Object.keys(section.examples || {}) as DocsLanguage[]
const selectedLanguage = (section: DocsSection) => resolveLanguage(section.id, languages(section))
const example = (section: DocsSection) => section.examples?.[selectedLanguage(section)] || ''

function onSelectSectionLanguage(section: DocsSection, language: DocsLanguage) {
  selectLanguage(section.id, language, languages(section))
}

// The shared switch cross-fades the page; the class flips inside that transition, so follow it instead of guessing.
const colorMode = useSiteColorMode()
watch(colorMode.isDark, (dark) => { isDark.value = dark })

function toggleTheme() {
  toggleColorMode()
}

function methodClass(method: string): string {
  switch (method) {
    case 'GET': return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300'
    case 'POST': return 'bg-blue-50 text-blue-700 dark:bg-blue-500/10 dark:text-blue-300'
    case 'PUT': return 'bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300'
    case 'DELETE': return 'bg-rose-50 text-rose-700 dark:bg-rose-500/10 dark:text-rose-300'
    default: return 'bg-slate-100 text-slate-700 dark:bg-dark-800 dark:text-slate-200'
  }
}

function quickActionTo(action: Pick<DocsQuickAction, 'kind' | 'page'>) {
  if (action.kind === 'createKey') {
    return authStore.isAuthenticated
      ? { path: '/keys', query: { action: 'create' } }
      : { path: '/login', query: { redirect: '/keys?action=create' } }
  }
  if (action.kind === 'manageKeys') {
    return authStore.isAuthenticated
      ? '/keys'
      : { path: '/login', query: { redirect: '/keys' } }
  }
  return docLocation(action.page || 'quick-start')
}

function quickActionLabel(action: Pick<DocsQuickAction, 'kind' | 'label' | 'unauthenticatedLabel'>): string {
  if (action.kind !== 'doc' && !authStore.isAuthenticated && action.unauthenticatedLabel) {
    return action.unauthenticatedLabel
  }
  return action.label
}

function quickActionTestId(action: DocsQuickAction): string {
  if (action.kind === 'createKey') return 'docs-quick-create-key'
  if (action.kind === 'manageKeys') return 'docs-quick-manage-keys'
  return `docs-quick-doc-${action.page || 'link'}`
}

function quickActionClass(variant?: DocsQuickAction['variant']): string {
  switch (variant) {
    case 'primary':
      return 'bg-blue-600 px-5 py-2 text-white shadow-xs hover:bg-blue-700 dark:bg-blue-500 dark:text-white dark:hover:bg-blue-400'
    case 'secondary':
      return 'border border-slate-200/90 bg-white px-4.5 py-2 text-slate-700 hover:border-slate-300 hover:bg-slate-50 hover:text-slate-950 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:border-dark-600 dark:hover:bg-dark-800 dark:hover:text-white'
    default:
      return 'px-3.5 py-2 text-slate-600 hover:bg-slate-100/80 hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800/80 dark:hover:text-white'
  }
}

function summaryBadgeClass(tone?: DocsSummaryCard['badgeTone']): string {
  switch (tone) {
    case 'emerald': return 'bg-emerald-50 text-emerald-700 ring-emerald-200/80 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-500/25'
    case 'amber': return 'bg-amber-50 text-amber-700 ring-amber-200/80 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/25'
    case 'blue': return 'bg-blue-50 text-blue-700 ring-blue-200/80 dark:bg-blue-500/10 dark:text-blue-300 dark:ring-blue-500/25'
    default: return 'bg-slate-100 text-slate-600 ring-slate-200/80 dark:bg-dark-800 dark:text-slate-300 dark:ring-dark-700'
  }
}

// A fade at the bottom of the sidebar says "there is more below" while the list is longer than the panel.
function updateNavCue() {
  const nav = navRef.value
  navHasMore.value = !!nav && nav.scrollHeight - nav.scrollTop - nav.clientHeight > 4
}

// The entry for the page being read must be in view when the page opens, however far down the list it sits.
function revealActiveEntry() {
  const nav = navRef.value
  if (!nav || !isCategoryExpanded(page.value.category)) { updateNavCue(); return }
  const active = nav.querySelector<HTMLElement>('[aria-current="page"]')
  if (!active) { updateNavCue(); return }
  const top = active.offsetTop - nav.offsetTop
  if (top < nav.scrollTop || top + active.offsetHeight > nav.scrollTop + nav.clientHeight) {
    nav.scrollTop = Math.max(0, top - nav.clientHeight / 2 + active.offsetHeight / 2)
  }
  updateNavCue()
}

function syncWideLayout(event: MediaQueryListEvent) {
  wideLayout.value = event.matches
  void nextTick(updateNavCue)
}

function handleGlobalKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    if (searchModalOpen.value) {
      searchModalOpen.value = false
      return
    }
    mobileOpen.value = false
    return
  }
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
    event.preventDefault()
    searchModalOpen.value = !searchModalOpen.value
    return
  }
  if (event.key === '/' && !searchModalOpen.value && !event.metaKey && !event.ctrlKey && !event.altKey) {
    const target = event.target as HTMLElement | null
    const isEditable = target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
    if (!isEditable) {
      event.preventDefault()
      searchModalOpen.value = true
    }
  }
}

function handleScroll() {
  if (typeof window === 'undefined') return
  showBackToTop.value = window.scrollY > 360
}

function scrollToTop() {
  if (typeof window === 'undefined') return
  window.scrollTo({
    top: 0,
    behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth',
  })
}

async function onSearchModalSelect(payload: { pageId: string; sectionId?: string }) {
  const hash = payload.sectionId ? `#${payload.sectionId}` : ''
  await router.push({ path: '/docs', query: { page: payload.pageId }, hash })
  await nextTick()
  if (payload.sectionId) {
    document.getElementById(payload.sectionId)?.scrollIntoView({
      behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth',
      block: 'start',
    })
  }
}

async function copyExample(section: DocsSection) {
  if (!await copyToClipboard(example(section), t('docs.copied'))) return
  copiedSection.value = section.id
  if (copiedTimer) clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => { copiedSection.value = '' }, 2000)
}

async function copyEndpoint(section: DocsSection) {
  if (!section.endpoint) return
  if (!await copyToClipboard(`${origin}${section.endpoint.path}`, t('docs.copied'))) return
  copiedEndpoint.value = section.id
  if (copiedEndpointTimer) clearTimeout(copiedEndpointTimer)
  copiedEndpointTimer = setTimeout(() => { copiedEndpoint.value = '' }, 2000)
}

async function copyFieldName(key: string, name: string) {
  if (!await copyToClipboard(name, t('docs.copied'))) return
  copiedField.value = key
  if (copiedFieldTimer) clearTimeout(copiedFieldTimer)
  copiedFieldTimer = setTimeout(() => { copiedField.value = '' }, 1600)
}

async function jumpTo(id: string) {
  const query: Record<string, string> = { page: page.value.id }
  if (isAgentSetupPage.value && selectedAgentId.value) query.agent = selectedAgentId.value
  await router.replace({ path: '/docs', query, hash: `#${id}` })
  document.getElementById(id)?.scrollIntoView({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'start' })
}

function disconnectSectionObserver() {
  sectionObserver?.disconnect()
  sectionObserver = undefined
  visibleSectionIds.clear()
}

// Highlights the section currently inside the reading band of the viewport.
// Environments without IntersectionObserver simply keep the TOC unhighlighted.
function observeSections() {
  disconnectSectionObserver()
  activeSection.value = ''
  if (typeof window === 'undefined' || typeof IntersectionObserver === 'undefined') return
  const sections = tocSections.value
  const elements = sections.map(section => document.getElementById(section.id)).filter((element): element is HTMLElement => element !== null)
  if (!elements.length) return
  const observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (entry.isIntersecting) visibleSectionIds.add(entry.target.id)
      else visibleSectionIds.delete(entry.target.id)
    }
    const current = tocSections.value.find(section => visibleSectionIds.has(section.id))
    if (current) activeSection.value = current.id
    else if (!visibleSectionIds.size) activeSection.value = ''
  }, { rootMargin: '-96px 0px -60% 0px', threshold: 0 })
  elements.forEach(element => observer.observe(element))
  sectionObserver = observer
}

watch(search, () => {
  searchCollapsedCategories.clear()
})
watch(() => page.value.category, (nextCategory, prevCategory) => {
  const wasExpanded = isCategoryExpanded(nextCategory)
  const collapsingPrev = Boolean(prevCategory && prevCategory !== nextCategory && !pinnedOpenCategories.has(prevCategory) && isCategoryExpanded(prevCategory))
  if (prevCategory && prevCategory !== nextCategory && !pinnedOpenCategories.has(prevCategory)) {
    openCategories.delete(prevCategory)
  }
  openCategories.add(nextCategory)
  pinnedOpenCategories.delete(nextCategory)
  if (!wasExpanded || collapsingPrev) {
    const alreadyAwaitingEnter = pendingRevealCategory === nextCategory && pendingRevealAwaitsEnter
    pendingRevealCategory = nextCategory
    pendingRevealAwaitsEnter = !wasExpanded || alreadyAwaitingEnter
  }
})
watch(mobileOpen, (open) => {
  if (open) void nextTick(revealActiveEntry)
})
watch([() => page.value.id, selectedAgentId], async () => {
  await nextTick()
  observeSections()
  revealActiveEntry()
})
watch(navigationGroups, async () => {
  await nextTick()
  updateNavCue()
})
watch(() => route.query.page, async () => {
  mobileOpen.value = false
  copiedSection.value = ''
  copiedEndpoint.value = ''
  copiedField.value = ''
  await nextTick()
  if (route.hash) document.getElementById(route.hash.slice(1))?.scrollIntoView({ block: 'start' })
})
watch([() => page.value.title, siteName], () => { if (typeof document !== 'undefined') document.title = `${page.value.title} · ${t('docs.title')} - ${siteName.value}` }, { immediate: true })
onMounted(() => {
  wideQuery?.addEventListener?.('change', syncWideLayout)
  document.addEventListener('keydown', handleGlobalKeydown)
  window.addEventListener('scroll', handleScroll, { passive: true })
  observeSections()
  revealActiveEntry()
  if (!appStore.publicSettingsLoaded) void appStore.fetchPublicSettings().catch(() => {})
  if (route.hash) document.getElementById(route.hash.slice(1))?.scrollIntoView({ block: 'start' })
})
onBeforeUnmount(() => {
  if (copiedTimer) clearTimeout(copiedTimer)
  if (copiedEndpointTimer) clearTimeout(copiedEndpointTimer)
  if (copiedFieldTimer) clearTimeout(copiedFieldTimer)
  disconnectSectionObserver()
  wideQuery?.removeEventListener?.('change', syncWideLayout)
  document.removeEventListener('keydown', handleGlobalKeydown)
  window.removeEventListener('scroll', handleScroll)
})
</script>

<style scoped>
.api-docs { font-family: ui-sans-serif, system-ui, sans-serif; }
.docs-code pre { tab-size: 2; }
.docs-group-chevron {
  transition-property: transform, rotate, color;
  transition-duration: 200ms;
  transition-timing-function: cubic-bezier(0.23, 1, 0.32, 1);
}
/* Multi-stop eased gradient eliminates the visible hard edge of a linear fade. */
.docs-nav-wrap::after {
  content: '';
  pointer-events: none;
  position: absolute;
  inset: auto 0 0 0;
  height: 40px;
  background: linear-gradient(
    to bottom,
    transparent 0%,
    color-mix(in oklab, var(--docs-sidebar-bg, #fff) 38%, transparent) 45%,
    color-mix(in oklab, var(--docs-sidebar-bg, #fff) 82%, transparent) 78%,
    var(--docs-sidebar-bg, #fff) 100%
  );
  opacity: 0;
  transition: opacity 150ms cubic-bezier(0.23, 1, 0.32, 1);
}
.docs-nav-wrap.docs-nav-more::after { opacity: 1; }
.docs-code code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
.api-docs :is(button, .docs-category, .docs-quick-action):active {
  transform: scale(0.97);
}
.docs-summary-card:active {
  transform: scale(0.985);
}
.docs-article-enter {
  animation: docs-article-glide 160ms cubic-bezier(0.23, 1, 0.32, 1) both;
}
@keyframes docs-article-glide {
  from {
    opacity: 0;
    transform: translateY(5px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
@media (prefers-reduced-transparency: reduce) {
  .docs-header {
    background-color: #fff;
    backdrop-filter: none;
  }
}
@media (prefers-reduced-motion: reduce) {
  .docs-article-enter { animation: none; }
  .docs-sidebar,
  .docs-group-chevron,
  .docs-nav-wrap::after { transition: none; }
  .api-docs :is(button, .docs-category, .docs-quick-action):active,
  .docs-summary-card:active { transform: none; }
}
</style>

<style>
.dark .api-docs .docs-nav-wrap {
  --docs-sidebar-bg: var(--color-dark-950);
}
@media (prefers-reduced-transparency: reduce) {
  .dark .api-docs .docs-header {
    background-color: var(--color-dark-950);
  }
}
</style>
