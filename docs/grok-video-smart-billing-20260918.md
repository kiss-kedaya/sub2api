# Grok Video Smart-Route Billing Incident

## Evidence

The create handler selected the routed API key, but the deferred video snapshot
did not store its group. Each status/content request authenticated the key again
with its primary group and passed that group into RecordUsage. Both price and
multiplier were wrong; this was not a display-only issue.

A read-only production lookup found usage 192551572: group 43, account 29131,
720p, six seconds, charged USD 0.0756. That account belongs to group 25; the
key's route order starts 43,25. The current group-25 price is USD 0.30/second
with independent video multiplier 1, so the configured charge is USD 1.8.
The difference is USD 1.7244. No balance correction or retroactive deduction
has been made. This one example is not an estimate of total historical losses.

## Fix

- Save the selected billing group separately from the original owner-binding
  group in the server-owned video snapshot. Publish it before returning the task
  ID, with a bounded detached write and one retry.
- Restore the complete group and subscription before checking billing eligibility
  or forwarding status/content. Settlement, media prices, multipliers and usage
  attribution all use that restored key. Never mutate the cached auth key.
- Keep task ownership and claim/durable dedup IDs unchanged. Reordering a key's
  groups does not move an existing task's owner binding or billing group.
- Legacy smart-route jobs can recover only when their bound account has exactly
  one group in the key's routes. Ambiguous/missing billing metadata returns a
  retryable billing error before exposing media or consuming a billing claim.
- Normal scheduling remains snapshot-only. Async task restoration alone may make
  a two-second lookup when its group cache is cold.

## Verification

The unchanged baseline fails the same handler regression: a six-second fixture
charges USD 0.0042 instead of USD 2.4, or USD 0.006 instead of USD 3.6 with an
independent video multiplier. Both status and content paths reproduce the bug.
The regression checks the actual settlement command and usage record, repeated
polls, unchanged auth key, key reorder, legacy jobs, snapshot failure, and
publication before the response body. All upstreams/storage in these tests are
synthetic; production billing verification is recorded separately after rollout.

## Rollout Constraints

Old handlers ignore the new snapshot group and can still claim the wrong charge
while versions overlap. A one-percent canary is only a validation phase, not
proof of global billing correctness. Promote both hosts after the canary and
verify a real administrator-owned smart-route video through the public endpoint.
Retain previous instances and connections with the existing health rollback guard.
This fix changes no database schema, balances, price settings, or resource limits.
