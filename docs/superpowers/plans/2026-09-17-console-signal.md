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
- [x] Add exact route opt-in and prove excluded routes stay unchanged.
- [x] Replace mesh backdrop only on opted-in pages with neutral surfaces.
- [x] Apply consistent navigation, breadcrumb, buttons, inputs and dialogs through explicit scoped selectors.
- [x] Preserve sidebar collapse/mobile menu, subscriptions, balance, language, profile and theme controls.
- [x] Check teleported overlays and remove style scope on route changes.

Owner worktree: `F:/GO/sub2-signal-shell`; only this task's files.

### Task 2: Dashboard

Files: `frontend/src/views/user/DashboardView.vue`, `frontend/src/components/user/dashboard/*`.

- [x] Identify existing data, amounts, chart components and quick actions.
- [x] Add compact brand-led heading; keep account summary visible in the first viewport.
- [x] Replace floating statistic-card sections with organized metric bands.
- [x] Restyle platform breakdown using existing provider logos.
- [x] Retain trend, model comparison, recent requests, loading/empty states and all existing actions.
- [x] Respect reduced motion for new Chart.js presentation and maintain stable chart dimensions.

Owner worktree: `F:/GO/sub2-signal-dashboard`; no shared chart or billing edits.

### Task 3: Keys And Usage

Files: `frontend/src/views/user/KeysView.vue`, `UsageView.vue`, `frontend/src/styles/console-workspace.css`, focused view tests.

- [x] Add compact semantic page titles and consistent action/filter hierarchy.
- [x] Keep keys table, provider dialog and smart routing behavior unchanged.
- [x] Use recognizable tool icons with accessible labels and tooltips.
- [x] Make usage statistics ruled bands and group charts as unframed sections.
- [x] Preserve all seven usage filters, error tab, column selection and export.
- [x] Ensure 320px layouts wrap controls; only table containers may scroll horizontally.

Owner: main agent in the feature branch.

### Task 4: Isolated Local Preview

Files: `frontend/dev/*`, `frontend/vite.config.ts`, `frontend/package.json` (one preview script only).

- [x] Add `console-preview` serve-only mode; bind 127.0.0.1 and require a loopback host.
- [x] Seed a clearly fictional user and safe local session; do not add a production authentication bypass.
- [x] Intercept all `/api`, `/v1`, `/setup` requests; no passthrough to any real backend.
- [x] Supply dashboard/usage charts and key groups with realistic-shaped demonstration fixtures.
- [x] Support local key CRUD, search and pagination; unsupported writes fail explicitly.
- [x] Label the environment as local demonstration data; reset/restart never touches real users.
- [x] Test production-mode exclusion and unknown endpoint rejection.

Owner worktree: `F:/GO/sub2-signal-preview`.

## Chunk 2: Integration And Acceptance

### Task 5: Tests And Visual Checks

- [x] Capture original keys desktop screenshot and run existing provider-dialog browser check at 1280/375/320.
- [x] Review each worker diff, then integrate local commits into the feature branch (no push).
- [x] Run `pnpm run typecheck` and eslint on every changed TS/Vue file.
- [x] Run KeysView, UsageView, locale completeness, relevant shell/dashboard and preview tests.
- [x] Build to `.artifacts/signal-dist` rather than overwriting backend embedded assets.
- [x] Compare production bundle size with baseline; target <=20KiB added gzip JS+CSS and zero new runtime dependency. Record actuals; investigate material excess.
- [x] Playwright: dashboard, keys, usage in dark/light at 1440, 375 and 320px, plus wide 1920px framing.
- [x] Verify chart canvas contains drawn pixels; inspect screenshots for overflow/overlap/low contrast.
- [x] Exercise sidebar, theme, date/filter, pagination, copy, create/edit/delete key and smart routing using local fixtures.
- [x] Check empty and failed API states, keyboard focus, reduced motion and excluded routes.
- [x] Sample settled-page main-thread work and interactions. No new decorative long tasks or perpetual rendering; dev timing is diagnostic, not a production Lighthouse claim.
- [x] Confirm browser requests remain local and a production build contains no demo auth/fixtures.

### Task 6: Handoff

- [x] Leave verified loopback server on `http://127.0.0.1:4317/dashboard`.
- [x] Record exact commands, test counts, screenshots, limitations and final local commit(s) below.
- [ ] Wait for owner experience/approval. Do not publish a release or deploy to production.

## Rollback

This is frontend-only, local-only work on a feature branch. Main and production are unchanged. Keep the baseline commit and separate worker commits so individual visual tasks can be reverted. Stop only the verified local preview process when retiring the preview; never stop a production service.

## Verification Record

- Baseline: key provider browser check passed at 1280/375/320; no page errors. Build baseline retained in `.artifacts/signal-baseline-dist`. The legacy screenshot helper reused its filenames in later runs; final screenshots have a separate directory below.
- Implementation and worker integration complete. Owner acceptance and production deployment are deliberately pending.
- Main-agent preliminary checks: existing 27 page/locale tests passed; after adding named-tool tests, KeysView 17 and UsageView 9 passed. Chart tests (5 token, 3 group, 4 model) passed, including theme switching and opt-in motion.
- Added a narrowly scoped chart presentation change: optional animation duration leaves other callers unchanged. The token chart now observes theme class changes, with VueUse-managed listener cleanup.
- Fixed the newly left-aligned keys toolbar's mobile column menu anchoring before browser acceptance.

### Final Evidence (2026-09-18)

- `pnpm run typecheck`: passed. ESLint over all 30 changed TS/Vue/MJS files: passed. `git diff --check`: passed.
- Focused Vitest suite: 85 tests across 12 files passed (dashboard, shell, chart contracts, keys, usage, locale keys). Isolated preview suite: 14 tests across 2 files passed. Total: 99.
- Build: `pnpm exec vite build --outDir ../.artifacts/signal-dist --logLevel error`, passed; backend embedded assets were not overwritten.
- Sum of all JS/CSS gzip assets: 1,671,992 bytes baseline, 1,678,375 bytes final. Increase: 6,383 bytes, below the 20KiB budget. No new runtime dependency. Existing large admin bundle/Browserslist warnings remain; they are not introduced by this branch.
- Browser matrix: 3 routes x 4 viewport widths (320, 375, 1440, 1920) x 2 themes = 24 captures. All have one H1, no document overflow, no broken visible images, and nonblank chart canvases where applicable.
- Browser interactions: search/empty state, key creation, provider changes clearing stale groups, mobile dialog and column menu, copy, edit, disable/enable, delete confirmation, CSV export, error tab, date presets/apply, sidebar collapse, mobile navigation, theme toggle, excluded route and trailing-slash route, initial overview failure and refresh recovery. No uncaught page errors.
- Cross-provider ordered smart-route preservation is also covered by the existing KeysView regression suite.
- Settled dashboard CDP sample: 4ms TaskDuration over 3,000ms, 24MiB JS heap in the final sample. This is one local Chrome dev-mode sample, not a production load test or memory-leak guarantee.
- Production asset scan: no demo token, demo identity or demo key markers. Preview tests reject public binding, foreign origins, rebinding hosts and unknown API writes; no real backend is configured in preview mode.
- The machine's security software injects a Kaspersky request into browser pages. Browser tests blocked it and recorded it separately from application requests; no application-origin external request was observed.
- Screenshots and reports: `.artifacts/console-signal/`; checks: `.artifacts/console-signal-check.cjs` and `.artifacts/console-signal-interactions.cjs`; bundle comparison: `bundle-report.json`.
- Start command in `frontend`: `pnpm run dev:console`. The delivered background process binds only `127.0.0.1:4317`. Preview data lives only in process memory and resets on restart.
- Local integrated commits: `dbc7595d0` (workspaces/plan), `92e388884` (shell), `c2fda3009` (dashboard), `a67a9b232` (isolated preview); final follow-up records integration fixes. No push, release, SSH, production configuration, database or traffic change.

### Lessons From Verification

1. A Vue class override can lose to scoped Tailwind dark utilities. Remove mutually exclusive root backgrounds and use explicit narrow selectors; screenshots caught what unit tests could not.
2. HTML-injected `localStorage.setItem` must receive a JSON string, not an object literal. Execute the bootstrap in a test context and parse the stored user, rather than only matching HTML text.
3. Preview contracts must mirror real form payloads, including `expires_at: ''` clearing expiry, supported sort fields and `by_platform` summaries. Fix the mock, not the production form.
4. Keep mobile labels from shrinking into vertical characters. Anchor menus inside the viewport, and wait for short entrance animations to finish before visual assertions.
5. Full route isolation includes matched routes with a trailing slash and teleported dialogs. Test both entering and leaving the scoped shell.
6. Treat a polished demo as visual/interaction evidence only. No real model request, payment or production rollout was tested or performed for this task.
