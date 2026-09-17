# SIGNAL Console Implementation Plan

> For agentic workers: use subagent-driven-development with disjoint worktrees and a final integration review. This branch is local-only until the owner accepts the preview.

**Goal:** A distinctive, restrained, responsive visual refresh of the existing dashboard, API keys and usage screens, previewable on loopback port 4317 without production access.

**Architecture:** Keep the existing Vue routes, API contracts, permissions and accounting. Opt three routes into a scoped visual system; reuse business components. A serve-only Vite plugin supplies explicit demonstration data and intercepts all API traffic in a separate preview mode.

**Tech Stack:** Vue 3, TypeScript, Tailwind 3, existing Icon/PlatformIcon, Chart.js, Vitest, Playwright. No new runtime dependency.

## Baseline And Boundaries

- Repository: `F:/GO/sub2-official-work`; baseline `924d9ebfe97ade95b3a21d6ae764b2f1590f3a32`.
- Branch: `feat/console-signal-design`. The initial tracked worktree was clean.
- Preserve `.artifacts` and release evidence. Do not clean unrelated worktrees.
- Only `/dashboard`, `/keys`, `/usage` receive the new shell. Authentication, home, admin, payments and recharge stay unchanged.
- No backend, database, price, balance formula, key routing, server configuration, release, SSH, or traffic change.
- API key provider selection, ordered smart-routing groups, quota, expiry, IP rules and copy/edit/delete remain operational.
- Cache tokens, actual/standard costs, error logs, pagination, filters and CSV export keep their current meaning.

## Design Decision

Three directions considered: SIGNAL instrument console, holographic laboratory, editorial minimalism. SIGNAL is selected for repeated operational use: precise data hierarchy with a recognizable material treatment, without an oversized hero or reduced information density.

Owner clarification: reference Linear, Vercel, Raycast, Resend and Supabase. Adopt their neutral dark material, precise hierarchy, fine borders and restrained local glow; do not migrate frameworks or copy marketing pages. Implementation and review workers were explicitly restarted with `gpt-5.6-sol` at the owner's request, preserving their worktrees.

- Light: background `#f5f6f7`, surface `#ffffff`, line `#dfe3e5`, text `#192322`, muted `#62706e`, accent `#087f6a`, amber `#a86610`.
- Dark: background `#111315`, surface `#191c1f`, line `#303538`, text `#edf2ef`, muted `#a0aca6`, accent `#6ce4bd`, amber `#eeb766`.
- Primary action: jade. Cost/reminder: amber. Error: existing red. Chart series retain distinguishable colors.
- Typography: existing local system stack; tabular/monospaced numerical data, zero letter spacing. Titles 24-32px, never viewport-scaled.
- Sections: unframed or ruled bands. Individual repeated items and dialogs may use 6-8px corners. No nested decorative cards.
- Assets: existing brand/model logos and real data visualizations. No stock illustration, glow balls, particles or decorative 3D.
- Motion: 120-220ms opacity/transform for focus/selection/entry, no continuous decorative animation. Reduced-motion disables nonessential motion.

## Chunk 1: Independent Implementation

### Task 1: Shell And Tokens

Files: `frontend/src/components/layout/AppLayout.vue`, `AppHeader.vue`, `AppSidebar.vue`, `frontend/src/styles/console-signal.css`, optional route predicate and tests.

- [x] Read existing layout, menus, theme and route boundaries.
- [ ] Add exact route opt-in and prove excluded routes stay unchanged.
- [ ] Replace mesh backdrop only on opted-in pages with neutral surfaces.
- [ ] Apply consistent navigation, breadcrumb, buttons, inputs and dialogs through explicit scoped selectors.
- [ ] Preserve sidebar collapse/mobile menu, subscriptions, balance, language, profile and theme controls.
- [ ] Check teleported overlays and remove style scope on route changes.

Owner worktree: `F:/GO/sub2-signal-shell`; only this task's files.

### Task 2: Dashboard

Files: `frontend/src/views/user/DashboardView.vue`, `frontend/src/components/user/dashboard/*`.

- [x] Identify existing data, amounts, chart components and quick actions.
- [ ] Add compact brand-led heading; keep account summary visible in the first viewport.
- [ ] Replace floating statistic-card sections with organized metric bands.
- [ ] Restyle platform breakdown using existing provider logos.
- [ ] Retain trend, model comparison, recent requests, loading/empty states and all existing actions.
- [ ] Respect reduced motion for new Chart.js presentation and maintain stable chart dimensions.

Owner worktree: `F:/GO/sub2-signal-dashboard`; no shared chart or billing edits.

### Task 3: Keys And Usage

Files: `frontend/src/views/user/KeysView.vue`, `UsageView.vue`, `frontend/src/styles/console-workspace.css`, focused view tests.

- [ ] Add compact semantic page titles and consistent action/filter hierarchy.
- [ ] Keep keys table, provider dialog and smart routing behavior unchanged.
- [ ] Use recognizable tool icons with accessible labels and tooltips.
- [ ] Make usage statistics ruled bands and group charts as unframed sections.
- [ ] Preserve all seven usage filters, error tab, column selection and export.
- [ ] Ensure 320px layouts wrap controls; only table containers may scroll horizontally.

Owner: main agent in the feature branch.

### Task 4: Isolated Local Preview

Files: `frontend/dev/*`, `frontend/vite.config.ts`, `frontend/package.json` (one preview script only).

- [ ] Add `console-preview` serve-only mode; bind 127.0.0.1 and require a loopback host.
- [ ] Seed a clearly fictional user and safe local session; do not add a production authentication bypass.
- [ ] Intercept all `/api`, `/v1`, `/setup` requests; no passthrough to any real backend.
- [ ] Supply dashboard/usage charts and key groups with realistic-shaped demonstration fixtures.
- [ ] Support local key CRUD, search and pagination; unsupported writes fail explicitly.
- [ ] Label the environment as local demonstration data; reset/restart never touches real users.
- [ ] Test production-mode exclusion and unknown endpoint rejection.

Owner worktree: `F:/GO/sub2-signal-preview`.

## Chunk 2: Integration And Acceptance

### Task 5: Tests And Visual Checks

- [x] Capture original keys desktop screenshot and run existing provider-dialog browser check at 1280/375/320.
- [ ] Review each worker diff, then integrate local commits into the feature branch (no push).
- [ ] Run `pnpm run typecheck` and eslint on every changed TS/Vue file.
- [ ] Run KeysView, UsageView, locale completeness, relevant shell/dashboard and preview tests.
- [ ] Build to `.artifacts/signal-dist` rather than overwriting backend embedded assets.
- [ ] Compare production bundle size with baseline; target <=20KiB added gzip JS+CSS and zero new runtime dependency. Record actuals; investigate material excess.
- [ ] Playwright: dashboard, keys, usage in dark/light at 1440, 375 and 320px, plus wide 1920px framing.
- [ ] Verify chart canvas contains drawn pixels; inspect screenshots for overflow/overlap/low contrast.
- [ ] Exercise sidebar, theme, date/filter, pagination, copy, create/edit/delete key and smart routing using local fixtures.
- [ ] Check empty and failed API states, keyboard focus, reduced motion and excluded routes.
- [ ] Sample settled-page main-thread work and interactions. No new decorative long tasks or perpetual rendering; dev timing is diagnostic, not a production Lighthouse claim.
- [ ] Confirm browser requests remain local and a production build contains no demo auth/fixtures.

### Task 6: Handoff

- [ ] Leave verified loopback server on `http://127.0.0.1:4317/dashboard`.
- [ ] Record exact commands, test counts, screenshots, limitations and final local commit(s) below.
- [ ] Wait for owner experience/approval. Do not publish a release or deploy to production.

## Rollback

This is frontend-only, local-only work on a feature branch. Main and production are unchanged. Keep the baseline commit and separate worker commits so individual visual tasks can be reverted. Stop only the verified local preview process when retiring the preview; never stop a production service.

## Verification Record

- Baseline: key provider browser check passed at 1280/375/320; no page errors. Original screenshot: `.artifacts/key-provider-before.png`.
- Pending: implementation, integration, final browser and build checks.
- Main-agent preliminary checks: existing 27 page/locale tests passed; after adding named-tool tests, KeysView 17 and UsageView 9 passed. Chart tests (5 token, 3 group, 4 model) passed, including theme switching and opt-in motion.
- Added a narrowly scoped chart presentation change: optional animation duration leaves other callers unchanged. The token chart now observes theme class changes, with VueUse-managed listener cleanup.
- Fixed the newly left-aligned keys toolbar's mobile column menu anchoring before browser acceptance.
