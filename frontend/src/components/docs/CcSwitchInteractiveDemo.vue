<template>
  <div
    class="ccs-demo overflow-hidden rounded-2xl border border-slate-200/90 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-950"
    data-testid="ccswitch-interactive-demo"
  >
    <!-- Authentic macOS-style interactive window chrome (inspired by ccswitch.io/zh/) -->
    <div class="flex items-center justify-between gap-3 border-b border-slate-200/90 bg-slate-50/90 px-4 py-2.5 dark:border-dark-700/90 dark:bg-dark-900/90">
      <div class="flex items-center gap-2">
        <span class="flex items-center gap-1.5" aria-hidden="true">
          <span class="h-2.5 w-2.5 rounded-full bg-[#ff5f56]" />
          <span class="h-2.5 w-2.5 rounded-full bg-[#ffbd2e]" />
          <span class="h-2.5 w-2.5 rounded-full bg-[#27c93f]" />
        </span>
        <span class="ml-1.5 truncate font-mono text-[11px] font-medium text-slate-500 dark:text-slate-400">
          {{ windowTitle }}
        </span>
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <span class="inline-flex items-center gap-1.5 rounded-full bg-blue-50 px-2.5 py-0.5 text-[11px] font-medium text-blue-700 dark:bg-blue-500/15 dark:text-blue-300">
          <span class="h-1.5 w-1.5 rounded-full bg-blue-500" aria-hidden="true" />
          {{ t('docs.agentSetup.demoInteractiveBadge') }}
        </span>
      </div>
    </div>

    <!-- Mode A: Site Console Interactive Preview (Step 2: Create Key / Step 3: Receipt & Import to CCS) -->
    <div
      v-if="viewMode === 'site-create' || viewMode === 'site-import'"
      class="min-h-[380px] bg-slate-50/50 p-4 dark:bg-dark-950 sm:p-5"
      data-testid="ccs-demo-site-stage"
    >
      <!-- Simulated Site Console Create Key Dialog (Step 2) -->
      <div
        v-if="viewMode === 'site-create'"
        class="mx-auto max-w-lg rounded-2xl border border-slate-200 bg-white p-5 shadow-xs dark:border-dark-700 dark:bg-dark-900"
      >
        <div class="flex items-center justify-between gap-3 border-b border-slate-100 pb-3.5 dark:border-dark-800">
          <div class="flex items-center gap-2">
            <span class="flex h-7 w-7 items-center justify-center rounded-lg bg-blue-50 text-blue-600 dark:bg-blue-500/15 dark:text-blue-400">
              <Icon name="key" size="xs" />
            </span>
            <div>
              <p class="text-[14px] font-semibold text-slate-950 dark:text-slate-50">{{ t('docs.agentSetup.demoCreateKeyTitle') }}</p>
              <p class="text-[11px] text-slate-500 dark:text-slate-400">{{ siteName }} · {{ t('docs.agentSetup.demoConsoleKeysPath') }}</p>
            </div>
          </div>
          <span class="rounded-full bg-emerald-50 px-2.5 py-0.5 text-[11px] font-medium text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
            STEP 02
          </span>
        </div>

        <div class="mt-4 space-y-4">
          <div>
            <label class="block text-xs font-medium text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.demoKeyNameLabel') }}</label>
            <input
              v-model="demoKeyName"
              type="text"
              class="mt-1.5 w-full rounded-xl border border-slate-200 bg-slate-50/60 px-3.5 py-2 text-[13px] text-slate-900 outline-hidden transition focus:border-blue-500 focus:bg-white dark:border-dark-700 dark:bg-dark-950 dark:text-slate-100"
            />
          </div>

          <div
            class="flex items-start justify-between gap-3 rounded-xl border p-3.5 transition"
            :class="demoSmartRouting
              ? 'border-blue-300/90 bg-blue-50/50 dark:border-blue-500/40 dark:bg-blue-500/10'
              : 'border-slate-200 bg-slate-50/60 dark:border-dark-700 dark:bg-dark-950/60'"
          >
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <span class="text-[13px] font-semibold text-slate-900 dark:text-slate-100">{{ t('docs.agentSetup.demoSmartRoutingLabel') }}</span>
                <span class="rounded-full bg-blue-600 px-2 py-0.5 text-[10px] font-medium text-white">{{ t('docs.agentSetup.recommendedBadge') }}</span>
              </div>
              <p class="mt-1 text-xs leading-5 text-slate-600 dark:text-slate-300">
                {{ demoSmartRouting ? t('docs.agentSetup.demoSmartRoutingOnDesc') : t('docs.agentSetup.demoSmartRoutingOffDesc') }}
              </p>
            </div>
            <button
              type="button"
              role="switch"
              data-testid="demo-smart-routing-toggle"
              :aria-checked="demoSmartRouting"
              class="relative mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors"
              :class="demoSmartRouting ? 'bg-blue-600' : 'bg-slate-300 dark:bg-dark-600'"
              @click="demoSmartRouting = !demoSmartRouting"
            >
              <span
                class="inline-block h-4 w-4 rounded-full bg-white shadow-xs transition-transform"
                :class="demoSmartRouting ? 'translate-x-4.5' : 'translate-x-0.5'"
              />
            </button>
          </div>

          <div class="flex flex-wrap gap-1.5">
            <span
              v-for="tag in demoBoundGroups"
              :key="tag"
              class="inline-flex items-center gap-1 rounded-full border border-slate-200 bg-white px-2.5 py-1 text-[11px] font-medium text-slate-700 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-300"
            >
              <span class="h-1.5 w-1.5 rounded-full bg-emerald-500" aria-hidden="true" />
              {{ tag }}
            </span>
          </div>

          <div class="flex items-center justify-end gap-2 border-t border-slate-100 pt-3.5 dark:border-dark-800">
            <button
              type="button"
              data-testid="demo-simulate-create-key"
              class="inline-flex items-center gap-1.5 rounded-full bg-blue-600 px-4 py-2 text-[13px] font-medium text-white shadow-xs transition hover:bg-blue-700"
              @click="simulateCreateKey"
            >
              <Icon name="plus" size="xs" />
              <span>{{ t('docs.agentSetup.demoSimulateCreateBtn') }}</span>
            </button>
          </div>
        </div>
      </div>

      <!-- Simulated Receipt -> Import to CCS Modal (Step 3) -->
      <div
        v-else
        class="mx-auto max-w-lg rounded-2xl border border-slate-200 bg-white p-5 shadow-xs dark:border-dark-700 dark:bg-dark-900"
      >
        <div class="flex items-center justify-between gap-3 border-b border-slate-100 pb-3.5 dark:border-dark-800">
          <div class="flex items-center gap-2">
            <span class="flex h-7 w-7 items-center justify-center rounded-lg bg-emerald-50 text-emerald-600 dark:bg-emerald-500/15 dark:text-emerald-400">
              <Icon :name="importSubStage === 'modal' ? 'bolt' : 'checkCircle'" size="xs" />
            </span>
            <p class="text-[14px] font-semibold text-slate-950 dark:text-slate-50">
              {{ importSubStage === 'modal' ? t('docs.agentSetup.demoImportModalTitle') : t('docs.agentSetup.demoReceiptTitle') }}
            </p>
          </div>
          <div class="inline-flex rounded-full border border-slate-200 bg-slate-50 p-0.5 dark:border-dark-700 dark:bg-dark-950" role="tablist">
            <button
              type="button"
              role="tab"
              data-testid="demo-import-tab-receipt"
              :aria-selected="importSubStage === 'receipt'"
              class="rounded-full px-2.5 py-0.5 text-[11px] font-medium transition"
              :class="importSubStage === 'receipt'
                ? 'bg-slate-900 text-white dark:bg-white dark:text-slate-950'
                : 'text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-white'"
              @click="importSubStage = 'receipt'"
            >
              {{ t('docs.agentSetup.tabReceiptShot') }}
            </button>
            <button
              type="button"
              role="tab"
              data-testid="demo-import-tab-modal"
              :aria-selected="importSubStage === 'modal'"
              class="rounded-full px-2.5 py-0.5 text-[11px] font-medium transition"
              :class="importSubStage === 'modal'
                ? 'bg-slate-900 text-white dark:bg-white dark:text-slate-950'
                : 'text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-white'"
              @click="importSubStage = 'modal'"
            >
              {{ t('docs.agentSetup.tabModalShot') }}
            </button>
          </div>
        </div>

        <!-- Substage A: Plaintext Receipt with highlighted Import to CCS button -->
        <div v-if="importSubStage === 'receipt'" class="mt-4 space-y-4">
          <div class="rounded-xl border border-amber-200/80 bg-amber-50/70 px-3.5 py-2.5 text-xs text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
            {{ t('docs.agentSetup.demoReceiptNotice') }}
          </div>
          <div class="flex items-center justify-between gap-2 rounded-xl border border-slate-200 bg-slate-50 px-3.5 py-2.5 font-mono text-xs text-slate-800 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-200">
            <span class="truncate">{{ demoMaskedKey }}</span>
            <span class="shrink-0 rounded-md bg-white px-2 py-0.5 text-[11px] text-slate-500 ring-1 ring-slate-200 dark:bg-dark-900 dark:text-slate-400 dark:ring-dark-700">{{ demoKeyName }}</span>
          </div>
          <div class="flex flex-wrap items-center justify-between gap-2 border-t border-slate-100 pt-3.5 dark:border-dark-800">
            <button
              type="button"
              data-testid="demo-click-import-ccs"
              class="inline-flex items-center gap-1.5 rounded-full bg-blue-600 px-4 py-2 text-[13px] font-medium text-white shadow-xs ring-2 ring-blue-500/30 transition hover:bg-blue-700"
              @click="importSubStage = 'modal'"
            >
              <Icon name="bolt" size="xs" />
              <span>{{ t('docs.agentSetup.demoImportToCcsBtn') }}</span>
            </button>
            <span class="text-xs text-slate-400 dark:text-slate-500">{{ t('docs.agentSetup.demoClickToTry') }}</span>
          </div>
        </div>

        <!-- Substage B: Pick target app & model, confirm import -->
        <div v-else class="mt-4 space-y-3.5">
          <div>
            <span class="block text-xs font-medium text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.demoTargetAppLabel') }}</span>
            <div class="mt-1.5 flex flex-wrap gap-1.5">
              <button
                v-for="app in importableDemoApps"
                :key="app.id"
                type="button"
                :data-testid="`demo-import-app-${app.id}`"
                class="inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium transition"
                :class="selectedImportApp === app.id
                  ? 'border-blue-500 bg-blue-50 text-blue-700 dark:border-blue-400 dark:bg-blue-500/15 dark:text-blue-300'
                  : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300 dark:border-dark-700 dark:bg-dark-950 dark:text-slate-300'"
                @click="selectImportApp(app.id)"
              >
                <AgentBrandIcon :icon-key="app.iconKey" :platform="app.platform" size="sm" />
                <span>{{ app.label }}</span>
              </button>
            </div>
          </div>

          <div class="grid grid-cols-1 gap-2.5 rounded-xl border border-slate-200 bg-slate-50/80 p-3 text-xs dark:border-dark-700 dark:bg-dark-950/70 sm:grid-cols-2">
            <div class="min-w-0">
              <span class="text-slate-400 dark:text-slate-500">Endpoint</span>
              <p class="mt-0.5 truncate font-mono font-medium text-slate-800 dark:text-slate-200">{{ selectedImportEndpoint }}</p>
            </div>
            <div class="min-w-0">
              <span class="text-slate-400 dark:text-slate-500">{{ t('docs.agentSetup.ccsFieldModel') }}</span>
              <p class="mt-0.5 truncate font-mono font-medium text-slate-800 dark:text-slate-200">{{ selectedImportModel }}</p>
            </div>
          </div>

          <div class="flex flex-wrap items-center justify-between gap-2 border-t border-slate-100 pt-3.5 dark:border-dark-800">
            <span class="inline-flex items-center gap-1 text-[11px] text-emerald-700 dark:text-emerald-400">
              <Icon name="checkCircle" size="xs" />
              {{ t('docs.agentSetup.demoAutoBalanceScript') }}
            </span>
            <button
              type="button"
              data-testid="demo-confirm-import-ccs"
              class="inline-flex items-center gap-1.5 rounded-full bg-blue-600 px-4 py-2 text-[13px] font-medium text-white shadow-xs transition hover:bg-blue-700"
              @click="confirmDemoImport"
            >
              <Icon name="bolt" size="xs" />
              <span>{{ t('docs.agentSetup.demoConfirmImportBtn') }}</span>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Mode B: Authentic CC Switch Interactive Desktop Window (Step 1: Apps, Step 4: Provider Switch & Usage) -->
    <div v-else class="flex min-h-[380px] flex-col sm:flex-row" data-testid="ccs-demo-app-window">
      <!-- Left Sidebar matching ccswitch.io/zh/ -->
      <aside
        class="flex shrink-0 flex-col border-b border-slate-200/90 bg-slate-50/80 text-[12.5px] transition-[width] duration-200 dark:border-dark-700/90 dark:bg-dark-900/75 sm:border-b-0 sm:border-r"
        :class="sidebarCollapsed ? 'sm:w-14' : 'sm:w-48'"
      >
        <!-- Sidebar Header -->
        <div class="flex items-center justify-between gap-2 px-3 py-2.5">
          <div class="flex min-w-0 items-center gap-2">
            <AgentBrandIcon icon-key="ccswitch" size="sm" />
            <span v-if="!sidebarCollapsed" class="truncate text-[13px] font-semibold text-slate-900 dark:text-slate-100">CC Switch</span>
          </div>
          <button
            type="button"
            class="hidden h-6 w-6 items-center justify-center rounded-md text-slate-400 transition hover:bg-slate-200/70 hover:text-slate-700 dark:hover:bg-dark-800 dark:hover:text-slate-200 sm:inline-flex"
            :aria-label="t('docs.agentSetup.demoToggleSidebar')"
            @click="sidebarCollapsed = !sidebarCollapsed"
          >
            <Icon :name="sidebarCollapsed ? 'chevronRight' : 'chevronLeft'" size="xs" />
          </button>
        </div>

        <!-- Visible Apps List (responsive wrap on mobile, vertical list on desktop) -->
        <div class="flex gap-1 overflow-x-auto px-2 pb-2 sm:flex-1 sm:flex-col sm:overflow-y-auto sm:overflow-x-hidden">
          <button
            v-for="app in visibleSidebarApps"
            :key="app.id"
            type="button"
            :data-testid="`demo-sidebar-app-${app.id}`"
            class="flex shrink-0 items-center gap-2 rounded-lg px-2 py-1.5 text-left transition"
            :class="viewMode === 'provider' && activeDemoAppId === app.id
              ? 'bg-slate-200/85 font-medium text-slate-950 dark:bg-dark-800 dark:text-white'
              : 'text-slate-600 hover:bg-slate-200/50 hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800/70 dark:hover:text-white'"
            @click="openDemoApp(app.id)"
          >
            <AgentBrandIcon :icon-key="app.iconKey" :platform="app.platform" size="sm" />
            <span v-if="!sidebarCollapsed" class="min-w-0 flex-1 truncate">{{ app.label }}</span>
            <span
              v-if="!sidebarCollapsed && app.modeBadge"
              class="hidden rounded px-1.5 py-0.5 text-[10px] font-medium sm:inline"
              :class="app.modeBadge === 'route'
                ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
                : 'bg-purple-50 text-purple-700 dark:bg-purple-500/15 dark:text-purple-300'"
            >
              {{ app.modeBadge === 'route' ? t('docs.agentSetup.demoModeRoute') : t('docs.agentSetup.demoModeStack') }}
            </span>
          </button>
        </div>

        <!-- Utility Links (MCP / Skills / Usage) -->
        <div class="hidden border-t border-slate-200/80 px-2 py-1.5 dark:border-dark-700/80 sm:flex sm:flex-col sm:gap-0.5">
          <button
            type="button"
            data-testid="demo-sidebar-usage"
            class="flex items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-left transition"
            :class="viewMode === 'usage'
              ? 'bg-slate-200/85 font-medium text-slate-950 dark:bg-dark-800 dark:text-white'
              : 'text-slate-600 hover:bg-slate-200/50 hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800/70'"
            @click="viewMode = 'usage'"
          >
            <span class="flex min-w-0 items-center gap-2">
              <Icon name="chartBar" size="xs" />
              <span v-if="!sidebarCollapsed" class="truncate">{{ t('docs.agentSetup.demoNavUsage') }}</span>
            </span>
            <span v-if="!sidebarCollapsed" class="shrink-0 font-mono text-[10.5px] tabular-nums text-slate-400">$12.84</span>
          </button>
        </div>

        <!-- Bottom Pinned Apps Tab (just like ccswitch.io/zh/) -->
        <div class="flex items-center gap-1 border-t border-slate-200/80 px-2 py-2 dark:border-dark-700/80">
          <button
            type="button"
            data-testid="demo-sidebar-apps"
            class="flex flex-1 items-center justify-center gap-1.5 rounded-lg px-2 py-1.5 font-medium transition sm:justify-start"
            :class="viewMode === 'apps'
              ? 'bg-slate-200/85 text-slate-950 dark:bg-dark-800 dark:text-white'
              : 'text-slate-600 hover:bg-slate-200/50 hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800/70'"
            @click="openAppsPanel"
          >
            <Icon name="grid" size="xs" />
            <span v-if="!sidebarCollapsed">{{ t('docs.agentSetup.demoNavApps') }}</span>
          </button>
          <button
            type="button"
            class="flex flex-1 items-center justify-center gap-1.5 rounded-lg px-2 py-1.5 text-slate-600 transition hover:bg-slate-200/50 hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800/70 sm:hidden"
            :class="viewMode === 'usage' ? 'bg-slate-200/85 font-medium text-slate-950 dark:bg-dark-800 dark:text-white' : ''"
            @click="viewMode = 'usage'"
          >
            <Icon name="chartBar" size="xs" />
            <span>{{ t('docs.agentSetup.demoNavUsage') }}</span>
          </button>
        </div>
      </aside>

      <!-- Main Right Content Pane -->
      <div class="relative flex min-w-0 flex-1 flex-col bg-white dark:bg-dark-950">
        <!-- View 1: Apps Manager (Step 1, matching ccswitch.io/zh/ screenshot) -->
        <div v-if="viewMode === 'apps'" class="flex flex-1 flex-col" data-testid="ccs-demo-apps-pane">
          <div class="flex items-center justify-between gap-2 border-b border-slate-100 px-4 py-3 dark:border-dark-800">
            <div class="flex items-center gap-2">
              <Icon name="grid" size="sm" class="text-slate-600 dark:text-slate-300" />
              <h4 class="text-[14px] font-semibold text-slate-950 dark:text-slate-50">{{ t('docs.agentSetup.demoNavApps') }}</h4>
            </div>
            <button
              type="button"
              class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 bg-white px-2.5 py-1 text-xs font-medium text-slate-600 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-300 dark:hover:bg-dark-800"
              @click="triggerCheckUpdates"
            >
              <Icon name="refresh" size="xs" :class="{ 'animate-spin': checkingUpdates }" />
              <span>{{ checkingUpdates ? t('docs.agentSetup.demoCheckingUpdates') : t('docs.agentSetup.demoCheckUpdates') }}</span>
            </button>
          </div>

          <div class="flex-1 divide-y divide-slate-100 overflow-y-auto dark:divide-dark-800/80">
            <div class="flex items-center justify-between bg-slate-50/70 px-4 py-1.5 text-[11px] font-medium text-slate-500 dark:bg-dark-900/50 dark:text-slate-400">
              <span>{{ t('docs.agentSetup.demoInstalledCount', { count: installedAppsCount }) }}</span>
              <div class="flex items-center gap-5">
                <span class="hidden w-16 text-right sm:inline-block">{{ t('docs.agentSetup.demoColVersion') }}</span>
                <span class="w-11 text-right">{{ t('docs.agentSetup.demoColShow') }}</span>
              </div>
            </div>

            <div
              v-for="row in demoApps"
              :key="row.id"
              class="flex items-center justify-between gap-3 px-4 py-2.5 transition hover:bg-slate-50/60 dark:hover:bg-dark-900/40"
              :class="row.id === focusDemoAppId ? 'bg-blue-50/40 dark:bg-blue-500/[0.06]' : ''"
            >
              <div class="flex min-w-0 flex-1 items-center gap-2.5">
                <AgentBrandIcon :icon-key="row.iconKey" :platform="row.platform" size="sm" />
                <div class="min-w-0">
                  <div class="flex items-center gap-1.5">
                    <span class="truncate whitespace-nowrap text-[13px] font-medium text-slate-900 dark:text-slate-100">{{ row.label }}</span>
                    <span
                      v-if="row.source"
                      class="shrink-0 whitespace-nowrap rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-medium text-slate-500 dark:bg-dark-800 dark:text-slate-400"
                    >
                      {{ row.source }}
                    </span>
                  </div>
                  <p
                    v-if="!row.installed || row.path"
                    class="truncate font-mono text-[11px] text-slate-400 dark:text-slate-500"
                  >
                    {{ row.installed ? row.path : t('docs.agentSetup.demoNotInstalledHint') }}
                  </p>
                </div>
              </div>

              <div class="flex shrink-0 items-center gap-5">
                <template v-if="row.installed">
                  <span class="hidden w-16 whitespace-nowrap text-right font-mono text-xs tabular-nums text-slate-500 dark:text-slate-400 sm:inline-block">
                    {{ row.version }}
                  </span>
                </template>
                <button
                  v-else
                  type="button"
                  :data-testid="`demo-install-app-${row.id}`"
                  :disabled="installingAppId === row.id"
                  class="inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full bg-blue-600 px-3 py-1 text-[11.5px] font-medium text-white transition hover:bg-blue-700 disabled:opacity-60"
                  @click="installDemoApp(row.id)"
                >
                  <span>{{ installingAppId === row.id ? t('docs.agentSetup.demoInstalling') : t('docs.agentSetup.demoInstallBtn') }}</span>
                </button>

                <div class="flex w-11 justify-end">
                  <button
                    type="button"
                    role="switch"
                    :data-testid="`demo-toggle-app-${row.id}`"
                    :aria-checked="row.visible"
                    :aria-label="row.label"
                    class="relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors"
                    :class="row.visible ? 'bg-slate-900 dark:bg-blue-500' : 'bg-slate-200 dark:bg-dark-700'"
                    @click="toggleAppVisibility(row.id)"
                  >
                    <span
                      class="inline-block h-3.5 w-3.5 rounded-full bg-white shadow-xs transition-transform"
                      :class="row.visible ? 'translate-x-4.5' : 'translate-x-0.5'"
                    />
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- View 2: Provider Switch View for Active App (Step 4) -->
        <div v-else-if="viewMode === 'provider'" class="flex flex-1 flex-col" data-testid="ccs-demo-provider-pane">
          <div class="flex flex-wrap items-center justify-between gap-2 border-b border-slate-100 px-4 py-3 dark:border-dark-800">
            <div class="flex flex-wrap items-center gap-2">
              <AgentBrandIcon :icon-key="activeDemoApp.iconKey" :platform="activeDemoApp.platform" size="sm" />
              <h4 class="whitespace-nowrap text-[14px] font-semibold text-slate-950 dark:text-slate-50">{{ activeDemoApp.label }}</h4>
              <div class="ml-1 inline-flex shrink-0 rounded-full border border-slate-200 bg-slate-50 p-0.5 text-[11px] dark:border-dark-700 dark:bg-dark-900">
                <button
                  v-for="mode in connectionModes"
                  :key="mode.id"
                  type="button"
                  class="whitespace-nowrap rounded-full px-2.5 py-0.5 font-medium transition"
                  :class="activeConnMode === mode.id
                    ? 'bg-white text-slate-950 shadow-xs dark:bg-dark-800 dark:text-white'
                    : 'text-slate-500 hover:text-slate-900 dark:text-slate-400'"
                  @click="activeConnMode = mode.id"
                >
                  {{ mode.label }}
                </button>
              </div>
            </div>
            <span class="shrink-0 whitespace-nowrap rounded-full bg-emerald-50 px-2.5 py-0.5 font-mono text-[11px] font-medium text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
              {{ activeProviderKey === 'site' ? t('docs.agentSetup.demoActiveSiteStatus', { site: siteName }) : t('docs.agentSetup.demoActiveOfficialStatus') }}
            </span>
          </div>

          <div class="flex-1 space-y-3 p-4 pb-12">
            <!-- Our Site Imported Provider Card -->
            <div
              class="rounded-xl border p-3.5 transition"
              :class="activeProviderKey === 'site'
                ? 'border-blue-500/70 bg-blue-50/40 shadow-xs dark:border-blue-400/50 dark:bg-blue-500/[0.08]'
                : 'border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/50'"
            >
              <div class="flex flex-wrap items-start justify-between gap-3">
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="whitespace-nowrap text-[13.5px] font-semibold text-slate-950 dark:text-slate-50">{{ siteName }}</span>
                    <span class="shrink-0 whitespace-nowrap rounded-full bg-emerald-50 px-2 py-0.5 text-[10.5px] font-medium text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
                      {{ t('docs.agentSetup.demoImportedBadge') }}
                    </span>
                  </div>
                  <p class="mt-1 truncate font-mono text-[11.5px] text-slate-500 dark:text-slate-400">
                    {{ activeDemoApp.endpoint }}
                  </p>
                  <div class="mt-2 flex flex-wrap items-center gap-2 text-[11px]">
                    <span class="whitespace-nowrap rounded-md bg-slate-100 px-2 py-0.5 font-mono text-slate-700 dark:bg-dark-800 dark:text-slate-300">
                      {{ activeDemoApp.model }}
                    </span>
                    <span class="inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-md bg-emerald-50 px-2 py-0.5 font-mono text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
                      <Icon name="wallet" size="xs" />
                      {{ t('docs.agentSetup.demoBalancePreview') }}
                    </span>
                  </div>
                </div>
                <button
                  type="button"
                  data-testid="demo-activate-site-provider"
                  class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full px-3.5 py-1.5 text-xs font-medium transition"
                  :class="activeProviderKey === 'site'
                    ? 'bg-emerald-600 text-white'
                    : 'bg-blue-600 text-white hover:bg-blue-700'"
                  @click="activateProvider('site')"
                >
                  <Icon :name="activeProviderKey === 'site' ? 'check' : 'play'" size="xs" />
                  <span>{{ activeProviderKey === 'site' ? t('docs.agentSetup.demoInUse') : t('docs.agentSetup.demoActivateBtn') }}</span>
                </button>
              </div>
            </div>

            <!-- Secondary Official Card (lets user experience 1-click switching) -->
            <div
              class="rounded-xl border p-3.5 transition"
              :class="activeProviderKey === 'official'
                ? 'border-blue-500/70 bg-blue-50/40 dark:border-blue-400/50 dark:bg-blue-500/[0.08]'
                : 'border-slate-200 bg-white dark:border-dark-700 dark:bg-dark-900/50'"
            >
              <div class="flex items-center justify-between gap-3">
                <div class="min-w-0">
                  <p class="text-[13px] font-medium text-slate-800 dark:text-slate-200">{{ activeDemoApp.label }} Official</p>
                  <p class="mt-0.5 text-[11.5px] text-slate-400 dark:text-slate-500">{{ t('docs.agentSetup.demoOfficialDesc') }}</p>
                </div>
                <button
                  type="button"
                  data-testid="demo-activate-official-provider"
                  class="inline-flex shrink-0 items-center gap-1 rounded-full border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-700 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:bg-dark-800"
                  @click="activateProvider('official')"
                >
                  <span>{{ activeProviderKey === 'official' ? t('docs.agentSetup.demoInUse') : t('docs.agentSetup.demoActivateBtn') }}</span>
                </button>
              </div>
            </div>

            <!-- Connection Mode Explainer for Beginners -->
            <div class="rounded-xl border border-slate-200/80 bg-slate-50/75 px-3.5 py-2.5 text-xs leading-5 text-slate-600 dark:border-dark-700/80 dark:bg-dark-900/60 dark:text-slate-300">
              {{ activeConnModeHint }}
            </div>
          </div>
        </div>

        <!-- View 3: Usage Statistics Preview (interactive sidebar destination) -->
        <div v-else class="flex flex-1 flex-col p-4" data-testid="ccs-demo-usage-pane">
          <div class="flex items-center justify-between gap-2 pb-3">
            <h4 class="text-[14px] font-semibold text-slate-950 dark:text-slate-50">{{ t('docs.agentSetup.demoNavUsage') }}</h4>
            <span class="font-mono text-xs text-emerald-600 dark:text-emerald-400">{{ t('docs.agentSetup.demoUsageSynced') }}</span>
          </div>
          <div class="grid grid-cols-3 gap-2.5">
            <div class="rounded-xl border border-slate-200 bg-slate-50/70 p-3 dark:border-dark-700 dark:bg-dark-900/60">
              <span class="text-[11px] text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.demoUsageTodayRequests') }}</span>
              <p class="mt-1 font-mono text-lg font-semibold tabular-nums text-slate-950 dark:text-slate-50">42</p>
            </div>
            <div class="rounded-xl border border-slate-200 bg-slate-50/70 p-3 dark:border-dark-700 dark:bg-dark-900/60">
              <span class="text-[11px] text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.demoUsageCacheRate') }}</span>
              <p class="mt-1 font-mono text-lg font-semibold tabular-nums text-emerald-600 dark:text-emerald-400">86%</p>
            </div>
            <div class="rounded-xl border border-slate-200 bg-slate-50/70 p-3 dark:border-dark-700 dark:bg-dark-900/60">
              <span class="text-[11px] text-slate-500 dark:text-slate-400">{{ t('docs.agentSetup.demoUsageTodaySpend') }}</span>
              <p class="mt-1 font-mono text-lg font-semibold tabular-nums text-slate-950 dark:text-slate-50">$12.84</p>
            </div>
          </div>
          <div class="mt-3 divide-y divide-slate-100 rounded-xl border border-slate-200 dark:divide-dark-800 dark:border-dark-700">
            <div
              v-for="item in demoUsageRows"
              :key="item.id"
              class="flex items-center justify-between gap-2 px-3 py-2 text-xs"
            >
              <span class="font-mono font-medium text-slate-800 dark:text-slate-200">{{ item.model }}</span>
              <span class="text-slate-500 dark:text-slate-400">{{ item.tokens }}</span>
              <span class="font-mono tabular-nums text-slate-700 dark:text-slate-300">{{ item.cost }}</span>
            </div>
          </div>
        </div>

        <!-- Subtle bottom interactive toast -->
        <div
          v-if="demoToast"
          class="pointer-events-none absolute bottom-3 right-3 rounded-full bg-slate-900/90 px-3 py-1.5 text-xs font-medium text-white shadow-md dark:bg-white/95 dark:text-slate-950"
          role="status"
          data-testid="ccs-demo-toast"
        >
          {{ demoToast }}
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import AgentBrandIcon from '@/components/docs/AgentBrandIcon.vue'
import type { GroupPlatform } from '@/types'

interface DemoAppItem {
  id: string
  label: string
  iconKey: string
  platform?: GroupPlatform
  source?: string
  path: string
  version: string
  installed: boolean
  visible: boolean
  modeBadge?: 'route' | 'stack'
  endpoint: string
  model: string
}

const props = withDefaults(
  defineProps<{
    origin: string
    siteName: string
    activeStep?: number
    focusAgentId?: string
    maskedKeyPreview?: string
  }>(),
  {
    activeStep: 0,
    focusAgentId: '',
    maskedKeyPreview: '',
  },
)

const emit = defineEmits<{
  'step-change': [step: number]
}>()

const { t } = useI18n()

type DemoViewMode = 'apps' | 'site-create' | 'site-import' | 'provider' | 'usage'
const viewMode = ref<DemoViewMode>('apps')
const sidebarCollapsed = ref(false)
const checkingUpdates = ref(false)
const installingAppId = ref('')
const demoSmartRouting = ref(true)
const demoKeyName = ref('My Coding Key')
const importSubStage = ref<'receipt' | 'modal'>('receipt')
const selectedImportApp = ref('claude')
const activeDemoAppId = ref('claude')
const activeProviderKey = ref<'site' | 'official'>('site')
const activeConnMode = ref<'direct' | 'route' | 'stack'>('direct')
const demoToast = ref('')
let toastTimer: ReturnType<typeof setTimeout> | undefined
let actionTimer: ReturnType<typeof setTimeout> | undefined

const AGENT_TO_DEMO_APP: Record<string, string> = {
  'claude-code': 'claude',
  'claude-desktop': 'claude-desktop',
  codex: 'codex',
  'gemini-cli': 'gemini',
  'grok-build': 'grokbuild',
  opencode: 'opencode',
  openclaw: 'openclaw',
  'ccswitch-ecosystem': 'hermes',
}

const focusDemoAppId = computed(() => AGENT_TO_DEMO_APP[props.focusAgentId] ?? '')

const createInitialApps = (): DemoAppItem[] => [
  {
    id: 'claude',
    label: 'Claude Code',
    iconKey: 'anthropic',
    platform: 'anthropic',
    source: 'Homebrew',
    path: '/opt/homebrew/bin/claude',
    version: '2.4.3',
    installed: true,
    visible: true,
    endpoint: props.origin,
    model: 'claude-sonnet-4-6',
  },
  {
    id: 'claude-desktop',
    label: 'Claude Desktop',
    iconKey: 'anthropic',
    platform: 'anthropic',
    path: '',
    version: t('docs.agentSetup.demoDesktopConnected'),
    installed: true,
    visible: true,
    modeBadge: 'route',
    endpoint: props.origin,
    model: 'claude-sonnet-4-6',
  },
  {
    id: 'codex',
    label: 'Codex',
    iconKey: 'openai',
    platform: 'openai',
    source: 'npm',
    path: '~/.npm-global/bin/codex',
    version: '0.142.0',
    installed: true,
    visible: true,
    modeBadge: 'stack',
    endpoint: `${props.origin}/v1`,
    model: 'gpt-5.6-sol',
  },
  {
    id: 'gemini',
    label: 'Gemini CLI',
    iconKey: 'gemini',
    platform: 'gemini',
    source: 'npm',
    path: '~/.npm-global/bin/gemini',
    version: '0.32.0',
    installed: true,
    visible: true,
    endpoint: props.origin,
    model: 'gemini-2.5-pro',
  },
  {
    id: 'grokbuild',
    label: 'Grok Build',
    iconKey: 'grok',
    platform: 'grok',
    source: 'npm',
    path: '~/.npm-global/bin/grok',
    version: '0.1.6',
    installed: true,
    visible: true,
    endpoint: `${props.origin}/v1`,
    model: 'grok-4.6',
  },
  {
    id: 'opencode',
    label: 'OpenCode',
    iconKey: 'opencode',
    source: 'Homebrew',
    path: '/opt/homebrew/bin/opencode',
    version: '1.4.7',
    installed: true,
    visible: true,
    endpoint: `${props.origin}/v1`,
    model: 'claude-sonnet-4-6',
  },
  {
    id: 'openclaw',
    label: 'OpenClaw',
    iconKey: 'openclaw',
    source: 'npm',
    path: '~/.npm-global/bin/openclaw',
    version: '2026.9.30',
    installed: false,
    visible: false,
    endpoint: `${props.origin}/v1`,
    model: 'gpt-5.6-sol',
  },
  {
    id: 'hermes',
    label: 'Hermes / Pi',
    iconKey: 'ccswitch',
    source: 'npm',
    path: '~/.local/bin/hermes',
    version: '0.9.2',
    installed: true,
    visible: true,
    endpoint: props.origin,
    model: 'gpt-5.6-sol',
  },
]

const demoApps = ref<DemoAppItem[]>(createInitialApps())

watch(
  () => props.origin,
  (nextOrigin) => {
    for (const app of demoApps.value) {
      app.endpoint = ['codex', 'grokbuild', 'opencode', 'openclaw'].includes(app.id)
        ? `${nextOrigin}/v1`
        : nextOrigin
    }
  },
)

watch(
  () => props.activeStep,
  (step) => {
    if (step === 0) viewMode.value = 'apps'
    else if (step === 1) viewMode.value = 'site-create'
    else if (step === 2) {
      viewMode.value = 'site-import'
      importSubStage.value = 'receipt'
    } else {
      viewMode.value = 'provider'
    }
  },
  { immediate: true },
)

watch(
  focusDemoAppId,
  (appId) => {
    if (appId) {
      activeDemoAppId.value = appId
      selectedImportApp.value = appId
    }
  },
  { immediate: true },
)

const visibleSidebarApps = computed(() => demoApps.value.filter((app) => app.visible))
const installedAppsCount = computed(() => demoApps.value.filter((app) => app.installed).length)
const activeDemoApp = computed(
  () => demoApps.value.find((app) => app.id === activeDemoAppId.value) ?? demoApps.value[0],
)

const importableDemoApps = computed(() =>
  demoApps.value.filter((app) => ['claude', 'codex', 'opencode', 'gemini', 'grokbuild', 'openclaw'].includes(app.id)),
)

const selectedImportEndpoint = computed(() => {
  const found = demoApps.value.find((app) => app.id === selectedImportApp.value)
  return found?.endpoint ?? props.origin
})

const selectedImportModel = computed(() => {
  const found = demoApps.value.find((app) => app.id === selectedImportApp.value)
  return found?.model ?? 'claude-sonnet-4-6'
})

const demoBoundGroups = computed(() =>
  demoSmartRouting.value
    ? ['Anthropic', 'OpenAI', 'Gemini', 'Grok']
    : ['Anthropic'],
)

const demoMaskedKey = computed(() => props.maskedKeyPreview || 'demo-key-••••-8a9f')

const connectionModes = computed(() => [
  { id: 'direct' as const, label: t('docs.agentSetup.demoModeDirect') },
  { id: 'route' as const, label: t('docs.agentSetup.demoModeRoute') },
  { id: 'stack' as const, label: t('docs.agentSetup.demoModeStack') },
])

const activeConnModeHint = computed(() => {
  if (activeConnMode.value === 'route') return t('docs.agentSetup.demoModeRouteHint')
  if (activeConnMode.value === 'stack') return t('docs.agentSetup.demoModeStackHint')
  return t('docs.agentSetup.demoModeDirectHint')
})

const demoUsageRows = [
  { id: '1', model: 'claude-sonnet-4-6', tokens: '14.2k · 88% cache', cost: '$0.042' },
  { id: '2', model: 'gpt-5.6-sol', tokens: '8.9k · 81% cache', cost: '$0.028' },
  { id: '3', model: 'gemini-2.5-pro', tokens: '19.4k · 79% cache', cost: '$0.031' },
]

const windowTitle = computed(() => {
  if (viewMode.value === 'site-create') return `${props.siteName} — ${t('docs.agentSetup.demoCreateKeyTitle')}`
  if (viewMode.value === 'site-import') return `${props.siteName} — ${t('docs.agentSetup.demoImportModalTitle')}`
  if (viewMode.value === 'usage') return `CC Switch — ${t('docs.agentSetup.demoNavUsage')}`
  if (viewMode.value === 'provider') return `CC Switch — ${activeDemoApp.value.label}`
  return `CC Switch — ${t('docs.agentSetup.demoNavApps')}`
})

function showDemoToast(message: string) {
  demoToast.value = message
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    demoToast.value = ''
  }, 2400)
}

function triggerCheckUpdates() {
  checkingUpdates.value = true
  if (actionTimer) clearTimeout(actionTimer)
  actionTimer = setTimeout(() => {
    checkingUpdates.value = false
    showDemoToast(t('docs.agentSetup.demoUpToDateToast'))
  }, 600)
}

function installDemoApp(id: string) {
  installingAppId.value = id
  if (actionTimer) clearTimeout(actionTimer)
  actionTimer = setTimeout(() => {
    const target = demoApps.value.find((app) => app.id === id)
    if (target) {
      target.installed = true
      target.visible = true
      showDemoToast(t('docs.agentSetup.demoInstalledToast', { app: target.label }))
    }
    installingAppId.value = ''
  }, 450)
}

function toggleAppVisibility(id: string) {
  const target = demoApps.value.find((app) => app.id === id)
  if (!target) return
  target.visible = !target.visible
}

function openDemoApp(id: string) {
  activeDemoAppId.value = id
  viewMode.value = 'provider'
  emit('step-change', 3)
}

function openAppsPanel() {
  viewMode.value = 'apps'
  emit('step-change', 0)
}

function simulateCreateKey() {
  viewMode.value = 'site-import'
  importSubStage.value = 'receipt'
  emit('step-change', 2)
}

function selectImportApp(id: string) {
  selectedImportApp.value = id
}

function confirmDemoImport() {
  const target = demoApps.value.find((app) => app.id === selectedImportApp.value)
  if (target) {
    target.installed = true
    target.visible = true
  }
  activeDemoAppId.value = selectedImportApp.value
  activeProviderKey.value = 'site'
  viewMode.value = 'provider'
  emit('step-change', 3)
  showDemoToast(t('docs.agentSetup.demoImportedToast', { app: target?.label ?? 'Agent' }))
}

function activateProvider(which: 'site' | 'official') {
  activeProviderKey.value = which
  showDemoToast(
    which === 'site'
      ? t('docs.agentSetup.demoActivatedSiteToast', { site: props.siteName })
      : t('docs.agentSetup.demoActivatedOfficialToast'),
  )
}

onBeforeUnmount(() => {
  if (toastTimer) clearTimeout(toastTimer)
  if (actionTimer) clearTimeout(actionTimer)
})
</script>
