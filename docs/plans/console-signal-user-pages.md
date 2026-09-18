# SIGNAL User Page Completion

The user reported that channel status and recharge still use the old design after 329. Subsequent feedback rejected the overly flat visual treatment and explicitly added frequently used admin pages and interaction improvements. The current iteration must be reviewed locally on 4317 before another production release.

## Scope

- Channel status: all V1/V2/V3 modes and available channels; preserve polling, health colors, model/group filters and statistics.
- Recharge: amount/method selection, subscriptions, orders and payment status; preserve currencies, arithmetic, checkout actions and provider flows.
- Account: redemption, profile, affiliate and IP allowlist; retain existing validation and requests.
- Shared shell: include user routes and admin workspaces. Preserve public authentication and the canvas editor's own design.
- Admin: overview, account pool, groups, users and usage; preserve every money, permission, account-selection and API contract.
- Material and hierarchy: graphite/silver surfaces, restrained blue accents, precise border highlights, focused typography, compact toolbar groups and useful hover/focus feedback.
- Navigation: search frequently used destinations through the existing dialog, without changing permissions or exposing hidden payment/admin destinations.
- Local preview: add isolated fixtures for these pages and test interactions without real orders, transfers, redemptions or whitelist changes.

## Progress

- [x] Confirm 329 is full traffic on both hosts and retain 327 with automatic rollback.
- [x] Fix and visually verify new-host branding route.
- [x] Create separate branches/worktrees; assign disjoint channel/payment/preview work to gpt-5.6-sol agents.
- [x] Complete and integrate the initial user page layouts and shared-shell coverage.
- [x] Complete revised visual direction and admin page integration.
- [x] Finish independent review of the integrated admin presentation and navigation changes.
- [x] Verify existing critical regression suites and exact production build.
- [x] Inspect desktop/mobile screenshots, both themes, empty/loading/error states and key actions.
- [x] Leave the revised design running on 4317 for user review. Do not publish this rejected/intermediate direction as an approved release.
- [ ] After design acceptance, publish an immutable release and perform the established 1%-to-full guarded rollout.

## Reference Evidence

Attempted real browser inspection of ChatGPT, Grok, Claude, B.ai, Z.ai and DeepSeek. Mirasim visual runs timed out; direct Playwright reached B.ai and Z.ai. The others returned security/region blocks on both available exits, so no claim is made to have inspected their current application screens. Screenshots are local under `.artifacts/ai-design-references`. Z.ai demonstrates a narrow tool rail and a distinct primary work surface; B.ai demonstrates explicit brand identity with restrained neutral styling. These inform hierarchy and branding, not copying their assets or turning a data console into a chat page.

## Release Lessons

CI must run the actual production build, not only a partial typecheck. Check both embedded assets and separately hosted static index. Compare delivered assets, not just version labels. Wait for charts and images before screenshots. Inspect logo/resource routes that may accidentally fall through to SPA HTML. Separate mocked UI acceptance from real read-only or administrator-owned business probes. Do not infer absence of errors from an error-only journal; inspect business counters and classify existing routing failures separately.

## Local Acceptance, 2026-09-18

- User entry: `http://127.0.0.1:4317/__preview/user`; admin entry: `http://127.0.0.1:4317/__preview/admin`. These are loopback-only development routes. Identity selection affects the local demo process, not production authentication.
- Completed user routes include dashboard, keys, usage, channel monitor, available channels, recharge, orders, subscriptions, redemption, profile, affiliate, IP allowlist and payment-status/provider pages. Payment center iframe content remains owned by its external application.
- Admin overview, accounts, groups, users and usage received actual layout changes. Other admin pages inherit the revised shell and controls; they have not each been individually redesigned or accepted.
- Real browser acceptance: 16 user routes at 1440/375/320 in both themes (96 captures across batches) and five admin routes at the same sizes/themes (30 captures). No JavaScript exceptions, failed local assets, broken images or document-level overflow in the completed runs. Large tables retain internal scrolling.
- Browser interaction checks: destination search and empty results, Escape dismissal, ordinary-user destination isolation, recharge 50 CNY plus 0.6 percent fee equals 50.30 CNY, monitor range changes, account search, mobile filter expansion, user-create dialog open/cancel, admin quick navigation. No live funds or production records changed.
- Critical frontend suite: 24 files / 280 tests passed. Preview: 27 tests passed. Vue application/dev typechecks, targeted ESLint and production build passed. A release-asset scan found no preview identities, role switch API or fake keys. The build retains pre-existing large-chunk and mixed dynamic-import warnings.
- Found during integration: Vite held stale transformed modules after file changes; restarting only the verified local preview process made the actual new CSS available. Missing admin shell mock endpoints and a large group selector page size caused local 404/400; added explicit mock contracts, keeping unknown requests denied. Account filter flex wrapping prevented overlap with its action group.
- 329 remains deployed with its guard and retained fallback. No 330 tag, release or deployment was created for this new direction.
- Final independent review used normalized AST comparisons: Accounts, Users, Usage and Dashboard business scripts are unchanged; all 32 Groups API/router/emit calls match their prior contracts. It found one simple-mode search visibility mismatch, corrected to hide user management and retain group management as the existing sidebar does. A focused regression test passes; navigation-search tests now total four.

## Material Refinement, 2026-09-18

- Before: uniform frames competed with the data, recharge controls sat inside redundant framed sections, and admin pagination retained the legacy blue-gray background. After: neutral inset inputs, light surface highlights, separate popover shadows, tabular metric typography and consistent page controls. Recharge uses underlined tabs and open form sections; its total remains a framed checkout tool. No new dependencies, timers, large blur effects or persistent animations.
- Kept the existing workflows and layout structure. Payment calculations and pagination scripts are byte-identical to HEAD after parsing the Vue SFCs; only styles and the pagination root class changed. Channel availability thresholds, health colors, unknown-sample states and polling are unchanged.
- Found two cascade gaps during browser interaction checks: scoped mobile rules removed metric borders, and the accounts page uses a custom selection checkbox rather than DataTable's built-in selection marker. Fixed the metric selector and added a narrowly scoped selected-account-row rule; selection remains visible when hovered, including fixed columns.
- Local verification: 77 focused frontend tests plus three locale completeness tests passed. Targeted ESLint, Vue typecheck and the final production build passed. Existing Browserslist, chunk-size and mixed-import warnings remain. The shared studio stylesheet adds 710 gzip bytes compared with the preceding local revision; this is not a claim about total application transfer size.
- Browser verification: 16 user routes and five admin routes at 1440/375/320 in both themes. Material checks additionally cover stable hover geometry, all four metric borders, focused search contrast and focus restoration, recharge 50 plus 0.6 percent fee producing 50.30, selected rows, page switching and create-user dialog open/cancel. Screenshots and reports are under `.artifacts/signal-user-pages`, `.artifacts/signal-admin-pages` and `.artifacts/console-material`. All interaction data is local preview data, not production billing evidence.
- Lesson: screenshots without computed-style and interaction checks can miss selector conflicts and selection states. Check both built-in and slotted table checkboxes. Keep fine separation shadows on fixed columns so horizontal scrolling remains understandable. The darkest availability bars are an intentional existing below-30-percent status band, not a reason to change monitoring semantics during visual work.
- Preview remains on 4317. No tag, GitHub release, deployment, database change or server configuration change was made in this refinement.

## Layout And Interaction Revision, 2026-09-18

- The user requested actual interaction and layout changes after the material pass. Dashboard now places primary actions alongside the title, keeps account metrics visible, separates overview from platform breakdown with accessible keyboard tabs, and shows recent requests alongside the main chart on wide screens. Switching views preserves chart/date state and does not repeat dashboard requests. Simple mode still omits balance and platform data.
- Recharge now starts with amount entry and quick choices, followed by full-width payment-method choices. Account and checkout details share one receipt area. Mobile has one persistent checkout action, with bottom spacing so all content remains reachable; it disappears during another tab/payment phase. Orders are directly accessible. The amount entry displays the selected payment currency rather than a hard-coded dollar sign. Invalid pasted characters restore the visible accepted value instead of leaving a different amount on screen. No rate, fee, currency-conversion or create-order calculation was changed.
- A gpt-5.6-sol worker implemented account filtering in the isolated `F:/GO/sub2-ui-admin-interactions` worktree. Reviewed and integrated its commit as `55ecc49df`; the worker is closed. Search/actions share a toolbar, all viewports can collapse advanced filters, and removable tags plus reset preserve search and unrelated request parameters. Integration adds focus restoration and removes a duplicate search reload listener. Bulk commands wrap on small screens; mobile selected accounts now receive the same visible selection treatment as desktop.
- Shared ConsoleTabs is used by both dashboard and recharge. Arrow keys, Home and End move selection and focus with one tab stop. No new packages, polling loops or continuous animation were added.
- Verification: 21 focused test files / 201 tests plus three locale completeness tests passed; targeted ESLint, Vue typecheck and the final production build passed. Existing dependency-data and large-chunk/mixed-import warnings remain. Normalized Vue/TypeScript AST comparison against `e360d94ab` confirms unchanged business statements in dashboard, recharge, account management and bulk commands, excluding the explicit new UI state/imports.
- Browser coverage: five user routes and five admin routes at 1440/375/320 in both themes (60 page captures). Dedicated layout/interaction runs cover 1440/768/375/320 in both themes for user workflows and account filters (16 cases), including chart canvas pixels after tab switching, retained amounts, invalid input, checkout-dock visibility/clearance, exact single list requests, tag focus and selected mobile accounts. Artifacts live in `.artifacts/console-layout`. All used loopback preview fixtures; no live orders, funds or production records were touched.
- Lessons: use real DOM layout instead of CSS order for tabbed regions; retain chart instances when switching a local view; test slotted mobile selections separately from built-in table selections; do not wire both immediate model updates and a second debounced search event to the same list reload.
- Only the existing 4317 preview is updated. Production remains untouched and release approval is still pending.
