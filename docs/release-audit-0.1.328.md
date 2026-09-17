# 0.1.328 Release Audit

## Scope

- Publish the owner-approved SIGNAL design on dashboard, API keys and usage pages.
- Preserve account permissions, all key operations, ordered smart routes, billing amounts and API contracts.
- Correct chart theme updates, responsive controls and dashboard initial-error recovery.
- Local demonstration mode remains serve-only and loopback-only. Production assets contain no demo identity, key or authentication bootstrap.
- CI includes the new page/chart/shell regressions and the dedicated Node preview isolation suite.
- No backend runtime logic, schema, credentials, balances, rates or provider configuration changes.

## Baseline

- Main baseline: `924d9ebfe`; approved design: `f7bdc2e73`.
- Old host ns575199: 327 PID 2919068; new host ns572368: 327 PID 2220921.
- Both 327 guards were verified/100%/armed with no recent panic, OOM, database connection or auth timeout signals.
- Existing physical weights: old 40%, new 60%. Preserve the actual upstream configuration on each ingress as rollback evidence.
- New 328 candidate will retain 327 alive throughout, use migration validate mode, preserve Grok header timeout 600s and all other current application settings.

## Rollout

1. Obtain green CI and security for the release commit; publish immutable tag and artifacts.
2. Verify checksums, back up configurations and units, start candidates without ingress changes.
3. Stage all content-hashed assets additively. Preserve previous index and old assets.
4. Apply 1% candidate traffic, verify business successes and attributed platform errors.
5. Promote after healthy observation; retain old instances and automatic platform-health rollback.
6. Confirm public web assets, health, streamed business request and more than five minutes of full-traffic observation.

Upstream business 502/503 are observed but do not cause rollback by themselves. Candidate process failure, platform panic, failed public/origin health or exhausted memory margin do.

## Evidence

- Prior local acceptance: 99 focused tests, typecheck, ESLint and build passed; 24 responsive/theme captures and key/usage interactions passed.
- Final release CI, checksums and production verification will be appended after execution.
