# SIGNAL User Page Completion

The user reported that channel status and recharge still use the old design after 329. Extend the approved user-console visual system while preserving business behavior.

## Scope

- Channel status: all V1/V2/V3 modes and available channels; preserve polling, health colors, model/group filters and statistics.
- Recharge: amount/method selection, subscriptions, orders and payment status; preserve currencies, arithmetic, checkout actions and provider flows.
- Account: redemption, profile, affiliate and IP allowlist; retain existing validation and requests.
- Shared shell: opt these exact user routes into SIGNAL. Keep admin, public authentication and the canvas editor outside this change.
- Local preview: add isolated fixtures for these pages and test interactions without real orders, transfers, redemptions or whitelist changes.

## Progress

- [x] Confirm 329 is full traffic on both hosts and retain 327 with automatic rollback.
- [x] Fix and visually verify new-host branding route.
- [x] Create separate branches/worktrees; assign disjoint channel/payment/preview work to gpt-5.6-sol agents.
- [ ] Complete and integrate page layouts and shared-shell coverage.
- [ ] Review presentation diffs for changes to money, permissions, polling or API semantics.
- [ ] Verify existing regression suites and exact production build.
- [ ] Inspect desktop/mobile screenshots, both themes, empty/loading/error states and key actions.
- [ ] Publish the next immutable release with successful CI and release build.
- [ ] Roll out 1%, then full traffic with retained fallback, public validation and at least five minutes monitoring.

## Release Lessons

CI must run the actual production build, not only a partial typecheck. Check both embedded assets and separately hosted static index. Compare delivered assets, not just version labels. Wait for charts and images before screenshots. Inspect logo/resource routes that may accidentally fall through to SPA HTML. Separate mocked UI acceptance from real read-only or administrator-owned business probes. Do not infer absence of errors from an error-only journal; inspect business counters and classify existing routing failures separately.
