# 0.1.329 Release Audit

## Scope And Build Correction

This publishes the approved SIGNAL dashboard, API keys and usage design. All API, accounting, permission and routing behavior remains unchanged; no database migration is introduced.

The preceding v0.1.328 tag failed its release frontend build before any artifact or deployment. CI had checked the application with `vue-tsc --noEmit`, but release uses `vue-tsc -b`, which also compiles the Node config project. Its transitive import of the local preview exposed a composite-project/type boundary error. The immutable tag is retained; 328 was never installed.

The preview now has a separate `dev/vite.config.ts` entry, launched only by `dev:console`. Production config does not import demo code. CI additionally runs the exact production build, preview typecheck and preview isolation tests. This fixes the coverage gap rather than bypassing compiler checks.

## Production Contract

- Release runtime commit and all CI checks must be verified before installation.
- Baseline: both hosts serve 327 on private port 8099, old ns575199 PID 2919068, new ns572368 PID 2220921; old/new physical weights 40/60.
- Candidate 329 uses private HTTP 8101 and loopback pprof 6079. Existing environment settings, including Grok header timeout 600 and migration validate mode, are inherited unchanged.
- Back up units, environment and ingress config before changes; stage content-hashed assets additively.
- Observe 1% traffic for at least three minutes and sufficient successful business requests, then full traffic for at least five minutes with an active rollback guard.
- Retain 327 processes and all previous UI assets. New-host static index is switched atomically and is also restored on rollback if still owned by this rollout.
- Upstream business 502/503 alone do not trigger rollback. Failed public/origin health, candidate process failure, platform panic or insufficient memory margin do.

## Evidence

Prior design acceptance passed 99 tests, 24 viewport/theme captures and key/usage interactions. Actual build, artifact, installation, traffic and public-browser evidence will be appended as execution completes.

### Completed Deployment, 2026-09-18 UTC

- Runtime/tag commit: `bbdd21d98f30fbbce8e3566ae4a4d5b138798acc`; release `v0.1.329` is published. Main CI `35289945551`, main security `35289945565`, tag CI `35290412759`, tag security `35290412799`, release `35290412778` all succeeded. The extra create-triggered release was cancelled by concurrency deduplication.
- Linux archive SHA256: `8832bca1030377ed2360b39994fe7014b32fb3f516d8f7af6c2c13d76475353c`. Both installed binaries: `a99888c22fb018c592a2de403485659cd1776575934a89e999a6537d35734eef`.
- Old/new canary started at 00:35:09/00:35:14. Both exceeded three minutes, twelve guard checks and fifty successful candidate business requests before promotion. Old/new full traffic started at 00:45:59/00:46:21; physical weights remain 40/60.
- Both guards finalized after more than five minutes of full traffic, at 00:52:22/00:52:26, with 33/34 successful checks. State remains armed and verified. No application restart, observed panic or OOM occurred. Original 327 PIDs 2919068/2220921 are retained; 329 PIDs 3564549/2274487 serve new traffic.
- At finalization, candidate RSS was approximately 1.01/1.09 GiB and available system memory approximately 33.9/43.5 GiB. These are rollout snapshots, not evidence of a long-term memory-leak fix.
- Both origin entries and six referenced entry assets match release 329. Shared assets were installed additively; new-host static index was switched separately. Browser loads the new `index-ahKFxHua.js` and `index-BM9zakgt.css`.
- Public browser verification covered dashboard, keys and usage at 1440/375/320 widths, including key-provider selection, no overflow, missing assets, broken images or JavaScript exceptions. HTML, images and bundles came from production; account API data was locally mocked to avoid production writes. Final screenshots waited for chart loading and image decoding.
- Real administrator-owned Grok requests returned SSE 200 and `response.completed` through both candidate processes. Public request `4bedcddd-04b2-4286-ab28-d4c0995f938d` produced first reasoning delta at 1428 ms, text delta at 2247 ms and completion at 2887 ms; cached tokens 256, missing/nonmonotonic sequence numbers 0, custom tool-input mismatches 0.
- Kiro Messages returned 200 with `message_stop`; public Responses request `be863b13-aee6-49ea-baa3-77109a812707` returned 200 with `response.completed` in 2377 ms.
- Existing business failures include both upstream errors and routing 503s; they are not all upstream failures. The shared error table has no release identifier and ingress IDs did not directly join to application IDs in sampled records. No new backend business implementation was introduced relative to 327; only VERSION and a test fixture differ under backend. Therefore the rollout conclusion is no observed new platform regression, not zero business errors.

### Branding Route Correction

Visual inspection found the new host returned SPA HTML for `/site-logo`, whereas the old host returned the configured JPEG. Added only an exact `/site-logo` proxy location to the existing backend-locations snippet, after backing it up to `/root/sub2api-site-logo-20260918T005726Z`. Nginx validation, graceful reload, JPEG SHA256 comparison with 329 and retained 327, and readiness checks passed. Logo SHA256: `c80694bd910bb873975f727e4458a95f51805f1e8351d82ca98cde5965f70bc1`. The route follows the existing upstream, including rollback to 327. A subsequent public-browser check confirmed the image renders.

### Scope Follow-up

329 changes only dashboard, keys and usage. The user subsequently requested matching channel-status, recharge and related user pages. That work is on `feat/console-signal-user-pages`; it must not be described as already included in 329.
