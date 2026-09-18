# 0.1.330 Console Layout And Interaction Release

Status: published and deployed on both hosts on 2026-09-18. Both rollouts reached verified/full traffic with rollback protection armed. The retained 329 applications remain running. See execution evidence and residual issues below.

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

The execution evidence below supersedes the earlier unpublished draft.

## Preflight Evidence

- Full local frontend suite: 290 files / 2193 tests passed. Preview isolation and contracts: 27 tests passed. Full frontend ESLint passed. The exact production build and remote CI are checked again for release.
- Added snapshot cancellation, navigation, table, amount-input and admin-filter regressions to the CI critical suite. Existing production-build and Linux rollback simulations remain mandatory.
- Live read-only inspection confirmed both hosts still serve 329, with physical weights 40/60 and 327 fallback. Preserved 329 PIDs: old 3564549, new 2274487. Preserved 327 PIDs: old 2919068, new 2220921. All report zero restarts.
- Compared with 329, runtime backend changes are only VERSION. No schema, database data, account routing, authentication or protocol-conversion change is included.

## Completed Deployment, 2026-09-18 UTC

- Immutable release/tag commit: `14f36f4680f7d3d12b7a3d8cd7f963593316e3b1`, release `v0.1.330`. Main CI `35329074884`, main security `35329074895`, tag CI `35330052541`, tag security `35330052390` and release `35330052949` all succeeded. The duplicate push-triggered release `35330052376` was cancelled by the existing concurrency deduplication; the create-triggered release completed successfully.
- Full local frontend regression: 290 files / 2193 tests. Preview isolation: 27 tests. Production build, application/dev typechecks and full ESLint passed. CI also passed backend unit/integration tests, Go lint, shell tests and 64 Linux rollback simulations. No tests or runtime checks were bypassed.
- Linux archive SHA256: `3c1815f92b47a86383020b45cd5885609d18bca9d52102e91a085a6b1f2f9632`. Both installed binaries: `5aee7512e4d1c680f6e6fe1321a2f1f884cb19c8d8c87cb9c31e0894b41ba88b`. Frontend archive made from the same release workflow artifact: `a0049f5dcd53c1c8519e9bfa802f0c3e776d44abd07c8ad301c9670b861c7698`. No local-preview build was installed.
- Old/new install backups: `/root/sub2api-install330-20260918T094734Z` and `/root/sub2api-install330-20260918T094742Z`. Units, base/override environment and ingress configuration were backed up. Only the old host needed ports 6081/8103 added to its existing ephemeral-port exclusions, backed up under `/root/sub2api-330-ports-20260918T094729Z`; the new host's ephemeral range already excludes these ports. No resource limits, provider settings, database schema or records were altered by installation. Migration mode remains `validate`, Grok header timeout remains 600 seconds.
- Candidate PIDs: old 4170228, new 2322406, private HTTP 8103 and loopback pprof 6081. Retained 329 PIDs: old 3564549, new 2274487. Existing 327 PIDs 2919068/2220921 were also left running. No application was restarted or stopped for cutover.
- Old/new 1% canary began at 09:49:23/09:49:29; full traffic began at 09:57:38/09:57:45. Each ingress exceeded the minimum observation time, guard checks and fifty successful candidate business requests. Physical old/new weights remain 40/60. Only superseded guard timers were stopped during the ownership handoff; application processes and existing connections were retained.
- Old/new guards reached verified at 10:03:47/10:03:51, after more than six minutes of full traffic and 32/34 healthy checks. Guards remain armed and scheduled every ten seconds. Platform/origin/public-health failures recover to healthy 329 endpoints; unclassified business 5xx do not independently trigger rollback.
- At verification, candidate RSS was approximately 3.3/4.5 GiB and available memory 31.8/36.1 GiB. No application restart, panic, OOM, database-slot exhaustion or auth-lookup timeout was observed. These are short rollout observations, not proof that all historical memory issues are solved.
- Installed 190 content-hashed assets additively on each host. New-host static index was switched atomically with ownership-checked rollback; old assets and the `/site-logo` route remain intact. Both origin entrypoints and six referenced entry assets matched the release. The browser receives `index-DWqefOhA.js` and `index-NbJvnYY_.css`, not the 329 bundles.
- Browser verification of the GitHub frontend artifact and the actual old-host candidate each completed 72 viewport/theme captures with 105 assets and no JavaScript exceptions, failed assets, broken images or document overflow. Account API responses were mocked locally, while HTML, branding and assets came from the respective deployment. Live API checks are separate: both candidates and the public domain returned 200 for health/version, paginated users/accounts/groups and the real admin snapshot (one day row and 44 model rows). Public snapshot check took 207 ms in that sample. No real order or administrative mutation was submitted by browser testing.
- Administrator-owned Grok Responses calls completed on both candidates with valid event sequences and tool-input reconstruction. The public call `56706b69-6f38-44d8-9a60-a0f29cd1fade` returned SSE 200, first reasoning delta at 11222 ms and `response.completed` at 15302 ms, with 512 cached tokens. Stored usage `client:7f3b33cd-575b-48a9-a479-9d6b3ee5115b` agrees: 512 cache-read tokens, 125 uncached input and 308 output. Missing/nonmonotonic sequence numbers and custom-input mismatches were zero.
- Public Kiro Messages call `78ff9d16-4297-4220-aad0-3ae7e4f67ffd` returned SSE 200, first text at 2391 ms and `message_stop` at 2499 ms.

## Residual Issues And Lessons

- One candidate Grok call delivered its first content at 43723 ms and completed at 43807 ms. A repeat streamed reasoning from 6335 ms and completed at 9787 ms, carrying 512 cached tokens; 329 on the same host/account also streamed normally. A bounded read-only query found 40 similarly late-first-content records among 6842 streams on account 29131 during the ten minutes before canary, limited to outputs of at least 200 tokens. This establishes that the symptom predates 330, not its root cause. Intermittent Grok buffering/late delivery remains unresolved and must not be described as fixed by this UI release.
- Existing routing 503s are platform-owned, not upstream-owned. The five-minute windows before/after full traffic contained 240/289 such diagnostic events, all group 9 with `Service temporarily unavailable`. Upstream 502/503 and WebSocket policy failures also remain. These event counts are not request error rates, and the shared diagnostics table does not identify application versions. No new platform failure type or changed runtime implementation was found; this is not a claim of zero business errors. Group 9 availability needs separate investigation.
- The default Python urllib user agent receives Cloudflare error 1010 at public `/readyz`, while the same request to the origin succeeds. Curl probes, real browser navigation and authenticated verification using an explicit release-client user agent succeed. Do not weaken Cloudflare rules or roll back a healthy application solely because a blocked automation identity differs from real-client behavior.
- Browser evidence must distinguish actual deployed assets from mocked account data. Public screenshots also require suppressing both user and admin onboarding state in the test browser; an open first-use tour can obscure the intended layout without generating any runtime or asset error. No production onboarding behavior was changed.
- Reuse an immutable workflow artifact for embedded and standalone UI. A changed Go version label alone does not prove the new static homepage is live. Keep rollback ownership checks and old content-hashed assets so rollback does not break connected clients.

## Final Public Verification

- The repeated public-browser run after suppressing test-only onboarding completed 24 desktop/mobile captures across seven user pages and five admin pages, with 105 production resources, no JavaScript exceptions, failed resources, broken images or document overflow. The deployed site logo decoded successfully. API data in these screenshots remains mocked; the real authenticated API and business probes are documented separately above.
- Both guards remained verified/100%/armed after more than twelve minutes of full traffic. Old/new 329 and 327 PIDs were unchanged and active, with zero restarts. Physical weights remain 40/60. No cache purge, firewall-rule change, production data correction or application process shutdown was performed.
