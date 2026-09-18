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
- [ ] Complete revised visual direction and admin page integration.
- [ ] Review presentation diffs for changes to money, permissions, polling or API semantics.
- [ ] Verify existing regression suites and exact production build.
- [ ] Inspect desktop/mobile screenshots, both themes, empty/loading/error states and key actions.
- [ ] Leave the revised design running on 4317 for user review. Do not publish this rejected/intermediate direction as an approved release.
- [ ] After design acceptance, publish an immutable release and perform the established 1%-to-full guarded rollout.

## Reference Evidence

Attempted real browser inspection of ChatGPT, Grok, Claude, B.ai, Z.ai and DeepSeek. Mirasim visual runs timed out; direct Playwright reached B.ai and Z.ai. The others returned security/region blocks on both available exits, so no claim is made to have inspected their current application screens. Screenshots are local under `.artifacts/ai-design-references`. Z.ai demonstrates a narrow tool rail and a distinct primary work surface; B.ai demonstrates explicit brand identity with restrained neutral styling. These inform hierarchy and branding, not copying their assets or turning a data console into a chat page.

## Release Lessons

CI must run the actual production build, not only a partial typecheck. Check both embedded assets and separately hosted static index. Compare delivered assets, not just version labels. Wait for charts and images before screenshots. Inspect logo/resource routes that may accidentally fall through to SPA HTML. Separate mocked UI acceptance from real read-only or administrator-owned business probes. Do not infer absence of errors from an error-only journal; inspect business counters and classify existing routing failures separately.
