# c3api Performance Comparison

Reference reviewed: `is7Qin/c3api` at
`56bccc25f9d158b8b25f4df680e500d04c3faf2a` (2026-08-25).

This is an architectural comparison, not a claim that the two projects have
equivalent workloads. c3api describes beta synthetic tests with a fake upstream;
Sub2API production carries a much larger feature surface, Redis coordination,
multi-platform protocol conversion, and a 202 GB historical PostgreSQL dataset.

## Why c3api Can Use Less CPU

- Request rewrites use byte scanning and targeted edits instead of repeatedly
  decoding and re-encoding the complete JSON document.
- SSE relay readers and writers are pooled, limiting per-stream buffer churn.
- Authentication, routing, pricing, and balance admission use refreshed in-memory
  snapshots instead of database reads on every request.
- Usage records are buffered and bulk inserted. Billing consumes a durable
  `usage_logs` cursor and aggregates many rows into each balance update.
- Large event tables are partitioned, and retention can drop old partitions
  instead of deleting millions of rows through normal row churn.
- The implementation has a narrower product and compatibility surface, so each
  request executes fewer policy and protocol branches.

## Decisions For Sub2API

| c3api technique | Decision | Reason |
| --- | --- | --- |
| Raw/single-pass JSON inspection and one final allocation | Adopt independently | Production pprof showed JSON decoding, copies, and GC as top costs; Sub2API differential tests preserve unknown fields and exact numbers. |
| Pooled streaming buffers | Measure, then selectively adopt | Sub2API already pools SSE buffers; changing size without frame-size evidence risks truncation or extra fallback allocations. |
| Durable bulk usage logging and per-user settlement | Adopt with stronger recovery | Removes synchronous `users.balance` hot-row serialization while retaining idempotency, preauthorization, refunds, and crash recovery. |
| In-memory routing/pricing snapshots | Continue using caches | Valuable only with explicit invalidation, versioning, and stale-data behavior. |
| Daily partitions and partition-drop retention | Plan separately | High potential for 55 GB `usage_logs` and 20 GB `ops_error_logs`, but converting a live 202 GB database needs an audited online migration. |
| Global/per-user inflight gates | Reject | The production requirement is to preserve configured concurrency; rejection can trigger downstream retry amplification. |
| Stale balance snapshot plus forced negative settlement | Reject | It does not provide the required real-time preauthorization and debt protection semantics. |
| `GOGC=off` as a default | Reject | c3api documents a workload-specific experiment. Sub2API keeps Go defaults and permits a one-variable canary only after pprof. |
| Direct source copying | Reject | c3api is AGPL-3.0 while this project is LGPL-3.0; ideas are reimplemented from first principles with local tests. |

## Production Evidence Driving Current Work

- A 45-second production CPU profile attributed about 41% of samples to GC drain;
  OpenAI JSON decoding and body growth were major contributors.
- Heap evidence showed multi-gigabyte byte-slice growth under real traffic.
- Database inspection previously found many transactions contending on the same
  user balance row during high-concurrency billing.
- Host sampling retained CPU headroom only intermittently while I/O wait stayed
  low, so this is not explained by disk latency alone.
- `billing_attempt_outbox` is an orphaned 94 GB historical table and must be
  handled by a separate retention audit. It must not be silently dropped by a
  performance migration.

The first optimization sequence is therefore: reduce request-body allocations,
preserve exact billing with durable preauthorization, coalesce PostgreSQL balance
settlements, then recapture pprof under comparable traffic. Partition conversion
and historical table cleanup remain separate maintenance projects.
