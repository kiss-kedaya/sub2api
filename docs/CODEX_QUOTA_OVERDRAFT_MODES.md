# Codex quota-overdraft modes

Sub2API and `X-Zero-L/cpaproxy` solve the same operational problem—keep an
OAuth account useful after a visible Codex 5h/7d window reaches its limit—but
they are not the same implementation.

## Sub2API mode

`codex_quota_overdraft_enabled` controls the native Sub2API state machine:

- persists 5h/7d snapshots and cycle state in `accounts.extra`;
- runs a bounded, compatibility-aware probe before admitting an exhausted
  account back into scheduling;
- keeps the account eligible only while the reset timestamp is known and
  automatically recovers the temporary pause after reset;
- uses request-scoped, server-generated evidence so a client cannot spoof a
  successful hidden call.

This mode does not change real request bodies and is enabled by default.

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

The deployment environment variables are legacy bootstrap values for databases
that predate these keys; the persisted administrator settings are the normal
runtime controls. Missing keys on an existing installation fall back to the
legacy deployment value, while a fresh database writes the safe defaults
explicitly. Once a key exists, the administrator setting is authoritative.
