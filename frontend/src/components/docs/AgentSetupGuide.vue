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
        class="mb-10 grid grid-cols-1 gap-4 rounded-3xl border border-slate-200 bg-slate-50/75 p-5 dark:border-dark-700 dark:bg-dark-900/70 md:grid-cols-3"
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
              <span>{{ copiedId === 'agent-base-url' ? t('docs.copied') : t('docs.agentSetup.copyValue') }}</span>
            </button>
          </div>
          <code class="mt-2 block truncate font-mono text-[14px] font-semibold text-slate-900 dark:text-slate-100">{{ activeAgent.baseUrl }}</code>
          <p class="mt-1.5 text-xs leading-5 text-slate-500 dark:text-slate-400">{{ activeAgent.baseUrlRule }}</p>
        </div>
      </section>

      <!-- Main Setup Section: Manual First (Default) + Compact CC Switch 1-Click Tab -->
      <section id="quick-start" class="mb-16 scroll-mt-36">
        <div class="mb-6 flex flex-wrap items-center justify-between gap-4">
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
              <span>{{ t('docs.agentSetup.modeCcSwitchShort') }}</span>
            </button>
          </div>
        </div>

        <!-- Global Beginner-Proof API Key Bar: auto-fills every field, snippet, verify command & CCS import -->
        <DocsApiKeyBar
          ref="keyBarRef"
          v-model="liveApiKey"
          v-model:masked="maskKeyDisplay"
          class="mb-7 max-w-3xl"
          :origin="catalog.origin"
          :is-authenticated="isAuthenticated"
          :agent-id="activeAgent.id"
          @models-loaded="onModelsLoaded"
          @key-meta="onKeyMeta"
        />

        <!-- Mode A (DEFAULT): Manual / In-App Setup with Per-Field Copyable Param Cards & Auto-Filled Snippets -->
        <div v-if="!activeAgent.ccSwitch.supported || setupMode === 'manual'" data-testid="agent-manual-panel" class="space-y-7">
          <!-- OS + Model controls -->
          <div class="flex max-w-3xl flex-wrap items-start justify-between gap-5 rounded-2xl border border-slate-200 bg-[#f8f9fb] p-5 dark:border-dark-700 dark:bg-dark-900/70">
            <div>
              <div class="flex items-center gap-2">
                <span class="text-xs font-medium text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.shellSelector') }}</span>
                <span v-if="osAutoDetected" class="rounded-full bg-slate-200/70 px-2 py-0.5 text-[10.5px] text-slate-600 dark:bg-dark-800 dark:text-slate-400">
                  {{ t('docs.agentSetup.beginnerOsAutoHint') }}
                </span>
              </div>
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
                  @click="selectOs(os.id)"
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
                  v-for="preset in displayedModelPresets"
                  :key="preset"
                  type="button"
                  class="rounded-full border px-3 py-1.5 font-mono text-xs transition"
                  :class="effectiveModel === preset
                    ? 'border-blue-500 bg-blue-50 text-blue-700 dark:border-blue-400 dark:bg-blue-500/15 dark:text-blue-300'
                    : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-300'"
                  @click="customModel = preset"
                >
                  {{ preset }}
                </button>
              </div>
              <p class="mt-2 text-xs text-slate-500 dark:text-slate-400">
                {{ t('docs.agentSetup.beginnerModelHint') }}
              </p>
            </div>
          </div>

          <ol class="space-y-9 border-l border-slate-200 pl-7 dark:border-dark-700">
            <li v-for="(step, idx) in activeAgent.manualSteps" :key="step.title" class="relative">
              <span class="absolute -left-[41px] top-0.5 flex h-7 w-7 items-center justify-center rounded-full border border-slate-300 bg-white font-mono text-xs font-semibold text-slate-700 dark:border-dark-600 dark:bg-dark-900 dark:text-slate-200">
                {{ idx + 1 }}
              </span>
              <h3 class="text-[19px] font-semibold tracking-[-0.015em] text-slate-950 dark:text-slate-50">{{ step.title }}</h3>
              <p class="mt-2 max-w-2xl text-[15px] leading-7 text-slate-600 dark:text-slate-300">{{ step.description }}</p>

              <!-- Structured GUI Form Parameter Cards (Per-Row Copy, Zero Useless Whole-Block "Copy Code") -->
              <div
                v-for="(card, cIdx) in step.paramCards ?? []"
                :key="`${idx}-card-${cIdx}-${card.title}`"
                :data-testid="`agent-param-card-${idx}-${cIdx}`"
                class="mt-4 max-w-3xl overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xs dark:border-dark-700 dark:bg-dark-900/75"
              >
                <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-200 bg-slate-50/85 px-4.5 py-3 dark:border-dark-700 dark:bg-dark-900">
                  <span class="text-[14px] font-semibold text-slate-900 dark:text-slate-100">{{ card.title }}</span>
                  <span
                    v-if="card.badge"
                    class="rounded-full bg-blue-50 px-2.5 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-500/15 dark:text-blue-300"
                  >
                    {{ card.badge }}
                  </span>
                </div>

                <div class="divide-y divide-slate-100 dark:divide-dark-800">
                  <div
                    v-for="(row, rIdx) in card.rows"
                    :key="`${cIdx}-row-${rIdx}-${row.label}`"
                    class="flex flex-col gap-2 px-4.5 py-3.5 sm:flex-row sm:items-center sm:justify-between sm:gap-4"
                  >
                    <div class="min-w-[165px] shrink-0">
                      <span class="text-[13.5px] font-medium text-slate-600 dark:text-slate-300">{{ row.label }}</span>
                      <p v-if="row.hint" class="mt-0.5 text-xs text-slate-400 dark:text-slate-500">{{ row.hint }}</p>
                    </div>

                    <!-- Special API Key row: never ask beginners to manually edit YOUR_API_KEY -->
                    <template v-if="row.kind === 'apiKey'">
                      <div v-if="hasLiveKey" class="flex min-w-0 flex-1 items-center justify-between gap-3">
                        <div class="flex min-w-0 items-center gap-2">
                          <span class="h-2 w-2 shrink-0 rounded-full bg-emerald-500" aria-hidden="true" />
                          <code
                            :data-testid="`param-apikey-value-${idx}-${cIdx}-${rIdx}`"
                            class="min-w-0 truncate font-mono text-[13.5px] font-medium text-slate-900 dark:text-slate-100"
                          >{{ displayApiKey }}</code>
                          <span class="hidden shrink-0 rounded-full bg-emerald-50 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300 sm:inline">
                            {{ t('docs.agentSetup.paramKeyAutoFilled') }}
                          </span>
                        </div>
                        <button
                          type="button"
                          :data-testid="`agent-copy-param-${idx}-${cIdx}-${rIdx}`"
                          class="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-slate-200 bg-slate-50 px-3 py-1.5 text-xs font-medium text-slate-700 transition hover:border-blue-300 hover:bg-blue-50 hover:text-blue-700 dark:border-dark-600 dark:bg-dark-800 dark:text-slate-200 dark:hover:border-blue-500/50 dark:hover:bg-blue-500/15 dark:hover:text-blue-300"
                          @click="copyText(`param-${idx}-${cIdx}-${rIdx}`, liveApiKey.trim())"
                        >
                          <Icon :name="copiedId === `param-${idx}-${cIdx}-${rIdx}` ? 'check' : 'copy'" size="xs" />
                          <span>{{ copiedId === `param-${idx}-${cIdx}-${rIdx}` ? t('docs.copied') : t('docs.agentSetup.copyValue') }}</span>
                        </button>
                      </div>

                      <div v-else class="flex min-w-0 flex-1 flex-wrap items-center justify-between gap-2.5">
                        <span class="text-xs text-amber-700 dark:text-amber-300">
                          {{ t('docs.agentSetup.paramKeyMissingHint') }}
                        </span>
                        <button
                          type="button"
                          :data-testid="`param-fill-key-btn-${idx}-${cIdx}-${rIdx}`"
                          class="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-amber-500/15 px-3.5 py-1.5 text-xs font-semibold text-amber-800 ring-1 ring-inset ring-amber-500/35 transition hover:bg-amber-500/25 dark:bg-amber-400/15 dark:text-amber-200 dark:ring-amber-400/35"
                          @click="requestFillKey"
                        >
                          <Icon name="key" size="xs" />
                          <span>{{ t('docs.agentSetup.paramKeyMissingBtn') }}</span>
                        </button>
                      </div>
                    </template>

                    <!-- Dropdown / non-copyable selection row -->
                    <template v-else-if="row.kind === 'select' || !row.copyable">
                      <div class="flex min-w-0 flex-1 items-center justify-between gap-3">
                        <code class="min-w-0 truncate rounded-lg bg-slate-100 px-2.5 py-1 font-mono text-[13px] font-medium text-slate-900 dark:bg-dark-800 dark:text-slate-100">
                          {{ row.value }}
                        </code>
                        <span class="shrink-0 rounded-full bg-slate-100 px-2.5 py-1 text-[11px] font-medium text-slate-500 dark:bg-dark-800 dark:text-slate-400">
                          {{ t('docs.agentSetup.paramSelectHint') }}
                        </span>
                      </div>
                    </template>

                    <!-- Individual copyable parameter row (URL / Model ID / etc.) -->
                    <template v-else>
                      <div class="flex min-w-0 flex-1 items-center justify-between gap-3">
                        <code class="min-w-0 truncate font-mono text-[13.5px] font-medium text-slate-900 dark:text-slate-100">
                          {{ row.value }}
                        </code>
                        <button
                          type="button"
                          :data-testid="`agent-copy-param-${idx}-${cIdx}-${rIdx}`"
                          class="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-slate-200 bg-slate-50 px-3 py-1.5 text-xs font-medium text-slate-700 transition hover:border-blue-300 hover:bg-blue-50 hover:text-blue-700 dark:border-dark-600 dark:bg-dark-800 dark:text-slate-200 dark:hover:border-blue-500/50 dark:hover:bg-blue-500/15 dark:hover:text-blue-300"
                          @click="copyText(`param-${idx}-${cIdx}-${rIdx}`, row.value)"
                        >
                          <Icon :name="copiedId === `param-${idx}-${cIdx}-${rIdx}` ? 'check' : 'copy'" size="xs" />
                          <span>{{ copiedId === `param-${idx}-${cIdx}-${rIdx}` ? t('docs.copied') : t('docs.agentSetup.copyValue') }}</span>
                        </button>
                      </div>
                    </template>
                  </div>
                </div>

                <p v-if="card.note" class="border-t border-slate-200 bg-slate-50/60 px-4.5 py-3 text-[13px] leading-6 text-slate-500 dark:border-dark-700 dark:bg-dark-900/60 dark:text-slate-400">
                  {{ card.note }}
                </p>
              </div>

              <!-- Real Pasteable Code / Config Snippets (JSON, TOML, Shell commands) -->
              <div
                v-for="(snippet, sIdx) in stepSnippets(step)"
                :key="`${idx}-${sIdx}-${snippet.label}`"
                class="mt-4 max-w-3xl overflow-hidden rounded-2xl border border-slate-200 bg-[#f8f9fb] dark:border-dark-700 dark:bg-dark-900"
              >
                <div class="flex items-center justify-between gap-2 border-b border-slate-200 px-4 py-2.5 dark:border-dark-700">
                  <span class="min-w-0 truncate font-mono text-xs text-slate-600 dark:text-slate-300">{{ snippet.path || snippet.label }}</span>
                  <button
                    type="button"
                    :data-testid="`agent-copy-step-${idx}-${sIdx}`"
                    class="inline-flex shrink-0 items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium text-slate-600 hover:bg-white hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800 dark:hover:text-white"
                    @click="copyText(`manual-${idx}-${sIdx}`, resolveCopyableCode(snippet.code))"
                  >
                    <Icon :name="copiedId === `manual-${idx}-${sIdx}` ? 'check' : 'copy'" size="xs" />
                    <span>{{ copiedId === `manual-${idx}-${sIdx}` ? t('docs.copied') : snippetCopyLabel(snippet) }}</span>
                  </button>
                </div>

                <!-- Gentle inline reminder if snippet still has unfilled API key placeholder -->
                <div
                  v-if="!hasLiveKey && snippet.code.includes('YOUR_API_KEY')"
                  class="flex flex-wrap items-center justify-between gap-2 border-b border-amber-200/70 bg-amber-50/75 px-4 py-2 text-xs text-amber-900 dark:border-amber-500/25 dark:bg-amber-500/10 dark:text-amber-200"
                >
                  <span>{{ t('docs.agentSetup.snippetKeyWarning') }}</span>
                  <button
                    type="button"
                    class="inline-flex shrink-0 items-center gap-1 rounded-full bg-amber-600 px-3 py-1 text-[11.5px] font-medium text-white transition hover:bg-amber-700"
                    @click="requestFillKey"
                  >
                    <Icon name="key" size="xs" />
                    <span>{{ t('docs.agentSetup.snippetFillKeyBtn') }}</span>
                  </button>
                </div>

                <pre class="overflow-x-auto p-5 font-mono text-[13.5px] leading-7 text-slate-800 dark:text-slate-200"><code><template v-for="(seg, segIdx) in splitCodeForHighlight(snippet.code)" :key="segIdx"><button v-if="seg.isPlaceholder" type="button" class="mx-0.5 inline rounded bg-amber-200/80 px-1.5 py-0.5 font-mono text-[12.5px] font-semibold text-amber-950 underline decoration-amber-600/60 underline-offset-2 transition hover:bg-amber-300 dark:bg-amber-500/25 dark:text-amber-200" @click="requestFillKey">{{ seg.text }}</button><template v-else>{{ seg.text }}</template></template></code></pre>
                <p v-if="snippet.note" class="border-t border-slate-200 px-5 py-3 text-[13px] leading-6 text-slate-500 dark:border-dark-700 dark:text-slate-400">
                  {{ snippet.note }}
                </p>
              </div>
            </li>
          </ol>
        </div>

        <!-- Mode B (SECONDARY): Compact Agent-Specific CC Switch 1-Click Panel (No Duplication with Overview) -->
        <div v-else data-testid="agent-ccswitch-panel" class="max-w-3xl space-y-6">
          <div class="rounded-2xl border border-slate-200 bg-white p-5 shadow-2xs dark:border-dark-700 dark:bg-dark-900/75">
            <div class="flex flex-wrap items-start justify-between gap-4">
              <div class="min-w-0 flex-1">
                <h3 class="text-[18px] font-semibold text-slate-950 dark:text-slate-50">
                  {{ t('docs.agentSetup.ccsCompactTitle', { agent: activeAgent.name }) }}
                </h3>
                <p class="mt-1.5 text-[14.5px] leading-6 text-slate-600 dark:text-slate-300">
                  {{ activeAgent.ccSwitch.installNote }}
                </p>
                <p class="mt-1.5 text-[13.5px] leading-6 text-slate-500 dark:text-slate-400">
                  {{ t('docs.agentSetup.ccsCompactDesc') }}
                </p>
              </div>
            </div>

            <div class="mt-5 flex flex-wrap items-center gap-3">
              <button
                type="button"
                data-testid="agent-direct-ccs-import"
                class="inline-flex min-h-10 items-center gap-2 rounded-full bg-blue-600 px-5 py-2 text-[14px] font-medium text-white shadow-xs transition hover:bg-blue-700"
                @click="triggerDirectCcsImport"
              >
                <Icon name="bolt" size="xs" />
                <span>{{ hasLiveKey ? t('docs.agentSetup.ccsDirectImportBtn') : t('docs.agentSetup.ccsDirectImportNeedKeyBtn') }}</span>
              </button>
              <router-link
                :to="{ path: '/docs', query: { page: 'agent-setup' }, hash: '#site-ccswitch-workflow' }"
                class="inline-flex min-h-10 items-center gap-1.5 rounded-full border border-slate-200 bg-white px-4 py-2 text-[13.5px] font-medium text-slate-700 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-200 dark:hover:bg-dark-800"
              >
                <span>{{ t('docs.agentSetup.ccsViewFullWalkthrough') }}</span>
                <Icon name="arrowRight" size="xs" />
              </router-link>
            </div>

            <p v-if="ccsImportFeedback" class="mt-3 text-xs font-medium text-emerald-700 dark:text-emerald-400" role="status">
              {{ ccsImportFeedback }}
            </p>
          </div>

          <!-- Interactive CC Switch Preview focused on this specific agent -->
          <CcSwitchInteractiveDemo
            :origin="catalog.origin"
            :site-name="siteName"
            :active-step="3"
            :focus-agent-id="activeAgent.id"
            :masked-key-preview="maskedKeyForDemo"
          />

          <!-- Launch Command -->
          <div class="overflow-hidden rounded-2xl border border-slate-200 bg-[#f8f9fb] dark:border-dark-700 dark:bg-dark-900">
            <div class="flex items-center justify-between border-b border-slate-200 px-4 py-2.5 dark:border-dark-700">
              <span class="font-mono text-xs text-slate-500">{{ t('docs.agentSetup.launchCommandLabel') }}</span>
              <button
                type="button"
                class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium text-slate-600 hover:bg-white hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800 dark:hover:text-white"
                @click="copyText('ccs-launch', activeAgent.launchCommand)"
              >
                <Icon :name="copiedId === 'ccs-launch' ? 'check' : 'copy'" size="xs" />
                <span>{{ copiedId === 'ccs-launch' ? t('docs.copied') : t('docs.agentSetup.copyCommand') }}</span>
              </button>
            </div>
            <pre class="overflow-x-auto p-4.5 font-mono text-[13.5px] leading-7 text-slate-800 dark:text-slate-200"><code>{{ activeAgent.launchCommand }}</code></pre>
          </div>

          <!-- Collapsible fallback if user needs manual fields in CC Switch -->
          <details class="agent-details group rounded-2xl border border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/50">
            <summary class="flex cursor-pointer list-none items-center justify-between gap-3 px-5 py-3.5 text-[14px] font-medium text-slate-700 dark:text-slate-200">
              <span>{{ t('docs.agentSetup.ccsManualFieldsToggle') }}</span>
              <Icon name="chevronDown" size="sm" class="shrink-0 text-slate-400 transition-transform group-open:rotate-180" />
            </summary>
            <div class="divide-y divide-slate-200 border-t border-slate-200 dark:divide-dark-700 dark:border-dark-700">
              <div
                v-for="row in ccSwitchFieldRows"
                :key="row.key"
                class="flex flex-wrap items-center justify-between gap-3 px-4.5 py-3 text-[13.5px]"
              >
                <span class="w-32 shrink-0 font-medium text-slate-600 dark:text-slate-300">{{ row.label }}</span>
                <template v-if="row.key === 'key' && !hasLiveKey">
                  <button
                    type="button"
                    class="inline-flex items-center gap-1.5 rounded-full bg-amber-500/15 px-3 py-1 text-xs font-medium text-amber-800 dark:text-amber-200"
                    @click="requestFillKey"
                  >
                    <Icon name="key" size="xs" />
                    <span>{{ t('docs.agentSetup.paramKeyMissingBtn') }}</span>
                  </button>
                </template>
                <template v-else>
                  <code class="min-w-0 flex-1 truncate font-mono text-[13px] text-slate-900 dark:text-slate-100">{{ row.value }}</code>
                  <button
                    v-if="row.copyable"
                    type="button"
                    class="inline-flex shrink-0 items-center gap-1 rounded-full px-2.5 py-1 text-xs text-slate-500 hover:bg-slate-100 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-dark-800 dark:hover:text-white"
                    :aria-label="t('docs.agentSetup.copyValue')"
                    @click="copyText(`ccs-field-${row.key}`, row.key === 'key' ? liveApiKey.trim() : row.value)"
                  >
                    <Icon :name="copiedId === `ccs-field-${row.key}` ? 'check' : 'copy'" size="xs" />
                    <span>{{ copiedId === `ccs-field-${row.key}` ? t('docs.copied') : t('docs.agentSetup.copyValue') }}</span>
                  </button>
                </template>
              </div>
            </div>
          </details>
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

        <!-- Beginner-Friendly 1-Click In-Browser Connectivity Test -->
        <div
          data-testid="agent-browser-verify"
          class="mt-5 mb-6 max-w-3xl rounded-2xl border border-slate-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900/75"
        >
          <div class="flex flex-wrap items-center justify-between gap-4">
            <div class="min-w-0 flex-1">
              <h3 class="text-[15px] font-semibold text-slate-950 dark:text-slate-50">
                {{ t('docs.agentSetup.verifyBrowserTestTitle') }}
              </h3>
              <p class="mt-1 text-[13.5px] leading-6 text-slate-600 dark:text-slate-300">
                {{ t('docs.agentSetup.verifyBrowserTestDesc') }}
              </p>
            </div>
            <button
              type="button"
              data-testid="agent-browser-verify-btn"
              :disabled="verifyRunning"
              class="inline-flex min-h-10 shrink-0 items-center gap-2 rounded-full px-5 py-2 text-[14px] font-medium transition disabled:opacity-60"
              :class="hasLiveKey
                ? 'bg-blue-600 text-white shadow-xs hover:bg-blue-700'
                : 'border border-amber-300 bg-amber-50 text-amber-900 hover:bg-amber-100 dark:border-amber-500/40 dark:bg-amber-500/15 dark:text-amber-200'"
              @click="hasLiveKey ? runBrowserConnectivityTest() : requestFillKey()"
            >
              <Icon :name="verifyRunning ? 'refresh' : hasLiveKey ? 'play' : 'key'" size="xs" :class="{ 'animate-spin': verifyRunning }" />
              <span>
                {{ verifyRunning ? t('docs.agentSetup.verifyBrowserTesting') : hasLiveKey ? t('docs.agentSetup.verifyBrowserTestBtn') : t('docs.agentSetup.verifyBrowserTestNeedKey') }}
              </span>
            </button>
          </div>

          <div
            v-if="verifyResult"
            data-testid="agent-browser-verify-result"
            class="mt-4 rounded-xl border px-4 py-3 text-[13.5px] leading-6"
            :class="verifyResult.ok
              ? 'border-emerald-200 bg-emerald-50/75 text-emerald-900 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-200'
              : 'border-rose-200 bg-rose-50/75 text-rose-900 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-200'"
            role="status"
          >
            {{ verifyResult.message }}
          </div>
        </div>

        <!-- Optional Terminal Verify Command (Cleanly Wrapped, Auto-Filled Key) -->
        <div v-if="activeVerifyCommand" class="mb-8 max-w-3xl">
          <p class="mb-2.5 text-xs font-medium text-slate-500 dark:text-slate-400">
            {{ t('docs.agentSetup.verifyTerminalFallbackLabel') }}
          </p>
          <div class="overflow-hidden rounded-2xl border border-slate-200 bg-[#f8f9fb] dark:border-dark-700 dark:bg-dark-900">
            <div class="flex items-center justify-between gap-2 border-b border-slate-200 px-4 py-2.5 dark:border-dark-700">
              <span class="font-mono text-xs text-slate-500">{{ currentOsLabel }}</span>
              <button
                type="button"
                data-testid="agent-copy-verify"
                class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium text-slate-600 hover:bg-white hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800 dark:hover:text-white"
                @click="copyText('agent-verify', resolveCopyableCode(activeVerifyCommand))"
              >
                <Icon :name="copiedId === 'agent-verify' ? 'check' : 'copy'" size="xs" />
                <span>{{ copiedId === 'agent-verify' ? t('docs.copied') : t('docs.agentSetup.copyCommand') }}</span>
              </button>
            </div>
            <div
              v-if="!hasLiveKey && activeVerifyCommand.includes('YOUR_API_KEY')"
              class="flex flex-wrap items-center justify-between gap-2 border-b border-amber-200/70 bg-amber-50/75 px-4 py-2 text-xs text-amber-900 dark:border-amber-500/25 dark:bg-amber-500/10 dark:text-amber-200"
            >
              <span>{{ t('docs.agentSetup.snippetKeyWarning') }}</span>
              <button
                type="button"
                class="inline-flex shrink-0 items-center gap-1 rounded-full bg-amber-600 px-3 py-1 text-[11.5px] font-medium text-white transition hover:bg-amber-700"
                @click="requestFillKey"
              >
                <Icon name="key" size="xs" />
                <span>{{ t('docs.agentSetup.snippetFillKeyBtn') }}</span>
              </button>
            </div>
            <pre class="whitespace-pre-wrap break-all p-5 font-mono text-[13.5px] leading-7 text-slate-800 dark:text-slate-200"><code><template v-for="(seg, segIdx) in splitCodeForHighlight(activeVerifyCommand)" :key="segIdx"><button v-if="seg.isPlaceholder" type="button" class="mx-0.5 inline rounded bg-amber-200/80 px-1.5 py-0.5 font-mono text-[12.5px] font-semibold text-amber-950 underline decoration-amber-600/60 underline-offset-2 transition hover:bg-amber-300 dark:bg-amber-500/25 dark:text-amber-200" @click="requestFillKey">{{ seg.text }}</button><template v-else>{{ seg.text }}</template></template></code></pre>
          </div>
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
              :aria-label="t('docs.agentSetup.copyValue')"
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

    <!-- ==================== OVERVIEW: INTERACTIVE CC SWITCH SHOWCASE (NO STATIC SCREENSHOTS) ==================== -->
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

        <!-- Clean Pill Primary Actions -->
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
            :title="t('docs.agentSetup.copyValue')"
            @click="copyText('overview-origin', catalog.origin)"
          >
            <Icon :name="copiedId === 'overview-origin' ? 'check' : 'copy'" size="xs" />
            <span>{{ copiedId === 'overview-origin' ? t('docs.copied') : t('docs.agentSetup.copyGatewayUrl') }}</span>
            <code class="hidden font-mono text-xs text-slate-400 dark:text-slate-500 sm:inline">{{ catalog.origin }}</code>
          </button>
        </div>
      </header>

      <!-- Section 1: Interactive 4-Step Walkthrough + Live CC Switch Simulator -->
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

        <div class="mt-7 space-y-5">
          <!-- Top: 4 Horizontal Interactive Step Cards (keeps simulator full-width like ccswitch.io) -->
          <ol class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <li
              v-for="(step, idx) in catalog.siteWorkflowSteps"
              :key="step.step"
              class="min-w-0"
            >
              <button
                type="button"
                :data-testid="`workflow-step-${idx + 1}`"
                :aria-pressed="activeWorkflowStep === idx"
                class="group flex h-full w-full flex-col rounded-2xl border p-4 text-left transition"
                :class="activeWorkflowStep === idx
                  ? 'border-blue-500/75 bg-blue-50/60 shadow-xs dark:border-blue-400/55 dark:bg-blue-500/[0.1]'
                  : 'border-slate-200/90 bg-white hover:border-slate-300 dark:border-dark-700/90 dark:bg-dark-900/55 dark:hover:border-dark-600'"
                @click="activeWorkflowStep = idx"
              >
                <div class="flex items-center gap-2.5">
                  <span
                    class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full font-mono text-xs font-semibold transition"
                    :class="activeWorkflowStep === idx
                      ? 'bg-blue-600 text-white dark:bg-blue-500'
                      : 'bg-slate-100 text-slate-500 dark:bg-dark-800 dark:text-slate-400'"
                  >
                    {{ step.step }}
                  </span>
                  <h3 class="min-w-0 text-[15px] font-semibold leading-5 text-slate-950 dark:text-slate-50">
                    {{ step.title }}
                  </h3>
                </div>
                <p
                  class="mt-2.5 text-[13px] leading-5.5 transition-colors"
                  :class="activeWorkflowStep === idx ? 'text-slate-700 dark:text-slate-200' : 'text-slate-500 dark:text-slate-400'"
                >
                  {{ step.description }}
                </p>
              </button>
            </li>
          </ol>

          <!-- Full-Width Live Interactive CC Switch Simulator (Zero Static Screenshots) -->
          <div class="min-w-0">
            <div class="rounded-3xl border border-slate-200/90 bg-gradient-to-b from-slate-50/95 to-slate-100/60 p-4 dark:border-dark-700 dark:from-dark-900/90 dark:to-dark-950/90 sm:p-5">
              <div class="mb-3 flex flex-wrap items-center justify-between gap-2 px-1">
                <span class="font-mono text-xs font-medium tracking-wide text-slate-500 dark:text-slate-400">
                  STEP {{ activeWorkflowItem.step }} · {{ activeWorkflowItem.where }}
                </span>
              </div>

              <CcSwitchInteractiveDemo
                :origin="catalog.origin"
                :site-name="siteName"
                :active-step="activeWorkflowStep"
                :masked-key-preview="maskedKeyForDemo"
                @step-change="activeWorkflowStep = $event"
              />

              <div class="mt-4 flex flex-wrap items-center justify-between gap-3 px-1">
                <span class="min-w-0 flex-1 text-[13.5px] leading-6 text-slate-600 dark:text-slate-300">
                  {{ t('docs.agentSetup.overviewDemoCaption') }}
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
              </div>
            </div>
          </div>
        </div>
      </section>

      <!-- Section 2: Pick your agent -->
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

        <!-- Agent cards grid -->
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
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import AgentBrandIcon from '@/components/docs/AgentBrandIcon.vue'
import CcSwitchInteractiveDemo from '@/components/docs/CcSwitchInteractiveDemo.vue'
import DocsApiKeyBar from '@/components/docs/DocsApiKeyBar.vue'
import { useClipboard } from '@/composables/useClipboard'
import {
  buildCcSwitchImportDeeplink,
  parseCcSwitchModelsList,
  pickCcSwitchModel,
} from '@/utils/ccswitchImport'
import {
  getAgentSetupCatalog,
  resolveAgentGuide,
  type AgentCodeSnippet,
  type AgentFilterKey,
  type AgentSetupStep,
  type AgentShellOs,
  type AgentWorkflow,
} from '@/data/agentSetupGuides'
import type { GroupPlatform } from '@/types'

export interface AgentTocItem {
  id: string
  title: string
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

const keyBarRef = ref<InstanceType<typeof DocsApiKeyBar> | null>(null)
const activeFilter = ref<AgentFilterKey>('all')
const activeWorkflowStep = ref(0)
const selectedOs = ref<AgentShellOs>('unix')
const osAutoDetected = ref(false)
const customModel = ref('')
// Specific agent detail pages always default to manual setup so they never duplicate the overview's CC Switch walkthrough.
const setupMode = ref<'manual' | 'ccswitch'>('manual')
const liveApiKey = ref('')
const maskKeyDisplay = ref(false)
const liveModels = ref<string[]>([])
const liveKeyPlatform = ref<GroupPlatform | undefined>(undefined)
const verifyRunning = ref(false)
const verifyResult = ref<{ ok: boolean; message: string } | null>(null)
const ccsImportFeedback = ref('')
const copiedId = ref('')
let copiedTimer: ReturnType<typeof setTimeout> | undefined
let ccsFeedbackTimer: ReturnType<typeof setTimeout> | undefined

const hasLiveKey = computed(() => liveApiKey.value.trim().length > 0)

const maskedKeyForDemo = computed(() => {
  const val = liveApiKey.value.trim()
  if (!val) return ''
  if (val.length <= 12) return '••••••••••••'
  return `${val.slice(0, 7)}••••••••${val.slice(-4)}`
})

const displayApiKey = computed(() => {
  const val = liveApiKey.value.trim()
  if (!val) return 'YOUR_API_KEY'
  return maskKeyDisplay.value ? maskedKeyForDemo.value : val
})

const catalog = computed(() =>
  getAgentSetupCatalog(locale.value, props.origin, customModel.value, displayApiKey.value),
)
const activeAgent = computed(() => resolveAgentGuide(catalog.value.agents, props.selectedAgentId))
const activeWorkflowItem = computed(
  () => catalog.value.siteWorkflowSteps[activeWorkflowStep.value] ?? catalog.value.siteWorkflowSteps[0],
)
const otherAgents = computed(() =>
  activeAgent.value ? catalog.value.agents.filter((agent) => agent.id !== activeAgent.value!.id) : [],
)

const keysActionPath = computed(() => (props.isAuthenticated ? '/keys' : '/login'))
const usageActionPath = computed(() => (props.isAuthenticated ? '/usage' : '/login'))
const overviewLocation = { path: '/docs', query: { page: 'agent-setup' } }
const agentLocation = (id: string) => ({ path: '/docs', query: { page: 'agent-setup', agent: id } })

const shellOptions: Array<{ id: AgentShellOs; label: string }> = [
  { id: 'unix', label: 'macOS / Linux' },
  { id: 'powershell', label: 'Windows PowerShell' },
  { id: 'cmd', label: 'Windows CMD' },
]

const currentOsLabel = computed(
  () => shellOptions.find((option) => option.id === selectedOs.value)?.label ?? 'macOS / Linux',
)

const effectiveModel = computed(() => customModel.value.trim() || activeAgent.value?.defaultModel || 'claude-sonnet-4-6')

const displayedModelPresets = computed(() => {
  if (!activeAgent.value) return []
  if (liveModels.value.length > 0) {
    const preferred = pickCcSwitchModel(
      activeAgent.value.defaultModel,
      liveModels.value,
      activeAgent.value.ccSwitch.appIds[0],
    )
    const rest = liveModels.value.filter((id) => id !== preferred)
    return [preferred, ...rest].slice(0, 4)
  }
  return activeAgent.value.modelPresets.slice(0, 4)
})

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
    { key: 'key', label: t('docs.agentSetup.ccsFieldKey'), value: displayApiKey.value, copyable: hasLiveKey.value },
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

function snippetCopyLabel(snippet: AgentCodeSnippet): string {
  if (snippet.copyKind === 'command') return t('docs.agentSetup.copyCommand')
  if (snippet.copyKind === 'config') return t('docs.agentSetup.copyConfig')
  if (snippet.copyKind === 'value') return t('docs.agentSetup.copyValue')
  return t('docs.copy')
}

function resolveCopyableCode(text: string): string {
  const rawKey = liveApiKey.value.trim()
  if (!rawKey) return text
  if (maskKeyDisplay.value && maskedKeyForDemo.value) {
    return text.split(maskedKeyForDemo.value).join(rawKey)
  }
  return text
}

function splitCodeForHighlight(code: string): Array<{ text: string; isPlaceholder: boolean }> {
  if (hasLiveKey.value || !code.includes('YOUR_API_KEY')) {
    return [{ text: code, isPlaceholder: false }]
  }
  const parts = code.split('YOUR_API_KEY')
  const segments: Array<{ text: string; isPlaceholder: boolean }> = []
  parts.forEach((part, index) => {
    if (part) segments.push({ text: part, isPlaceholder: false })
    if (index < parts.length - 1) segments.push({ text: 'YOUR_API_KEY', isPlaceholder: true })
  })
  return segments
}

function requestFillKey() {
  keyBarRef.value?.requestFill()
}

function selectOs(os: AgentShellOs) {
  selectedOs.value = os
  osAutoDetected.value = false
}

function onModelsLoaded(models: string[]) {
  liveModels.value = models
  if (!models.length || !activeAgent.value || customModel.value.trim()) return
  const matched = pickCcSwitchModel(
    activeAgent.value.defaultModel,
    models,
    activeAgent.value.ccSwitch.appIds[0],
  )
  if (matched) customModel.value = matched
}

function onKeyMeta(meta: { name: string; platform?: GroupPlatform }) {
  liveKeyPlatform.value = meta.platform
}

async function runBrowserConnectivityTest() {
  const key = liveApiKey.value.trim()
  if (!key || !activeAgent.value) {
    requestFillKey()
    return
  }
  verifyRunning.value = true
  verifyResult.value = null
  const isGemini = activeAgent.value.id === 'gemini-cli'
  const url = isGemini ? `${catalog.value.origin}/v1beta/models` : `${catalog.value.v1BaseUrl}/models`
  const headers: Record<string, string> = isGemini
    ? { 'x-goog-api-key': key }
    : { Authorization: `Bearer ${key}` }
  const started = performance.now()
  try {
    const resp = await fetch(url, { headers })
    const elapsedMs = Math.max(1, Math.round(performance.now() - started))
    if (resp.ok) {
      const payload = await resp.json().catch(() => ({}))
      const models = parseCcSwitchModelsList(payload)
      if (models.length > 0) {
        onModelsLoaded(models)
      }
      verifyResult.value = {
        ok: true,
        message: t('docs.agentSetup.verifyBrowserTestOk', { ms: elapsedMs, count: models.length }),
      }
    } else if (resp.status === 401) {
      verifyResult.value = { ok: false, message: t('docs.agentSetup.verifyBrowserTestErr401') }
    } else if (resp.status === 403) {
      verifyResult.value = { ok: false, message: t('docs.agentSetup.verifyBrowserTestErr403') }
    } else {
      verifyResult.value = {
        ok: false,
        message: t('docs.agentSetup.verifyBrowserTestErrOther', { detail: `HTTP ${resp.status}` }),
      }
    }
  } catch (err) {
    const detail = err instanceof Error ? err.message : 'Network error'
    verifyResult.value = {
      ok: false,
      message: t('docs.agentSetup.verifyBrowserTestErrOther', { detail }),
    }
  } finally {
    verifyRunning.value = false
  }
}

function triggerDirectCcsImport() {
  const key = liveApiKey.value.trim()
  if (!key || !activeAgent.value?.ccSwitch.supported) {
    requestFillKey()
    return
  }
  const app = activeAgent.value.ccSwitch.appIds[0]
  const usageScript = `({
    request: {
      url: "{{baseUrl}}".replace(/\\/+$/, "").replace(/\\/v1$/, "") + "/v1/usage",
      method: "GET",
      headers: { "Authorization": "Bearer {{apiKey}}" }
    },
    extractor: function(response) {
      const remaining = response?.remaining ?? response?.quota?.remaining ?? response?.balance;
      const unit = response?.unit ?? response?.quota?.unit ?? "USD";
      return {
        isValid: response?.is_active ?? response?.isValid ?? true,
        remaining,
        unit
      };
    }
  })`
  const deeplink = buildCcSwitchImportDeeplink({
    baseUrl: catalog.value.origin,
    platform: liveKeyPlatform.value ?? activeAgent.value.platform ?? 'anthropic',
    providerName: props.siteName.trim() || 'sub2api',
    apiKey: key,
    usageScript,
    app,
    model: effectiveModel.value,
  })
  window.open(deeplink, '_self')
  ccsImportFeedback.value = t('docs.agentSetup.ccsImportTriggered')
  if (ccsFeedbackTimer) clearTimeout(ccsFeedbackTimer)
  ccsFeedbackTimer = setTimeout(() => {
    ccsImportFeedback.value = ''
  }, 4000)
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
    verifyResult.value = null
    ccsImportFeedback.value = ''
    setupMode.value = 'manual'
    if (liveModels.value.length > 0 && activeAgent.value) {
      const matched = pickCcSwitchModel(
        activeAgent.value.defaultModel,
        liveModels.value,
        activeAgent.value.ccSwitch.appIds[0],
      )
      if (matched) customModel.value = matched
    }
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
  if (typeof navigator !== 'undefined' && /win/i.test(navigator.platform || navigator.userAgent || '')) {
    selectedOs.value = 'powershell'
    osAutoDetected.value = true
  }
})

onBeforeUnmount(() => {
  if (copiedTimer) clearTimeout(copiedTimer)
  if (ccsFeedbackTimer) clearTimeout(ccsFeedbackTimer)
})
</script>

<style scoped>
.agent-details summary::-webkit-details-marker {
  display: none;
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
  .agent-setup :is(button, a[data-testid^='agent-'], [role='tab']):active,
  .agent-card-interactive:hover,
  .agent-card-interactive:active {
    transform: none;
  }
}
</style>
