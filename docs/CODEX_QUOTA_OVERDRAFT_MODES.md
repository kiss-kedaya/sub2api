# Codex quota-overdraft modes

Sub2API and `X-Zero-L/cpaproxy` solve the same operational problem—keep an
OAuth account useful after a visible Codex 5h/7d window reaches its limit—but
they are not the same implementation.

## Sub2API mode

`codex_quota_overdraft_enabled` controls the native Sub2API state machine:

- persists 5h/7d snapshots and cycle state in `accounts.extra`;
- runs a bounded, compatibility-aware probe after quota exhaustion is observed
  and uses the persisted probe state to gate subsequent re-admission;
- keeps the account eligible only while the reset timestamp is known and
  automatically recovers the temporary pause after reset;
- uses request-scoped, server-generated evidence so a client cannot spoof a
  successful hidden call.

This mode does not change real request bodies and is enabled by default.

The current production integration covers the HTTP gateway paths. WebSocket
turns do not yet capture the same request-scoped overdraft snapshot, so they
continue to use the ordinary scheduler until a dedicated WS hook is added.

## CPA-compatible injection mode

`codex_quota_overdraft_business_injection_enabled` is a separate, explicit
compatibility switch. When the native mode is also enabled, it permits the
server-generated no-op tool pair used as hidden business evidence.

The full CPAProxy implementation additionally has hidden-dispatch tiers,
per-auth cooldown/penalty state, canonical usage correction, and richer
management statistics. Those parts are not silently claimed here. In
particular, Sub2API currently keeps this switch off by default because an
upstream may count the synthetic pair as input tokens; enabling it without an
exact usage-correction policy can overcharge users or distort upstream cost.

The deployment environment variables are bootstrap values for databases that
predate these keys; the persisted administrator settings are the normal runtime
controls. The detector environment value is also an emergency upper bound:
setting it to `false` disables the detector even when the database switch is
`true`. Missing keys fall back to the deployment value, while migration 231
materializes the compatibility defaults without overwriting an existing choice.
