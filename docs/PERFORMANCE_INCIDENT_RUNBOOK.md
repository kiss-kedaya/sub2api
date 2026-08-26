# Production Performance Incident Runbook

Use this runbook before changing concurrency, database pools, Redis pools, timeouts,
or billing semantics. Resource saturation is a symptom; the first task is to prove
which work consumes CPU, memory, locks, and I/O.

## Safety Rules

- Profile one low-weight canary instance first. Do not add an equal-weight
  replica to an already saturated upstream and do not enable pprof everywhere.
- Keep pprof on loopback. Never proxy `/debug/pprof` through the public HTTP server.
- Preserve billing, idempotency, partial usage, failover, and audit behavior while optimizing.
- Do not change user-configured concurrency as an incident mitigation. Rejection can
  trigger aggressive downstream retries, amplify load, and corrupt channel-health
  signals; remove the measured shared bottleneck without changing admission policy.
- Change one variable at a time and keep an immediate rollback artifact.
- Compare the same traffic window before and after each change.

A microbenchmark or a standalone test binary is not a canary. A valid canary is
the exact release artifact, connected to the same migrated schema and production-like
data, receiving a small controlled share of comparable traffic. Never upload or
start an application binary against a database whose migration state is unknown.

## Migration Gate

Use the release binary itself for all three steps:

```bash
# Read-only. It is expected to fail and list pending migrations before rollout.
/app/sub2api -check-migrations

# Run once as a dedicated job, with no HTTP listener or background workers.
/app/sub2api -migrate-only

# Must succeed before a canary is allowed to receive traffic.
/app/sub2api -check-migrations
```

Start the canary with `DATABASE_MIGRATION_MODE=validate`. This mode reads
`schema_migrations` once and rejects a missing or checksum-mismatched embedded
migration; it never applies schema changes. Extra historical migration rows are
allowed because production deployments may contain audited custom migrations.

Before `-migrate-only`, verify backup recovery, migration disk headroom, lock
impact, and old/new binary compatibility. A migration that installs a trigger is
cluster-wide even when only one application replica is a canary, so it needs its
own rollback and load assessment.

## Evidence Checklist

Capture these together for a representative load window:

1. CPU, heap after GC, allocations, goroutines, mutex, block, and execution trace.
2. Request rate, active streams, request-body size distribution, latency, and error rate.
3. PostgreSQL CPU, pool wait, active transactions, lock waits, slow statements, and oldest transaction.
4. Redis CPU, latency, blocked clients, memory, evictions, command rate, and Lua latency.
5. Nginx active connections, upstream response time, retries, status distribution, and socket errors.
6. Container CPU, RSS, OOM events, throttling, open files, sockets, and host pressure.

Do not infer causality from a container CPU percentage alone. Confirm the responsible
stack in CPU profiles and the retained owners in heap profiles. Use mutex/block profiles
to separate CPU work from waiting.

## Capture

Enable the isolated diagnostics listener on a single canary:

```text
PPROF_ENABLED=true
PPROF_ADDR=127.0.0.1:6060
PPROF_BLOCK_RATE=1000000
PPROF_MUTEX_FRACTION=10
```

Then run from that host or container namespace:

```bash
sh backend/scripts/capture-pprof.sh 45 ./data/diagnostics
```

The script writes profiles plus a SHA-256 manifest into a timestamped directory.
Analyze with the exact binary that produced the profiles:

```bash
go tool pprof -top ./sub2api ./data/diagnostics/<capture>/cpu.pprof
go tool pprof -top ./sub2api ./data/diagnostics/<capture>/heap-gc.pprof
go tool pprof -http=127.0.0.1:0 ./sub2api ./data/diagnostics/<capture>/cpu.pprof
go tool trace ./data/diagnostics/<capture>/trace.out
```

Use the narrowest tool that can prove the suspected cost:

| Question | Evidence |
| --- | --- |
| Which Go stacks consume CPU or retain memory? | CPU, heap, allocs, goroutine, mutex, block pprof profiles |
| Is GC pacing or scheduling the cost? | Comparable pprof plus a short `GODEBUG=gctrace=1` capture and `go tool trace` |
| Did an allocation rewrite improve the hot path? | Differential tests, `go test -benchmem`, then `benchstat` over repeated samples |
| Is PostgreSQL CPU, locking, or I/O dominant? | `pg_stat_activity`, `pg_stat_progress_vacuum`, pool wait metrics, and `pg_stat_statements` after a planned preload/restart |
| Is one SQL plan the problem? | `EXPLAIN (ANALYZE, BUFFERS, WAL)` on staging or a replica, not an unbounded production query |
| Is Redis the bottleneck? | `INFO commandstats`, `INFO latency`, `SLOWLOG`, blocked clients, and Lua latency |
| Is the host or cgroup saturated? | `pidstat`, `vmstat`, `iostat`, `perf`, Docker stats/events, PSI, and OOM counters |

`GOGC` and `GOMEMLIMIT` are experiment variables, not a first-line fix. Compose
keeps Go defaults (`100` and `off`). Change them on one validated canary only,
leave memory headroom below the 12 GiB cgroup, and compare throughput, CPU per
request, GC pause/frequency, live heap, RSS, and errors under the same traffic.

## Decision Order

1. Remove repeated full-body parsing and copying before tuning GC percentages.
2. Remove synchronous hot-row writes through durable, idempotent coalescing before increasing database connections.
3. Fix lock or pool waits before adding application replicas; replicas can multiply database pressure.
4. Fix upstream and socket timeout ownership before extending every timeout globally.
5. Tune Nginx and kernel limits only after measuring connection state and file-descriptor pressure.
6. Add capacity only after the per-request cost and shared bottleneck are understood.

For JSON rewrites, preserve unknown fields, number representation, string escaping,
duplicate-key behavior, and unchanged bytes. Prefer a read-only view, collect non-overlapping
patches, and allocate the final body once. Guard these properties with differential tests,
fuzzing, and `-benchmem` at 4 KiB, 256 KiB, 4 MiB, and 16 MiB.

For billing changes, prove these crash boundaries: before authorization, after Redis hold,
after durable finalization, after Redis settlement, and after PostgreSQL settlement. Every
state must converge through retry or recovery without a lost charge or double charge.

## Acceptance Gate

A performance change is ready for rollout only when:

- targeted tests, race tests, full tests, and production build pass;
- benchmark allocation and latency deltas are recorded;
- canary error rate and billing reconciliation do not regress;
- CPU and heap profiles are recaptured under comparable load;
- the rollback path has been exercised or mechanically verified.
