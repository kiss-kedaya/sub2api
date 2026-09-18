# 0.1.330 Console Layout And Interaction Release

Status: release authorized on 2026-09-18 after local design review. Publish and deploy only after the exact release commit passes CI and the release artifact build. Deployment evidence is pending; this document alone does not establish that 330 is live.

## Intended Behavior

Channel status and recharge were still using their prior presentation in 329. This release extends the revised console to monitoring, recharge, orders, subscriptions, redemption, profile, affiliate and IP allowlist pages. Admin overview, accounts, groups, users and usage receive layout and interaction updates; other admin routes share the new shell but were not individually redesigned. Authentication and the canvas editor retain their own layouts. Payment amounts, currencies, fees, order creation, provider integrations, channel metrics and polling retain their existing contracts.

The simplified dashboard uses the existing snapshot-v2 endpoint for trend and model data. Quotas and the five most recent requests load when expanded, with cancellation and stale-response protection. Total-usage labels say recent totals because the existing aggregate and raw-log paths have different retention windows; this release does not change backend aggregation semantics or fabricate comparison data.

The recharge center can embed a separately hosted payment page. The gateway controls its own navigation, frame and actions; content inside that external frame belongs to the payment application.

## Validation And Rollout

- Runtime backend changes are limited to VERSION; no migration or production data mutation is needed for the design.
- Reuse the tested 329 rollout mechanism with candidate HTTP 8103 and loopback pprof 6081. Immediate fallback is retained 329/8101. During 1% canary, existing 327/8099 backup endpoints remain configured. No application process is stopped by rollout or rollback.
- Both gateways retain physical traffic weights old/new 40/60. Install release assets additively and update the new-host static index atomically after promotion.
- Promotion requires at least 180 seconds, twelve healthy guard checks, a recent check and fifty successful candidate business requests on each ingress. Full traffic observation requires at least 300 seconds and twenty-four healthy checks.
- Pure upstream 502/503 do not trigger rollback. Platform/origin/public health failures, panic and exhausted memory margin do.
- The new-host exact `/site-logo` route added during 329 remains compatible with both candidate and fallback. Verify image decoding as well as JS/CSS responses.

Execution evidence will be added after validation and deployment. This document is not a claim that 330 is already live.

## Preflight Evidence

- Full local frontend suite: 290 files / 2193 tests passed. Preview isolation and contracts: 27 tests passed. Full frontend ESLint passed. The exact production build and remote CI are checked again for release.
- Added snapshot cancellation, navigation, table, amount-input and admin-filter regressions to the CI critical suite. Existing production-build and Linux rollback simulations remain mandatory.
- Live read-only inspection confirmed both hosts still serve 329, with physical weights 40/60 and 327 fallback. Preserved 329 PIDs: old 3564549, new 2274487. Preserved 327 PIDs: old 2919068, new 2220921. All report zero restarts.
- Compared with 329, runtime backend changes are only VERSION. No schema, database data, account routing, authentication or protocol-conversion change is included.
