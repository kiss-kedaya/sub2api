# 0.1.312 Release Audit Notes

Date: 2026-09-15

## Release Result

- Baseline: live 0.1.303 commit `498f5333e32d12bc509770b38ae0d93f8b212d2c`.
- Releases: `v0.1.311` was built but never exposed to traffic; final audited release is `v0.1.312` at merge commit `4a3d70f34a00a35537efd0d0000aa2f9cb472fa3`.
- PRs: #46 carried the audited upstream sync and post-303 fixes; #47 closed the follow-up audit findings.
- Database migrations remain validate-only. The destructive quota migration 243 was not restored or executed.

## Findings And Fixes

- Gemini-compatible HTTP 422 responses now preserve Google error semantics: resource exhaustion remains 429, invalid arguments remain 400, and unknown 422 failures become 502.
- OpenAI WS capacity waits now wake after a waiter cancels, while pool shutdown and request contexts still bound the wait.
- Exhausted upstream `503 server_error` responses remain 503 instead of being rewritten to 502.
- Infinite Canvas uses a rewrite-only reverse proxy. The old `Director=nil` workaround triggered Go 1.26 staticcheck and was replaced with an explicit `ReverseProxy{Rewrite: ...}`.
- SSE/media handling uses a bounded 16 MiB scanner and cancels completed upstream contexts. This prevents large media truncation and completed non-streaming requests retaining upstream resources.
- Image URL-to-base64 backfill removes provider URLs from the client response. A regression test previously required the leak and was corrected.
- Scheduler selection keeps the original diagnostic path while preferring explicit model mappings over empty mappings.
- Grok nullable root-tool simplification expands only resolved local `#/$defs/...` references. Unresolved, external, and nested references are preserved instead of becoming an empty schema or dangling reference.

## Verification

- Go unit tests, Linux CI integration tests, frontend full tests (283 files / 2113 tests), frontend build, security scans, lint, and shell checks passed.
- Targeted race tests for WS, Canvas, Gemini, Grok, quota, and OpenAI monitor fixtures passed. The initial full race run exposed shared test-fixture writes; the fixture was synchronized. Do not treat an unrelated full-race failure as a production leak without a runtime reproduction.
- Production pprof sampling on 312 generated a 134 KB CPU profile. No startup panic/fatal error was observed.
- Grok production smoke test returned HTTP 200, visible output, and a matching usage ledger entry. GPT smoke requests returned 502 on both old and new versions, so they were treated as upstream/account failures rather than a 312 regression.

## Rollout Safety

- Production topology has two live 303 processes: `127.0.0.1:8080` and `127.0.0.1:8081`. They were never stopped. Established connections remain on their original workers.
- 312 runs independently on `127.0.0.1:8082`, with pprof on loopback `127.0.0.1:6061` and systemd enabled.
- The shared environment file contains `SERVER_PORT=8081`. A later-loaded per-release override file is required; placing `SERVER_PORT=8082` earlier in the unit is ineffective.
- Canary traffic was 99/1 for five minutes, then full traffic was observed for five minutes. Public `/health` and `/readyz` checks passed 300/300 during canary and 100/100 after promotion. New-upstream HTTP 502/503 count was zero in the rollout log.
- Rollback is configuration-only: restore the recorded nginx files and graceful-reload nginx. The guard checks old PIDs before every phase and automatically rolls back on health failure or external configuration edits.
- A PostgreSQL custom-format backup was completed before traffic promotion on the database host: approximately 22 GB, TOC verified, SHA-256 `6b59da5569d3030e12dd228a73a2c16c9ee361e5c282b9d6a61d09cf21991665`.

## Repeatable Lessons

1. Compare every post-release change against the actual live commit, not a nearby PR or tag.
2. Preserve billing, quota, and migration semantics during mixed-version rollout; never repair a checksum by falsifying migration history.
3. Test each protocol boundary with real request and response shapes, including SSE, tool schemas, error status bodies, and large media.
4. Verify memory behavior with bounded buffers, context cancellation, pprof, and long-running runtime evidence; unit tests alone do not prove leak freedom.
5. Validate the complete systemd environment after overrides. A correct-looking `Environment=` line can be overridden by `EnvironmentFile` values.
6. A health endpoint is necessary but insufficient: pair it with real model output, usage ledger, status-code distribution, logs, and upstream attribution.
7. Keep old processes alive during rollout. Change only nginx weights, use graceful reload, and make rollback idempotent and PID-aware.
