# `/v1` Hot Path Optimization Implementation Plan

> **For agentic workers:** REQUIRED: Use subagent-driven development or executing-plans when available. Steps use checkbox syntax for tracking.

**Goal:** Reduce PostgreSQL and per-request coordination overhead on the high-volume `/v1` request path while preserving authentication, billing, routing, failover, streaming, and usage semantics.

**Architecture:** Keep PostgreSQL authoritative for durable state and usage records. Use existing API-key, subscription, channel, scheduler snapshot, Redis projection, and request-scoped caches only where their freshness and invalidation contracts are explicit; fail closed for unknown scheduler health and fail open only where the existing billing contract requires it. Each behavior change is isolated in its own commit and verified before rollout.

**Tech Stack:** Go, Gin, Ent/PostgreSQL, Redis, scheduler snapshot, Ristretto/singleflight, existing frontend build and deployment tooling.

---

## Chunk 1: Request-local scheduling reuse

**Files:** `backend/internal/service/scheduler_freshness.go`, `gateway_scheduling.go`, `openai_gateway_scheduling.go`, and focused service tests.

- [x] Add request-local hydrated-account storage with deep private copies.
- [x] Reuse already resolved group context in legacy and mixed scheduling branches.
- [x] Run focused and full service tests.
- [x] Commit each optimization independently.

## Chunk 2: `/v1` flow audit and safe hot-path fixes

**Files to inspect:** API-key middleware, subscription/billing cache service, channel mapping, gateway/OpenAI handlers, scheduler/failover, upstream forwarders, and usage worker submission.

- [ ] Trace every PostgreSQL call reachable from `/v1` and classify it as required durable read, cache miss fallback, maintenance, or accidental repeat.
- [ ] Add tests before any semantic change to cache freshness, billing, failover, or usage idempotency.
- [ ] Implement only evidence-backed fixes; keep each optimization or bug fix in a separate commit.

## Chunk 3: Upstream compatibility review

- [ ] Compare `upstream/main` commit-by-commit; do not merge or force cherry-pick the divergent history.
- [ ] Manually port only isolated fixes that apply cleanly after checking local schema and service contracts.
- [ ] Add regression coverage for every ported behavior and commit it separately.

## Chunk 4: Verification and release

- [ ] Run repository/service/handler tests, race tests, vet, and diff checks.
- [ ] Run frontend tests, typecheck, and build if frontend or embedded assets changed.
- [ ] Ask multiple read-only review agents to inspect scheduling, billing/usage, forwarding/streaming, and upstream compatibility.
- [ ] Fix review findings in independent commits, then bump the version in its own commit.
- [ ] Publish to the new pool first, perform health and low-frequency smoke tests, and only then update the old Docker pool.

## Rollout guardrails

- Never use `git reset --hard` or overwrite `.artifacts/` or unrelated user changes.
- Do not run pressure tests against production servers.
- Keep the existing Cloudflare load-balancer policy unchanged until application health and error rates are verified.
- Preserve rollback by disabling the relevant feature switch or reverting its isolated commit.
