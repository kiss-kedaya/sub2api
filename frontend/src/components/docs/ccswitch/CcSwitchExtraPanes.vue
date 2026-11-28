<template>
  <!-- 1. MCP Pane -->
  <div v-if="pageKind === 'mcp'" class="flex h-full min-h-0 flex-col" data-testid="ccs-demo-mcp-pane">
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--ccs-border)] px-4 sm:pe-4 sm:ps-6">
      <div class="flex min-w-0 items-center gap-2.5">
        <span class="flex h-[22px] w-[22px] shrink-0 items-center justify-center text-[var(--ccs-fg)]">
          <CcSwitchLucideIcon name="server" class-name="h-5 w-5" :stroke-width="1.5" />
        </span>
        <h3 class="truncate text-lg font-semibold text-[var(--ccs-fg)]">MCP</h3>
        <button
          type="button"
          class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
          @mouseenter="tooltip.show($event, mcpMsg.help, mcpMsg.helpTitle)"
          @mouseleave="tooltip.hide()"
        >
          <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
        </button>
      </div>
      <span class="flex-1" />
      <div class="flex shrink-0 items-center gap-2">
        <button
          type="button"
          class="hidden h-8 items-center gap-1.5 rounded-md px-3.5 text-[13px] font-medium text-[var(--ccs-fg)] hover:bg-[var(--app-subtle)] md:inline-flex"
        >
          <CcSwitchLucideIcon name="download" class-name="h-4 w-4" />
          <span>{{ mcpMsg.importFromApps }}</span>
        </button>
        <button
          type="button"
          class="inline-flex h-8 items-center gap-1.5 rounded-md bg-[var(--app-action)] px-3.5 text-[13px] font-semibold text-white hover:bg-[var(--app-action-hover)]"
          @click="workbench.toast({ text: mcpMsg.add })"
        >
          <CcSwitchLucideIcon name="plus" class-name="h-4 w-4" :stroke-width="2" />
          <span class="hidden sm:inline">{{ mcpMsg.add }}</span>
        </button>
        <CcSwitchMenuButton
          large
          :label="mcpMsg.moreActions"
          width="w-[260px]"
          :items="[
            { label: mcpMsg.resync, onSelect: () => workbench.toast({ text: fmt(mcpMsg.toastResynced, { count: matrixApps.length }) }) },
            { label: mcpMsg.copyAll },
          ]"
        />
      </div>
    </header>

    <div class="flex h-14 shrink-0 items-center gap-3 px-4 sm:px-6">
      <div class="relative w-[320px] min-w-[150px] shrink">
        <CcSwitchLucideIcon name="search" class-name="pointer-events-none absolute start-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--app-fg3)]" />
        <input
          v-model="mcpQuery"
          type="text"
          :placeholder="mcpMsg.searchPlaceholder"
          class="h-8 w-full rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] pe-8 ps-8 text-[13px] text-[var(--ccs-fg)] outline-none placeholder:text-[var(--app-fg3)] focus:border-[var(--app-action)]"
        />
        <button
          v-if="mcpQuery"
          type="button"
          class="absolute end-1 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded text-[var(--app-fg3)] hover:text-[var(--ccs-fg)]"
          @click="mcpQuery = ''"
        >
          <CcSwitchLucideIcon name="x" class-name="h-3.5 w-3.5" />
        </button>
      </div>
      <span class="shrink-0 whitespace-nowrap text-xs tabular-nums text-[var(--app-fg2)]">
        {{ mcpQuery.trim() ? fmt(mcpMsg.countFiltered, { shown: filteredMcp.length, count: activeMcpList.length }) : fmt(mcpMsg.count, { count: activeMcpList.length }) }}
      </span>
      <span class="ms-auto hidden truncate text-xs text-[var(--app-fg2)] lg:block">{{ mcpMsg.overwriteNote }}</span>
    </div>

    <div class="flex min-h-0 flex-1 flex-col px-4 pb-5 sm:px-6">
      <div class="min-h-0 overflow-auto rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
        <div class="min-w-[560px]">
          <div class="sticky top-0 z-10 flex h-11 items-center border-b border-[var(--ccs-border)] bg-[var(--app-subtle)] pe-2 ps-4">
            <span class="flex-1 text-xs font-semibold text-[var(--app-fg2)]">{{ mcpMsg.columnName }}</span>
            <div v-for="appId in matrixApps" :key="appId" class="relative flex w-9 shrink-0 justify-center">
              <button
                type="button"
                class="flex h-10 w-[34px] flex-col items-center justify-center gap-0.5 rounded-md text-[var(--app-fg2)] transition-colors"
                :class="hoveredColApp === appId ? 'bg-[var(--app-selected)] text-[var(--ccs-fg)]' : ''"
                @mouseenter="hoveredColApp = appId; tooltip.show($event, appSpec(appId).label)"
                @mouseleave="hoveredColApp = null; tooltip.hide()"
                @click="toggleAllMcpForApp(appId)"
              >
                <CcSwitchAppIcon
                  :app="appSpec(appId)"
                  :size="16"
                  :badge-class="hoveredColApp === appId ? 'border-[var(--app-selected)] bg-[var(--app-selected)]' : 'border-[var(--app-subtle)] bg-[var(--app-subtle)]'"
                />
                <span class="text-[11px] font-medium tabular-nums leading-4">
                  {{ filteredMcp.filter((m) => (mcpMap[m.id] ?? []).includes(appId)).length }}
                </span>
              </button>
            </div>
            <span class="w-16 shrink-0" />
          </div>

          <div v-if="filteredMcp.length === 0" class="flex flex-col items-center gap-3 px-6 py-10 text-center">
            <p class="text-[13px] text-[var(--app-fg2)]">{{ fmt(mcpMsg.noMatch, { query: mcpQuery }) }}</p>
            <button
              type="button"
              class="h-7 rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-3 text-[13px] font-medium hover:bg-[var(--app-subtle)]"
              @click="mcpQuery = ''"
            >
              {{ mcpMsg.clearSearch }}
            </button>
          </div>
          <ul v-else>
            <li
              v-for="(item, idx) in filteredMcp"
              :key="item.id"
              class="flex h-14 items-center pe-2 ps-4 transition-colors hover:bg-[var(--app-subtle)]"
              :class="idx > 0 ? 'border-t border-[var(--ccs-border)]' : ''"
            >
              <div class="flex min-w-0 flex-1 flex-col pe-3">
                <div class="flex min-w-0 items-center gap-1.5">
                  <span class="truncate text-[13px] font-medium text-[var(--ccs-fg)]">{{ item.id }}</span>
                  <span class="inline-flex h-[18px] shrink-0 items-center rounded-full border border-[var(--app-border-strong)] px-1.5 font-mono text-[11px] text-[var(--app-fg2)]">
                    {{ item.transport }}
                  </span>
                  <span
                    v-if="(mcpMap[item.id] ?? []).length === 0"
                    class="inline-flex h-[18px] shrink-0 items-center rounded-full border border-[var(--app-border-strong)] px-1.5 text-[11px] text-[var(--app-fg2)]"
                  >
                    {{ mcpMsg.notEnabled }}
                  </span>
                </div>
                <span class="truncate font-mono text-xs text-[var(--app-fg2)]">{{ item.summary }}</span>
              </div>

              <div v-for="appId in matrixApps" :key="appId" class="flex w-9 shrink-0 justify-center">
                <button
                  type="button"
                  role="checkbox"
                  :aria-checked="(mcpMap[item.id] ?? []).includes(appId)"
                  class="flex h-7 w-7 items-center justify-center rounded-md"
                  @mouseenter="hoveredColApp = appId; tooltip.show($event, fmt((mcpMap[item.id] ?? []).includes(appId) ? matrixMsg.cellOn : matrixMsg.cellOff, { name: item.id, app: appSpec(appId).label }))"
                  @mouseleave="hoveredColApp = null; tooltip.hide()"
                  @click="toggleMcpCell(item.id, appId)"
                >
                  <span
                    class="flex h-4 w-4 items-center justify-center rounded-[4px] border-[1.5px] transition-colors"
                    :class="(mcpMap[item.id] ?? []).includes(appId) ? 'border-[var(--app-action)] bg-[var(--app-action)] text-white' : 'border-[var(--app-border-strong)] bg-[var(--app-surface)]'"
                  >
                    <CcSwitchLucideIcon v-if="(mcpMap[item.id] ?? []).includes(appId)" name="check" class-name="h-3 w-3" :stroke-width="3" />
                  </span>
                </button>
              </div>

              <div class="flex w-16 shrink-0 justify-end gap-1">
                <CcSwitchMenuButton
                  :label="msg.window.more"
                  width="w-[200px]"
                  :items="[
                    { label: mcpMsg.copyJson, onSelect: () => workbench.toast({ text: fmt(mcpMsg.toastCopiedOne, { id: item.id }) }) },
                    { label: mcpMsg.enableEverywhere, onSelect: () => setMcpRowAll(item.id, true) },
                    { label: mcpMsg.disableEverywhere, onSelect: () => setMcpRowAll(item.id, false) },
                    { label: commonMsg.delete, danger: true, separatorBefore: true, onSelect: () => deleteMcp(item.id) },
                  ]"
                />
              </div>
            </li>
          </ul>
        </div>
      </div>
    </div>
  </div>

  <!-- 2. Skills Pane -->
  <div v-else-if="pageKind === 'skills'" class="flex h-full min-h-0 flex-col" data-testid="ccs-demo-skills-pane">
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--ccs-border)] px-4 sm:pe-4 sm:ps-6">
      <div class="flex min-w-0 items-center gap-2.5">
        <span class="flex h-[22px] w-[22px] shrink-0 items-center justify-center text-[var(--ccs-fg)]">
          <CcSwitchLucideIcon name="skills" class-name="h-5 w-5" />
        </span>
        <h3 class="truncate text-lg font-semibold text-[var(--ccs-fg)]">Skills</h3>
        <button
          type="button"
          class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
          @mouseenter="tooltip.show($event, skillsMsg.help, skillsMsg.helpTitle)"
          @mouseleave="tooltip.hide()"
        >
          <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
        </button>
        <button
          v-if="skillUpdatableCount > 0"
          type="button"
          class="ms-1 hidden h-6 items-center rounded-md px-1.5 text-[13px] font-medium sm:inline-flex"
          :class="skillFilter === 'updates' ? 'bg-[var(--app-selected)] text-[var(--ccs-fg)]' : 'text-[var(--app-fg2)] underline decoration-[var(--app-border-strong)] underline-offset-[3px] hover:bg-[var(--app-subtle)]'"
          @click="skillsTab = 'installed'; skillFilter = skillFilter === 'updates' ? 'all' : 'updates'"
        >
          {{ fmt(skillsMsg.headerUpdates, { count: skillUpdatableCount }) }}
        </button>
      </div>
      <span class="flex-1" />
      <div class="flex shrink-0 items-center gap-2">
        <button
          type="button"
          class="inline-flex h-8 items-center gap-1.5 rounded-md bg-[var(--app-action)] px-3 text-[13px] font-semibold text-white hover:bg-[var(--app-action-hover)]"
          @click="skillsTab = 'discover'"
        >
          <CcSwitchLucideIcon name="plus" class-name="h-4 w-4" :stroke-width="2" />
          <span class="hidden sm:inline">{{ skillsMsg.add }}</span>
        </button>
        <CcSwitchMenuButton
          large
          :label="skillsMsg.moreActions"
          :items="[
            { label: skillsMsg.checkUpdates, onSelect: () => workbench.toast({ text: skillsMsg.noUpdates }) },
            { label: skillsMsg.restore },
            { label: skillsMsg.repos },
            { label: skillsMsg.storage },
          ]"
        />
      </div>
    </header>

    <div class="shrink-0 px-4 pt-1 sm:px-6">
      <div class="flex h-11 items-stretch gap-4 border-b border-[var(--ccs-border)]">
        <button
          v-for="tab in (['installed', 'discover'] as const)"
          :key="tab"
          type="button"
          class="relative -mb-px px-0.5 text-[13px]"
          :class="skillsTab === tab ? 'font-semibold text-[var(--ccs-fg)]' : 'font-medium text-[var(--app-fg2)] hover:text-[var(--ccs-fg)]'"
          @click="skillsTab = tab"
        >
          <span>{{ tab === 'installed' ? fmt(skillsMsg.viewInstalled, { count: installedSkills.length }) : skillsMsg.viewDiscover }}</span>
          <span v-if="skillsTab === tab" class="absolute inset-x-0 -bottom-px h-0.5 rounded-full bg-[var(--ccs-fg)]" />
        </button>

        <div class="ms-auto hidden items-center gap-2 sm:flex">
          <div v-if="skillsTab === 'installed'" class="relative w-[240px] min-w-[140px] shrink">
            <CcSwitchLucideIcon name="search" class-name="pointer-events-none absolute start-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--app-fg3)]" />
            <input
              v-model="skillsQuery"
              type="text"
              :placeholder="skillsMsg.searchPlaceholder"
              class="h-8 w-full rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] pe-8 ps-8 text-[13px] text-[var(--ccs-fg)] outline-none placeholder:text-[var(--app-fg3)]"
            />
          </div>
          <span v-else class="text-xs text-[var(--app-fg2)]">{{ skillsMsg.installTo }} Claude Code, Codex</span>
        </div>
      </div>
    </div>

    <!-- Installed Skills -->
    <template v-if="skillsTab === 'installed'">
      <div v-if="showUnmanagedBanner" class="shrink-0 px-4 pt-3 sm:px-6">
        <div class="flex items-center gap-3 rounded-[10px] bg-[var(--app-subtle)] py-2.5 pe-3 ps-4">
          <CcSwitchLucideIcon name="info" class-name="h-4 w-4 shrink-0 text-[var(--app-fg2)]" />
          <p class="min-w-0 flex-1 truncate text-[13px] text-[var(--ccs-fg)]">{{ fmt(skillsMsg.bannerUnmanaged, { count: 3 }) }}</p>
          <button type="button" class="h-7 rounded-md px-2.5 text-xs font-medium text-[var(--app-fg2)] hover:bg-[var(--app-surface)]" @click="showUnmanagedBanner = false">
            {{ skillsMsg.ignore }}
          </button>
        </div>
      </div>

      <div class="flex min-h-0 flex-1 flex-col px-4 pb-5 pt-3 sm:px-6">
        <div class="min-h-0 overflow-auto rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
          <div class="min-w-[600px]">
            <div class="sticky top-0 z-10 flex h-11 items-center border-b border-[var(--ccs-border)] bg-[var(--app-subtle)] px-2">
              <div class="ms-2 flex min-w-0 flex-1 items-center gap-2 text-xs font-medium text-[var(--app-fg2)]">
                <button
                  v-for="f in (['all', 'updates', 'none'] as const)"
                  :key="f"
                  type="button"
                  class="rounded px-2 py-1"
                  :class="skillFilter === f ? 'bg-[var(--app-surface)] text-[var(--ccs-fg)]' : 'hover:text-[var(--ccs-fg)]'"
                  @click="skillFilter = f"
                >
                  {{ f === 'all' ? skillsMsg.filterAll : f === 'updates' ? skillsMsg.filterUpdates : skillsMsg.filterNone }}
                </button>
              </div>
              <div v-for="appId in matrixApps" :key="appId" class="flex w-9 shrink-0 justify-center">
                <div class="flex h-10 w-[34px] flex-col items-center justify-center gap-0.5 text-[var(--app-fg2)]">
                  <CcSwitchAppIcon :app="appSpec(appId)" :size="16" badge-class="border-[var(--app-subtle)] bg-[var(--app-subtle)]" />
                  <span class="text-[11px] font-medium tabular-nums">
                    {{ filteredSkills.filter((sk) => sk.apps.includes(appId)).length }}
                  </span>
                </div>
              </div>
              <span class="w-16 shrink-0" />
            </div>

            <ul>
              <li
                v-for="(sk, idx) in filteredSkills"
                :key="sk.name"
                class="flex h-14 items-center px-2 transition-colors hover:bg-[var(--app-subtle)]"
                :class="idx > 0 ? 'border-t border-[var(--ccs-border)]' : ''"
              >
                <div class="ms-2 flex min-w-0 flex-1 flex-col pe-3">
                  <div class="flex min-w-0 items-center gap-1.5">
                    <span class="truncate text-[13px] font-medium text-[var(--ccs-fg)]">{{ sk.name }}</span>
                    <span v-if="sk.update" class="inline-flex h-[18px] shrink-0 items-center rounded-full bg-[var(--app-warning-soft)] px-1.5 text-[11px] font-medium text-[var(--app-warning-text)]">
                      {{ skillsMsg.updateAvailable }}
                    </span>
                    <span v-if="sk.apps.length === 0" class="inline-flex h-[18px] shrink-0 items-center rounded-full border border-[var(--app-border-strong)] px-1.5 text-[11px] text-[var(--app-fg2)]">
                      {{ mcpMsg.notEnabled }}
                    </span>
                  </div>
                  <span class="truncate text-xs text-[var(--app-fg2)]">
                    {{ sk.description[lang] }} · {{ sk.repo ?? skillsMsg.sourceLocal }}
                  </span>
                </div>

                <div v-for="appId in matrixApps" :key="appId" class="flex w-9 shrink-0 justify-center">
                  <button
                    type="button"
                    role="checkbox"
                    :aria-checked="sk.apps.includes(appId)"
                    class="flex h-7 w-7 items-center justify-center rounded-md"
                    @click="toggleSkillCell(sk.name, appId)"
                  >
                    <span
                      class="flex h-4 w-4 items-center justify-center rounded-[4px] border-[1.5px]"
                      :class="sk.apps.includes(appId) ? 'border-[var(--app-action)] bg-[var(--app-action)] text-white' : 'border-[var(--app-border-strong)] bg-[var(--app-surface)]'"
                    >
                      <CcSwitchLucideIcon v-if="sk.apps.includes(appId)" name="check" class-name="h-3 w-3" :stroke-width="3" />
                    </span>
                  </button>
                </div>

                <div class="flex w-16 shrink-0 justify-end">
                  <CcSwitchMenuButton
                    :label="msg.window.more"
                    width="w-[180px]"
                    :items="[
                      ...(sk.update ? [{ label: skillsMsg.update, onSelect: () => markSkillUpdated(sk.name) }] : []),
                      { label: skillsMsg.openDir },
                      { label: skillsMsg.uninstall, danger: true, separatorBefore: true, onSelect: () => uninstallSkill(sk.name) },
                    ]"
                  />
                </div>
              </li>
            </ul>
          </div>
        </div>
      </div>
    </template>

    <!-- Discover Skills -->
    <template v-else>
      <div class="flex h-12 shrink-0 items-center gap-2 px-4 sm:px-6">
        <div class="inline-flex h-7 rounded-lg bg-[var(--app-subtle)] p-[3px]">
          <button
            type="button"
            class="rounded-[5px] px-2.5 text-xs font-medium"
            :class="discoverSource === 'repos' ? 'bg-[var(--app-surface)] text-[var(--ccs-fg)] shadow-sm' : 'text-[var(--app-fg2)]'"
            @click="discoverSource = 'repos'"
          >
            {{ fmt(skillsMsg.sourceRepos, { count: 2 }) }}
          </button>
          <button
            type="button"
            class="rounded-[5px] px-2.5 text-xs font-medium"
            :class="discoverSource === 'skillssh' ? 'bg-[var(--app-surface)] text-[var(--ccs-fg)] shadow-sm' : 'text-[var(--app-fg2)]'"
            @click="discoverSource = 'skillssh'"
          >
            skills.sh
          </button>
        </div>
        <span class="flex-1" />
        <div class="relative w-[220px] min-w-[120px] shrink">
          <CcSwitchLucideIcon name="search" class-name="pointer-events-none absolute start-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--app-fg3)]" />
          <input
            v-model="discoverQuery"
            type="text"
            :placeholder="skillsMsg.discoverSearch"
            class="h-8 w-full rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] pe-4 ps-8 text-[13px] text-[var(--ccs-fg)] outline-none"
          />
        </div>
      </div>

      <div class="flex min-h-0 flex-1 flex-col px-4 pb-5 sm:px-6">
        <ul class="min-h-0 overflow-auto rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
          <li
            v-for="(item, idx) in filteredDiscoverSkills"
            :key="`${item.repo}/${item.name}`"
            class="flex h-14 items-center gap-3 pe-3 ps-4"
            :class="idx > 0 ? 'border-t border-[var(--ccs-border)]' : ''"
          >
            <div class="flex min-w-0 flex-1 flex-col">
              <div class="flex min-w-0 items-center gap-1.5">
                <span class="truncate text-[13px] font-medium text-[var(--ccs-fg)]">{{ item.name }}</span>
                <span v-if="discoverSource === 'repos'" class="inline-flex h-[18px] items-center rounded-full border border-[var(--app-border-strong)] px-1.5 font-mono text-[11px] text-[var(--app-fg2)]">
                  {{ item.repo }}
                </span>
                <span v-else class="inline-flex items-center gap-1 text-xs text-[var(--app-fg3)]">
                  <CcSwitchLucideIcon name="download" class-name="h-3 w-3" />
                  {{ item.installs }}
                </span>
              </div>
              <span class="truncate text-xs text-[var(--app-fg2)]">
                {{ discoverSource === 'repos' ? item.description[lang] : `skills.sh · ${item.repo}` }}
              </span>
            </div>
            <div class="flex w-[160px] shrink-0 justify-end">
              <button
                v-if="installedSkillMap.has(item.name)"
                type="button"
                class="inline-flex h-7 items-center gap-1 rounded-md px-2 text-[13px] text-[var(--app-fg2)] hover:bg-[var(--app-subtle)]"
                @click="skillsTab = 'installed'"
              >
                <span>{{ fmt(skillsMsg.installed, { count: installedSkillMap.get(item.name) ?? 2 }) }}</span>
                <CcSwitchLucideIcon name="chevronRight" class-name="h-3.5 w-3.5" />
              </button>
              <span v-else-if="installingSkill === item.name" class="inline-flex h-7 items-center gap-1.5 text-[13px] text-[var(--app-fg2)]">
                <CcSwitchLucideIcon name="loaderCircle" class-name="h-3.5 w-3.5 animate-spin" />
                {{ skillsMsg.installing }}
              </span>
              <button
                v-else
                type="button"
                class="h-7 rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-3 text-[13px] font-medium hover:bg-[var(--app-subtle)]"
                @click="installDiscoverSkill(item.name)"
              >
                {{ skillsMsg.install }}
              </button>
            </div>
          </li>
        </ul>
      </div>
    </template>
  </div>

  <!-- 3. Prompts Pane -->
  <div v-else-if="pageKind === 'prompts'" class="flex h-full min-h-0 flex-col" data-testid="ccs-demo-prompts-pane">
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--ccs-border)] px-4 sm:pe-4 sm:ps-6">
      <div class="flex min-w-0 items-center gap-2.5">
        <span class="flex h-[22px] w-[22px] shrink-0 items-center justify-center text-[var(--ccs-fg)]">
          <CcSwitchLucideIcon name="bookOpen" class-name="h-[18px] w-[18px]" :stroke-width="2" />
        </span>
        <h3 class="truncate text-lg font-semibold text-[var(--ccs-fg)]">{{ msg.window.nav.prompts }}</h3>
        <button
          type="button"
          class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
          @mouseenter="tooltip.show($event, promptApp === 'hermes' ? promptsMsg.helpHermes : promptsMsg.help, promptApp === 'hermes' ? promptsMsg.helpHermesTitle : promptsMsg.helpTitle)"
          @mouseleave="tooltip.hide()"
        >
          <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
        </button>
      </div>
      <span class="flex-1" />
      <div class="flex shrink-0 items-center gap-2">
        <button
          type="button"
          class="inline-flex h-8 items-center gap-1.5 rounded-md bg-[var(--app-action)] px-3.5 text-[13px] font-semibold text-white hover:bg-[var(--app-action-hover)]"
        >
          <CcSwitchLucideIcon name="plus" class-name="h-4 w-4" :stroke-width="2" />
          <span class="hidden sm:inline">{{ promptsMsg.add }}</span>
        </button>
        <CcSwitchMenuButton
          large
          :label="promptsMsg.moreActions"
          width="w-[280px]"
          :items="[
            { label: fmt(promptsMsg.importCurrent, { file: promptTarget.file }), disabled: Boolean(enabledPromptItem) },
            { label: promptsMsg.openFolder },
            { label: promptsMsg.copyFilePath, onSelect: () => workbench.toast({ text: fmt(promptsMsg.copiedPath, { path: promptTarget.path }) }) },
          ]"
        />
      </div>
    </header>

    <div class="mt-0.5 flex h-14 shrink-0 items-center gap-3 px-4 sm:px-6">
      <span ref="promptAppAnchorRef" class="inline-flex">
        <button
          type="button"
          class="inline-flex h-8 items-center gap-2 rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] pe-2 ps-2.5 text-[13px] font-medium hover:bg-[var(--app-subtle)]"
          @click="promptAppOpen = !promptAppOpen"
        >
          <CcSwitchAppIcon :app="appSpec(promptApp)" :size="16" badge-class="border-[var(--app-surface)] bg-[var(--app-surface)]" />
          <span class="hidden sm:inline">{{ appSpec(promptApp).label }}</span>
          <span class="text-xs font-normal tabular-nums text-[var(--app-fg2)]">{{ currentPromptBucket.items.length }}</span>
          <CcSwitchLucideIcon name="chevronDown" class-name="h-3.5 w-3.5 text-[var(--app-fg2)]" />
        </button>
        <CcSwitchPopover
          :anchor="promptAppAnchorRef"
          :open="promptAppOpen"
          align="start"
          panel-class="w-[280px]"
          @close="promptAppOpen = false"
        >
          <div class="flex flex-col">
            <button
              v-for="appId in promptAppChoices"
              :key="appId"
              type="button"
              class="flex min-h-8 items-center gap-2 rounded-md pe-2 ps-2.5 text-[13px] hover:bg-[var(--app-subtle)]"
              :class="appId === promptApp ? 'font-medium' : ''"
              @click="promptApp = appId; promptQuery = ''; promptAppOpen = false"
            >
              <CcSwitchAppIcon :app="appSpec(appId)" :size="16" badge-class="border-[var(--app-surface)] bg-[var(--app-surface)]" />
              <span class="flex-1 text-start">{{ appSpec(appId).label }}</span>
              <span class="text-xs tabular-nums text-[var(--app-fg2)]">{{ promptsState[appId]?.items.length ?? 0 }}</span>
              <CcSwitchLucideIcon name="check" class-name="h-3.5 w-3.5" :class="appId !== promptApp ? 'invisible' : ''" />
            </button>
            <div class="mx-1.5 my-1 h-px bg-[var(--ccs-border)]" />
            <p class="px-2.5 pb-1.5 pt-1 text-xs text-[var(--app-fg2)]">
              <span class="block">{{ promptsMsg.appNoteDesktop }}</span>
              <span class="block">{{ promptsMsg.appNoteOpenclaw }}</span>
            </p>
          </div>
        </CcSwitchPopover>
      </span>

      <span class="min-w-0 shrink truncate text-xs text-[var(--app-fg2)]">
        <span class="hidden sm:inline">{{ promptsMsg.targetFile }} </span>
        <code class="font-mono text-xs text-[var(--ccs-fg)]">{{ promptTarget.path }}</code>
        · {{ enabledPromptItem ? enabledPromptItem.size : promptsMsg.fileEmpty }}
      </span>
      <span class="flex-1" />
      <div class="relative hidden w-[220px] shrink-0 md:block">
        <CcSwitchLucideIcon name="search" class-name="pointer-events-none absolute start-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--app-fg3)]" />
        <input
          v-model="promptQuery"
          type="text"
          :placeholder="promptsMsg.searchPlaceholder"
          class="h-8 w-full rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] pe-4 ps-8 text-[13px] outline-none"
        />
      </div>
    </div>

    <div class="flex min-h-0 flex-1 flex-col px-4 pb-5 sm:px-6">
      <ul class="min-h-0 overflow-y-auto rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
        <li
          v-for="(item, idx) in filteredPrompts"
          :key="item.id"
          class="flex h-[60px] items-center gap-3 pe-2 ps-4 transition-colors hover:bg-[var(--app-subtle)]"
          :class="[idx > 0 ? 'border-t border-[var(--ccs-border)]' : '', currentPromptBucket.enabled === item.id ? 'bg-[var(--app-subtle)]' : '']"
        >
          <div class="flex min-w-0 flex-1 flex-col">
            <div class="flex items-center gap-1.5">
              <span class="truncate text-[13px] font-medium text-[var(--ccs-fg)]">{{ item.name[lang] }}</span>
              <span
                v-if="currentPromptBucket.enabled === item.id"
                class="inline-flex h-[18px] shrink-0 items-center rounded-full bg-[var(--app-success-soft)] px-[7px] text-[11px] font-medium text-[var(--app-success-text)]"
              >
                {{ promptsMsg.enabled }}
              </span>
            </div>
            <span class="truncate text-xs text-[var(--app-fg2)]">
              {{ item.description[lang] }} · {{ item.size }} · {{ item.updated[lang] }}
            </span>
          </div>
          <button
            type="button"
            class="h-7 min-w-14 shrink-0 rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-3 text-[13px] font-medium hover:bg-[var(--app-subtle)]"
            @click="togglePrompt(item)"
          >
            {{ currentPromptBucket.enabled === item.id ? promptsMsg.disable : promptsMsg.enable }}
          </button>
          <CcSwitchMenuButton
            :label="msg.window.more"
            width="w-[220px]"
            :items="[
              { label: promptsMsg.copyContent, onSelect: () => workbench.toast({ text: fmt(promptsMsg.copiedContent, { name: item.name[lang] }) }) },
              {
                label: commonMsg.delete,
                danger: true,
                separatorBefore: true,
                disabled: currentPromptBucket.enabled === item.id,
                hint: currentPromptBucket.enabled === item.id ? promptsMsg.deleteBlocked : undefined,
                onSelect: () => deletePrompt(item),
              },
            ]"
          />
        </li>
      </ul>
    </div>
  </div>

  <!-- 4. Sessions Pane (+ Reader) -->
  <CcSwitchSessionReader
    v-else-if="pageKind === 'sessions' && openSession"
    :session="openSession"
    :has-prev="openSessionIndex > 0"
    :has-next="openSessionIndex < filteredSessions.length - 1"
    @back="openSessionId = null"
    @prev="openSessionId = filteredSessions[openSessionIndex - 1]?.id ?? null"
    @next="openSessionId = filteredSessions[openSessionIndex + 1]?.id ?? null"
  />

  <div v-else-if="pageKind === 'sessions'" class="flex h-full min-h-0 flex-col" data-testid="ccs-demo-sessions-pane">
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--ccs-border)] px-4 sm:pe-4 sm:ps-6">
      <div class="flex min-w-0 items-center gap-2.5">
        <span class="flex h-[22px] w-[22px] shrink-0 items-center justify-center text-[var(--ccs-fg)]">
          <CcSwitchLucideIcon name="history" class-name="h-5 w-5" :stroke-width="1.5" />
        </span>
        <h3 class="truncate text-lg font-semibold text-[var(--ccs-fg)]">{{ msg.window.nav.sessions }}</h3>
        <button
          type="button"
          class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
          @mouseenter="tooltip.show($event, sessionsMsg.help, sessionsMsg.helpTitle)"
          @mouseleave="tooltip.hide()"
        >
          <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
        </button>
      </div>
      <span class="flex-1" />
      <CcSwitchMenuButton
        large
        :label="sessionsMsg.moreActions"
        width="w-[196px]"
        :items="[
          { label: sessionsMsg.refreshList, onSelect: () => workbench.toast({ text: sessionsMsg.listRefreshed }) },
          { label: sessionsMsg.selectMany },
          { label: sessionsMsg.preferredTerminal },
          { label: sessionsMsg.whereMenu, separatorBefore: true },
        ]"
      />
    </header>

    <div class="flex shrink-0 items-center gap-2 px-4 pt-3.5 sm:px-6">
      <span ref="sessAppAnchorRef" class="inline-flex">
        <button
          type="button"
          class="inline-flex h-8 items-center gap-2 rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] pe-2 ps-2.5 text-[13px] font-medium hover:bg-[var(--app-subtle)]"
          @click="sessAppOpen = !sessAppOpen"
        >
          <CcSwitchLucideIcon v-if="sessionAppFilter === 'all'" name="layoutGrid" class-name="h-4 w-4 text-[var(--app-fg2)]" />
          <CcSwitchAppIcon v-else :app="appSpec(sessionAppFilter)" :size="16" badge-class="border-[var(--app-surface)] bg-[var(--app-surface)]" />
          <span class="hidden sm:inline">{{ sessionAppFilter === 'all' ? sessionsMsg.allApps : appSpec(sessionAppFilter).label }}</span>
          <span class="text-xs font-normal tabular-nums text-[var(--app-fg2)]">
            {{ sessionAppFilter === 'all' ? activeSessions.length : countSessionsForApp(sessionAppFilter) || sessionsMsg.noSessions }}
          </span>
          <CcSwitchLucideIcon name="chevronDown" class-name="h-3.5 w-3.5 text-[var(--app-fg2)]" />
        </button>
        <CcSwitchPopover
          :anchor="sessAppAnchorRef"
          :open="sessAppOpen"
          align="start"
          panel-class="w-[240px]"
          @close="sessAppOpen = false"
        >
          <div class="flex flex-col">
            <button
              v-for="(appId, idx) in ['all', ...sortedSessionApps]"
              :key="appId"
              type="button"
              class="flex h-8 w-full items-center gap-2 rounded-md px-2.5 text-[13px] hover:bg-[var(--app-subtle)]"
              :class="[idx === 1 ? 'mt-1 border-t border-[var(--ccs-border)] pt-1' : '', appId === sessionAppFilter ? 'font-medium' : '']"
              @click="sessionAppFilter = appId; sessAppOpen = false"
            >
              <CcSwitchLucideIcon v-if="appId === 'all'" name="layoutGrid" class-name="h-4 w-4 text-[var(--app-fg2)]" />
              <CcSwitchAppIcon v-else :app="appSpec(appId)" :size="16" badge-class="border-[var(--app-surface)] bg-[var(--app-surface)]" />
              <span class="flex-1 text-start">{{ appId === 'all' ? sessionsMsg.allApps : appSpec(appId).label }}</span>
              <span class="text-xs tabular-nums text-[var(--app-fg2)]">
                {{ appId === 'all' ? activeSessions.length : countSessionsForApp(appId) || sessionsMsg.noSessions }}
              </span>
              <CcSwitchLucideIcon name="check" class-name="h-3.5 w-3.5" :class="appId !== sessionAppFilter ? 'invisible' : ''" />
            </button>
          </div>
        </CcSwitchPopover>
      </span>

      <div class="relative min-w-0 flex-1">
        <CcSwitchLucideIcon name="search" class-name="pointer-events-none absolute start-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--app-fg3)]" />
        <input
          v-model="sessionQuery"
          type="text"
          :placeholder="sessionsMsg.searchPlaceholder"
          class="h-8 w-full rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] pe-4 ps-8 text-[13px] outline-none"
        />
      </div>

      <div role="radiogroup" class="hidden h-8 shrink-0 rounded-lg bg-[var(--app-subtle)] p-[3px] sm:inline-flex">
        <button
          type="button"
          class="rounded-[5px] px-3 text-[13px] font-medium"
          :class="sessionGroupMode === 'time' ? 'bg-[var(--app-surface)] text-[var(--ccs-fg)] shadow-sm' : 'text-[var(--app-fg2)]'"
          @click="sessionGroupMode = 'time'"
        >
          {{ sessionsMsg.groupByTime }}
        </button>
        <button
          type="button"
          class="rounded-[5px] px-3 text-[13px] font-medium"
          :class="sessionGroupMode === 'project' ? 'bg-[var(--app-surface)] text-[var(--ccs-fg)] shadow-sm' : 'text-[var(--app-fg2)]'"
          @click="sessionGroupMode = 'project'"
        >
          {{ sessionsMsg.groupByProject }}
        </button>
      </div>
    </div>

    <div class="mx-4 mb-4 mt-3 min-h-0 overflow-y-auto rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)] sm:mx-6">
      <div v-if="filteredSessions.length === 0" class="flex flex-col items-center gap-3 px-6 py-10 text-center">
        <p class="text-[13px] text-[var(--app-fg2)]">{{ fmt(sessionsMsg.emptySearch, { query: sessionQuery }) }}</p>
        <button type="button" class="h-7 rounded-md border border-[var(--app-border-strong)] px-3 text-[13px]" @click="sessionQuery = ''">
          {{ sessionsMsg.clearSearch }}
        </button>
      </div>

      <template v-else-if="sessionGroupMode === 'project'">
        <div v-for="(proj, pIdx) in sessionProjects" :key="proj">
          <div
            class="sticky top-0 z-[1] flex h-9 items-center gap-3 bg-[var(--app-subtle)] pe-3 ps-2.5"
            :class="pIdx > 0 ? 'border-t border-[var(--ccs-border)]' : ''"
          >
            <button
              type="button"
              class="flex h-7 min-w-0 flex-1 items-center gap-2.5 rounded-md pe-2 ps-1.5 text-[13px] font-semibold text-[var(--ccs-fg)] hover:bg-[var(--app-selected)]"
              @click="toggleProjectGroup(proj)"
            >
              <CcSwitchLucideIcon
                name="chevronDown"
                class-name="h-4 w-4 shrink-0 text-[var(--app-fg2)] transition-transform duration-150"
                :class="!isProjectExpanded(proj) ? '-rotate-90' : ''"
              />
              <span class="shrink-0">{{ proj === '__unknown__' ? sessionsMsg.unknownDirectory : proj.split('/').pop() }}</span>
              <span v-if="proj !== '__unknown__'" class="hidden truncate text-xs font-normal text-[var(--app-fg3)] sm:inline">{{ proj }}</span>
            </button>
            <span class="max-w-[55%] truncate text-xs tabular-nums text-[var(--app-fg2)]">
              {{ fmt(sessionsMsg.groupMeta, { count: sessionsByProject(proj).length, time: sessionAgo(sessionsByProject(proj)[0]) }) }}
            </span>
          </div>

          <template v-if="isProjectExpanded(proj)">
            <div
              v-for="s in sessionsByProject(proj)"
              :key="s.id"
              class="group relative flex h-14 items-center gap-2.5 border-t border-[var(--ccs-border)] bg-[var(--app-surface)] pe-3 ps-4 hover:bg-[var(--app-subtle)]"
            >
              <button type="button" data-ccs-overlay class="absolute inset-0" :aria-label="s.title[lang]" @click="openSessionId = s.id" />
              <div class="pointer-events-none flex min-w-0 flex-1 flex-col">
                <div class="flex min-w-0 items-center gap-2">
                  <CcSwitchAppIcon v-if="sessionAppFilter === 'all'" :app="appSpec(s.app)" :size="16" badge-class="border-[var(--app-surface)] bg-[var(--app-surface)]" />
                  <span class="truncate text-[13px] font-medium text-[var(--ccs-fg)]">{{ s.title[lang] }}</span>
                </div>
                <div class="flex min-h-[18px] min-w-0 items-center gap-1.5 whitespace-nowrap text-xs text-[var(--app-fg2)]" :class="sessionAppFilter === 'all' ? 'ps-6' : ''">
                  <span class="truncate">{{ fmt(sessionsMsg.lastPrefix, { text: s.last[lang] }) }}</span>
                  <span v-if="s.archived" class="inline-flex h-[18px] shrink-0 items-center rounded-full border border-[var(--app-border-strong)] px-1.5 text-[11px]">
                    {{ sessionsMsg.archived }}
                  </span>
                </div>
              </div>
              <div class="relative w-[92px] shrink-0 self-stretch">
                <span class="absolute end-0 top-[9px] text-xs tabular-nums text-[var(--app-fg2)] group-hover:invisible">{{ sessionAgo(s) }}</span>
                <div class="invisible absolute -end-0.5 top-3.5 flex gap-0.5 group-hover:visible">
                  <button
                    v-if="s.app !== 'hermes' && s.app !== 'openclaw'"
                    type="button"
                    class="inline-flex h-7 w-7 items-center justify-center rounded-md text-[var(--app-fg2)] hover:bg-[var(--app-selected)]"
                    @click.stop="workbench.toast({ text: fmt(sessionsMsg.launchedIn, { terminal: 'Terminal' }) })"
                  >
                    <CcSwitchLucideIcon name="play" class-name="h-3.5 w-3.5" />
                  </button>
                  <CcSwitchMenuButton
                    :label="msg.window.more"
                    width="w-[200px]"
                    :items="[
                      { label: sessionsMsg.copySessionId, onSelect: () => workbench.toast({ text: sessionsMsg.sessionIdCopied }) },
                      { label: sessionsMsg.copySourcePath, onSelect: () => workbench.toast({ text: sessionsMsg.sourcePathCopied }) },
                      { label: commonMsg.delete, danger: true, separatorBefore: true, onSelect: () => deletedSessions.push(s.id) },
                    ]"
                  />
                </div>
              </div>
            </div>
          </template>
        </div>
      </template>

      <template v-else>
        <template v-for="(bucket, bIdx) in TIME_BUCKETS" :key="bucket">
          <div v-if="sessionsByBucket(bucket).length > 0">
            <h4 class="flex h-7 items-end px-4 pb-0.5 text-xs font-semibold text-[var(--app-fg2)]" :class="bIdx > 0 ? 'border-t border-[var(--ccs-border)]' : ''">
              {{ sessionsMsg[bucket] }}
            </h4>
            <div
              v-for="(s, sIdx) in sessionsByBucket(bucket)"
              :key="s.id"
              class="group relative flex h-14 items-center gap-2.5 bg-[var(--app-surface)] pe-3 ps-4 hover:bg-[var(--app-subtle)]"
              :class="sIdx > 0 ? 'border-t border-[var(--ccs-border)]' : ''"
            >
              <button type="button" data-ccs-overlay class="absolute inset-0" :aria-label="s.title[lang]" @click="openSessionId = s.id" />
              <div class="pointer-events-none flex min-w-0 flex-1 flex-col">
                <span class="truncate text-[13px] font-medium text-[var(--ccs-fg)]">{{ s.title[lang] }}</span>
                <span class="truncate text-xs text-[var(--app-fg2)]">{{ fmt(sessionsMsg.lastPrefix, { text: s.last[lang] }) }}</span>
              </div>
              <span class="text-xs tabular-nums text-[var(--app-fg2)]">{{ sessionAgo(s) }}</span>
            </div>
          </div>
        </template>
      </template>
    </div>
  </div>

  <!-- 5. Accounts / Auth Pane -->
  <div v-else-if="pageKind === 'auth'" class="flex h-full min-h-0 flex-col" data-testid="ccs-demo-auth-pane">
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--ccs-border)] px-4 sm:pe-4 sm:ps-6">
      <div class="flex min-w-0 items-center gap-2.5">
        <span class="flex h-[22px] w-[22px] shrink-0 items-center justify-center text-[var(--ccs-fg)]">
          <CcSwitchLucideIcon name="keyRound" class-name="h-5 w-5" :stroke-width="1.5" />
        </span>
        <h3 class="truncate text-lg font-semibold text-[var(--ccs-fg)]">{{ msg.window.nav.auth }}</h3>
        <button
          type="button"
          class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
          @mouseenter="tooltip.show($event, authMsg.help, authMsg.helpTitle)"
          @mouseleave="tooltip.hide()"
        >
          <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
        </button>
        <span class="ms-1 inline-flex h-5 items-center rounded-full border border-[var(--ccs-border)] px-1.5 text-[11px] font-medium text-[var(--app-fg2)]">
          {{ authMsg.beta }}
        </span>
      </div>
      <span class="flex-1" />
      <button
        type="button"
        class="inline-flex h-8 items-center gap-1.5 rounded-md px-3 text-[13px] font-medium text-[var(--ccs-fg)] hover:bg-[var(--app-subtle)]"
        @click="refreshAllAuth"
      >
        <CcSwitchLucideIcon name="refreshCw" class-name="h-4 w-4" :class="authRefreshing ? 'animate-spin' : ''" />
        <span class="hidden sm:inline">{{ authMsg.refreshAll }}</span>
      </button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto px-4 pb-10 pt-4 sm:px-6">
      <div class="flex flex-col gap-3">
        <!-- Auto open browser toggle -->
        <div class="flex items-center gap-4 rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)] px-4 py-3">
          <div class="min-w-0 flex-1 space-y-0.5">
            <p class="text-sm font-medium text-[var(--ccs-fg)]">{{ authMsg.openBrowserTitle }}</p>
            <p class="text-xs text-[var(--app-fg2)]">{{ authMsg.openBrowserBody }}</p>
          </div>
          <button
            type="button"
            role="switch"
            :aria-checked="authOpenBrowser"
            class="relative inline-flex h-[18px] w-[30px] shrink-0 items-center rounded-full transition-colors"
            :class="authOpenBrowser ? 'bg-[var(--app-action)]' : 'bg-[var(--ccs-switch-off)]'"
            @click="authOpenBrowser = !authOpenBrowser"
          >
            <span class="absolute left-0.5 h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform" :class="authOpenBrowser ? 'translate-x-3' : 'translate-x-0'" />
          </button>
        </div>

        <!-- 8 Auth Service Cards -->
        <section
          v-for="svc in AUTH_SERVICES"
          :key="svc.id"
          class="rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]"
        >
          <div class="flex h-[52px] items-center gap-3 pe-2.5 ps-4">
            <span class="flex h-7 w-7 items-center justify-center">
              <span
                v-if="svc.iconSvg"
                class="inline-flex h-5 w-5 items-center justify-center [&>svg]:block [&>svg]:h-full [&>svg]:w-full"
                v-html="svc.iconSvg"
              />
              <img v-else-if="svc.iconSrc" :src="svc.iconSrc" :alt="svc.name" class="h-5 w-5 object-contain" />
            </span>
            <div class="flex min-w-0 flex-1 items-center gap-0.5">
              <h4 class="truncate text-sm font-semibold text-[var(--ccs-fg)]">{{ svc.name }}</h4>
              <button
                type="button"
                class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
                @mouseenter="tooltip.show($event, authServiceHelp(svc.id), fmt(authMsg.groupHelpTitle, { service: svc.name }))"
                @mouseleave="tooltip.hide()"
              >
                <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
              </button>
            </div>
            <button
              type="button"
              class="inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] pe-3 ps-2.5 text-[13px] font-medium hover:bg-[var(--app-subtle)]"
              @click="startAddAuthAccount(svc)"
            >
              <CcSwitchLucideIcon name="plus" class-name="h-3.5 w-3.5" :stroke-width="2" />
              <span class="hidden sm:inline">{{ authMsg.addAccount }}</span>
            </button>
          </div>

          <div
            v-for="acc in authAccounts[svc.id] ?? []"
            :key="acc.login"
            class="flex min-h-[56px] items-center gap-3 border-t border-[var(--ccs-border)] py-2 pe-2.5 ps-4"
          >
            <span class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-[var(--ccs-border)] bg-[var(--app-subtle)] text-xs font-semibold uppercase text-[var(--app-fg2)]">
              {{ acc.login[0] }}
            </span>
            <div class="flex min-w-0 flex-1 flex-col">
              <div class="flex min-w-0 items-center gap-1.5">
                <span class="truncate text-[13px] font-medium text-[var(--ccs-fg)]">{{ acc.login }}</span>
                <span v-if="acc.isDefault" class="inline-flex h-[18px] items-center rounded-full border border-[var(--app-border-strong)] px-1.5 text-[11px] font-medium text-[var(--app-fg2)]">
                  {{ authDefaultLabel(svc.id) }}
                </span>
                <span v-if="acc.needsReauth" class="inline-flex h-[18px] items-center rounded-full bg-[var(--app-danger-soft)] px-1.5 text-[11px] font-medium text-[var(--app-danger-text)]">
                  {{ authMsg.needsReauth }}
                </span>
              </div>
              <span class="truncate text-xs text-[var(--app-fg2)]">
                {{ acc.needsReauth ? authMsg.xaiReauthNote : formatAuthAccountSubtitle(svc.id, acc) }}
              </span>
            </div>

            <!-- Quotas / Points / Wallet -->
            <div v-if="!acc.needsReauth" class="hidden w-[212px] shrink-0 flex-col gap-0.5 md:flex">
              <span v-if="acc.noSubscription" class="text-end text-xs text-[var(--app-fg2)]">{{ authMsg.kimiNoSubscription }}</span>
              <div v-for="q in acc.quota" :key="q.tier" class="flex h-[18px] items-center gap-2 text-xs">
                <span class="min-w-[52px] shrink-0 text-[var(--app-fg2)]">{{ authTierLabel(q.tier) }}</span>
                <span class="relative h-1 min-w-[24px] flex-1 rounded-full bg-[var(--app-chart-grid)]">
                  <span class="absolute inset-y-0 start-0 rounded-full" :class="quotaBarClass(q.left)" :style="{ width: `${q.left}%` }" />
                </span>
                <span class="min-w-14 shrink-0 text-end tabular-nums" :class="quotaTextClass(q.left)">
                  {{ q.left === 0 ? authMsg.usedUp : fmt(authMsg.quotaLeft, { value: q.left }) }}
                </span>
              </div>
              <span v-if="acc.wallet" class="text-end text-xs tabular-nums text-[var(--ccs-fg)]">
                {{ fmt(authMsg.dshBalance, { amount: acc.wallet.balance }) }}
              </span>
              <span v-if="acc.credits" class="text-end text-xs tabular-nums text-[var(--app-fg2)]">
                {{ authMsg.creditsLabel }} {{ new Intl.NumberFormat(lang).format(acc.credits) }}
              </span>
            </div>

            <CcSwitchMenuButton
              :label="msg.window.more"
              width="w-[176px]"
              :items="[
                ...(AUTH_DEFAULT_SUPPORTED.includes(svc.id) && !acc.isDefault && !acc.needsReauth
                  ? [{ label: authMsg.setDefault, onSelect: () => setDefaultAuth(svc.id, acc.login) }]
                  : []),
                { label: authMsg.removeAccount, danger: true, onSelect: () => removeAuthAccount(svc.id, acc.login) },
              ]"
            />
          </div>

          <div v-if="(authAccounts[svc.id] ?? []).length === 0" class="border-t border-[var(--ccs-border)] py-3.5 pe-3 ps-4 text-xs text-[var(--app-fg2)]">
            {{ fmt(authMsg.empty, { service: svc.name }) }}
          </div>

          <!-- Pending device-code / browser auth banner -->
          <div v-if="pendingAuthSvc === svc.id" class="mx-3 mb-3 flex flex-col gap-2 rounded-lg bg-[var(--app-subtle)] px-3.5 py-3">
            <div class="flex items-center gap-2">
              <CcSwitchLucideIcon name="loaderCircle" class-name="h-[15px] w-[15px] animate-spin text-[var(--app-fg2)]" />
              <span class="min-w-0 flex-1 truncate text-[13px] font-medium">
                {{ fmt(authMsg.pendingAdd, { service: svc.name }) }}
              </span>
              <button type="button" class="h-7 rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-2.5 text-xs" @click="cancelPendingAuth">
                {{ commonMsg.cancel }}
              </button>
            </div>
            <div v-if="svc.login === 'device'" class="flex flex-wrap items-center gap-2 text-[13px]">
              <span class="text-[var(--app-fg2)]">{{ authMsg.enterCode }}</span>
              <span class="inline-flex h-7 items-center rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-2.5 font-mono text-[15px] tracking-[1px]">
                8F2K-4QXM
              </span>
              <button type="button" class="h-7 rounded-md px-2 text-xs text-[var(--app-fg2)] hover:bg-[var(--app-surface)]" @click="workbench.toast({ text: authMsg.codeCopied })">
                <CcSwitchLucideIcon name="copy" class-name="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
        </section>
      </div>
    </div>
  </div>

  <!-- 6. Settings General Pane -->
  <div v-else class="flex h-full min-h-0 flex-col" data-testid="ccs-demo-settings-pane">
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--ccs-border)] px-4 sm:pe-4 sm:ps-6">
      <div class="flex min-w-0 items-center gap-2.5">
        <span class="flex h-[22px] w-[22px] shrink-0 items-center justify-center text-[var(--ccs-fg)]">
          <CcSwitchLucideIcon name="slidersHorizontal" class-name="h-5 w-5" :stroke-width="1.5" />
        </span>
        <h3 class="truncate text-lg font-semibold text-[var(--ccs-fg)]">{{ settingsMsg.sections.general }}</h3>
      </div>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto">
      <div class="mx-auto w-full max-w-[760px] space-y-7 px-4 pb-10 pt-6 sm:px-6">
        <!-- Appearance -->
        <section class="space-y-3">
          <h4 class="flex min-h-7 items-center text-[15px] font-semibold text-[var(--ccs-fg)]">{{ settingsMsg.appearance }}</h4>
          <div class="divide-y divide-[var(--ccs-border)] overflow-hidden rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
            <div class="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <span class="min-w-0 flex-1 text-[13px] font-medium text-[var(--ccs-fg)]">{{ settingsMsg.language }}</span>
              <span class="inline-flex h-8 w-[160px] items-center justify-between rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-2.5 text-[13px] text-[var(--ccs-fg)]">
                <span>{{ lang === 'zh' ? '简体中文' : 'English' }}</span>
                <CcSwitchLucideIcon name="chevronDown" class-name="h-3.5 w-3.5 text-[var(--app-fg2)]" />
              </span>
            </div>
            <div class="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <span class="min-w-0 flex-1 text-[13px] font-medium text-[var(--ccs-fg)]">{{ settingsMsg.theme }}</span>
              <div role="radiogroup" class="inline-flex h-7 shrink-0 rounded-lg bg-[var(--app-subtle)] p-[3px]">
                <button
                  v-for="tOpt in themeOptions"
                  :key="tOpt.id"
                  type="button"
                  role="radio"
                  :aria-checked="workbench.themePref.value === tOpt.id"
                  class="flex items-center gap-1.5 rounded-[5px] px-2.5 text-xs font-medium transition-colors"
                  :class="workbench.themePref.value === tOpt.id ? 'bg-[var(--app-surface)] text-[var(--ccs-fg)] shadow-sm' : 'text-[var(--app-fg2)] hover:text-[var(--ccs-fg)]'"
                  @click="workbench.setThemePref(tOpt.id)"
                >
                  <CcSwitchLucideIcon :name="tOpt.icon" class-name="h-3.5 w-3.5" />
                  <span>{{ tOpt.label }}</span>
                </button>
              </div>
            </div>
          </div>
        </section>

        <!-- Sidebar & Header -->
        <section class="space-y-3">
          <h4 class="flex min-h-7 items-center text-[15px] font-semibold text-[var(--ccs-fg)]">{{ settingsMsg.sidebarAndHeader }}</h4>
          <div class="divide-y divide-[var(--ccs-border)] overflow-hidden rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
            <div class="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <span class="min-w-0 flex-1 text-[13px] font-medium text-[var(--ccs-fg)]">{{ settingsMsg.visibleApps }}</span>
              <button
                type="button"
                class="inline-flex h-7 items-center gap-1.5 rounded-md px-3 text-[13px] font-medium text-[var(--ccs-fg)] hover:bg-[var(--app-subtle)]"
                @click="workbench.openPage('apps')"
              >
                <span>{{ settingsMsg.goToApps }}</span>
                <CcSwitchLucideIcon name="chevronRight" class-name="h-3.5 w-3.5" />
              </button>
            </div>
            <div class="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <div class="flex min-w-0 flex-1 items-center gap-1">
                <span class="text-[13px] font-medium text-[var(--ccs-fg)]">{{ settingsMsg.showProfileSwitcher }}</span>
                <button
                  type="button"
                  class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)]"
                  @mouseenter="tooltip.show($event, settingsMsg.showProfileSwitcherHelp, settingsMsg.showProfileSwitcher)"
                  @mouseleave="tooltip.hide()"
                >
                  <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
                </button>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="settingsFlags.profile"
                class="relative inline-flex h-[18px] w-[30px] shrink-0 items-center rounded-full transition-colors"
                :class="settingsFlags.profile ? 'bg-[var(--app-action)]' : 'bg-[var(--ccs-switch-off)]'"
                @click="settingsFlags.profile = !settingsFlags.profile"
              >
                <span class="absolute left-0.5 h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform" :class="settingsFlags.profile ? 'translate-x-3' : 'translate-x-0'" />
              </button>
            </div>
          </div>
        </section>

        <!-- Updates -->
        <section class="space-y-3">
          <h4 class="flex min-h-7 items-center text-[15px] font-semibold text-[var(--ccs-fg)]">{{ settingsMsg.updates }}</h4>
          <div class="overflow-hidden rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
            <div class="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <div class="flex min-w-0 flex-1 items-center gap-1">
                <span class="text-[13px] font-medium text-[var(--ccs-fg)]">{{ settingsMsg.checkToolUpdates }}</span>
                <button
                  type="button"
                  class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)]"
                  @mouseenter="tooltip.show($event, settingsMsg.checkToolUpdatesHelp, settingsMsg.checkToolUpdates)"
                  @mouseleave="tooltip.hide()"
                >
                  <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
                </button>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="settingsFlags.toolUpdates"
                class="relative inline-flex h-[18px] w-[30px] shrink-0 items-center rounded-full transition-colors"
                :class="settingsFlags.toolUpdates ? 'bg-[var(--app-action)]' : 'bg-[var(--ccs-switch-off)]'"
                @click="settingsFlags.toolUpdates = !settingsFlags.toolUpdates"
              >
                <span class="absolute left-0.5 h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform" :class="settingsFlags.toolUpdates ? 'translate-x-3' : 'translate-x-0'" />
              </button>
            </div>
          </div>
        </section>

        <!-- Window & Terminal -->
        <section class="space-y-3">
          <h4 class="flex min-h-7 items-center text-[15px] font-semibold text-[var(--ccs-fg)]">{{ settingsMsg.windowAndTerminal }}</h4>
          <div class="divide-y divide-[var(--ccs-border)] overflow-hidden rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
            <div class="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <span class="min-w-0 flex-1 text-[13px] font-medium text-[var(--ccs-fg)]">{{ settingsMsg.launchOnStartup }}</span>
              <button
                type="button"
                role="switch"
                :aria-checked="settingsFlags.launch"
                class="relative inline-flex h-[18px] w-[30px] shrink-0 items-center rounded-full transition-colors"
                :class="settingsFlags.launch ? 'bg-[var(--app-action)]' : 'bg-[var(--ccs-switch-off)]'"
                @click="settingsFlags.launch = !settingsFlags.launch"
              >
                <span class="absolute left-0.5 h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform" :class="settingsFlags.launch ? 'translate-x-3' : 'translate-x-0'" />
              </button>
            </div>
            <div v-if="settingsFlags.launch" class="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <div class="flex min-w-0 flex-1 items-center gap-1">
                <span class="text-[13px] font-medium text-[var(--ccs-fg)]">{{ settingsMsg.silentStartup }}</span>
                <button
                  type="button"
                  class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)]"
                  @mouseenter="tooltip.show($event, settingsMsg.silentStartupHelp, settingsMsg.silentStartup)"
                  @mouseleave="tooltip.hide()"
                >
                  <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
                </button>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="settingsFlags.silent"
                class="relative inline-flex h-[18px] w-[30px] shrink-0 items-center rounded-full transition-colors"
                :class="settingsFlags.silent ? 'bg-[var(--app-action)]' : 'bg-[var(--ccs-switch-off)]'"
                @click="settingsFlags.silent = !settingsFlags.silent"
              >
                <span class="absolute left-0.5 h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform" :class="settingsFlags.silent ? 'translate-x-3' : 'translate-x-0'" />
              </button>
            </div>
            <div class="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <span class="min-w-0 flex-1 text-[13px] font-medium text-[var(--ccs-fg)]">{{ settingsMsg.minimizeToTray }}</span>
              <button
                type="button"
                role="switch"
                :aria-checked="settingsFlags.tray"
                class="relative inline-flex h-[18px] w-[30px] shrink-0 items-center rounded-full transition-colors"
                :class="settingsFlags.tray ? 'bg-[var(--app-action)]' : 'bg-[var(--ccs-switch-off)]'"
                @click="settingsFlags.tray = !settingsFlags.tray"
              >
                <span class="absolute left-0.5 h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform" :class="settingsFlags.tray ? 'translate-x-3' : 'translate-x-0'" />
              </button>
            </div>
            <div class="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
              <span class="min-w-0 flex-1 text-[13px] font-medium text-[var(--ccs-fg)]">{{ settingsMsg.preferredTerminal }}</span>
              <span class="inline-flex h-8 w-[160px] items-center justify-between rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-2.5 text-[13px] text-[var(--ccs-fg)]">
                <span>Terminal.app</span>
                <CcSwitchLucideIcon name="chevronDown" class-name="h-3.5 w-3.5 text-[var(--app-fg2)]" />
              </span>
            </div>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import { fmt, useCcSwitchI18n, useCcSwitchTooltip, useCcSwitchWorkbench } from './ccswitchContext'
import {
  AUTH_DEFAULT_SUPPORTED,
  AUTH_NEW_ACCOUNT_TEMPLATES,
  AUTH_SERVICES,
  CC_SWITCH_APP_MAP,
  DISCOVER_SKILLS,
  INITIAL_AUTH_ACCOUNTS,
  INITIAL_INSTALLED_SKILLS,
  INITIAL_MCP_SERVERS,
  INITIAL_SESSIONS,
  MATRIX_APPS,
  PROMPT_APP_ORDER,
  PROMPT_TARGET_FILES,
  SESSION_APP_ORDER,
  createInitialPromptsState,
  quotaTone,
  type CcsAuthServiceSpec,
} from './ccswitchDemoData'
import CcSwitchAppIcon from './CcSwitchAppIcon.vue'
import CcSwitchLucideIcon from './CcSwitchLucideIcon.vue'
import CcSwitchMenuButton from './CcSwitchMenuButton.vue'
import CcSwitchPopover from './CcSwitchPopover.vue'
import CcSwitchSessionReader from './CcSwitchSessionReader.vue'
import type { CcsAuthAccount, CcsInstalledSkill, CcsPageKind, CcsPromptItem, CcsSessionItem, CcsThemePref } from './ccswitchTypes'

defineProps<{
  pageKind: Exclude<CcsPageKind, 'app' | 'usage' | 'apps'>
}>()

const { lang, msg } = useCcSwitchI18n()
const tooltip = useCcSwitchTooltip()
const workbench = useCcSwitchWorkbench()

const commonMsg = computed(() => msg.value.window.pages.common)
const matrixMsg = computed(() => msg.value.window.pages.matrix)
const mcpMsg = computed(() => msg.value.window.pages.mcp)
const skillsMsg = computed(() => msg.value.window.pages.skills)
const promptsMsg = computed(() => msg.value.window.pages.prompts)
const sessionsMsg = computed(() => msg.value.window.pages.sessions)
const authMsg = computed(() => msg.value.window.pages.auth)
const settingsMsg = computed(() => msg.value.window.pages.settings)

function appSpec(id: string) {
  return CC_SWITCH_APP_MAP[id] ?? CC_SWITCH_APP_MAP.claude
}

// ---------- MCP State ----------
const mcpMap = ref<Record<string, string[]>>(Object.fromEntries(INITIAL_MCP_SERVERS.map((s) => [s.id, [...s.apps]])))
const deletedMcpIds = ref<string[]>([])
const mcpQuery = ref('')
const hoveredColApp = ref<string | null>(null)
const matrixApps = computed(() => MATRIX_APPS.filter((id) => workbench.visibleApps.value.includes(id)))
const activeMcpList = computed(() => INITIAL_MCP_SERVERS.filter((s) => !deletedMcpIds.value.includes(s.id)))
const filteredMcp = computed(() => {
  const q = mcpQuery.value.trim().toLowerCase()
  if (!q) return activeMcpList.value
  return activeMcpList.value.filter((s) => `${s.id} ${s.transport} ${s.summary}`.toLowerCase().includes(q))
})

function toggleMcpCell(id: string, appId: string) {
  const cur = mcpMap.value[id] ?? []
  const nextOn = !cur.includes(appId)
  mcpMap.value = {
    ...mcpMap.value,
    [id]: nextOn ? [...cur, appId] : cur.filter((a) => a !== appId),
  }
  workbench.toast({ text: fmt(mcpMsg.value.toastWritten, { app: appSpec(appId).label }) })
}

function toggleAllMcpForApp(appId: string) {
  const prev = Object.fromEntries(Object.entries(mcpMap.value).map(([k, v]) => [k, [...v]]))
  const allOn = filteredMcp.value.every((m) => (mcpMap.value[m.id] ?? []).includes(appId))
  const nextOn = !allOn
  const next = { ...mcpMap.value }
  let changed = 0
  for (const item of filteredMcp.value) {
    const cur = next[item.id] ?? []
    if (cur.includes(appId) !== nextOn) {
      changed++
      next[item.id] = nextOn ? [...cur, appId] : cur.filter((a) => a !== appId)
    }
  }
  mcpMap.value = next
  workbench.toast({
    text: fmt(nextOn ? matrixMsg.value.toastEnabled : matrixMsg.value.toastDisabled, {
      app: appSpec(appId).label,
      count: changed,
      noun: mcpMsg.value.noun,
    }),
    undo: () => {
      mcpMap.value = prev
      workbench.toast({ text: commonMsg.value.undone })
    },
  })
}

function setMcpRowAll(id: string, on: boolean) {
  const prev = Object.fromEntries(Object.entries(mcpMap.value).map(([k, v]) => [k, [...v]]))
  mcpMap.value = { ...mcpMap.value, [id]: on ? [...MATRIX_APPS] : [] }
  workbench.toast({
    text: on ? fmt(mcpMsg.value.toastRowAllOn, { id, count: MATRIX_APPS.length }) : fmt(mcpMsg.value.toastRowAllOff, { id }),
    undo: () => {
      mcpMap.value = prev
      workbench.toast({ text: commonMsg.value.undone })
    },
  })
}

function deleteMcp(id: string) {
  deletedMcpIds.value = [...deletedMcpIds.value, id]
  workbench.toast({
    text: fmt(mcpMsg.value.toastDeleted, { id }),
    undo: () => {
      deletedMcpIds.value = deletedMcpIds.value.filter((x) => x !== id)
    },
  })
}

// ---------- Skills State ----------
const skillsTab = ref<'installed' | 'discover'>('installed')
const installedSkills = ref<CcsInstalledSkill[]>(INITIAL_INSTALLED_SKILLS.map((s) => ({ ...s, apps: [...s.apps] })))
const skillFilter = ref<'all' | 'updates' | 'none'>('all')
const skillsQuery = ref('')
const showUnmanagedBanner = ref(true)
const discoverSource = ref<'repos' | 'skillssh'>('repos')
const discoverQuery = ref('')
const installingSkill = ref<string | null>(null)
let skillTimer: ReturnType<typeof setTimeout> | undefined

const skillUpdatableCount = computed(() => installedSkills.value.filter((s) => s.update).length)
const filteredSkills = computed(() => {
  const q = skillsQuery.value.trim().toLowerCase()
  return installedSkills.value.filter((s) => {
    if (skillFilter.value === 'updates' && !s.update) return false
    if (skillFilter.value === 'none' && s.apps.length > 0) return false
    if (q && !`${s.name} ${s.description[lang.value]} ${s.repo ?? ''}`.toLowerCase().includes(q)) return false
    return true
  })
})
const installedSkillMap = computed(() => new Map(installedSkills.value.map((s) => [s.name, s.apps.length])))
const filteredDiscoverSkills = computed(() => {
  const q = discoverQuery.value.trim().toLowerCase()
  return DISCOVER_SKILLS.filter((s) => !q || `${s.name} ${s.repo} ${s.description[lang.value]}`.toLowerCase().includes(q))
})

function toggleSkillCell(name: string, appId: string) {
  installedSkills.value = installedSkills.value.map((s) => {
    if (s.name !== name) return s
    const has = s.apps.includes(appId)
    return { ...s, apps: has ? s.apps.filter((a) => a !== appId) : [...s.apps, appId] }
  })
}

function markSkillUpdated(name: string) {
  installedSkills.value = installedSkills.value.map((s) => (s.name === name ? { ...s, update: false } : s))
}

function uninstallSkill(name: string) {
  installedSkills.value = installedSkills.value.filter((s) => s.name !== name)
}

function installDiscoverSkill(name: string) {
  if (installingSkill.value) return
  installingSkill.value = name
  if (skillTimer) clearTimeout(skillTimer)
  skillTimer = setTimeout(() => {
    const found = DISCOVER_SKILLS.find((d) => d.name === name)
    if (found) {
      installedSkills.value = [...installedSkills.value, { name: found.name, description: found.description, repo: found.repo, apps: ['claude', 'codex'] }]
      workbench.toast({ text: fmt(skillsMsg.value.toastInstalled, { name, apps: 'Claude Code, Codex' }) })
    }
    installingSkill.value = null
  }, 700)
}

// ---------- Prompts State ----------
const promptsState = ref(createInitialPromptsState())
const promptAppChoices = computed(() =>
  PROMPT_APP_ORDER.filter((id) => workbench.visibleApps.value.includes(id) || (id === 'claude' && workbench.visibleApps.value.includes('claude-desktop'))),
)
const promptApp = ref('claude')
const promptQuery = ref('')
const promptAppOpen = ref(false)
const promptAppAnchorRef = ref<HTMLElement | null>(null)
const currentPromptBucket = computed(() => promptsState.value[promptApp.value] ?? { enabled: null, items: [] })
const promptTarget = computed(() => PROMPT_TARGET_FILES[promptApp.value] ?? PROMPT_TARGET_FILES.claude)
const enabledPromptItem = computed(() => currentPromptBucket.value.items.find((i) => i.id === currentPromptBucket.value.enabled) ?? null)
const filteredPrompts = computed(() => {
  const q = promptQuery.value.trim().toLowerCase()
  return q
    ? currentPromptBucket.value.items.filter((i) => `${i.name[lang.value]} ${i.description[lang.value]}`.toLowerCase().includes(q))
    : currentPromptBucket.value.items
})

function togglePrompt(item: CcsPromptItem) {
  const snapshot = JSON.parse(JSON.stringify(promptsState.value))
  const cur = currentPromptBucket.value.enabled === item.id
  promptsState.value = {
    ...promptsState.value,
    [promptApp.value]: { ...currentPromptBucket.value, enabled: cur ? null : item.id },
  }
  workbench.toast({
    text: fmt(cur ? promptsMsg.value.toastDisabled : promptsMsg.value.toastEnabled, {
      name: item.name[lang.value],
      path: promptTarget.value.path,
    }),
    undo: () => {
      promptsState.value = snapshot
      workbench.toast({ text: commonMsg.value.undone })
    },
  })
}

function deletePrompt(item: CcsPromptItem) {
  const snapshot = JSON.parse(JSON.stringify(promptsState.value))
  promptsState.value = {
    ...promptsState.value,
    [promptApp.value]: {
      ...currentPromptBucket.value,
      items: currentPromptBucket.value.items.filter((i) => i.id !== item.id),
    },
  }
  workbench.toast({
    text: fmt(promptsMsg.value.toastDeleted, { name: item.name[lang.value] }),
    detail: fmt(promptsMsg.value.toastDeletedSub, { path: promptTarget.value.path }),
    undo: () => {
      promptsState.value = snapshot
      workbench.toast({ text: commonMsg.value.undone })
    },
  })
}

// ---------- Sessions State ----------
const TIME_BUCKETS = ['today', 'yesterday', 'thisWeek', 'earlier'] as const
const sessionAppFilter = ref('claude')
const sessionGroupMode = ref<'time' | 'project'>('project')
const sessionQuery = ref('')
const openSessionId = ref<string | null>(null)
const deletedSessions = ref<string[]>([])
const expandedProjects = ref<string[]>(['~/Code/cc-switch'])
const sessAppOpen = ref(false)
const sessAppAnchorRef = ref<HTMLElement | null>(null)

const visibleSessionApps = computed(() =>
  SESSION_APP_ORDER.filter((id) => workbench.visibleApps.value.includes(id) || (id === 'claude' && workbench.visibleApps.value.includes('claude-desktop'))),
)
const activeSessions = computed(() =>
  INITIAL_SESSIONS.filter((s) => !deletedSessions.value.includes(s.id) && visibleSessionApps.value.includes(s.app)),
)
const countSessionsForApp = (appId: string) => activeSessions.value.filter((s) => s.app === appId).length
const sortedSessionApps = computed(() => [...visibleSessionApps.value].sort((a, b) => countSessionsForApp(b) - countSessionsForApp(a)))
const filteredSessions = computed(() => {
  const q = sessionQuery.value.trim().toLowerCase()
  return activeSessions.value.filter((s) => {
    if (sessionAppFilter.value !== 'all' && s.app !== sessionAppFilter.value) return false
    if (!q) return true
    return `${s.title[lang.value]} ${s.last[lang.value]} ${s.project ?? ''} ${s.id}`.toLowerCase().includes(q)
  })
})
const openSessionIndex = computed(() => (openSessionId.value ? filteredSessions.value.findIndex((s) => s.id === openSessionId.value) : -1))
const openSession = computed<CcsSessionItem | null>(() => (openSessionIndex.value >= 0 ? filteredSessions.value[openSessionIndex.value] : null))

const sessionProjects = computed(() =>
  Array.from(new Set(filteredSessions.value.map((s) => s.project ?? '__unknown__'))).sort((a, b) =>
    a === '__unknown__' ? 1 : b === '__unknown__' ? -1 : 0,
  ),
)
const sessionsByProject = (proj: string) => filteredSessions.value.filter((s) => (s.project ?? '__unknown__') === proj)
const sessionsByBucket = (bucket: (typeof TIME_BUCKETS)[number]) => filteredSessions.value.filter((s) => s.bucket === bucket)
const isProjectExpanded = (proj: string) => Boolean(sessionQuery.value.trim()) || expandedProjects.value.includes(proj)
function toggleProjectGroup(proj: string) {
  expandedProjects.value = expandedProjects.value.includes(proj)
    ? expandedProjects.value.filter((p) => p !== proj)
    : [...expandedProjects.value, proj]
}
function sessionAgo(s: CcsSessionItem) {
  if (s.ago.unit === 'minutes') return fmt(commonMsg.value.minutesAgo, { count: s.ago.count })
  if (s.ago.unit === 'hours') return fmt(commonMsg.value.hoursAgo, { count: s.ago.count })
  return fmt(commonMsg.value.daysAgo, { count: s.ago.count })
}

// ---------- Auth State ----------
const authOpenBrowser = ref(true)
const authRefreshing = ref(false)
const pendingAuthSvc = ref<string | null>(null)
const authAccounts = ref<Record<string, CcsAuthAccount[]>>(JSON.parse(JSON.stringify(INITIAL_AUTH_ACCOUNTS)))
let authTimer: ReturnType<typeof setTimeout> | undefined

function authServiceHelp(id: string): string {
  const map: Record<string, string> = {
    claude: authMsg.value.claudeHelp,
    copilot: authMsg.value.copilotHelp,
    chatgpt: authMsg.value.chatgptHelp,
    xai: authMsg.value.xaiHelp,
    kimi: authMsg.value.kimiHelp,
    dsh: authMsg.value.dshHelp,
    agy: authMsg.value.agyHelp,
    workbuddy: authMsg.value.workbuddyHelp,
  }
  return map[id] ?? ''
}

function authDefaultLabel(svcId: string) {
  if (svcId === 'kimi') return authMsg.value.kimiCurrent
  if (svcId === 'agy') return authMsg.value.agyCurrent
  if (svcId === 'workbuddy') return authMsg.value.workbuddyCurrent
  return authMsg.value.default
}

function authTierLabel(tier: string): string {
  const map: Record<string, string> = {
    fiveHour: authMsg.value.fiveHour,
    weekly: authMsg.value.weekly,
    premium: authMsg.value.premium,
    geminiFiveHour: authMsg.value.geminiFiveHour,
    geminiWeekly: authMsg.value.geminiWeekly,
    claudeGptWeekly: authMsg.value.claudeGptWeekly,
  }
  return map[tier] ?? tier
}

function quotaBarClass(left: number) {
  const tone = quotaTone(left)
  return tone === 'danger' ? 'bg-[var(--app-danger)]' : tone === 'warning' ? 'bg-[var(--app-warning)]' : 'bg-[var(--app-chart)]'
}

function quotaTextClass(left: number) {
  const tone = quotaTone(left)
  return tone === 'danger'
    ? 'text-red-500 dark:text-red-400'
    : tone === 'warning'
      ? 'text-orange-500 dark:text-orange-400'
      : 'text-green-600 dark:text-green-400'
}

function formatAuthAccountSubtitle(svcId: string, acc: CcsAuthAccount): string {
  const d = acc.signedIn[lang.value]
  if (svcId === 'claude') return [acc.plan, fmt(authMsg.value.addedOn, { date: d })].filter(Boolean).join(' · ')
  if (svcId === 'kimi') return [acc.region === 'global' ? authMsg.value.kimiGlobal : authMsg.value.kimiMainland, acc.primary ? authMsg.value.kimiPrimary : ''].filter(Boolean).join(' · ')
  if (svcId === 'agy') return fmt(authMsg.value.addedOn, { date: d })
  if (svcId === 'dsh') return [acc.contact, authMsg.value.dshShared].filter(Boolean).join(' · ')
  if (svcId === 'workbuddy') return [acc.planText?.[lang.value], fmt(authMsg.value.workbuddyAddedOn, { date: d })].filter(Boolean).join(' · ')
  return [acc.host, fmt(authMsg.value.signedInOn, { date: d }), fmt(authMsg.value.usedBy, { count: acc.usedBy ?? 0 })].filter(Boolean).join(' · ')
}

function startAddAuthAccount(svc: CcsAuthServiceSpec) {
  if (svc.login === 'import') {
    const tpl = AUTH_NEW_ACCOUNT_TEMPLATES[svc.id]
    const list = authAccounts.value[svc.id] ?? []
    authAccounts.value = {
      ...authAccounts.value,
      [svc.id]: [
        ...list.filter((a) => a.login !== tpl.login),
        { ...tpl, signedIn: { zh: '今天', en: 'today' }, isDefault: list.length === 0, checkedMinutesAgo: 0 },
      ],
    }
    workbench.toast({ text: fmt(authMsg.value.toastAdded, { service: svc.name }) })
    return
  }
  pendingAuthSvc.value = svc.id
  if (authTimer) clearTimeout(authTimer)
  authTimer = setTimeout(() => {
    const tpl = AUTH_NEW_ACCOUNT_TEMPLATES[svc.id]
    const list = authAccounts.value[svc.id] ?? []
    authAccounts.value = {
      ...authAccounts.value,
      [svc.id]: [
        ...list.filter((a) => a.login !== tpl.login),
        { ...tpl, signedIn: { zh: '今天', en: 'today' }, isDefault: AUTH_DEFAULT_SUPPORTED.includes(svc.id) && list.length === 0, checkedMinutesAgo: 0 },
      ],
    }
    pendingAuthSvc.value = null
    workbench.toast({ text: fmt(authMsg.value.toastAdded, { service: svc.name }) })
  }, 1800)
}

function cancelPendingAuth() {
  if (authTimer) clearTimeout(authTimer)
  pendingAuthSvc.value = null
}

function setDefaultAuth(svcId: string, login: string) {
  authAccounts.value = {
    ...authAccounts.value,
    [svcId]: (authAccounts.value[svcId] ?? []).map((a) => ({ ...a, isDefault: a.login === login })),
  }
}

function removeAuthAccount(svcId: string, login: string) {
  authAccounts.value = {
    ...authAccounts.value,
    [svcId]: (authAccounts.value[svcId] ?? []).filter((a) => a.login !== login),
  }
}

function refreshAllAuth() {
  authRefreshing.value = true
  setTimeout(() => {
    authRefreshing.value = false
  }, 800)
}

// ---------- Settings State ----------
const settingsFlags = reactive({
  profile: false,
  toolUpdates: false,
  launch: false,
  silent: false,
  tray: true,
})

const themeOptions = computed<Array<{ id: CcsThemePref; label: string; icon: string }>>(() => [
  { id: 'system', label: settingsMsg.value.themeSystem, icon: 'monitor' },
  { id: 'light', label: settingsMsg.value.themeLight, icon: 'sun' },
  { id: 'dark', label: settingsMsg.value.themeDark, icon: 'moon' },
])

onBeforeUnmount(() => {
  if (skillTimer) clearTimeout(skillTimer)
  if (authTimer) clearTimeout(authTimer)
})
</script>
