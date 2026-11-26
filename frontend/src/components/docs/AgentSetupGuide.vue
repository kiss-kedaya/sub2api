<template>
  <div class="agent-setup min-w-0" data-testid="agent-setup-guide">
    <!-- ==================== AGENT DETAIL VIEW ==================== -->
    <template v-if="activeAgent">
      <div class="mb-7">
        <router-link
          :to="overviewLocation"
          data-testid="agent-back-all"
          class="inline-flex items-center gap-2 text-[15px] font-medium text-slate-500 transition hover:text-blue-600 dark:text-slate-400 dark:hover:text-blue-400"
        >
          <Icon name="arrowLeft" size="sm" />
          <span>{{ t('docs.agentSetup.allAgents') }}</span>
        </router-link>
      </div>

      <!-- Detail hero -->
      <header class="mb-10 border-b border-slate-200 pb-10 dark:border-dark-700" data-testid="agent-detail-header">
        <div class="flex flex-wrap items-start gap-5">
          <div class="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl border border-slate-200 bg-slate-50 dark:border-dark-700 dark:bg-dark-900">
            <AgentBrandIcon :icon-key="activeAgent.iconKey" :platform="activeAgent.platform" size="lg" />
          </div>
          <div class="min-w-0 flex-1">
            <h1 class="text-balance text-[34px] font-semibold leading-[1.14] tracking-[-0.03em] text-slate-950 dark:text-slate-50 sm:text-[42px]">
              {{ activeAgent.name }} + {{ siteName }}
            </h1>
            <p class="mt-4 max-w-2xl text-[17px] leading-[1.68] text-slate-600 dark:text-slate-300">
              {{ activeAgent.description }}
            </p>
          </div>
        </div>

        <!-- Primary Action Pills -->
        <div class="mt-7 flex flex-wrap items-center gap-3">
          <router-link
            :to="keysActionPath"
            data-testid="agent-detail-keys-cta"
            class="inline-flex min-h-11 items-center gap-2 rounded-full bg-slate-950 px-6 py-2.5 text-[15px] font-medium text-white transition hover:bg-slate-800 dark:bg-white dark:text-slate-950 dark:hover:bg-slate-200"
          >
            <Icon name="key" size="sm" />
            <span>{{ isAuthenticated ? t('docs.agentSetup.goCreateKeyAndImport') : t('docs.agentSetup.signInForKey') }}</span>
          </router-link>
          <a
            v-if="activeAgent.ccSwitch.supported"
            :href="catalog.ccSwitchUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex min-h-11 items-center gap-2 rounded-full border border-slate-300 bg-white px-5 py-2.5 text-[15px] font-medium text-slate-800 transition hover:border-slate-400 hover:bg-slate-50 dark:border-dark-600 dark:bg-dark-900 dark:text-slate-100 dark:hover:bg-dark-800"
          >
            <AgentBrandIcon icon-key="ccswitch" size="sm" />
            <span>{{ t('docs.agentSetup.downloadCcSwitch') }}</span>
            <Icon name="externalLink" size="xs" />
          </a>
          <a
            :href="activeAgent.officialDocsUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex min-h-11 items-center gap-1.5 rounded-full px-4 py-2 text-[14px] font-medium text-slate-600 transition hover:bg-slate-100 hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800 dark:hover:text-white"
          >
            <span>{{ t('docs.agentSetup.officialDocs') }}</span>
            <Icon name="externalLink" size="xs" />
          </a>
        </div>

        <div class="mt-4 flex flex-wrap items-center gap-x-6 gap-y-2 text-[14px] text-slate-500 dark:text-slate-400">
          <router-link to="/model-plaza" class="inline-flex items-center gap-1.5 transition hover:text-slate-950 dark:hover:text-white">
            <Icon name="grid" size="xs" />
            <span>{{ t('docs.agentSetup.browseModelPlaza') }}</span>
          </router-link>
          <router-link :to="usageActionPath" class="inline-flex items-center gap-1.5 transition hover:text-slate-950 dark:hover:text-white">
            <Icon name="chartBar" size="xs" />
            <span>{{ t('docs.agentSetup.openUsageRecords') }}</span>
          </router-link>
          <span class="font-mono text-xs text-slate-400 dark:text-slate-500">{{ activeAgent.protocolLabel }}</span>
        </div>
      </header>

      <!-- Site Quick Summary Strip -->
      <section
        class="mb-12 grid grid-cols-1 gap-4 rounded-3xl border border-slate-200 bg-slate-50/75 p-5 dark:border-dark-700 dark:bg-dark-900/70 md:grid-cols-3"
        :aria-label="t('docs.agentSetup.siteSummaryLabel')"
      >
        <div class="min-w-0 rounded-2xl border border-slate-200/90 bg-white p-4.5 dark:border-dark-700 dark:bg-dark-950/80">
          <span class="block text-xs font-medium text-slate-400 dark:text-slate-500">{{ t('docs.agentSetup.recommendedGroupLabel') }}</span>
          <p class="mt-2 text-[15px] font-semibold leading-6 text-slate-900 dark:text-slate-100">{{ activeAgent.siteGroupAdvice }}</p>
        </div>

        <div class="min-w-0 rounded-2xl border border-slate-200/90 bg-white p-4.5 dark:border-dark-700 dark:bg-dark-950/80">
          <span class="block text-xs font-medium text-slate-400 dark:text-slate-500">{{ t('docs.agentSetup.ccsAppSelectionLabel') }}</span>
          <p class="mt-2 font-mono text-[15px] font-semibold leading-6 text-slate-900 dark:text-slate-100">{{ activeAgent.ccSwitch.appLabel }}</p>
        </div>

        <div class="min-w-0 rounded-2xl border border-slate-200/90 bg-white p-4.5 dark:border-dark-700 dark:bg-dark-950/80">
          <div class="flex items-center justify-between gap-2">
            <span class="text-xs font-medium text-slate-400 dark:text-slate-500">{{ t('docs.agentSetup.recommendedBaseUrl') }}</span>
            <button
              type="button"
              data-testid="agent-copy-baseurl"
              class="inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium text-slate-600 transition hover:bg-slate-100 hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800 dark:hover:text-white"
              @click="copyText('agent-base-url', activeAgent.baseUrl)"
            >
              <Icon :name="copiedId === 'agent-base-url' ? 'check' : 'copy'" size="xs" />
              <span>{{ copiedId === 'agent-base-url' ? t('docs.copied') : t('docs.copy') }}</span>
            </button>
          </div>
          <code class="mt-2 block truncate font-mono text-[14px] font-semibold text-slate-900 dark:text-slate-100">{{ activeAgent.baseUrl }}</code>
          <p class="mt-1.5 text-xs leading-5 text-slate-500 dark:text-slate-400">{{ activeAgent.baseUrlRule }}</p>
        </div>
      </section>

      <!-- Main Setup Section: CC Switch (Primary) vs Manual Fallback -->
      <section id="quick-start" class="mb-16 scroll-mt-36">
        <div class="mb-7 flex flex-wrap items-center justify-between gap-4">
          <h2 class="text-[24px] font-semibold leading-[1.2] tracking-[-0.025em] text-slate-950 dark:text-slate-50 sm:text-[28px]">
            {{ t('docs.agentSetup.quickStartTitle') }}
          </h2>

          <div
            v-if="activeAgent.ccSwitch.supported"
            class="inline-flex rounded-full border border-slate-200 bg-slate-100/80 p-1 dark:border-dark-700 dark:bg-dark-900"
            role="tablist"
            :aria-label="t('docs.agentSetup.setupMethodLabel')"
          >
            <button
              type="button"
              role="tab"
              data-testid="setup-mode-ccswitch"
              :aria-selected="setupMode === 'ccswitch'"
              class="inline-flex min-h-9 items-center gap-1.5 rounded-full px-4 py-1.5 text-[14px] font-medium transition"
              :class="setupMode === 'ccswitch'
                ? 'bg-white text-slate-950 shadow-xs dark:bg-dark-800 dark:text-white'
                : 'text-slate-600 hover:text-slate-950 dark:text-slate-300 dark:hover:text-white'"
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
              class="inline-flex min-h-9 items-center gap-1.5 rounded-full px-4 py-1.5 text-[14px] font-medium transition"
              :class="setupMode === 'manual'
                ? 'bg-white text-slate-950 shadow-xs dark:bg-dark-800 dark:text-white'
                : 'text-slate-600 hover:text-slate-950 dark:text-slate-300 dark:hover:text-white'"
              @click="setupMode = 'manual'"
            >
              <Icon name="terminal" size="xs" />
              <span>{{ t('docs.agentSetup.modeManual') }}</span>
            </button>
          </div>
        </div>

        <!-- Mode A: Primary Site + CC Switch Guided Setup -->
        <div v-if="activeAgent.ccSwitch.supported && setupMode === 'ccswitch'" data-testid="agent-ccswitch-panel" class="space-y-6">
          <ol class="space-y-10 border-l border-slate-200 pl-7 dark:border-dark-700">
            <!-- Step 1: One-click install inside CC Switch → Apps -->
            <li class="relative">
              <span class="absolute -left-[41px] top-0.5 flex h-7 w-7 items-center justify-center rounded-full border border-blue-200 bg-blue-50 font-mono text-xs font-semibold text-blue-700 dark:border-blue-800 dark:bg-blue-500/15 dark:text-blue-300">1</span>
              <h3 class="text-[19px] font-semibold tracking-[-0.015em] text-slate-950 dark:text-slate-50">
                {{ activeAgent.ccSwitch.oneClickInstallInApp ? t('docs.agentSetup.ccsStep1InAppTitle', { agent: activeAgent.ccSwitch.sidebarName }) : t('docs.agentSetup.ccsStep1EnableTitle', { agent: activeAgent.ccSwitch.sidebarName }) }}
              </h3>
              <p class="mt-2 max-w-2xl text-[15px] leading-7 text-slate-600 dark:text-slate-300">
                {{ activeAgent.ccSwitch.installNote }}
              </p>
              <figure class="mt-4 max-w-2xl">
                <button
                  type="button"
                  data-testid="detail-shot-ccs-apps"
                  class="group relative block w-full overflow-hidden rounded-2xl border border-slate-200 bg-slate-950 text-left transition hover:border-blue-400 dark:border-dark-700"
                  @click="openLightbox(guideShots.ccsApps)"
                >
                  <img
                    :src="guideShots.ccsApps.src"
                    :alt="guideShots.ccsApps.alt"
                    loading="lazy"
                    class="block w-full object-cover"
                  />
                  <span class="absolute bottom-3 right-3 inline-flex items-center gap-1.5 rounded-full bg-slate-900/80 px-3 py-1 text-xs font-medium text-white backdrop-blur-xs">
                    <Icon name="search" size="xs" />
                    {{ t('docs.agentSetup.screenshotZoomHint') }}
                  </span>
                </button>
                <figcaption class="mt-2.5 text-[14px] text-slate-500 dark:text-slate-400">
                  {{ guideShots.ccsApps.caption }}
                </figcaption>
              </figure>
            </li>

            <!-- Step 2: Create Key on our site with right group -->
            <li class="relative">
              <span class="absolute -left-[41px] top-0.5 flex h-7 w-7 items-center justify-center rounded-full border border-blue-200 bg-blue-50 font-mono text-xs font-semibold text-blue-700 dark:border-blue-800 dark:bg-blue-500/15 dark:text-blue-300">2</span>
              <h3 class="text-[19px] font-semibold tracking-[-0.015em] text-slate-950 dark:text-slate-50">{{ t('docs.agentSetup.ccsStep2Title') }}</h3>
              <p class="mt-2 max-w-2xl text-[15px] leading-7 text-slate-600 dark:text-slate-300">
                {{ t('docs.agentSetup.ccsStep2SiteDesc', { group: activeAgent.siteGroupAdvice }) }}
              </p>
              <div class="mt-4 flex flex-wrap items-center gap-3">
                <router-link
                  :to="keysActionPath"
                  class="inline-flex min-h-10 items-center gap-2 rounded-full bg-blue-600 px-5 py-2 text-[14px] font-medium text-white transition hover:bg-blue-700"
                >
                  <Icon name="key" size="xs" />
                  <span>{{ isAuthenticated ? t('docs.agentSetup.openKeys') : t('docs.agentSetup.signInForKey') }}</span>
                </router-link>
                <router-link
                  to="/model-plaza"
                  class="inline-flex min-h-10 items-center gap-2 rounded-full border border-slate-200 bg-white px-5 py-2 text-[14px] font-medium text-slate-700 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:bg-dark-800"
                >
                  <Icon name="grid" size="xs" />
                  <span>{{ t('docs.agentSetup.browseModelPlaza') }}</span>
                </router-link>
              </div>
              <figure class="mt-4 max-w-2xl">
                <button
                  type="button"
                  class="group relative block w-full overflow-hidden rounded-2xl border border-slate-200 bg-white text-left transition hover:border-blue-400 dark:border-dark-700"
                  @click="openLightbox(guideShots.createKey)"
                >
                  <img
                    :src="guideShots.createKey.src"
                    :alt="guideShots.createKey.alt"
                    loading="lazy"
                    class="agent-shot-light block w-full object-cover"
                  />
                  <span class="absolute bottom-3 right-3 inline-flex items-center gap-1.5 rounded-full bg-slate-900/75 px-3 py-1 text-xs font-medium text-white backdrop-blur-xs">
                    <Icon name="search" size="xs" />
                    {{ t('docs.agentSetup.screenshotZoomHint') }}
                  </span>
                </button>
                <figcaption class="mt-2.5 text-[14px] text-slate-500 dark:text-slate-400">
                  {{ guideShots.createKey.caption }}
                </figcaption>
              </figure>
            </li>

            <!-- Step 3: Click Import to CCS in key receipt -->
            <li class="relative">
              <span class="absolute -left-[41px] top-0.5 flex h-7 w-7 items-center justify-center rounded-full border border-blue-200 bg-blue-50 font-mono text-xs font-semibold text-blue-700 dark:border-blue-800 dark:bg-blue-500/15 dark:text-blue-300">3</span>
              <h3 class="text-[19px] font-semibold tracking-[-0.015em] text-slate-950 dark:text-slate-50">{{ t('docs.agentSetup.ccsStep3ImportTitle') }}</h3>
              <p class="mt-2 max-w-2xl text-[15px] leading-7 text-slate-600 dark:text-slate-300">
                {{ activeAgent.ccSwitch.importHint }}
              </p>

              <!-- Side-by-side real screenshots: receipt -> Import to CCS modal -->
              <div class="mt-4 grid max-w-3xl grid-cols-1 gap-4 sm:grid-cols-2">
                <figure>
                  <button
                    type="button"
                    class="group relative block w-full overflow-hidden rounded-2xl border border-slate-200 bg-white text-left transition hover:border-blue-400 dark:border-dark-700"
                    @click="openLightbox(guideShots.keyReceipt)"
                  >
                    <img
                      :src="guideShots.keyReceipt.src"
                      :alt="guideShots.keyReceipt.alt"
                      loading="lazy"
                      class="agent-shot-light block w-full object-cover"
                    />
                    <span class="absolute bottom-2.5 right-2.5 inline-flex items-center gap-1 rounded-full bg-slate-900/75 px-2.5 py-1 text-xs font-medium text-white">
                      <Icon name="search" size="xs" />
                    </span>
                  </button>
                  <figcaption class="mt-2 text-[13px] text-slate-500 dark:text-slate-400">
                    {{ guideShots.keyReceipt.caption }}
                  </figcaption>
                </figure>
                <figure>
                  <button
                    type="button"
                    class="group relative block w-full overflow-hidden rounded-2xl border border-slate-200 bg-white text-left transition hover:border-blue-400 dark:border-dark-700"
                    @click="openLightbox(guideShots.importCcs)"
                  >
                    <img
                      :src="guideShots.importCcs.src"
                      :alt="guideShots.importCcs.alt"
                      loading="lazy"
                      class="agent-shot-light block w-full object-cover"
                    />
                    <span class="absolute bottom-2.5 right-2.5 inline-flex items-center gap-1 rounded-full bg-slate-900/75 px-2.5 py-1 text-xs font-medium text-white">
                      <Icon name="search" size="xs" />
                    </span>
                  </button>
                  <figcaption class="mt-2 text-[13px] text-slate-500 dark:text-slate-400">
                    {{ guideShots.importCcs.caption }}
                  </figcaption>
                </figure>
              </div>

              <!-- Collapsible fallback if user needs manual fields in CC Switch -->
              <details class="agent-details group mt-5 max-w-2xl rounded-2xl border border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/50">
                <summary class="flex cursor-pointer list-none items-center justify-between gap-3 px-5 py-3.5 text-[14px] font-medium text-slate-700 dark:text-slate-200">
                  <span>{{ t('docs.agentSetup.ccsManualFieldsToggle') }}</span>
                  <Icon name="chevronDown" size="sm" class="shrink-0 text-slate-400 transition-transform group-open:rotate-180" />
                </summary>
                <div class="border-t border-slate-200 dark:border-dark-700">
                  <table class="w-full border-collapse text-left text-[14px]">
                    <tbody>
                      <tr
                        v-for="row in ccSwitchFieldRows"
                        :key="row.key"
                        class="border-b border-slate-200 last:border-b-0 dark:border-dark-700"
                      >
                        <th class="w-36 bg-slate-50 px-4 py-3 font-medium text-slate-600 dark:bg-dark-900 dark:text-slate-300">{{ row.label }}</th>
                        <td class="px-4 py-3 font-mono text-[13px] text-slate-800 dark:text-slate-100">{{ row.value }}</td>
                        <td class="w-16 px-2 py-2.5 text-right">
                          <button
                            v-if="row.copyable"
                            type="button"
                            class="inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs text-slate-500 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-dark-800 dark:hover:text-white"
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
              <span class="absolute -left-[41px] top-0.5 flex h-7 w-7 items-center justify-center rounded-full border border-blue-200 bg-blue-50 font-mono text-xs font-semibold text-blue-700 dark:border-blue-800 dark:bg-blue-500/15 dark:text-blue-300">4</span>
              <h3 class="text-[19px] font-semibold tracking-[-0.015em] text-slate-950 dark:text-slate-50">{{ t('docs.agentSetup.ccsStep4Title') }}</h3>
              <p class="mt-2 max-w-2xl text-[15px] leading-7 text-slate-600 dark:text-slate-300">
                {{ activeAgent.firstLaunchNote }}
              </p>
              <div class="mt-4 max-w-2xl overflow-hidden rounded-2xl border border-slate-200 bg-[#f8f9fb] dark:border-dark-700 dark:bg-dark-900">
                <div class="flex items-center justify-between border-b border-slate-200 px-4 py-2.5 dark:border-dark-700">
                  <span class="font-mono text-xs text-slate-500">{{ t('docs.agentSetup.launchCommandLabel') }}</span>
                  <button
                    type="button"
                    class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium text-slate-600 hover:bg-white hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800 dark:hover:text-white"
                    @click="copyText('ccs-launch', activeAgent.launchCommand)"
                  >
                    <Icon :name="copiedId === 'ccs-launch' ? 'check' : 'copy'" size="xs" />
                    <span>{{ copiedId === 'ccs-launch' ? t('docs.copied') : t('docs.copy') }}</span>
                  </button>
                </div>
                <pre class="overflow-x-auto p-5 font-mono text-[13.5px] leading-7 text-slate-800 dark:text-slate-200"><code>{{ activeAgent.launchCommand }}</code></pre>
              </div>
              <div class="mt-4 flex flex-wrap gap-3">
                <router-link
                  :to="usageActionPath"
                  class="inline-flex min-h-10 items-center gap-2 rounded-full border border-slate-200 bg-white px-4.5 py-2 text-[14px] font-medium text-slate-700 hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:bg-dark-800"
                >
                  <Icon name="chartBar" size="xs" />
                  <span>{{ t('docs.agentSetup.openUsageRecords') }}</span>
                </router-link>
                <router-link
                  :to="monitorActionPath"
                  class="inline-flex min-h-10 items-center gap-2 rounded-full border border-slate-200 bg-white px-4.5 py-2 text-[14px] font-medium text-slate-700 hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:bg-dark-800"
                >
                  <Icon name="trendingUp" size="xs" />
                  <span>{{ t('docs.agentSetup.openChannelStatus') }}</span>
                </router-link>
              </div>
            </li>
          </ol>
        </div>

        <!-- Mode B: Secondary Manual / IDE Setup -->
        <div v-else data-testid="agent-manual-panel" class="space-y-7">
          <!-- OS + Model controls -->
          <div class="flex flex-wrap items-end justify-between gap-5 rounded-2xl border border-slate-200 bg-[#f8f9fb] p-5 dark:border-dark-700 dark:bg-dark-900/70">
            <div>
              <span class="block text-xs font-medium text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.shellSelector') }}</span>
              <div class="mt-2 inline-flex flex-wrap gap-1 rounded-full border border-slate-200 bg-white p-1 dark:border-dark-700 dark:bg-dark-950" role="group" :aria-label="t('docs.agentSetup.shellSelector')">
                <button
                  v-for="os in shellOptions"
                  :key="os.id"
                  type="button"
                  :data-testid="`agent-os-${os.id}`"
                  :aria-pressed="selectedOs === os.id"
                  class="rounded-full px-3.5 py-1.5 text-[13px] font-medium transition"
                  :class="selectedOs === os.id
                    ? 'bg-slate-900 text-white dark:bg-blue-500 dark:text-white'
                    : 'text-slate-600 hover:text-slate-900 dark:text-slate-300 dark:hover:text-white'"
                  @click="selectedOs = os.id"
                >
                  {{ os.label }}
                </button>
              </div>
            </div>

            <div class="min-w-[240px] flex-1">
              <label for="agent-model-input" class="block text-xs font-medium text-slate-500 dark:text-slate-400">
                {{ t('docs.agentSetup.modelSelector') }}
              </label>
              <div class="mt-2 flex flex-wrap items-center gap-2">
                <input
                  id="agent-model-input"
                  v-model="customModel"
                  data-testid="agent-model-input"
                  type="text"
                  :placeholder="activeAgent.defaultModel"
                  class="min-w-[160px] flex-1 rounded-full border border-slate-200 bg-white px-4 py-2 font-mono text-[13px] text-slate-800 outline-hidden transition focus:border-blue-500 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-100"
                />
                <button
                  v-for="preset in activeAgent.modelPresets.slice(0, 3)"
                  :key="preset"
                  type="button"
                  class="rounded-full border px-3 py-1.5 font-mono text-xs transition"
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

          <ol class="space-y-8 border-l border-slate-200 pl-7 dark:border-dark-700">
            <li v-for="(step, idx) in activeAgent.manualSteps" :key="step.title" class="relative">
              <span class="absolute -left-[41px] top-0.5 flex h-7 w-7 items-center justify-center rounded-full border border-slate-300 bg-white font-mono text-xs font-semibold text-slate-700 dark:border-dark-600 dark:bg-dark-900 dark:text-slate-200">
                {{ idx + 1 }}
              </span>
              <h3 class="text-[19px] font-semibold tracking-[-0.015em] text-slate-950 dark:text-slate-50">{{ step.title }}</h3>
              <p class="mt-2 max-w-2xl text-[15px] leading-7 text-slate-600 dark:text-slate-300">{{ step.description }}</p>

              <div v-for="(snippet, sIdx) in stepSnippets(step)" :key="`${idx}-${sIdx}-${snippet.label}`" class="mt-4 max-w-3xl overflow-hidden rounded-2xl border border-slate-200 bg-[#f8f9fb] dark:border-dark-700 dark:bg-dark-900">
                <div class="flex items-center justify-between gap-2 border-b border-slate-200 px-4 py-2.5 dark:border-dark-700">
                  <span class="min-w-0 truncate font-mono text-xs text-slate-600 dark:text-slate-300">{{ snippet.path || snippet.label }}</span>
                  <button
                    type="button"
                    :data-testid="`agent-copy-step-${idx}-${sIdx}`"
                    class="inline-flex shrink-0 items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium text-slate-600 hover:bg-white hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800 dark:hover:text-white"
                    @click="copyText(`manual-${idx}-${sIdx}`, snippet.code)"
                  >
                    <Icon :name="copiedId === `manual-${idx}-${sIdx}` ? 'check' : 'copy'" size="xs" />
                    <span>{{ copiedId === `manual-${idx}-${sIdx}` ? t('docs.copied') : t('docs.copy') }}</span>
                  </button>
                </div>
                <pre class="overflow-x-auto p-5 font-mono text-[13.5px] leading-7 text-slate-800 dark:text-slate-200"><code>{{ snippet.code }}</code></pre>
                <p v-if="snippet.note" class="border-t border-slate-200 px-5 py-3 text-[13px] leading-6 text-slate-500 dark:border-dark-700 dark:text-slate-400">
                  {{ snippet.note }}
                </p>
              </div>
            </li>
          </ol>
        </div>
      </section>

      <!-- Verify & Example Prompts -->
      <section id="verify-connection" class="mb-16 scroll-mt-36">
        <h2 class="text-[24px] font-semibold leading-[1.2] tracking-[-0.025em] text-slate-950 dark:text-slate-50 sm:text-[28px]">
          {{ t('docs.agentSetup.verifyTitle') }}
        </h2>
        <p class="mt-2.5 max-w-2xl text-[15px] leading-7 text-slate-600 dark:text-slate-300">
          {{ t('docs.agentSetup.verifyDesc') }}
        </p>

        <div v-if="activeVerifyCommand" class="mt-5 mb-8 max-w-3xl overflow-hidden rounded-2xl border border-slate-200 bg-[#f8f9fb] dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-center justify-between gap-2 border-b border-slate-200 px-4 py-2.5 dark:border-dark-700">
            <span class="font-mono text-xs text-slate-500">{{ currentOsLabel }}</span>
            <button
              type="button"
              data-testid="agent-copy-verify"
              class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium text-slate-600 hover:bg-white hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800 dark:hover:text-white"
              @click="copyText('agent-verify', activeVerifyCommand)"
            >
              <Icon :name="copiedId === 'agent-verify' ? 'check' : 'copy'" size="xs" />
              <span>{{ copiedId === 'agent-verify' ? t('docs.copied') : t('docs.copy') }}</span>
            </button>
          </div>
          <pre class="overflow-x-auto p-5 font-mono text-[13.5px] leading-7 text-slate-800 dark:text-slate-200"><code>{{ activeVerifyCommand }}</code></pre>
        </div>

        <h3 class="mb-3.5 text-[18px] font-semibold text-slate-950 dark:text-slate-50">
          {{ t('docs.agentSetup.examplePromptsTitle') }}
        </h3>
        <div class="max-w-3xl space-y-3">
          <div
            v-for="(prompt, pIdx) in activeAgent.examplePrompts"
            :key="pIdx"
            class="flex items-start justify-between gap-4 rounded-2xl border border-slate-200 bg-white px-5 py-4 dark:border-dark-700 dark:bg-dark-900/60"
          >
            <p class="text-[15px] leading-7 text-slate-700 dark:text-slate-200">{{ prompt }}</p>
            <button
              type="button"
              class="inline-flex shrink-0 items-center gap-1 rounded-full p-2 text-slate-500 hover:bg-slate-100 hover:text-slate-950 dark:text-slate-400 dark:hover:bg-dark-800 dark:hover:text-white"
              :aria-label="t('docs.copy')"
              @click="copyText(`prompt-${pIdx}`, prompt)"
            >
              <Icon :name="copiedId === `prompt-${pIdx}` ? 'check' : 'copy'" size="xs" />
            </button>
          </div>
        </div>

        <div v-if="activeAgent.tips.length" class="mt-7 max-w-3xl rounded-2xl border border-blue-200/80 bg-blue-50/50 p-5 dark:border-blue-800/70 dark:bg-blue-500/10">
          <h3 class="text-[15px] font-semibold text-blue-900 dark:text-blue-200">{{ t('docs.agentSetup.tipsTitle') }}</h3>
          <ul class="mt-2.5 list-disc space-y-2 pl-5 text-[15px] leading-7 text-blue-800 dark:text-blue-200/90">
            <li v-for="tip in activeAgent.tips" :key="tip">{{ tip }}</li>
          </ul>
        </div>
      </section>

      <!-- FAQ -->
      <section v-if="activeAgent.faq.length" id="agent-faq" class="mb-14 scroll-mt-36">
        <h2 class="mb-5 text-[24px] font-semibold leading-[1.2] tracking-[-0.025em] text-slate-950 dark:text-slate-50 sm:text-[28px]">
          {{ t('docs.agentSetup.faqTitle') }}
        </h2>
        <div class="max-w-3xl space-y-3">
          <details
            v-for="item in activeAgent.faq"
            :key="item.question"
            class="agent-details group rounded-2xl border border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/50"
          >
            <summary class="flex cursor-pointer list-none items-center justify-between gap-4 px-5 py-4 text-[16px] font-medium text-slate-900 dark:text-slate-100">
              <span>{{ item.question }}</span>
              <Icon name="chevronDown" size="sm" class="shrink-0 text-slate-400 transition-transform group-open:rotate-180" />
            </summary>
            <div class="border-t border-slate-100 px-5 py-4 text-[15px] leading-7 text-slate-600 dark:border-dark-800 dark:text-slate-300">
              {{ item.answer }}
            </div>
          </details>
        </div>
      </section>

      <!-- Troubleshooting -->
      <section v-if="activeAgent.troubleshooting.length" id="agent-troubleshooting" class="mb-14 scroll-mt-36">
        <h2 class="mb-5 text-[24px] font-semibold leading-[1.2] tracking-[-0.025em] text-slate-950 dark:text-slate-50 sm:text-[28px]">
          {{ t('docs.agentSetup.detailTroubleshootingTitle') }}
        </h2>
        <div class="max-w-3xl space-y-3">
          <details
            v-for="item in activeAgent.troubleshooting"
            :key="item.question"
            class="agent-details group rounded-2xl border border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/50"
          >
            <summary class="flex cursor-pointer list-none items-center justify-between gap-4 px-5 py-4 text-[16px] font-medium text-slate-900 dark:text-slate-100">
              <span>{{ item.question }}</span>
              <Icon name="chevronDown" size="sm" class="shrink-0 text-slate-400 transition-transform group-open:rotate-180" />
            </summary>
            <div class="border-t border-slate-100 px-5 py-4 text-[15px] leading-7 text-slate-600 dark:border-dark-800 dark:text-slate-300">
              {{ item.answer }}
            </div>
          </details>
        </div>
      </section>

      <!-- Other Agents -->
      <section id="other-agents" class="mb-4 scroll-mt-36 border-t border-slate-200 pt-10 dark:border-dark-700">
        <h2 class="mb-6 text-[24px] font-semibold leading-[1.2] tracking-[-0.025em] text-slate-950 dark:text-slate-50 sm:text-[28px]">
          {{ t('docs.agentSetup.otherAgentsTitle') }}
        </h2>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <router-link
            v-for="other in otherAgents"
            :key="other.id"
            :to="agentLocation(other.id)"
            class="agent-card-interactive group flex min-w-0 flex-col justify-between rounded-2xl border border-slate-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900/50"
          >
            <div class="min-w-0">
              <div class="flex min-w-0 items-center gap-3">
                <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-slate-200 bg-slate-50 dark:border-dark-700 dark:bg-dark-950">
                  <AgentBrandIcon :icon-key="other.iconKey" :platform="other.platform" size="md" />
                </span>
                <p class="truncate text-[16px] font-semibold text-slate-900 dark:text-slate-100">{{ other.name }}</p>
              </div>
              <p class="mt-3 line-clamp-2 text-[14px] leading-6 text-slate-500 dark:text-slate-400">{{ other.summary }}</p>
            </div>
            <span class="mt-4 inline-flex items-center gap-1.5 text-[14px] font-medium text-blue-600 dark:text-blue-400">
              <span>{{ t('docs.agentSetup.viewGuide') }}</span>
              <Icon name="arrowRight" size="xs" class="transition-transform group-hover:translate-x-0.5" />
            </span>
          </router-link>
        </div>
      </section>
    </template>

    <!-- ==================== OVERVIEW: ANTIGRAVITY-INSPIRED SPACIOUS SHOWCASE ==================== -->
    <template v-else>
      <header class="mb-16 pb-2">
        <p class="text-[14px] font-medium text-blue-600 dark:text-blue-400">
          {{ t('docs.category.integrations') }}
        </p>
        <h1 class="mt-3 text-balance text-[36px] font-semibold leading-[1.12] tracking-[-0.03em] text-slate-950 dark:text-slate-50 sm:text-[46px]">
          {{ t('docs.agentSetup.title') }}
        </h1>
        <p class="mt-5 max-w-2xl text-[17px] leading-[1.68] text-slate-600 dark:text-slate-300 sm:text-[19px]">
          {{ t('docs.agentSetup.subtitle') }}
        </p>

        <!-- Clean Pill Primary Actions (Antigravity Style) -->
        <div class="mt-8 flex flex-wrap items-center gap-3.5" data-testid="agent-quick-actions">
          <router-link
            :to="keysActionPath"
            data-testid="agent-overview-keys-cta"
            class="inline-flex min-h-12 items-center gap-2.5 rounded-full bg-slate-950 px-7 py-3 text-[15px] font-medium text-white transition hover:bg-slate-800 dark:bg-white dark:text-slate-950 dark:hover:bg-slate-200"
          >
            <Icon name="key" size="sm" />
            <span>{{ isAuthenticated ? t('docs.agentSetup.openKeys') : t('docs.agentSetup.signInForKey') }}</span>
          </router-link>
          <a
            :href="catalog.ccSwitchUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex min-h-12 items-center gap-2.5 rounded-full border border-slate-300 bg-white px-6 py-3 text-[15px] font-medium text-slate-800 transition hover:border-slate-400 hover:bg-slate-50 dark:border-dark-600 dark:bg-dark-900 dark:text-slate-100 dark:hover:bg-dark-800"
          >
            <AgentBrandIcon icon-key="ccswitch" size="sm" />
            <span>{{ t('docs.agentSetup.downloadCcSwitch') }}</span>
            <Icon name="externalLink" size="xs" />
          </a>
        </div>

        <!-- Quiet Inline Secondary Links -->
        <div class="mt-5 flex flex-wrap items-center gap-x-6 gap-y-2.5 text-[14px] text-slate-500 dark:text-slate-400">
          <router-link
            to="/model-plaza"
            data-testid="agent-quick-plaza"
            class="inline-flex items-center gap-1.5 transition hover:text-slate-950 dark:hover:text-white"
          >
            <Icon name="grid" size="xs" />
            <span>{{ t('docs.agentSetup.browseModelPlaza') }}</span>
          </router-link>
          <router-link
            :to="usageActionPath"
            data-testid="agent-quick-usage"
            class="inline-flex items-center gap-1.5 transition hover:text-slate-950 dark:hover:text-white"
          >
            <Icon name="chartBar" size="xs" />
            <span>{{ t('docs.agentSetup.openUsageRecords') }}</span>
          </router-link>
          <button
            type="button"
            data-testid="agent-copy-gateway"
            class="inline-flex items-center gap-1.5 transition hover:text-slate-950 dark:hover:text-white"
            :title="t('docs.copy')"
            @click="copyText('overview-origin', catalog.origin)"
          >
            <Icon :name="copiedId === 'overview-origin' ? 'check' : 'copy'" size="xs" />
            <span>{{ copiedId === 'overview-origin' ? t('docs.copied') : t('docs.agentSetup.copyGatewayUrl') }}</span>
            <code class="hidden font-mono text-xs text-slate-400 dark:text-slate-500 sm:inline">{{ catalog.origin }}</code>
          </button>
        </div>
      </header>

      <!-- Section 1: Interactive 4-Step Showcase Stage -->
      <section
        id="site-ccswitch-workflow"
        data-testid="site-ccswitch-workflow"
        class="mb-20 scroll-mt-36"
      >
        <div class="flex flex-wrap items-end justify-between gap-4">
          <div>
            <h2 class="text-[26px] font-semibold leading-[1.18] tracking-[-0.025em] text-slate-950 dark:text-slate-50 sm:text-[32px]">
              {{ t('docs.agentSetup.workflowSectionTitle') }}
            </h2>
            <p class="mt-2 text-[15px] leading-7 text-slate-500 dark:text-slate-400">
              {{ t('docs.agentSetup.workflowSectionSubtitle') }}
            </p>
          </div>
        </div>

        <div class="mt-8 grid grid-cols-1 gap-7 lg:grid-cols-12 lg:items-start">
          <!-- Left: 4 Spacious Interactive Step Selectors -->
          <ol class="space-y-3 lg:col-span-5">
            <li
              v-for="(step, idx) in catalog.siteWorkflowSteps"
              :key="step.step"
              class="min-w-0"
            >
              <button
                type="button"
                :data-testid="`workflow-step-${idx + 1}`"
                :aria-pressed="activeWorkflowStep === idx"
                class="group flex w-full items-start gap-4 rounded-2xl border p-5 text-left transition"
                :class="activeWorkflowStep === idx
                  ? 'border-blue-500/70 bg-blue-50/55 shadow-xs dark:border-blue-400/50 dark:bg-blue-500/[0.09]'
                  : 'border-slate-200/90 bg-white hover:border-slate-300 dark:border-dark-700/90 dark:bg-dark-900/55 dark:hover:border-dark-600'"
                @click="activeWorkflowStep = idx"
              >
                <span
                  class="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-full font-mono text-xs font-semibold transition"
                  :class="activeWorkflowStep === idx
                    ? 'bg-blue-600 text-white dark:bg-blue-500'
                    : 'bg-slate-100 text-slate-500 dark:bg-dark-800 dark:text-slate-400'"
                >
                  {{ step.step }}
                </span>
                <div class="min-w-0 flex-1">
                  <div class="flex items-center justify-between gap-2">
                    <h3 class="text-[17px] font-semibold leading-6 text-slate-950 dark:text-slate-50">
                      {{ step.title }}
                    </h3>
                  </div>
                  <p
                    class="mt-1.5 text-[14px] leading-6 transition-colors"
                    :class="activeWorkflowStep === idx ? 'text-slate-700 dark:text-slate-200' : 'text-slate-500 dark:text-slate-400'"
                  >
                    {{ step.description }}
                  </p>
                </div>
              </button>
            </li>
          </ol>

          <!-- Right: Single Large Focused Real Screenshot Stage -->
          <div class="min-w-0 lg:sticky lg:top-32 lg:col-span-7">
            <div class="rounded-3xl border border-slate-200/90 bg-slate-50/80 p-4 dark:border-dark-700 dark:bg-dark-900/80 dark:shadow-[inset_0_1px_0_rgba(255,255,255,0.04)] sm:p-5">
              <div class="mb-3.5 flex flex-wrap items-center justify-between gap-3 px-1">
                <span class="font-mono text-xs font-medium tracking-wide text-slate-500 dark:text-slate-400">
                  STEP {{ activeWorkflowItem.step }} · {{ activeWorkflowItem.where }}
                </span>

                <!-- Step 3 receipt vs modal toggle -->
                <div
                  v-if="activeWorkflowStep === 2"
                  class="inline-flex rounded-full border border-slate-200 bg-white p-0.5 dark:border-dark-700 dark:bg-dark-950"
                  role="tablist"
                >
                  <button
                    type="button"
                    role="tab"
                    data-testid="step3-shot-tab-receipt"
                    :aria-selected="step3ShotTab === 'receipt'"
                    class="rounded-full px-3 py-1 text-xs font-medium transition"
                    :class="step3ShotTab === 'receipt'
                      ? 'bg-slate-900 text-white dark:bg-white dark:text-slate-950'
                      : 'text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-white'"
                    @click="step3ShotTab = 'receipt'"
                  >
                    {{ t('docs.agentSetup.tabReceiptShot') }}
                  </button>
                  <button
                    type="button"
                    role="tab"
                    data-testid="step3-shot-tab-modal"
                    :aria-selected="step3ShotTab === 'modal'"
                    class="rounded-full px-3 py-1 text-xs font-medium transition"
                    :class="step3ShotTab === 'modal'
                      ? 'bg-slate-900 text-white dark:bg-white dark:text-slate-950'
                      : 'text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-white'"
                    @click="step3ShotTab = 'modal'"
                  >
                    {{ t('docs.agentSetup.tabModalShot') }}
                  </button>
                </div>
              </div>

              <figure>
                <button
                  type="button"
                  data-testid="overview-active-shot"
                  class="group relative block w-full overflow-hidden rounded-2xl border border-slate-200 bg-white text-left transition hover:border-blue-400 dark:border-dark-700 dark:bg-slate-950"
                  @click="openLightbox(activeStageShot)"
                >
                  <img
                    :key="activeStageShot.src"
                    :src="activeStageShot.src"
                    :alt="activeStageShot.alt"
                    class="stage-shot-enter block aspect-[16/10] w-full object-cover"
                    :class="[
                      activeWorkflowStep === 2 ? 'object-center' : 'object-top',
                      activeWorkflowStep === 0 ? '' : 'agent-shot-light',
                    ]"
                  />
                  <span class="absolute bottom-3 right-3 inline-flex items-center gap-1.5 rounded-full bg-slate-900/80 px-3 py-1 text-xs font-medium text-white backdrop-blur-xs">
                    <Icon name="search" size="xs" />
                    {{ t('docs.agentSetup.screenshotZoomHint') }}
                  </span>
                </button>
                <figcaption class="mt-4 flex flex-wrap items-center justify-between gap-4 px-1">
                  <span class="min-w-0 flex-1 text-[14px] leading-6 text-slate-600 dark:text-slate-300">
                    {{ activeStageShot.caption }}
                  </span>
                  <a
                    v-if="activeWorkflowStep === 0"
                    :href="catalog.ccSwitchUrl"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="inline-flex min-h-9 shrink-0 items-center gap-1.5 rounded-full border border-slate-300 bg-white px-4 py-1.5 text-[13px] font-medium text-slate-800 transition hover:bg-slate-50 dark:border-dark-600 dark:bg-dark-950 dark:text-slate-100 dark:hover:bg-dark-800"
                  >
                    <span>{{ t('docs.agentSetup.downloadCcSwitch') }}</span>
                    <Icon name="externalLink" size="xs" />
                  </a>
                  <router-link
                    v-else-if="activeWorkflowStep === 1 || activeWorkflowStep === 2"
                    :to="keysActionPath"
                    class="inline-flex min-h-9 shrink-0 items-center gap-1.5 rounded-full bg-blue-600 px-4 py-1.5 text-[13px] font-medium text-white transition hover:bg-blue-700"
                  >
                    <Icon name="key" size="xs" />
                    <span>{{ isAuthenticated ? t('docs.agentSetup.openKeys') : t('docs.agentSetup.signInForKey') }}</span>
                  </router-link>
                  <router-link
                    v-else
                    :to="usageActionPath"
                    class="inline-flex min-h-9 shrink-0 items-center gap-1.5 rounded-full border border-slate-300 bg-white px-4 py-1.5 text-[13px] font-medium text-slate-800 transition hover:bg-slate-50 dark:border-dark-600 dark:bg-dark-950 dark:text-slate-100 dark:hover:bg-dark-800"
                  >
                    <Icon name="chartBar" size="xs" />
                    <span>{{ t('docs.agentSetup.openUsageRecords') }}</span>
                  </router-link>
                </figcaption>
              </figure>
            </div>
          </div>
        </div>
      </section>

      <!-- Section 2: Pick your agent (Airy 2-Column Cards) -->
      <section id="pick-your-agent" class="mb-20 scroll-mt-36">
        <div class="flex flex-wrap items-end justify-between gap-4">
          <div>
            <h2 class="text-[26px] font-semibold leading-[1.18] tracking-[-0.025em] text-slate-950 dark:text-slate-50 sm:text-[32px]">
              {{ t('docs.agentSetup.pickTitle') }}
            </h2>
            <p class="mt-2 text-[15px] leading-7 text-slate-500 dark:text-slate-400">
              {{ t('docs.agentSetup.pickDescription') }}
            </p>
          </div>
          <span class="text-sm tabular-nums text-slate-400 dark:text-slate-500">
            {{ t('docs.agentSetup.matchedCount', { count: filteredAgents.length, total: catalog.agents.length }) }}
          </span>
        </div>

        <!-- Spacious Rounded-Full Filter Pills -->
        <div class="mt-6 flex flex-wrap items-center gap-2.5" role="group" :aria-label="t('docs.agentSetup.filterByWorkflow')">
          <button
            v-for="filter in workflowFilters"
            :key="filter.key"
            type="button"
            :data-testid="`agent-filter-${filter.key}`"
            :aria-pressed="activeFilter === filter.key"
            class="inline-flex min-h-10 items-center gap-2 rounded-full border px-4.5 py-2 text-[14px] font-medium transition"
            :class="activeFilter === filter.key
              ? 'border-slate-950 bg-slate-950 text-white dark:border-white dark:bg-white dark:text-slate-950'
              : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300 hover:text-slate-950 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-300 dark:hover:text-white'"
            @click="activeFilter = filter.key"
          >
            <span>{{ filter.label }}</span>
            <span class="tabular-nums opacity-65">{{ filter.count }}</span>
          </button>
        </div>

        <!-- Agent cards grid: zero clutter, generous spacing -->
        <div v-if="filteredAgents.length" class="mt-7 grid grid-cols-1 gap-5 sm:grid-cols-2 2xl:grid-cols-3" data-testid="agent-card-grid">
          <router-link
            v-for="agent in filteredAgents"
            :key="agent.id"
            :to="agentLocation(agent.id)"
            :data-testid="`agent-card-${agent.id}`"
            class="agent-card-interactive group flex min-w-0 flex-col justify-between rounded-2xl border border-slate-200/90 bg-white p-6 dark:border-dark-700 dark:bg-dark-900/70 dark:shadow-[inset_0_1px_0_rgba(255,255,255,0.04)] sm:p-7"
          >
            <div class="min-w-0">
              <div class="flex items-center justify-between gap-3">
                <div class="flex min-w-0 items-center gap-3.5">
                  <span class="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl border border-slate-200 bg-slate-50 dark:border-dark-700 dark:bg-dark-950">
                    <AgentBrandIcon :icon-key="agent.iconKey" :platform="agent.platform" size="md" />
                  </span>
                  <h3 class="truncate text-[18px] font-semibold tracking-[-0.015em] text-slate-950 dark:text-slate-50">
                    {{ agent.name }}
                  </h3>
                </div>
                <span
                  v-if="agent.ccSwitch.supported"
                  class="shrink-0 rounded-full bg-emerald-50 px-2.5 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-500/12 dark:text-emerald-300"
                >
                  {{ t('docs.agentSetup.ccsImportBadge') }}
                </span>
              </div>

              <p class="mt-4 text-[15px] leading-7 text-slate-600 dark:text-slate-300">
                {{ agent.summary }}
              </p>
            </div>

            <div class="mt-5 flex items-center justify-between gap-3">
              <span class="inline-flex items-center gap-1.5 text-[14px] font-medium text-blue-600 dark:text-blue-400">
                <span>{{ t('docs.agentSetup.viewGuide') }}</span>
                <Icon name="arrowRight" size="xs" class="transition-transform group-hover:translate-x-0.5" />
              </span>
            </div>
          </router-link>
        </div>

        <div v-else class="mt-7 rounded-2xl border border-dashed border-slate-300 p-10 text-center dark:border-dark-700">
          <p class="text-[15px] text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.noMatchingAgents') }}</p>
          <button
            type="button"
            data-testid="agent-clear-filter"
            class="mt-4 rounded-full border border-slate-200 px-5 py-2 text-[14px] font-medium text-slate-700 hover:bg-slate-50 dark:border-dark-700 dark:text-slate-200 dark:hover:bg-dark-800"
            @click="activeFilter = 'all'"
          >
            {{ t('docs.agentSetup.clearFilter') }}
          </button>
        </div>
      </section>

      <!-- Section 3: Collapsible Reference Table & FAQ -->
      <section id="troubleshooting" class="mb-6 scroll-mt-36">
        <h2 class="mb-6 text-[26px] font-semibold leading-[1.18] tracking-[-0.025em] text-slate-950 dark:text-slate-50 sm:text-[32px]">
          {{ t('docs.agentSetup.troubleshootingTitle') }}
        </h2>

        <div class="space-y-3">
          <!-- Collapsible Quick-Reference Compare Table -->
          <details class="agent-details group rounded-2xl border border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/55">
            <summary class="flex cursor-pointer list-none items-center justify-between gap-4 px-6 py-4.5 text-[16px] font-medium text-slate-950 dark:text-slate-100">
              <span>{{ t('docs.agentSetup.compareTitle') }}</span>
              <Icon name="chevronDown" size="sm" class="shrink-0 text-slate-400 transition-transform group-open:rotate-180" />
            </summary>
            <div class="overflow-x-auto border-t border-slate-200 dark:border-dark-700">
              <table class="w-full min-w-[760px] border-collapse text-left text-[14px]" data-testid="agent-compare-table">
                <thead class="bg-slate-50 text-xs text-slate-500 dark:bg-dark-900 dark:text-slate-400">
                  <tr>
                    <th class="whitespace-nowrap px-6 py-4 font-medium">{{ t('docs.agentSetup.colAgent') }}</th>
                    <th class="whitespace-nowrap px-6 py-4 font-medium">{{ t('docs.agentSetup.colSiteGroup') }}</th>
                    <th class="whitespace-nowrap px-6 py-4 font-medium">{{ t('docs.agentSetup.colCcSwitch') }}</th>
                    <th class="whitespace-nowrap px-6 py-4 font-medium">{{ t('docs.agentSetup.colBaseUrl') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="agent in catalog.agents"
                    :key="agent.id"
                    class="border-t border-slate-200 transition hover:bg-slate-50/70 dark:border-dark-700 dark:hover:bg-dark-900/50"
                  >
                    <td class="whitespace-nowrap px-6 py-4 font-medium">
                      <router-link :to="agentLocation(agent.id)" class="inline-flex items-center gap-2.5 whitespace-nowrap text-blue-600 hover:underline dark:text-blue-400">
                        <AgentBrandIcon :icon-key="agent.iconKey" :platform="agent.platform" size="sm" />
                        <span>{{ agent.name }}</span>
                      </router-link>
                    </td>
                    <td class="whitespace-nowrap px-6 py-4 text-slate-600 dark:text-slate-300">
                      {{ agent.siteGroupAdvice }}
                    </td>
                    <td class="whitespace-nowrap px-6 py-4 text-[13px]">
                      <span
                        v-if="agent.ccSwitch.supported"
                        class="inline-flex items-center gap-1 whitespace-nowrap font-medium text-emerald-700 dark:text-emerald-400"
                      >
                        <Icon name="bolt" size="xs" />
                        {{ agent.ccSwitch.appIds.join(', ') }}
                      </span>
                      <span v-else class="whitespace-nowrap text-slate-400">{{ t('docs.agentSetup.inAppSettings') }}</span>
                    </td>
                    <td class="whitespace-nowrap px-6 py-4">
                      <code class="whitespace-nowrap font-mono text-[13px] text-slate-600 dark:text-slate-300">{{ agent.baseUrl }}</code>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </details>

          <details
            v-for="item in catalog.globalTroubleshooting"
            :key="item.question"
            class="agent-details group rounded-2xl border border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/55"
          >
            <summary class="flex cursor-pointer list-none items-center justify-between gap-4 px-6 py-4.5 text-[16px] font-medium text-slate-950 dark:text-slate-100">
              <span>{{ item.question }}</span>
              <Icon name="chevronDown" size="sm" class="shrink-0 text-slate-400 transition-transform group-open:rotate-180" />
            </summary>
            <div class="border-t border-slate-100 px-6 py-4 text-[15px] leading-7 text-slate-600 dark:border-dark-800 dark:text-slate-300">
              {{ item.answer }}
            </div>
          </details>
        </div>
      </section>
    </template>

    <!-- Fullscreen Screenshot Lightbox -->
    <Teleport to="body">
      <Transition name="agent-lightbox">
        <div
          v-if="lightboxShot"
          data-testid="agent-screenshot-lightbox"
          role="dialog"
          aria-modal="true"
          :aria-label="lightboxShot.alt"
          class="agent-lightbox-backdrop fixed inset-0 z-70 flex items-center justify-center bg-black/80 p-4 backdrop-blur-sm sm:p-8"
          @click.self="closeLightbox"
        >
          <div class="agent-lightbox-panel relative max-h-[90vh] w-full max-w-5xl overflow-hidden rounded-2xl border border-dark-700 bg-dark-900 shadow-2xl">
            <div class="flex items-center justify-between gap-4 border-b border-dark-700 px-5 py-3.5">
              <p class="min-w-0 truncate text-[14px] font-medium text-slate-200">{{ lightboxShot.caption }}</p>
              <button
                type="button"
                data-testid="agent-lightbox-close"
                class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-slate-400 transition-colors duration-150 hover:bg-dark-800 hover:text-white active:scale-[0.95]"
                :aria-label="t('docs.agentSetup.closePreview')"
                @click="closeLightbox"
              >
                <Icon name="x" size="sm" />
              </button>
            </div>
            <div class="max-h-[calc(90vh-58px)] overflow-auto bg-dark-950 p-3">
              <img
                :src="lightboxShot.src"
                :alt="lightboxShot.alt"
                class="mx-auto block max-h-[80vh] w-auto rounded-xl object-contain"
              />
            </div>
          </div>
        </div>
      </Transition>
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
const activeWorkflowStep = ref(0)
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

const activeWorkflowItem = computed(
  () => catalog.value.siteWorkflowSteps[activeWorkflowStep.value] ?? catalog.value.siteWorkflowSteps[0],
)

const activeStageShot = computed<GuideShot>(() => {
  switch (activeWorkflowStep.value) {
    case 0:
      return guideShots.value.ccsApps
    case 1:
      return guideShots.value.createKey
    case 2:
      return activeStep3Shot.value
    default:
      return guideShots.value.usageRecords
  }
})

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
      items.push({ id: 'agent-troubleshooting', title: t('docs.agentSetup.detailTroubleshootingTitle') })
    }
    items.push({ id: 'other-agents', title: t('docs.agentSetup.otherAgentsTitle') })
    return items
  }
  return [
    { id: 'site-ccswitch-workflow', title: t('docs.agentSetup.workflowSectionTitle') },
    { id: 'pick-your-agent', title: t('docs.agentSetup.pickTitle') },
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

.stage-shot-enter {
  animation: stage-shot-fade 180ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)) both;
}

@keyframes stage-shot-fade {
  from {
    opacity: 0;
    transform: translateY(3px) scale(0.992);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

/* Tactile press feedback across buttons, tabs, filter pills and interactive cards (emil-design-eng). */
.agent-setup :is(button, a[data-testid^='agent-'], [role='tab']):not(:disabled) {
  transition:
    transform 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)),
    background-color 150ms ease,
    border-color 150ms ease,
    color 150ms ease,
    box-shadow 160ms ease;
}

.agent-setup :is(button, a[data-testid^='agent-'], [role='tab']):not(:disabled):active {
  transform: scale(0.97);
}

.agent-card-interactive {
  transition:
    transform 160ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)),
    border-color 150ms ease,
    background-color 150ms ease,
    box-shadow 160ms ease;
}

.agent-card-interactive:active {
  transform: scale(0.985);
}

@media (hover: hover) and (pointer: fine) {
  .agent-card-interactive:hover {
    transform: translateY(-2px);
    border-color: rgb(59 130 246 / 0.5);
    box-shadow: var(--app-shadow-card);
  }
}

@media (prefers-reduced-motion: reduce) {
  .stage-shot-enter {
    animation: none;
  }
  .agent-setup :is(button, a[data-testid^='agent-'], [role='tab']):active,
  .agent-card-interactive:hover,
  .agent-card-interactive:active {
    transform: none;
  }
}
</style>

<style>
/* Soften glaring white-background UI screenshots slightly in dark mode so they sit naturally on obsidian surfaces. */
.dark .agent-shot-light {
  filter: brightness(0.92) contrast(1.03);
}

/* Asymmetric enter/exit lightbox transition from scale(0.96), never scale(0). */
.agent-lightbox-enter-active {
  transition: opacity 200ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}
.agent-lightbox-leave-active {
  transition: opacity 140ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}
.agent-lightbox-enter-active .agent-lightbox-panel {
  transition:
    transform 220ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)),
    opacity 200ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}
.agent-lightbox-leave-active .agent-lightbox-panel {
  transition:
    transform 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)),
    opacity 140ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}
.agent-lightbox-enter-from,
.agent-lightbox-leave-to {
  opacity: 0;
}
.agent-lightbox-enter-from .agent-lightbox-panel {
  opacity: 0;
  transform: scale(0.96) translateY(6px);
}
.agent-lightbox-leave-to .agent-lightbox-panel {
  opacity: 0;
  transform: scale(0.97);
}

@media (prefers-reduced-motion: reduce) {
  .agent-lightbox-enter-from .agent-lightbox-panel,
  .agent-lightbox-leave-to .agent-lightbox-panel {
    transform: none;
  }
}
</style>
