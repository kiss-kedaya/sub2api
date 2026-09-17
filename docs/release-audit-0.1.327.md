# 0.1.327 Release Audit

## Scope

- Smart routes validate candidate group authorization and status, use candidate
  model mappings, and bind billing, subscription, reservation, and forwarding
  policy to the selected route. Snapshot-only selection no longer falls back to
  repeated account database scans.
- Gemini bridges honor the configured upstream protocol and preserve exclusive
  normal/cache input usage, partial usage, cancellation, and terminal errors.
- The API-key editor groups available choices by provider, disables empty
  providers, clears stale single-group selections, and preserves ordered
  cross-provider smart routes. Existing quota and permission APIs are unchanged.
- No schema migration, account credentials, user balances, or group rates change.

## Verification

- Smart-route backend fixes passed full GitHub unit/integration, lint, frontend,
  and security checks at 25f896f712ce99745decba6f817e794e1fb8e00d.
- Provider editor: 19 focused Vue/i18n tests, TypeScript and ESLint passed.
- Playwright checked 1280, 375, and 320 pixel viewports with mocked APIs; provider
  filtering, empty categories, stale selection clearing, and layout passed.
- Release commit CI and immutable release artifact checks are required before
  traffic changes. Earlier test results alone do not approve production rollout.
- Initial main CI found an existing WebSocket preemption test ordering race:
  its fake upstream completion was released before the old client observed the
  preemption close frame. The same-commit tag unit suite passed. A test-only
  follow-up keeps the old request blocked for same-thread preemption and retains
  the close-status, reason, and exactly-one-preempted-session assertions. No
  production logic or the immutable 327 tag is changed by this follow-up.

## Rollout Contract

- Confirmed live baseline: the old-host ingress serves 0.1.326-grok600 on 8097
  with 326/8095 as backup. The new-host ingress rolled back to 326/8095 after
  memory headroom dropped below its guard threshold. Both versions remain alive.
  Physical weighting is old host 40%, new host 60%; preserve each ingress's
  actual initial configuration as its rollback target.
- Preserve the current 600-second Grok header timeout and other application
  settings. Candidate startup uses database migration validate mode.
- Back up configuration and units before changes; retain the serving 326-grok600
  processes, their connections, and a tested rollback target throughout rollout.
- Start with 1% candidate traffic, inspect real request/usage errors, then promote
  only after sufficient successful business traffic and healthy probes.
- Keep the health watchdog active through at least five minutes after promotion.
  Upstream business 502/503 alone are not rollback triggers. New platform faults,
  failed public/origin health, process restarts, or exhausted memory margins are.
- The plan alone is not deployment evidence. Actual production evidence below
  is retained in each host's versioned rollout directory.

## Production Evidence

- Immutable release/tag commit: `4bb0ed006195405a67e8d8ca92d34c980617a1ac`.
  Release `35230975481`, tag CI `35230975518`, and tag security `35230975136`
  succeeded. Test-only follow-up `0addf419f` passed main CI `35232439269` and
  security `35232439312`; it does not change the released runtime code.
- Linux archive SHA256:
  `cdb9f320c3e023a8b7e96c99f0a2aadacfb4294893560a61ad808a8eaacae9a3`.
  Both installed binaries:
  `7fc5f6ab33ccaf5fef2964c33fada6f97853d9d8b5d5ee12ab25cad8fb33f0ed`.
- New 327 PIDs: old host `2919068`, new host `2220921`. HTTP 8099 is private;
  pprof 6077 is loopback-only. Migration mode remains validate, Grok header
  timeout remains 600 seconds. Existing limits and proxy timeouts are unchanged.
- Preserved 326 PIDs: old `1257623` / `1276993`, new `2108208` / `2110135`.
  Their applications were never stopped or restarted during this rollout.
- Previously authorized retirement was limited to old processes with no nginx
  reference and zero inbound connections in two samples plus a final check.
  New-host 319/320/321/322/323/325 and old-host 318/319/320/321/323/325 stopped
  gracefully and were disabled; binary/config files were retained. New-host 318
  and old-host 322 still have separate proxy references and were left alone.
- New-host available memory increased from about 16 GiB to 46 GiB after removing
  the unreferenced processes. This is released capacity, not proof of a fixed
  memory leak. Both current 326 versions remain available for fallback.
- Retirement manifests: `/root/sub2api-pre327-retirement-20260917T141932Z`
  (new), `/root/sub2api-pre327-retirement-20260917T141940Z` (old).
- Install backups: `/root/sub2api-install327-20260917T142407Z` (old),
  `/root/sub2api-install327-20260917T142415Z` (new). Reserved-port backups are
  `/root/sub2api-327-ports-20260917T142353Z` and
  `/root/sub2api-327-ports-20260917T142401Z`; only 6077/8099 were appended.
- Both guards passed 27 simulations including panic attribution, upstream-error
  non-rollback, interrupted reload recovery, healthy rollback target selection,
  stale configuration ownership, and each ingress's distinct baseline.
  Installed guard SHA256:
  `3614d6828a6fd89b0b401d73142084b01b93ba1697206790e59e067677c419ec`.
- 1% started at 14:28:04 / 14:28:11 UTC, with physical old 40% / new 60% intact.
  At 14:39 UTC, old/new ingress candidate success counts were 257/238; health
  probes were 98/86 successful, with no candidate restart, panic, or OOM.
- Full traffic started at 14:41:48 / 14:41:53 UTC. Guards continue running in
  `/opt/sub2api/rollout-327-20260917`; their state and phase backups, not release
  metadata, are the authoritative current production status.
- At 14:48:08 / 14:48:14 UTC, both completed more than six minutes at full traffic
  and were marked verified/100%/armed, with 33/36 successful guard checks. Public
  version was 0.1.327, original PIDs were intact, candidate restart counts were
  zero, and no panic/OOM/auth-timeout signals appeared. Available memory was
  about 30.8 / 43.5 GiB; candidate RSS about 3.7 / 4.1 GiB. Monitoring remains
  active after this finite observation window; this is not a long-term guarantee.

## Request Checks And Boundaries

- Grok candidate tests on both machines returned 200 and response.completed;
  first streamed events arrived at 1.223 / 1.424 seconds, total 3.905 / 4.139
  seconds. No missing/nonmonotonic sequence numbers or custom-tool input mismatch.
- Full-traffic public Grok request `0ae2699b-33c3-4521-9581-caf6a68f25bf` returned
  200/completed, first event 1.743 seconds, total 3.849 seconds, cached input 256.
- Usage rows `189619453`, `189621585`, `189662945` matched downstream cached
  input 128/128/256. The final row recorded ordinary input 75 plus cached input
  256, not 331 ordinary plus cached input; first-token 1676 ms vs total 3785 ms.
  This verifies these real requests only, not every protocol or historic bill.
- Kiro candidate Messages and public Responses returned 200 with their proper
  terminal events. Public Responses total was 2.425 seconds.
- GPT diagnostic `gpt-5.6` was not a supported catalog name. `gpt-5.5` returned
  provider 502 "unknown provider for model" on both new 327 and original 326.
  `gpt-5.6-sol` / `gpt-6-astra` returned provider-overload 503 in terminal events;
  the ops records attribute these to upstream accounts, not platform failures.
  These are unresolved provider issues, not successful GPT smoke tests.
- No synthetic real-user keys were created. Frontend browser checks used the
  actual public 327 assets with mocked API data at 1280/375/320 pixels. Normal
  Grok/Kiro probes used existing administrator-owned test keys; credentials were
  never placed in release notes, frontend files, or public logs.

## Static UI Rollout

- The new ingress serves its SPA from `/opt/sub2api/ui`; replacing the Go binary
  alone does not update that HTML. The old ingress previously proxied the SPA.
- The immutable release's frontend-dist artifact was staged as
  `/opt/sub2api/ui-0.1.327`. Its 182 content-hashed assets were added, with byte
  equality checks on existing names, without deleting previous assets.
- Old ingress received a scoped `/assets/` static location for the existing
  kedaya.ai/sub.kedaya.xyz/17777 application routes. Missing legacy assets use
  retained 326-grok600/8097. New ingress's existing missing-asset fallback uses
  retained 326/8095, avoiding version lottery during gradual traffic changes.
- New-host index.html was switched atomically after full backend promotion,
  preserving its existing title. The old host serves the 327 embedded index.
  No API proxy limits, provider routing, TLS, or global nginx tuning changed.
- Original HTML and changed nginx files are in each guard directory's
  `ui-config-backup`, with before/after hashes. New frontend uses unchanged APIs
  and remains compatible with backend rollback; both generations of assets stay.
- Public browser verification passed after replacing networkidle with a concrete
  UI-ready condition. Background network activity is not an application failure.
