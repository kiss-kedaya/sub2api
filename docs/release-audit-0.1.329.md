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
