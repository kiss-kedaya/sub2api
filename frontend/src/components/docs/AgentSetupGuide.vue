<template>
  <div class="agent-setup min-w-0" data-testid="agent-setup-guide">
    <!-- ==================== AGENT DETAIL VIEW ==================== -->
    <template v-if="activeAgent">
      <div class="mb-6">
        <router-link
          :to="overviewLocation"
          data-testid="agent-back-all"
          class="inline-flex items-center gap-1.5 text-sm font-medium text-slate-500 transition hover:text-blue-600 dark:text-slate-400 dark:hover:text-blue-400"
        >
          <Icon name="arrowLeft" size="xs" />
          <span>{{ t('docs.agentSetup.allAgents') }}</span>
        </router-link>
      </div>

      <!-- Detail hero -->
      <header class="mb-8 border-b border-slate-200 pb-8 dark:border-dark-700" data-testid="agent-detail-header">
        <div class="flex flex-wrap items-start gap-4">
          <div class="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl border border-slate-200 bg-slate-50 dark:border-dark-700 dark:bg-dark-900">
            <AgentBrandIcon :icon-key="activeAgent.iconKey" :platform="activeAgent.platform" size="lg" />
          </div>
          <div class="min-w-0 flex-1">
            <p class="text-xs font-medium tracking-wide text-blue-600 dark:text-blue-400">{{ activeAgent.vendor }}</p>
            <h1 class="mt-1 text-[28px] font-semibold leading-tight tracking-tight text-slate-900 dark:text-slate-100 sm:text-[32px]">
              {{ activeAgent.name }} + {{ siteName }}
            </h1>
            <p class="mt-3 text-[15px] leading-7 text-slate-600 dark:text-slate-300">
              {{ activeAgent.description }}
            </p>
          </div>
        </div>

        <div class="mt-5 flex flex-wrap items-center gap-2">
          <span
            v-if="activeAgent.ccSwitch.supported"
            class="inline-flex items-center gap-1 rounded-full border border-emerald-200 bg-emerald-50/80 px-2.5 py-0.5 text-xs font-medium text-emerald-700 dark:border-emerald-800 dark:bg-emerald-500/10 dark:text-emerald-300"
          >
            <Icon name="bolt" size="xs" />
            {{ activeAgent.ccSwitch.oneClickInstallInApp ? t('docs.agentSetup.ccSwitchInstallAndImport') : t('docs.agentSetup.ccSwitchReady') }}
          </span>
          <span
            v-for="wf in activeAgent.workflows"
            :key="wf"
            class="inline-flex items-center rounded-full border border-slate-200 bg-slate-50 px-2.5 py-0.5 text-xs font-medium text-slate-600 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-300"
          >
            {{ workflowLabel(wf) }}
          </span>
          <span class="inline-flex items-center rounded-full border border-blue-200 bg-blue-50/70 px-2.5 py-0.5 font-mono text-xs text-blue-700 dark:border-blue-800 dark:bg-blue-500/10 dark:text-blue-300">
            {{ activeAgent.protocolLabel }}
          </span>
        </div>

        <!-- Quick Action Buttons -->
        <div class="mt-5 flex flex-wrap items-center gap-2.5 text-sm">
          <router-link
            :to="keysActionPath"
            data-testid="agent-detail-keys-cta"
            class="inline-flex min-h-9 items-center gap-1.5 rounded-lg bg-slate-900 px-3.5 py-1.5 text-xs font-medium text-white transition hover:bg-slate-800 dark:bg-white dark:text-slate-900 dark:hover:bg-slate-200"
          >
            <Icon name="key" size="xs" />
            <span>{{ isAuthenticated ? t('docs.agentSetup.goCreateKeyAndImport') : t('docs.agentSetup.signInForKey') }}</span>
          </router-link>
          <router-link
            to="/model-plaza"
            class="inline-flex min-h-9 items-center gap-1.5 rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-700 transition hover:border-slate-300 hover:bg-slate-50 dark:border-dark-700 dark:text-slate-200 dark:hover:bg-dark-900"
          >
            <Icon name="grid" size="xs" />
            <span>{{ t('docs.agentSetup.browseModelPlaza') }}</span>
          </router-link>
          <router-link
            :to="usageActionPath"
            class="inline-flex min-h-9 items-center gap-1.5 rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-700 transition hover:border-slate-300 hover:bg-slate-50 dark:border-dark-700 dark:text-slate-200 dark:hover:bg-dark-900"
          >
            <Icon name="chartBar" size="xs" />
            <span>{{ t('docs.agentSetup.openUsageRecords') }}</span>
          </router-link>
          <a
            :href="catalog.ccSwitchUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex min-h-9 items-center gap-1.5 rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-700 transition hover:border-slate-300 hover:bg-slate-50 dark:border-dark-700 dark:text-slate-200 dark:hover:bg-dark-900"
          >
            <span>{{ t('docs.agentSetup.ccSwitchWebsite') }}</span>
            <Icon name="externalLink" size="xs" />
          </a>
          <a
            :href="activeAgent.officialDocsUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex min-h-9 items-center gap-1 text-xs font-medium text-blue-600 underline decoration-blue-200 underline-offset-4 hover:decoration-blue-600 dark:text-blue-400"
          >
            <span>{{ t('docs.agentSetup.officialDocs') }}</span>
            <Icon name="externalLink" size="xs" />
          </a>
        </div>
      </header>

      <!-- Site Quick Summary Strip -->
      <section
        class="mb-9 grid grid-cols-1 gap-3 rounded-2xl border border-slate-200 bg-[#f8f9fb] p-4 dark:border-dark-700 dark:bg-dark-900/70 md:grid-cols-3"
        :aria-label="t('docs.agentSetup.siteSummaryLabel')"
      >
        <div class="min-w-0 rounded-xl border border-slate-200 bg-white p-3.5 dark:border-dark-700 dark:bg-dark-950/80">
          <span class="block text-[11px] font-medium text-slate-400">{{ t('docs.agentSetup.recommendedGroupLabel') }}</span>
          <p class="mt-1 text-xs font-semibold leading-5 text-slate-800 dark:text-slate-100">{{ activeAgent.siteGroupAdvice }}</p>
        </div>

        <div class="min-w-0 rounded-xl border border-slate-200 bg-white p-3.5 dark:border-dark-700 dark:bg-dark-950/80">
          <span class="block text-[11px] font-medium text-slate-400">{{ t('docs.agentSetup.ccsAppSelectionLabel') }}</span>
          <p class="mt-1 font-mono text-xs font-semibold leading-5 text-slate-800 dark:text-slate-100">{{ activeAgent.ccSwitch.appLabel }}</p>
        </div>

        <div class="min-w-0 rounded-xl border border-slate-200 bg-white p-3.5 dark:border-dark-700 dark:bg-dark-950/80">
          <div class="flex items-center justify-between gap-2">
            <span class="text-[11px] font-medium text-slate-400">{{ t('docs.agentSetup.recommendedBaseUrl') }}</span>
            <button
              type="button"
              data-testid="agent-copy-baseurl"
              class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] text-slate-500 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-dark-800 dark:hover:text-white"
              @click="copyText('agent-base-url', activeAgent.baseUrl)"
            >
              <Icon :name="copiedId === 'agent-base-url' ? 'check' : 'copy'" size="xs" />
              <span>{{ copiedId === 'agent-base-url' ? t('docs.copied') : t('docs.copy') }}</span>
            </button>
          </div>
          <code class="mt-1 block truncate font-mono text-xs font-semibold text-slate-800 dark:text-slate-100">{{ activeAgent.baseUrl }}</code>
          <p class="mt-1 text-[11px] leading-4 text-slate-500 dark:text-slate-400">{{ activeAgent.baseUrlRule }}</p>
        </div>
      </section>

      <!-- Main Setup Section: CC Switch (Primary) vs Manual Fallback -->
      <section id="quick-start" class="mb-11 scroll-mt-36">
        <div class="mb-5 flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">
            {{ t('docs.agentSetup.quickStartTitle') }}
          </h2>

          <div
            v-if="activeAgent.ccSwitch.supported"
            class="inline-flex rounded-xl border border-slate-200 bg-slate-50 p-1 dark:border-dark-700 dark:bg-dark-900"
            role="tablist"
            :aria-label="t('docs.agentSetup.setupMethodLabel')"
          >
            <button
              type="button"
              role="tab"
              data-testid="setup-mode-ccswitch"
              :aria-selected="setupMode === 'ccswitch'"
              class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition"
              :class="setupMode === 'ccswitch'
                ? 'bg-white text-blue-600 shadow-xs dark:bg-dark-800 dark:text-blue-400'
                : 'text-slate-600 hover:text-slate-900 dark:text-slate-300 dark:hover:text-white'"
              @click="setupMode = 'ccswitch'"
            >
              <Icon name="bolt" size="xs" />
              <span>{{ t('docs.agentSetup.modeCcSwitch') }}</span>
            </button>
            <button
              type="button"
              role="tab"
              data-testid="setup-mode-manual"
              :aria-selected="setupMode === 'manual'"
              class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition"
              :class="setupMode === 'manual'
                ? 'bg-white text-blue-600 shadow-xs dark:bg-dark-800 dark:text-blue-400'
                : 'text-slate-600 hover:text-slate-900 dark:text-slate-300 dark:hover:text-white'"
              @click="setupMode = 'manual'"
            >
              <Icon name="terminal" size="xs" />
              <span>{{ t('docs.agentSetup.modeManual') }}</span>
            </button>
          </div>
        </div>

        <!-- Mode A: Primary Site + CC Switch Guided Setup -->
        <div v-if="activeAgent.ccSwitch.supported && setupMode === 'ccswitch'" data-testid="agent-ccswitch-panel" class="space-y-6">
          <ol class="space-y-7 border-l border-slate-200 pl-5 dark:border-dark-700">
            <!-- Step 1: One-click install inside CC Switch → Apps -->
            <li class="relative">
              <span class="absolute -left-[29px] top-0.5 flex h-6 w-6 items-center justify-center rounded-full border border-blue-200 bg-blue-50 font-mono text-[11px] font-semibold text-blue-700 dark:border-blue-800 dark:bg-blue-500/15 dark:text-blue-300">1</span>
              <h3 class="text-base font-semibold text-slate-900 dark:text-slate-100">
                {{ activeAgent.ccSwitch.oneClickInstallInApp ? t('docs.agentSetup.ccsStep1InAppTitle', { agent: activeAgent.ccSwitch.sidebarName }) : t('docs.agentSetup.ccsStep1EnableTitle', { agent: activeAgent.ccSwitch.sidebarName }) }}
              </h3>
              <p class="mt-1.5 text-sm leading-7 text-slate-600 dark:text-slate-300">
                {{ activeAgent.ccSwitch.installNote }}
              </p>
              <div class="mt-2.5 flex flex-wrap items-center gap-2.5 text-xs">
                <span class="text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.ccsNotInstalledYet') }}</span>
                <a
                  :href="catalog.ccSwitchUrl"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="inline-flex items-center gap-1 rounded-md border border-slate-200 bg-white px-2.5 py-1 font-medium text-blue-600 transition hover:border-blue-300 dark:border-dark-700 dark:bg-dark-900 dark:text-blue-400"
                >
                  <span>{{ t('docs.agentSetup.downloadCcSwitch') }}</span>
                  <Icon name="externalLink" size="xs" />
                </a>
              </div>
              <figure class="mt-3 max-w-xl">
                <button
                  type="button"
                  data-testid="detail-shot-ccs-apps"
                  class="group relative block w-full overflow-hidden rounded-xl border border-slate-200 bg-slate-950 text-left transition hover:border-blue-400 dark:border-dark-700"
                  @click="openLightbox(guideShots.ccsApps)"
                >
                  <img
                    :src="guideShots.ccsApps.src"
                    :alt="guideShots.ccsApps.alt"
                    loading="lazy"
                    class="block w-full object-cover"
                  />
                  <span class="absolute bottom-2.5 right-2.5 inline-flex items-center gap-1 rounded-md bg-slate-900/80 px-2 py-1 text-[11px] font-medium text-white backdrop-blur-xs">
                    <Icon name="search" size="xs" />
                    {{ t('docs.agentSetup.screenshotZoomHint') }}
                  </span>
                </button>
                <figcaption class="mt-1.5 text-xs text-slate-500 dark:text-slate-400">
                  {{ guideShots.ccsApps.caption }}
                </figcaption>
              </figure>
            </li>

            <!-- Step 2: Create Key on our site with right group -->
            <li class="relative">
              <span class="absolute -left-[29px] top-0.5 flex h-6 w-6 items-center justify-center rounded-full border border-blue-200 bg-blue-50 font-mono text-[11px] font-semibold text-blue-700 dark:border-blue-800 dark:bg-blue-500/15 dark:text-blue-300">2</span>
              <h3 class="text-base font-semibold text-slate-900 dark:text-slate-100">{{ t('docs.agentSetup.ccsStep2Title') }}</h3>
              <p class="mt-1.5 text-sm leading-7 text-slate-600 dark:text-slate-300">
                {{ t('docs.agentSetup.ccsStep2SiteDesc', { group: activeAgent.siteGroupAdvice }) }}
              </p>
              <div class="mt-3 flex flex-wrap items-center gap-2.5">
                <router-link
                  :to="keysActionPath"
                  class="inline-flex min-h-9 items-center gap-1.5 rounded-lg bg-blue-600 px-3.5 py-1.5 text-xs font-medium text-white transition hover:bg-blue-700"
                >
                  <Icon name="key" size="xs" />
                  <span>{{ isAuthenticated ? t('docs.agentSetup.openKeys') : t('docs.agentSetup.signInForKey') }}</span>
                </router-link>
                <router-link
                  to="/model-plaza"
                  class="inline-flex min-h-9 items-center gap-1.5 rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200"
                >
                  <Icon name="grid" size="xs" />
                  <span>{{ t('docs.agentSetup.browseModelPlaza') }}</span>
                </router-link>
              </div>
              <figure class="mt-3 max-w-xl">
                <button
                  type="button"
                  class="group relative block w-full overflow-hidden rounded-xl border border-slate-200 bg-white text-left transition hover:border-blue-400 dark:border-dark-700"
                  @click="openLightbox(guideShots.createKey)"
                >
                  <img
                    :src="guideShots.createKey.src"
                    :alt="guideShots.createKey.alt"
                    loading="lazy"
                    class="block w-full object-cover"
                  />
                  <span class="absolute bottom-2.5 right-2.5 inline-flex items-center gap-1 rounded-md bg-slate-900/75 px-2 py-1 text-[11px] font-medium text-white backdrop-blur-xs">
                    <Icon name="search" size="xs" />
                    {{ t('docs.agentSetup.screenshotZoomHint') }}
                  </span>
                </button>
                <figcaption class="mt-1.5 text-xs text-slate-500 dark:text-slate-400">
                  {{ guideShots.createKey.caption }}
                </figcaption>
              </figure>
            </li>

            <!-- Step 3: Click Import to CCS in key receipt -->
            <li class="relative">
              <span class="absolute -left-[29px] top-0.5 flex h-6 w-6 items-center justify-center rounded-full border border-blue-200 bg-blue-50 font-mono text-[11px] font-semibold text-blue-700 dark:border-blue-800 dark:bg-blue-500/15 dark:text-blue-300">3</span>
              <h3 class="text-base font-semibold text-slate-900 dark:text-slate-100">{{ t('docs.agentSetup.ccsStep3ImportTitle') }}</h3>
              <p class="mt-1.5 text-sm leading-7 text-slate-600 dark:text-slate-300">
                {{ activeAgent.ccSwitch.importHint }}
              </p>

              <!-- Side-by-side real screenshots: receipt -> Import to CCS modal -->
              <div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                <figure>
                  <button
                    type="button"
                    class="group relative block w-full overflow-hidden rounded-xl border border-slate-200 bg-white text-left transition hover:border-blue-400 dark:border-dark-700"
                    @click="openLightbox(guideShots.keyReceipt)"
                  >
                    <img
                      :src="guideShots.keyReceipt.src"
                      :alt="guideShots.keyReceipt.alt"
                      loading="lazy"
                      class="block w-full object-cover"
                    />
                    <span class="absolute bottom-2 right-2 inline-flex items-center gap-1 rounded bg-slate-900/75 px-1.5 py-0.5 text-[10px] font-medium text-white">
                      <Icon name="search" size="xs" />
                    </span>
                  </button>
                  <figcaption class="mt-1.5 text-xs text-slate-500 dark:text-slate-400">
                    {{ guideShots.keyReceipt.caption }}
                  </figcaption>
                </figure>
                <figure>
                  <button
                    type="button"
                    class="group relative block w-full overflow-hidden rounded-xl border border-slate-200 bg-white text-left transition hover:border-blue-400 dark:border-dark-700"
                    @click="openLightbox(guideShots.importCcs)"
                  >
                    <img
                      :src="guideShots.importCcs.src"
                      :alt="guideShots.importCcs.alt"
                      loading="lazy"
                      class="block w-full object-cover"
                    />
                    <span class="absolute bottom-2 right-2 inline-flex items-center gap-1 rounded bg-slate-900/75 px-1.5 py-0.5 text-[10px] font-medium text-white">
                      <Icon name="search" size="xs" />
                    </span>
                  </button>
                  <figcaption class="mt-1.5 text-xs text-slate-500 dark:text-slate-400">
                    {{ guideShots.importCcs.caption }}
                  </figcaption>
                </figure>
              </div>

              <!-- Collapsible fallback if user needs manual fields in CC Switch -->
              <details class="agent-details group mt-3 rounded-xl border border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/50">
                <summary class="flex cursor-pointer list-none items-center justify-between gap-3 px-4 py-2.5 text-xs font-medium text-slate-600 dark:text-slate-300">
                  <span>{{ t('docs.agentSetup.ccsManualFieldsToggle') }}</span>
                  <Icon name="chevronDown" size="xs" class="shrink-0 text-slate-400 transition-transform group-open:rotate-180" />
                </summary>
                <div class="border-t border-slate-200 dark:border-dark-700">
                  <table class="w-full border-collapse text-left text-xs">
                    <tbody>
                      <tr
                        v-for="row in ccSwitchFieldRows"
                        :key="row.key"
                        class="border-b border-slate-200 last:border-b-0 dark:border-dark-700"
                      >
                        <th class="w-32 bg-slate-50 px-3.5 py-2.5 font-medium text-slate-600 dark:bg-dark-900 dark:text-slate-300">{{ row.label }}</th>
                        <td class="px-3.5 py-2.5 font-mono text-slate-800 dark:text-slate-100">{{ row.value }}</td>
                        <td class="w-16 px-2 py-2 text-right">
                          <button
                            v-if="row.copyable"
                            type="button"
                            class="inline-flex items-center gap-1 rounded px-2 py-1 text-[11px] text-slate-500 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-dark-800 dark:hover:text-white"
                            :aria-label="t('docs.copy')"
                            @click="copyText(`ccs-field-${row.key}`, row.value)"
                          >
                            <Icon :name="copiedId === `ccs-field-${row.key}` ? 'check' : 'copy'" size="xs" />
                          </button>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </details>
            </li>

            <!-- Step 4: Activate in CC Switch & Launch -->
            <li class="relative">
              <span class="absolute -left-[29px] top-0.5 flex h-6 w-6 items-center justify-center rounded-full border border-blue-200 bg-blue-50 font-mono text-[11px] font-semibold text-blue-700 dark:border-blue-800 dark:bg-blue-500/15 dark:text-blue-300">4</span>
              <h3 class="text-base font-semibold text-slate-900 dark:text-slate-100">{{ t('docs.agentSetup.ccsStep4Title') }}</h3>
              <p class="mt-1.5 text-sm leading-7 text-slate-600 dark:text-slate-300">
                {{ activeAgent.firstLaunchNote }}
              </p>
              <div class="mt-3 overflow-hidden rounded-xl border border-slate-200 bg-[#f8f9fb] dark:border-dark-700 dark:bg-dark-900">
                <div class="flex items-center justify-between border-b border-slate-200 px-3.5 py-2 dark:border-dark-700">
                  <span class="font-mono text-[11px] text-slate-500">{{ t('docs.agentSetup.launchCommandLabel') }}</span>
                  <button
                    type="button"
                    class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-slate-500 hover:bg-white hover:text-slate-900 dark:hover:bg-dark-800 dark:hover:text-white"
                    @click="copyText('ccs-launch', activeAgent.launchCommand)"
                  >
                    <Icon :name="copiedId === 'ccs-launch' ? 'check' : 'copy'" size="xs" />
                    <span>{{ copiedId === 'ccs-launch' ? t('docs.copied') : t('docs.copy') }}</span>
                  </button>
                </div>
                <pre class="overflow-x-auto p-4 font-mono text-xs leading-6 text-slate-700 dark:text-slate-200"><code>{{ activeAgent.launchCommand }}</code></pre>
              </div>
              <div class="mt-3 flex flex-wrap gap-2.5">
                <router-link
                  :to="usageActionPath"
                  class="inline-flex min-h-8 items-center gap-1.5 rounded-lg border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-700 hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200"
                >
                  <Icon name="chartBar" size="xs" />
                  <span>{{ t('docs.agentSetup.openUsageRecords') }}</span>
                </router-link>
                <router-link
                  :to="monitorActionPath"
                  class="inline-flex min-h-8 items-center gap-1.5 rounded-lg border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-700 hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200"
                >
                  <Icon name="trendingUp" size="xs" />
                  <span>{{ t('docs.agentSetup.openChannelStatus') }}</span>
                </router-link>
              </div>
            </li>
          </ol>
        </div>

        <!-- Mode B: Secondary Manual / IDE Setup -->
        <div v-else data-testid="agent-manual-panel" class="space-y-5">
          <!-- OS + Model controls -->
          <div class="flex flex-wrap items-end justify-between gap-4 rounded-xl border border-slate-200 bg-[#f8f9fb] p-3.5 dark:border-dark-700 dark:bg-dark-900/70">
            <div>
              <span class="block text-[11px] font-medium text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.shellSelector') }}</span>
              <div class="mt-1.5 inline-flex flex-wrap gap-1 rounded-lg border border-slate-200 bg-white p-1 dark:border-dark-700 dark:bg-dark-950" role="group" :aria-label="t('docs.agentSetup.shellSelector')">
                <button
                  v-for="os in shellOptions"
                  :key="os.id"
                  type="button"
                  :data-testid="`agent-os-${os.id}`"
                  :aria-pressed="selectedOs === os.id"
                  class="rounded-md px-2.5 py-1 text-xs font-medium transition"
                  :class="selectedOs === os.id
                    ? 'bg-slate-900 text-white dark:bg-blue-500 dark:text-white'
                    : 'text-slate-600 hover:text-slate-900 dark:text-slate-300 dark:hover:text-white'"
                  @click="selectedOs = os.id"
                >
                  {{ os.label }}
                </button>
              </div>
            </div>

            <div class="min-w-[220px] flex-1">
              <label for="agent-model-input" class="block text-[11px] font-medium text-slate-500 dark:text-slate-400">
                {{ t('docs.agentSetup.modelSelector') }}
              </label>
              <div class="mt-1.5 flex flex-wrap items-center gap-1.5">
                <input
                  id="agent-model-input"
                  v-model="customModel"
                  data-testid="agent-model-input"
                  type="text"
                  :placeholder="activeAgent.defaultModel"
                  class="min-w-[140px] flex-1 rounded-lg border border-slate-200 bg-white px-2.5 py-1.5 font-mono text-xs text-slate-800 outline-hidden transition focus:border-blue-500 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-100"
                />
                <button
                  v-for="preset in activeAgent.modelPresets.slice(0, 3)"
                  :key="preset"
                  type="button"
                  class="rounded-md border px-2 py-1 font-mono text-[11px] transition"
                  :class="effectiveModel === preset
                    ? 'border-blue-500 bg-blue-50 text-blue-700 dark:border-blue-400 dark:bg-blue-500/15 dark:text-blue-300'
                    : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-300'"
                  @click="customModel = preset === 'YOUR_MODEL_ID' ? '' : preset"
                >
                  {{ preset }}
                </button>
              </div>
            </div>
          </div>

          <ol class="space-y-6 border-l border-slate-200 pl-5 dark:border-dark-700">
            <li v-for="(step, idx) in activeAgent.manualSteps" :key="step.title" class="relative">
              <span class="absolute -left-[29px] top-0.5 flex h-6 w-6 items-center justify-center rounded-full border border-slate-300 bg-white font-mono text-[11px] font-semibold text-slate-700 dark:border-dark-600 dark:bg-dark-900 dark:text-slate-200">
                {{ idx + 1 }}
              </span>
              <h3 class="text-base font-semibold text-slate-900 dark:text-slate-100">{{ step.title }}</h3>
              <p class="mt-1.5 text-sm leading-7 text-slate-600 dark:text-slate-300">{{ step.description }}</p>

              <div v-for="(snippet, sIdx) in stepSnippets(step)" :key="`${idx}-${sIdx}-${snippet.label}`" class="mt-3 overflow-hidden rounded-xl border border-slate-200 bg-[#f8f9fb] dark:border-dark-700 dark:bg-dark-900">
                <div class="flex items-center justify-between gap-2 border-b border-slate-200 px-3.5 py-2 dark:border-dark-700">
                  <span class="min-w-0 truncate font-mono text-[11px] text-slate-600 dark:text-slate-300">{{ snippet.path || snippet.label }}</span>
                  <button
                    type="button"
                    :data-testid="`agent-copy-step-${idx}-${sIdx}`"
                    class="inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-1 text-xs text-slate-500 hover:bg-white hover:text-slate-900 dark:hover:bg-dark-800 dark:hover:text-white"
                    @click="copyText(`manual-${idx}-${sIdx}`, snippet.code)"
                  >
                    <Icon :name="copiedId === `manual-${idx}-${sIdx}` ? 'check' : 'copy'" size="xs" />
                    <span>{{ copiedId === `manual-${idx}-${sIdx}` ? t('docs.copied') : t('docs.copy') }}</span>
                  </button>
                </div>
                <pre class="overflow-x-auto p-4 font-mono text-xs leading-6 text-slate-700 dark:text-slate-200"><code>{{ snippet.code }}</code></pre>
                <p v-if="snippet.note" class="border-t border-slate-200 px-4 py-2 text-xs leading-5 text-slate-500 dark:border-dark-700 dark:text-slate-400">
                  {{ snippet.note }}
                </p>
              </div>
            </li>
          </ol>
        </div>
      </section>

      <!-- Verify & Example Prompts -->
      <section id="verify-connection" class="mb-11 scroll-mt-36">
        <h2 class="mb-4 text-xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">
          {{ t('docs.agentSetup.verifyTitle') }}
        </h2>
        <p class="mb-4 text-sm leading-7 text-slate-600 dark:text-slate-300">
          {{ t('docs.agentSetup.verifyDesc') }}
        </p>

        <div v-if="activeVerifyCommand" class="mb-6 overflow-hidden rounded-xl border border-slate-200 bg-[#f8f9fb] dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-center justify-between gap-2 border-b border-slate-200 px-3.5 py-2 dark:border-dark-700">
            <span class="font-mono text-[11px] text-slate-500">{{ currentOsLabel }}</span>
            <button
              type="button"
              data-testid="agent-copy-verify"
              class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-slate-500 hover:bg-white hover:text-slate-900 dark:hover:bg-dark-800 dark:hover:text-white"
              @click="copyText('agent-verify', activeVerifyCommand)"
            >
              <Icon :name="copiedId === 'agent-verify' ? 'check' : 'copy'" size="xs" />
              <span>{{ copiedId === 'agent-verify' ? t('docs.copied') : t('docs.copy') }}</span>
            </button>
          </div>
          <pre class="overflow-x-auto p-4 font-mono text-xs leading-6 text-slate-700 dark:text-slate-200"><code>{{ activeVerifyCommand }}</code></pre>
        </div>

        <h3 class="mb-3 text-base font-semibold text-slate-900 dark:text-slate-100">
          {{ t('docs.agentSetup.examplePromptsTitle') }}
        </h3>
        <div class="space-y-2.5">
          <div
            v-for="(prompt, pIdx) in activeAgent.examplePrompts"
            :key="pIdx"
            class="flex items-start justify-between gap-3 rounded-xl border border-slate-200 bg-white px-4 py-3 dark:border-dark-700 dark:bg-dark-900/60"
          >
            <p class="text-sm leading-6 text-slate-700 dark:text-slate-200">{{ prompt }}</p>
            <button
              type="button"
              class="inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-1 text-xs text-slate-500 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-dark-800 dark:hover:text-white"
              :aria-label="t('docs.copy')"
              @click="copyText(`prompt-${pIdx}`, prompt)"
            >
              <Icon :name="copiedId === `prompt-${pIdx}` ? 'check' : 'copy'" size="xs" />
            </button>
          </div>
        </div>

        <div v-if="activeAgent.tips.length" class="mt-6 rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-800/70 dark:bg-blue-500/10">
          <h3 class="text-sm font-semibold text-blue-900 dark:text-blue-200">{{ t('docs.agentSetup.tipsTitle') }}</h3>
          <ul class="mt-2 list-disc space-y-1.5 pl-5 text-sm leading-6 text-blue-800 dark:text-blue-200/90">
            <li v-for="tip in activeAgent.tips" :key="tip">{{ tip }}</li>
          </ul>
        </div>
      </section>

      <!-- FAQ -->
      <section v-if="activeAgent.faq.length" id="agent-faq" class="mb-11 scroll-mt-36">
        <h2 class="mb-4 text-xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">
          {{ t('docs.agentSetup.faqTitle') }}
        </h2>
        <div class="space-y-2.5">
          <details
            v-for="item in activeAgent.faq"
            :key="item.question"
            class="agent-details group rounded-xl border border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/50"
          >
            <summary class="flex cursor-pointer list-none items-center justify-between gap-3 px-4 py-3.5 text-sm font-medium text-slate-900 dark:text-slate-100">
              <span>{{ item.question }}</span>
              <Icon name="chevronDown" size="sm" class="shrink-0 text-slate-400 transition-transform group-open:rotate-180" />
            </summary>
            <div class="border-t border-slate-100 px-4 py-3 text-sm leading-7 text-slate-600 dark:border-dark-800 dark:text-slate-300">
              {{ item.answer }}
            </div>
          </details>
        </div>
      </section>

      <!-- Troubleshooting -->
      <section v-if="activeAgent.troubleshooting.length" id="agent-troubleshooting" class="mb-11 scroll-mt-36">
        <h2 class="mb-4 text-xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">
          {{ t('docs.agentSetup.troubleshootingTitle') }}
        </h2>
        <div class="space-y-2.5">
          <details
            v-for="item in activeAgent.troubleshooting"
            :key="item.question"
            class="agent-details group rounded-xl border border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/50"
          >
            <summary class="flex cursor-pointer list-none items-center justify-between gap-3 px-4 py-3.5 text-sm font-medium text-slate-900 dark:text-slate-100">
              <span>{{ item.question }}</span>
              <Icon name="chevronDown" size="sm" class="shrink-0 text-slate-400 transition-transform group-open:rotate-180" />
            </summary>
            <div class="border-t border-slate-100 px-4 py-3 text-sm leading-7 text-slate-600 dark:border-dark-800 dark:text-slate-300">
              {{ item.answer }}
            </div>
          </details>
        </div>
      </section>

      <!-- Other Agents -->
      <section id="other-agents" class="mb-4 scroll-mt-36 border-t border-slate-200 pt-8 dark:border-dark-700">
        <h2 class="mb-4 text-xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">
          {{ t('docs.agentSetup.otherAgentsTitle') }}
        </h2>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <router-link
            v-for="other in otherAgents"
            :key="other.id"
            :to="agentLocation(other.id)"
            class="group flex min-w-0 flex-col justify-between rounded-xl border border-slate-200 bg-white p-4 transition hover:-translate-y-0.5 hover:border-blue-300 hover:shadow-xs dark:border-dark-700 dark:bg-dark-900/50 dark:hover:border-blue-500/60"
          >
            <div class="min-w-0">
              <div class="flex min-w-0 items-center gap-2.5">
                <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-slate-200 bg-slate-50 dark:border-dark-700 dark:bg-dark-950">
                  <AgentBrandIcon :icon-key="other.iconKey" :platform="other.platform" size="sm" />
                </span>
                <div class="min-w-0">
                  <p class="truncate text-[11px] text-slate-400">{{ other.vendor }}</p>
                  <p class="truncate text-sm font-semibold text-slate-900 dark:text-slate-100">{{ other.name }}</p>
                </div>
              </div>
              <p class="mt-2.5 line-clamp-2 text-xs leading-5 text-slate-500 dark:text-slate-400">{{ other.summary }}</p>
            </div>
            <span class="mt-3 inline-flex items-center gap-1 text-xs font-medium text-blue-600 dark:text-blue-400">
              <span>{{ t('docs.agentSetup.viewGuide') }}</span>
              <Icon name="arrowRight" size="xs" class="transition-transform group-hover:translate-x-0.5" />
            </span>
          </router-link>
        </div>
      </section>
    </template>

    <!-- ==================== OVERVIEW: SITE + CC SWITCH ONBOARDING ==================== -->
    <template v-else>
      <header class="mb-9 border-b border-slate-200 pb-8 dark:border-dark-700">
        <p class="mb-2.5 text-xs font-medium tracking-wide text-blue-600 dark:text-blue-400">
          {{ t('docs.category.integrations') }}
        </p>
        <h1 class="text-[30px] font-semibold leading-tight tracking-tight text-slate-900 dark:text-slate-100 sm:text-[34px]">
          {{ t('docs.agentSetup.title') }}
        </h1>
        <p class="mt-4 max-w-2xl text-[15px] leading-8 text-slate-600 dark:text-slate-300">
          {{ t('docs.agentSetup.subtitle') }}
        </p>

        <!-- Primary Quick-Action Bar -->
        <div class="mt-6 flex flex-wrap items-center gap-2.5" data-testid="agent-quick-actions">
          <router-link
            :to="keysActionPath"
            data-testid="agent-overview-keys-cta"
            class="inline-flex min-h-10 items-center gap-2 rounded-lg bg-slate-900 px-4 py-2 text-sm font-medium text-white transition hover:bg-slate-800 dark:bg-white dark:text-slate-900 dark:hover:bg-slate-200"
          >
            <Icon name="key" size="sm" />
            <span>{{ isAuthenticated ? t('docs.agentSetup.openKeys') : t('docs.agentSetup.signInForKey') }}</span>
          </router-link>
          <a
            :href="catalog.ccSwitchUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex min-h-10 items-center gap-2 rounded-lg border border-slate-200 bg-white px-3.5 py-2 text-sm font-medium text-slate-700 transition hover:border-slate-300 hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:bg-dark-800"
          >
            <AgentBrandIcon icon-key="ccswitch" size="sm" />
            <span>{{ t('docs.agentSetup.downloadCcSwitch') }}</span>
            <Icon name="externalLink" size="xs" />
          </a>
          <router-link
            to="/model-plaza"
            data-testid="agent-quick-plaza"
            class="inline-flex min-h-10 items-center gap-1.5 rounded-lg border border-slate-200 bg-white px-3.5 py-2 text-sm font-medium text-slate-700 transition hover:border-slate-300 hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:bg-dark-800"
          >
            <Icon name="grid" size="sm" />
            <span>{{ t('docs.agentSetup.browseModelPlaza') }}</span>
          </router-link>
          <router-link
            :to="usageActionPath"
            data-testid="agent-quick-usage"
            class="inline-flex min-h-10 items-center gap-1.5 rounded-lg border border-slate-200 bg-white px-3.5 py-2 text-sm font-medium text-slate-700 transition hover:border-slate-300 hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:bg-dark-800"
          >
            <Icon name="chartBar" size="sm" />
            <span>{{ t('docs.agentSetup.openUsageRecords') }}</span>
          </router-link>
          <div class="inline-flex min-h-10 max-w-full items-center gap-2 rounded-lg border border-slate-200 bg-[#f8f9fb] px-3 py-1.5 text-xs dark:border-dark-700 dark:bg-dark-900">
            <span class="shrink-0 text-slate-400">{{ t('docs.agentSetup.gatewayLabel') }}</span>
            <code class="truncate font-mono text-slate-700 dark:text-slate-200">{{ catalog.origin }}</code>
            <button
              type="button"
              data-testid="agent-copy-gateway"
              class="inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-1 text-slate-600 hover:bg-white hover:text-slate-900 dark:text-slate-300 dark:hover:bg-dark-800 dark:hover:text-white"
              :title="t('docs.copy')"
              :aria-label="t('docs.copy')"
              @click="copyText('overview-origin', catalog.origin)"
            >
              <Icon :name="copiedId === 'overview-origin' ? 'check' : 'copy'" size="xs" />
            </button>
          </div>
        </div>
      </header>

      <!-- Section 1: 4-Step Site + CC Switch Visual Tutorial (with real screenshots & quick buttons) -->
      <section
        id="site-ccswitch-workflow"
        data-testid="site-ccswitch-workflow"
        class="mb-12 scroll-mt-36 rounded-2xl border border-slate-200 bg-[#f8f9fb] p-5 dark:border-dark-700 dark:bg-dark-900/70 sm:p-6"
      >
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="flex items-start gap-3">
            <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-indigo-200 bg-white dark:border-indigo-800/70 dark:bg-dark-950">
              <AgentBrandIcon icon-key="ccswitch" size="md" />
            </span>
            <div>
              <h2 class="text-lg font-semibold tracking-tight text-slate-900 dark:text-slate-100">
                {{ t('docs.agentSetup.workflowSectionTitle') }}
              </h2>
              <p class="mt-1 max-w-2xl text-sm leading-6 text-slate-600 dark:text-slate-300">
                {{ t('docs.agentSetup.workflowSectionSubtitle') }}
              </p>
            </div>
          </div>
          <a
            :href="catalog.ccSwitchUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex min-h-8 items-center gap-1 rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-200"
          >
            <span>{{ t('docs.agentSetup.ccSwitchWebsite') }}</span>
            <Icon name="externalLink" size="xs" />
          </a>
        </div>

        <ol class="mt-5 grid grid-cols-1 gap-4 md:grid-cols-2">
          <!-- Step 01 Card + CC Switch Apps real screenshot -->
          <li class="flex min-w-0 flex-col justify-between rounded-xl border border-slate-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-950/85">
            <div>
              <div class="flex items-center justify-between gap-2">
                <span class="font-mono text-xs font-semibold text-blue-600 dark:text-blue-400">STEP {{ catalog.siteWorkflowSteps[0].step }}</span>
                <span class="rounded-md bg-slate-100 px-2 py-0.5 text-[11px] text-slate-500 dark:bg-dark-800 dark:text-slate-400">{{ catalog.siteWorkflowSteps[0].where }}</span>
              </div>
              <h3 class="mt-2 text-sm font-semibold text-slate-900 dark:text-slate-100">{{ catalog.siteWorkflowSteps[0].title }}</h3>
              <p class="mt-1.5 text-xs leading-6 text-slate-600 dark:text-slate-300">{{ catalog.siteWorkflowSteps[0].description }}</p>

              <figure class="mt-3">
                <button
                  type="button"
                  data-testid="overview-shot-step1"
                  class="group relative block w-full overflow-hidden rounded-lg border border-slate-200 bg-slate-950 text-left transition hover:border-blue-400 dark:border-dark-700"
                  @click="openLightbox(guideShots.ccsApps)"
                >
                  <img
                    :src="guideShots.ccsApps.src"
                    :alt="guideShots.ccsApps.alt"
                    loading="lazy"
                    class="block aspect-[16/10] w-full object-cover object-top"
                  />
                  <span class="absolute bottom-2 right-2 inline-flex items-center gap-1 rounded bg-slate-900/80 px-2 py-0.5 text-[10px] font-medium text-white">
                    <Icon name="search" size="xs" />
                    {{ t('docs.agentSetup.screenshotZoomHint') }}
                  </span>
                </button>
                <figcaption class="mt-1.5 text-[11px] leading-4 text-slate-500 dark:text-slate-400">
                  {{ guideShots.ccsApps.caption }}
                </figcaption>
              </figure>
            </div>
            <div class="mt-3 flex flex-wrap items-center gap-2 border-t border-slate-100 pt-3 dark:border-dark-800">
              <a
                :href="catalog.ccSwitchUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="inline-flex min-h-7 items-center gap-1 rounded-md border border-slate-200 bg-slate-50 px-2.5 py-1 text-[11px] font-medium text-slate-700 hover:border-blue-300 hover:text-blue-600 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200"
              >
                <span>{{ t('docs.agentSetup.downloadCcSwitch') }}</span>
                <Icon name="externalLink" size="xs" />
              </a>
              <a
                href="#pick-your-agent"
                class="inline-flex min-h-7 items-center gap-1 rounded-md px-2 py-1 text-[11px] font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-500/10"
              >
                <span>{{ t('docs.agentSetup.jumpPickAgent') }}</span>
                <Icon name="arrowDown" size="xs" />
              </a>
            </div>
          </li>

          <!-- Step 02 Card + Create Key real screenshot -->
          <li class="flex min-w-0 flex-col justify-between rounded-xl border border-slate-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-950/85">
            <div>
              <div class="flex items-center justify-between gap-2">
                <span class="font-mono text-xs font-semibold text-blue-600 dark:text-blue-400">STEP {{ catalog.siteWorkflowSteps[1].step }}</span>
                <span class="rounded-md bg-slate-100 px-2 py-0.5 text-[11px] text-slate-500 dark:bg-dark-800 dark:text-slate-400">{{ catalog.siteWorkflowSteps[1].where }}</span>
              </div>
              <h3 class="mt-2 text-sm font-semibold text-slate-900 dark:text-slate-100">{{ catalog.siteWorkflowSteps[1].title }}</h3>
              <p class="mt-1.5 text-xs leading-6 text-slate-600 dark:text-slate-300">{{ catalog.siteWorkflowSteps[1].description }}</p>

              <figure class="mt-3">
                <button
                  type="button"
                  data-testid="overview-shot-step2"
                  class="group relative block w-full overflow-hidden rounded-lg border border-slate-200 bg-white text-left transition hover:border-blue-400 dark:border-dark-700"
                  @click="openLightbox(guideShots.createKey)"
                >
                  <img
                    :src="guideShots.createKey.src"
                    :alt="guideShots.createKey.alt"
                    loading="lazy"
                    class="block aspect-[16/10] w-full object-cover object-top"
                  />
                  <span class="absolute bottom-2 right-2 inline-flex items-center gap-1 rounded bg-slate-900/75 px-2 py-0.5 text-[10px] font-medium text-white">
                    <Icon name="search" size="xs" />
                    {{ t('docs.agentSetup.screenshotZoomHint') }}
                  </span>
                </button>
                <figcaption class="mt-1.5 text-[11px] leading-4 text-slate-500 dark:text-slate-400">
                  {{ guideShots.createKey.caption }}
                </figcaption>
              </figure>
            </div>
            <div class="mt-3 flex flex-wrap items-center gap-2 border-t border-slate-100 pt-3 dark:border-dark-800">
              <router-link
                :to="keysActionPath"
                class="inline-flex min-h-7 items-center gap-1 rounded-md bg-blue-600 px-2.5 py-1 text-[11px] font-medium text-white hover:bg-blue-700"
              >
                <Icon name="key" size="xs" />
                <span>{{ isAuthenticated ? t('docs.agentSetup.openKeys') : t('docs.agentSetup.signInForKey') }}</span>
              </router-link>
              <router-link
                to="/model-plaza"
                class="inline-flex min-h-7 items-center gap-1 rounded-md border border-slate-200 bg-slate-50 px-2.5 py-1 text-[11px] font-medium text-slate-700 hover:border-blue-300 hover:text-blue-600 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200"
              >
                <Icon name="grid" size="xs" />
                <span>{{ t('docs.agentSetup.browseModelPlaza') }}</span>
              </router-link>
            </div>
          </li>

          <!-- Step 03 Card + Receipt / Import to CCS interactive screenshot switcher -->
          <li class="flex min-w-0 flex-col justify-between rounded-xl border border-slate-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-950/85">
            <div>
              <div class="flex items-center justify-between gap-2">
                <span class="font-mono text-xs font-semibold text-blue-600 dark:text-blue-400">STEP {{ catalog.siteWorkflowSteps[2].step }}</span>
                <span class="rounded-md bg-slate-100 px-2 py-0.5 text-[11px] text-slate-500 dark:bg-dark-800 dark:text-slate-400">{{ catalog.siteWorkflowSteps[2].where }}</span>
              </div>
              <h3 class="mt-2 text-sm font-semibold text-slate-900 dark:text-slate-100">{{ catalog.siteWorkflowSteps[2].title }}</h3>
              <p class="mt-1.5 text-xs leading-6 text-slate-600 dark:text-slate-300">{{ catalog.siteWorkflowSteps[2].description }}</p>

              <div class="mt-3">
                <div class="mb-2 inline-flex rounded-lg border border-slate-200 bg-slate-50 p-0.5 dark:border-dark-700 dark:bg-dark-900" role="tablist">
                  <button
                    type="button"
                    role="tab"
                    data-testid="step3-shot-tab-receipt"
                    :aria-selected="step3ShotTab === 'receipt'"
                    class="rounded-md px-2 py-1 text-[11px] font-medium transition"
                    :class="step3ShotTab === 'receipt' ? 'bg-white text-blue-600 shadow-xs dark:bg-dark-800 dark:text-blue-400' : 'text-slate-500 hover:text-slate-900 dark:text-slate-400'"
                    @click="step3ShotTab = 'receipt'"
                  >
                    {{ t('docs.agentSetup.tabReceiptShot') }}
                  </button>
                  <button
                    type="button"
                    role="tab"
                    data-testid="step3-shot-tab-modal"
                    :aria-selected="step3ShotTab === 'modal'"
                    class="rounded-md px-2 py-1 text-[11px] font-medium transition"
                    :class="step3ShotTab === 'modal' ? 'bg-white text-blue-600 shadow-xs dark:bg-dark-800 dark:text-blue-400' : 'text-slate-500 hover:text-slate-900 dark:text-slate-400'"
                    @click="step3ShotTab = 'modal'"
                  >
                    {{ t('docs.agentSetup.tabModalShot') }}
                  </button>
                </div>
                <figure>
                  <button
                    type="button"
                    data-testid="overview-shot-step3"
                    class="group relative block w-full overflow-hidden rounded-lg border border-slate-200 bg-white text-left transition hover:border-blue-400 dark:border-dark-700"
                    @click="openLightbox(activeStep3Shot)"
                  >
                    <img
                      :src="activeStep3Shot.src"
                      :alt="activeStep3Shot.alt"
                      loading="lazy"
                      class="block aspect-[16/10] w-full object-cover object-center"
                    />
                    <span class="absolute bottom-2 right-2 inline-flex items-center gap-1 rounded bg-slate-900/75 px-2 py-0.5 text-[10px] font-medium text-white">
                      <Icon name="search" size="xs" />
                      {{ t('docs.agentSetup.screenshotZoomHint') }}
                    </span>
                  </button>
                  <figcaption class="mt-1.5 text-[11px] leading-4 text-slate-500 dark:text-slate-400">
                    {{ activeStep3Shot.caption }}
                  </figcaption>
                </figure>
              </div>
            </div>
            <div class="mt-3 flex flex-wrap items-center gap-2 border-t border-slate-100 pt-3 dark:border-dark-800">
              <router-link
                :to="keysActionPath"
                class="inline-flex min-h-7 items-center gap-1 rounded-md bg-blue-600 px-2.5 py-1 text-[11px] font-medium text-white hover:bg-blue-700"
              >
                <Icon name="bolt" size="xs" />
                <span>{{ isAuthenticated ? t('docs.agentSetup.goCreateKeyAndImport') : t('docs.agentSetup.signInForKey') }}</span>
              </router-link>
              <button
                type="button"
                class="inline-flex min-h-7 items-center gap-1 rounded-md border border-slate-200 bg-slate-50 px-2.5 py-1 text-[11px] font-medium text-slate-700 hover:border-blue-300 hover:text-blue-600 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200"
                @click="copyText('step3-origin', catalog.origin)"
              >
                <Icon :name="copiedId === 'step3-origin' ? 'check' : 'copy'" size="xs" />
                <span>{{ copiedId === 'step3-origin' ? t('docs.copied') : t('docs.agentSetup.copyGatewayUrl') }}</span>
              </button>
            </div>
          </li>

          <!-- Step 04 Card + Usage Records real screenshot -->
          <li class="flex min-w-0 flex-col justify-between rounded-xl border border-slate-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-950/85">
            <div>
              <div class="flex items-center justify-between gap-2">
                <span class="font-mono text-xs font-semibold text-blue-600 dark:text-blue-400">STEP {{ catalog.siteWorkflowSteps[3].step }}</span>
                <span class="rounded-md bg-slate-100 px-2 py-0.5 text-[11px] text-slate-500 dark:bg-dark-800 dark:text-slate-400">{{ catalog.siteWorkflowSteps[3].where }}</span>
              </div>
              <h3 class="mt-2 text-sm font-semibold text-slate-900 dark:text-slate-100">{{ catalog.siteWorkflowSteps[3].title }}</h3>
              <p class="mt-1.5 text-xs leading-6 text-slate-600 dark:text-slate-300">{{ catalog.siteWorkflowSteps[3].description }}</p>

              <figure class="mt-3">
                <button
                  type="button"
                  data-testid="overview-shot-step4"
                  class="group relative block w-full overflow-hidden rounded-lg border border-slate-200 bg-white text-left transition hover:border-blue-400 dark:border-dark-700"
                  @click="openLightbox(guideShots.usageRecords)"
                >
                  <img
                    :src="guideShots.usageRecords.src"
                    :alt="guideShots.usageRecords.alt"
                    loading="lazy"
                    class="block aspect-[16/10] w-full object-cover object-top"
                  />
                  <span class="absolute bottom-2 right-2 inline-flex items-center gap-1 rounded bg-slate-900/75 px-2 py-0.5 text-[10px] font-medium text-white">
                    <Icon name="search" size="xs" />
                    {{ t('docs.agentSetup.screenshotZoomHint') }}
                  </span>
                </button>
                <figcaption class="mt-1.5 text-[11px] leading-4 text-slate-500 dark:text-slate-400">
                  {{ guideShots.usageRecords.caption }}
                </figcaption>
              </figure>
            </div>
            <div class="mt-3 flex flex-wrap items-center gap-2 border-t border-slate-100 pt-3 dark:border-dark-800">
              <router-link
                :to="usageActionPath"
                class="inline-flex min-h-7 items-center gap-1 rounded-md border border-slate-200 bg-slate-50 px-2.5 py-1 text-[11px] font-medium text-slate-700 hover:border-blue-300 hover:text-blue-600 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200"
              >
                <Icon name="chartBar" size="xs" />
                <span>{{ t('docs.agentSetup.openUsageRecords') }}</span>
              </router-link>
              <router-link
                :to="monitorActionPath"
                class="inline-flex min-h-7 items-center gap-1 rounded-md border border-slate-200 bg-slate-50 px-2.5 py-1 text-[11px] font-medium text-slate-700 hover:border-blue-300 hover:text-blue-600 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200"
              >
                <Icon name="trendingUp" size="xs" />
                <span>{{ t('docs.agentSetup.openChannelStatus') }}</span>
              </router-link>
            </div>
          </li>
        </ol>

        <div class="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-slate-200 pt-4 dark:border-dark-700">
          <div class="min-w-0 flex-1">
            <p class="text-xs font-medium text-slate-500 dark:text-slate-400">
              {{ t('docs.agentSetup.ccsSupportedApps', { count: catalog.ccSwitchApps.length }) }}
            </p>
            <div class="mt-2 flex flex-wrap gap-1.5">
              <span
                v-for="app in catalog.ccSwitchApps"
                :key="app"
                class="rounded-full border border-slate-200 bg-white px-2.5 py-0.5 font-mono text-[11px] text-slate-600 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-300"
              >
                {{ app }}
              </span>
            </div>
          </div>
          <router-link
            :to="keysActionPath"
            class="inline-flex min-h-9 shrink-0 items-center gap-1.5 rounded-lg bg-blue-600 px-3.5 py-1.5 text-xs font-medium text-white transition hover:bg-blue-700"
          >
            <Icon name="key" size="xs" />
            <span>{{ isAuthenticated ? t('docs.agentSetup.goCreateKeyAndImport') : t('docs.agentSetup.signInForKey') }}</span>
          </router-link>
        </div>
      </section>

      <!-- Section 2: Pick your agent -->
      <section id="pick-your-agent" class="mb-12 scroll-mt-36">
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 class="text-xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">
              {{ t('docs.agentSetup.pickTitle') }}
            </h2>
            <p class="mt-1.5 text-sm text-slate-500 dark:text-slate-400">
              {{ t('docs.agentSetup.pickDescription') }}
            </p>
          </div>
          <span class="text-xs tabular-nums text-slate-400">
            {{ t('docs.agentSetup.matchedCount', { count: filteredAgents.length, total: catalog.agents.length }) }}
          </span>
        </div>

        <!-- Workflow filter pills -->
        <div class="mt-4 flex flex-wrap items-center gap-2" role="group" :aria-label="t('docs.agentSetup.filterByWorkflow')">
          <button
            v-for="filter in workflowFilters"
            :key="filter.key"
            type="button"
            :data-testid="`agent-filter-${filter.key}`"
            :aria-pressed="activeFilter === filter.key"
            class="inline-flex min-h-8 items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium transition"
            :class="activeFilter === filter.key
              ? 'border-blue-600 bg-blue-600 text-white dark:border-blue-500 dark:bg-blue-500'
              : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300 hover:text-slate-900 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-300 dark:hover:text-white'"
            @click="activeFilter = filter.key"
          >
            <span>{{ filter.label }}</span>
            <span class="tabular-nums opacity-75">{{ filter.count }}</span>
          </button>
        </div>

        <!-- Agent cards grid -->
        <div v-if="filteredAgents.length" class="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-2" data-testid="agent-card-grid">
          <router-link
            v-for="agent in filteredAgents"
            :key="agent.id"
            :to="agentLocation(agent.id)"
            :data-testid="`agent-card-${agent.id}`"
            class="group flex min-w-0 flex-col justify-between rounded-2xl border border-slate-200 bg-white p-5 transition hover:-translate-y-0.5 hover:border-blue-300 hover:shadow-sm dark:border-dark-700 dark:bg-dark-900/60 dark:hover:border-blue-500/60"
          >
            <div class="min-w-0">
              <div class="flex items-start justify-between gap-3">
                <div class="flex min-w-0 items-center gap-3">
                  <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-slate-200 bg-slate-50 dark:border-dark-700 dark:bg-dark-950">
                    <AgentBrandIcon :icon-key="agent.iconKey" :platform="agent.platform" size="md" />
                  </span>
                  <div class="min-w-0">
                    <span class="block text-[11px] font-medium text-slate-400">{{ agent.vendor }}</span>
                    <h3 class="truncate text-base font-semibold text-slate-900 dark:text-slate-100">{{ agent.name }}</h3>
                  </div>
                </div>
                <span
                  v-if="agent.recommended"
                  class="shrink-0 rounded-full bg-blue-50 px-2 py-0.5 text-[11px] font-medium text-blue-700 dark:bg-blue-500/15 dark:text-blue-300"
                >
                  {{ t('docs.agentSetup.recommendedBadge') }}
                </span>
              </div>

              <p class="mt-3 text-sm leading-6 text-slate-600 dark:text-slate-300">
                {{ agent.summary }}
              </p>

              <p class="mt-2 text-xs text-slate-500 dark:text-slate-400">
                <span class="font-medium text-slate-700 dark:text-slate-200">{{ t('docs.agentSetup.cardGroupPrefix') }}</span>
                {{ agent.siteGroupAdvice }}
              </p>
            </div>

            <div class="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-slate-100 pt-3.5 dark:border-dark-800">
              <div class="flex flex-wrap items-center gap-1.5">
                <span
                  v-if="agent.ccSwitch.supported"
                  class="inline-flex items-center gap-0.5 rounded-md bg-emerald-50 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300"
                >
                  <Icon name="bolt" size="xs" />
                  {{ agent.ccSwitch.oneClickInstallInApp ? t('docs.agentSetup.ccsInstallImportBadge') : t('docs.agentSetup.ccsImportBadge') }}
                </span>
                <span v-else class="rounded-md bg-slate-100 px-2 py-0.5 text-[11px] text-slate-600 dark:bg-dark-800 dark:text-slate-300">
                  {{ t('docs.agentSetup.inAppSettings') }}
                </span>
              </div>
              <span class="inline-flex items-center gap-1 text-xs font-medium text-blue-600 dark:text-blue-400">
                <span>{{ t('docs.agentSetup.viewGuide') }}</span>
                <Icon name="arrowRight" size="xs" class="transition-transform group-hover:translate-x-0.5" />
              </span>
            </div>
          </router-link>
        </div>

        <div v-else class="mt-5 rounded-xl border border-dashed border-slate-300 p-8 text-center dark:border-dark-700">
          <p class="text-sm text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.noMatchingAgents') }}</p>
          <button
            type="button"
            data-testid="agent-clear-filter"
            class="mt-3 rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 dark:border-dark-700 dark:text-slate-200 dark:hover:bg-dark-800"
            @click="activeFilter = 'all'"
          >
            {{ t('docs.agentSetup.clearFilter') }}
          </button>
        </div>
      </section>

      <!-- Section 3: Compare agents table -->
      <section id="compare-agents" class="mb-12 scroll-mt-36">
        <h2 class="text-xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">
          {{ t('docs.agentSetup.compareTitle') }}
        </h2>
        <p class="mt-1.5 text-sm text-slate-500 dark:text-slate-400">
          {{ t('docs.agentSetup.compareSubtitle') }}
        </p>

        <div class="mt-4 overflow-x-auto rounded-xl border border-slate-200 dark:border-dark-700">
          <table class="w-full border-collapse text-left text-[13px]" data-testid="agent-compare-table">
            <thead class="bg-slate-50 text-xs text-slate-500 dark:bg-dark-900 dark:text-slate-400">
              <tr>
                <th class="px-4 py-3 font-medium">{{ t('docs.agentSetup.colAgent') }}</th>
                <th class="px-4 py-3 font-medium">{{ t('docs.agentSetup.colSiteGroup') }}</th>
                <th class="px-4 py-3 font-medium">{{ t('docs.agentSetup.colCcsInstall') }}</th>
                <th class="px-4 py-3 font-medium">{{ t('docs.agentSetup.colCcSwitch') }}</th>
                <th class="px-4 py-3 font-medium">{{ t('docs.agentSetup.colBaseUrl') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="agent in catalog.agents"
                :key="agent.id"
                class="border-t border-slate-200 transition hover:bg-slate-50/70 dark:border-dark-700 dark:hover:bg-dark-900/50"
              >
                <td class="px-4 py-3 font-medium">
                  <router-link :to="agentLocation(agent.id)" class="inline-flex items-center gap-2 text-blue-600 hover:underline dark:text-blue-400">
                    <AgentBrandIcon :icon-key="agent.iconKey" :platform="agent.platform" size="sm" />
                    <span>{{ agent.name }}</span>
                  </router-link>
                </td>
                <td class="px-4 py-3 text-xs text-slate-600 dark:text-slate-300">
                  {{ agent.siteGroupAdvice }}
                </td>
                <td class="px-4 py-3 text-xs">
                  <span v-if="agent.ccSwitch.oneClickInstallInApp" class="inline-flex items-center gap-1 font-medium text-emerald-700 dark:text-emerald-400">
                    <Icon name="check" size="xs" />
                    {{ t('docs.agentSetup.yesInCcsApps') }}
                  </span>
                  <span v-else class="text-slate-400">{{ t('docs.agentSetup.standaloneClient') }}</span>
                </td>
                <td class="px-4 py-3 text-xs">
                  <span
                    v-if="agent.ccSwitch.supported"
                    class="inline-flex items-center gap-1 font-medium text-emerald-700 dark:text-emerald-400"
                  >
                    <Icon name="bolt" size="xs" />
                    {{ agent.ccSwitch.appIds.join(', ') }}
                  </span>
                  <span v-else class="text-slate-400">{{ t('docs.agentSetup.inAppSettings') }}</span>
                </td>
                <td class="px-4 py-3">
                  <code class="font-mono text-[11px] text-slate-600 dark:text-slate-300">{{ agent.baseUrl }}</code>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- Section 4: Using this site effectively -->
      <section id="understanding-agents" class="mb-12 scroll-mt-36 space-y-8">
        <div>
          <h2 class="text-xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">
            {{ t('docs.agentSetup.understandingTitle') }}
          </h2>
          <p class="mt-1.5 text-sm text-slate-500 dark:text-slate-400">
            {{ t('docs.agentSetup.understandingSubtitle') }}
          </p>
        </div>

        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div
            v-for="item in catalog.keyConcepts"
            :key="item.title"
            class="rounded-xl border border-slate-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900/50"
          >
            <h3 class="text-sm font-semibold text-slate-900 dark:text-slate-100">{{ item.title }}</h3>
            <p class="mt-1.5 text-xs leading-6 text-slate-600 dark:text-slate-300">{{ item.description }}</p>
          </div>
        </div>

        <div>
          <h3 class="mb-3 text-base font-semibold text-slate-900 dark:text-slate-100">{{ t('docs.agentSetup.tradeoffsHeading') }}</h3>
          <div class="divide-y divide-slate-200 rounded-xl border border-slate-200 bg-white dark:divide-dark-700 dark:border-dark-700 dark:bg-dark-900/50">
            <div v-for="item in catalog.tradeoffs" :key="item.left" class="p-4">
              <div class="flex flex-wrap items-center gap-2 text-sm font-semibold text-slate-900 dark:text-slate-100">
                <span>{{ item.left }}</span>
                <span class="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] font-normal text-slate-500 dark:bg-dark-800 dark:text-slate-400">vs.</span>
                <span>{{ item.right }}</span>
              </div>
              <p class="mt-1.5 text-xs leading-6 text-slate-600 dark:text-slate-300">{{ item.summary }}</p>
            </div>
          </div>
        </div>
      </section>

      <!-- Section 5: Troubleshooting -->
      <section id="troubleshooting" class="mb-4 scroll-mt-36">
        <h2 class="mb-4 text-xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">
          {{ t('docs.agentSetup.troubleshootingTitle') }}
        </h2>
        <div class="space-y-2.5">
          <details
            v-for="item in catalog.globalTroubleshooting"
            :key="item.question"
            class="agent-details group rounded-xl border border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/50"
          >
            <summary class="flex cursor-pointer list-none items-center justify-between gap-3 px-4 py-3.5 text-sm font-medium text-slate-900 dark:text-slate-100">
              <span>{{ item.question }}</span>
              <Icon name="chevronDown" size="sm" class="shrink-0 text-slate-400 transition-transform group-open:rotate-180" />
            </summary>
            <div class="border-t border-slate-100 px-4 py-3 text-sm leading-7 text-slate-600 dark:border-dark-800 dark:text-slate-300">
              {{ item.answer }}
            </div>
          </details>
        </div>
      </section>
    </template>

    <!-- Fullscreen Screenshot Lightbox -->
    <Teleport to="body">
      <div
        v-if="lightboxShot"
        data-testid="agent-screenshot-lightbox"
        role="dialog"
        aria-modal="true"
        :aria-label="lightboxShot.alt"
        class="fixed inset-0 z-70 flex items-center justify-center bg-slate-950/85 p-4 backdrop-blur-xs sm:p-8"
        @click.self="closeLightbox"
      >
        <div class="relative max-h-[90vh] w-full max-w-5xl overflow-hidden rounded-2xl border border-slate-700 bg-slate-900 shadow-2xl">
          <div class="flex items-center justify-between gap-4 border-b border-slate-800 px-4 py-3">
            <p class="min-w-0 truncate text-xs font-medium text-slate-200">{{ lightboxShot.caption }}</p>
            <button
              type="button"
              data-testid="agent-lightbox-close"
              class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-slate-400 hover:bg-slate-800 hover:text-white"
              :aria-label="t('docs.agentSetup.closePreview')"
              @click="closeLightbox"
            >
              <Icon name="x" size="sm" />
            </button>
          </div>
          <div class="max-h-[calc(90vh-52px)] overflow-auto bg-slate-950 p-2">
            <img
              :src="lightboxShot.src"
              :alt="lightboxShot.alt"
              class="mx-auto block max-h-[82vh] w-auto rounded-lg object-contain"
            />
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import AgentBrandIcon from '@/components/docs/AgentBrandIcon.vue'
import { useClipboard } from '@/composables/useClipboard'
import {
  getAgentSetupCatalog,
  resolveAgentGuide,
  type AgentCodeSnippet,
  type AgentFilterKey,
  type AgentSetupStep,
  type AgentShellOs,
  type AgentWorkflow,
} from '@/data/agentSetupGuides'
import ccsAppsImg from '@/assets/docs/agent-setup/ccswitch-apps.webp'
import createKeyImg from '@/assets/docs/agent-setup/site-create-key.webp'
import keyReceiptImg from '@/assets/docs/agent-setup/site-key-receipt.webp'
import importCcsImg from '@/assets/docs/agent-setup/site-import-ccs.webp'
import usageRecordsImg from '@/assets/docs/agent-setup/site-usage-records.webp'

export interface AgentTocItem {
  id: string
  title: string
}

interface GuideShot {
  src: string
  alt: string
  caption: string
}

const props = defineProps<{
  origin: string
  siteName: string
  isAuthenticated: boolean
  selectedAgentId?: string | null
}>()

const emit = defineEmits<{
  (e: 'toc-change', items: AgentTocItem[]): void
}>()

const { t, locale } = useI18n()
const { copyToClipboard } = useClipboard()

const activeFilter = ref<AgentFilterKey>('all')
const selectedOs = ref<AgentShellOs>('unix')
const customModel = ref('')
const setupMode = ref<'ccswitch' | 'manual'>('ccswitch')
const step3ShotTab = ref<'receipt' | 'modal'>('receipt')
const lightboxShot = ref<GuideShot | null>(null)
const copiedId = ref('')
let copiedTimer: ReturnType<typeof setTimeout> | undefined

const catalog = computed(() => getAgentSetupCatalog(locale.value, props.origin, customModel.value))
const activeAgent = computed(() => resolveAgentGuide(catalog.value.agents, props.selectedAgentId))
const otherAgents = computed(() =>
  activeAgent.value ? catalog.value.agents.filter((agent) => agent.id !== activeAgent.value!.id) : [],
)

const keysActionPath = computed(() => (props.isAuthenticated ? '/keys' : '/login'))
const usageActionPath = computed(() => (props.isAuthenticated ? '/usage' : '/login'))
const monitorActionPath = computed(() => (props.isAuthenticated ? '/monitor' : '/login'))
const overviewLocation = { path: '/docs', query: { page: 'agent-setup' } }
const agentLocation = (id: string) => ({ path: '/docs', query: { page: 'agent-setup', agent: id } })

const guideShots = computed<Record<'ccsApps' | 'createKey' | 'keyReceipt' | 'importCcs' | 'usageRecords', GuideShot>>(() => ({
  ccsApps: {
    src: ccsAppsImg,
    alt: t('docs.agentSetup.shotCcsAppsAlt'),
    caption: t('docs.agentSetup.shotCcsAppsCaption'),
  },
  createKey: {
    src: createKeyImg,
    alt: t('docs.agentSetup.shotCreateKeyAlt'),
    caption: t('docs.agentSetup.shotCreateKeyCaption'),
  },
  keyReceipt: {
    src: keyReceiptImg,
    alt: t('docs.agentSetup.shotKeyReceiptAlt'),
    caption: t('docs.agentSetup.shotKeyReceiptCaption'),
  },
  importCcs: {
    src: importCcsImg,
    alt: t('docs.agentSetup.shotImportCcsAlt'),
    caption: t('docs.agentSetup.shotImportCcsCaption'),
  },
  usageRecords: {
    src: usageRecordsImg,
    alt: t('docs.agentSetup.shotUsageRecordsAlt'),
    caption: t('docs.agentSetup.shotUsageRecordsCaption'),
  },
}))

const activeStep3Shot = computed(() =>
  step3ShotTab.value === 'modal' ? guideShots.value.importCcs : guideShots.value.keyReceipt,
)

function openLightbox(shot: GuideShot) {
  lightboxShot.value = shot
}

function closeLightbox() {
  lightboxShot.value = null
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && lightboxShot.value) {
    closeLightbox()
  }
}

const shellOptions: Array<{ id: AgentShellOs; label: string }> = [
  { id: 'unix', label: 'macOS / Linux' },
  { id: 'powershell', label: 'Windows PowerShell' },
  { id: 'cmd', label: 'Windows CMD' },
]

const currentOsLabel = computed(
  () => shellOptions.find((option) => option.id === selectedOs.value)?.label ?? 'macOS / Linux',
)

const effectiveModel = computed(() => customModel.value.trim() || 'YOUR_MODEL_ID')

const workflowFilters = computed<Array<{ key: AgentFilterKey; label: string; count: number }>>(() => {
  const agents = catalog.value.agents
  const countBy = (predicate: (agent: (typeof agents)[number]) => boolean) => agents.filter(predicate).length
  return [
    { key: 'all', label: t('docs.agentSetup.filterAll'), count: agents.length },
    { key: 'ccswitch', label: t('docs.agentSetup.filterCcSwitch'), count: countBy((a) => a.ccSwitch.supported) },
    { key: 'terminal', label: t('docs.agentSetup.workflowTerminal'), count: countBy((a) => a.workflows.includes('terminal')) },
    { key: 'ide', label: t('docs.agentSetup.workflowIde'), count: countBy((a) => a.workflows.includes('ide') || a.workflows.includes('extension')) },
    { key: 'desktop', label: t('docs.agentSetup.workflowDesktop'), count: countBy((a) => a.workflows.includes('desktop')) },
  ]
})

const filteredAgents = computed(() => {
  const agents = catalog.value.agents
  switch (activeFilter.value) {
    case 'all':
      return agents
    case 'ide':
      return agents.filter((agent) => agent.workflows.includes('ide') || agent.workflows.includes('extension'))
    case 'ccswitch':
      return agents.filter((agent) => agent.ccSwitch.supported)
    default:
      return agents.filter((agent) => agent.workflows.includes(activeFilter.value as AgentWorkflow))
  }
})

const ccSwitchFieldRows = computed(() => {
  if (!activeAgent.value) return []
  const ccs = activeAgent.value.ccSwitch
  const modelValue = customModel.value.trim() || ccs.defaultModel
  return [
    { key: 'app', label: t('docs.agentSetup.ccsFieldApp'), value: ccs.appLabel, copyable: false },
    { key: 'endpoint', label: t('docs.agentSetup.ccsFieldBaseUrl'), value: ccs.endpoint, copyable: true },
    { key: 'protocol', label: t('docs.agentSetup.ccsFieldProtocol'), value: ccs.protocolLabel, copyable: false },
    { key: 'model', label: t('docs.agentSetup.ccsFieldModel'), value: modelValue, copyable: true },
    { key: 'key', label: t('docs.agentSetup.ccsFieldKey'), value: 'YOUR_API_KEY', copyable: false },
  ]
})

const activeVerifyCommand = computed(() => {
  if (!activeAgent.value) return ''
  return activeAgent.value.verifyCommands[selectedOs.value] || activeAgent.value.verifyCommands.unix || ''
})

function workflowLabel(wf: AgentWorkflow): string {
  switch (wf) {
    case 'terminal':
      return t('docs.agentSetup.workflowTerminal')
    case 'ide':
      return t('docs.agentSetup.workflowIdeOnly')
    case 'extension':
      return t('docs.agentSetup.workflowExtension')
    case 'desktop':
      return t('docs.agentSetup.workflowDesktop')
  }
}

function stepSnippets(step: AgentSetupStep): AgentCodeSnippet[] {
  const byOs = step.snippets?.[selectedOs.value] ?? step.snippets?.unix ?? []
  const generic = step.genericSnippets ?? []
  return [...byOs, ...generic]
}

async function copyText(id: string, text: string) {
  if (!text) return
  if (!(await copyToClipboard(text, t('docs.copied')))) return
  copiedId.value = id
  if (copiedTimer) clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => {
    copiedId.value = ''
  }, 2000)
}

const tocItems = computed<AgentTocItem[]>(() => {
  if (activeAgent.value) {
    const items: AgentTocItem[] = [
      { id: 'quick-start', title: t('docs.agentSetup.quickStartTitle') },
      { id: 'verify-connection', title: t('docs.agentSetup.verifyTitle') },
    ]
    if (activeAgent.value.faq.length) {
      items.push({ id: 'agent-faq', title: t('docs.agentSetup.faqTitle') })
    }
    if (activeAgent.value.troubleshooting.length) {
      items.push({ id: 'agent-troubleshooting', title: t('docs.agentSetup.troubleshootingTitle') })
    }
    items.push({ id: 'other-agents', title: t('docs.agentSetup.otherAgentsTitle') })
    return items
  }
  return [
    { id: 'site-ccswitch-workflow', title: t('docs.agentSetup.workflowSectionTitle') },
    { id: 'pick-your-agent', title: t('docs.agentSetup.pickTitle') },
    { id: 'compare-agents', title: t('docs.agentSetup.compareTitle') },
    { id: 'understanding-agents', title: t('docs.agentSetup.understandingTitle') },
    { id: 'troubleshooting', title: t('docs.agentSetup.troubleshootingTitle') },
  ]
})

watch(
  () => activeAgent.value?.id,
  () => {
    customModel.value = ''
    setupMode.value = activeAgent.value?.ccSwitch.supported ? 'ccswitch' : 'manual'
  },
  { immediate: true },
)

watch(
  tocItems,
  (items) => {
    emit('toc-change', items)
  },
  { immediate: true },
)

onMounted(() => {
  document.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  if (copiedTimer) clearTimeout(copiedTimer)
  document.removeEventListener('keydown', onKeydown)
})
</script>

<style scoped>
.agent-details summary::-webkit-details-marker {
  display: none;
}
</style>
