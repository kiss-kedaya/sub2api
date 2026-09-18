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
- [ ] Finish independent review of the integrated admin presentation and navigation changes.
- [x] Verify existing critical regression suites and exact production build.
- [x] Inspect desktop/mobile screenshots, both themes, empty/loading/error states and key actions.
- [ ] Leave the revised design running on 4317 for user review. Do not publish this rejected/intermediate direction as an approved release.
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
