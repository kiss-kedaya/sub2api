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

## Release And Deployment Evidence

- PR 49 was merged as `ff7dd3bee30b52531de33e89b6d5de453649b75b` and
  released as `v0.1.331`. PR/push CI 35362070082/35362065488, main CI
  35365332716, tag CI 35365406638, main/tag security 35365332701/35365406601,
  and release 35365406949 all succeeded. The duplicate push release was
  cancelled by existing workflow concurrency; the create release succeeded.
- Local handler/service unit suites, focused race regressions, lint, and both
  Linux hosts' 32 rollback simulations passed before publishing. The release
  CI also ran integration tests and Codex ticket concurrency regressions.
- Both hosts downloaded the immutable GitHub Linux archive and verified SHA256
  `e28570eaa0d1a982ceb271a53728355d3265143096bacac1b261140da64c1867`.
  Installed binary SHA256 on both:
  `edd62051bb918432a582504f69c85cf46b0c1c7319b1f7b35525fe7cf707a7b6`.
  Frontend artifact came from the same release run; its archive SHA256 is
  `e6e401d60aba906f6fe8cef2c8ffa1252bbc593d41b19012c2ca4c1a72d2ebe6`.
- Candidate HTTP is 8105; pprof is loopback 6083. On old/new hosts,
  331 PIDs are 385329/2354947. Retained 330 PIDs are 4170228/2322406.
  All remained active with zero restarts. Physical old/new weights stay 40/60.
- Configuration backups are `/root/sub2api-install331-20260918T161140Z`
  and `/root/sub2api-install331-20260918T161149Z`. Only the old host needed
  6083/8105 appended to existing reserved ephemeral ports; backup is
  `/root/sub2api-331-ports-20260918T161139Z`. Migration mode remains
  `validate`; Grok response-header timeout remains 600 seconds.
- Both candidates were started before guard initialization, because guard
  health checks deliberately probe both hosts. No traffic changed during
  installation. One-percent canary began at 16:16:23/16:16:29 UTC;
  full traffic began at 16:23:55/16:24:09 UTC on 2026-09-18.
  Promotion required at least 180 seconds, twelve healthy checks and fifty
  successful candidate business requests at each ingress.
- Guards reached verified/full/armed at 16:31:43/16:31:47 UTC. Final checks
  at 16:33:58/16:34:02 observed 604/593 seconds of full traffic and 51/54
  successful health checks. Candidate RSS was about 925/1254 MiB; available
  memory exceeded 35 GiB on both hosts. This is a short rollout observation,
  not a claim that every historical memory issue has been resolved.
- Public `/health` and `/readyz` passed. Both private candidates and the
  public domain returned version 0.1.331 and successful authenticated users,
  accounts, groups and dashboard-snapshot responses. The last public snapshot
  check took 175 ms. No administrative data mutation was used for these probes.
- Added/verified 190 frontend assets on each host and retained old assets.
  New-host static index switched atomically; both origin entrypoints and all
  six entry scripts/styles match the release artifact. Public visual checks
  through GUI and local Playwright timed out, so this run does not establish
  browser-rendered acceptance. Server-side asset/API checks succeeded.

## Live Billing Acceptance Is Still Blocked

At 16:12:57 UTC, the administrator-owned smart-route key 31896 returned
`No eligible Grok media accounts` before contacting an upstream. A read-only
lookup showed active account 29131 no longer advertises any video model;
accounts 29154/29155 advertise video 1.5 but are paused. Other active accounts
in group 25 do not advertise video. These production settings were left intact.

No new video task was created and no successful live-video settlement can be
claimed. Permission to temporarily restore account 29131's video model for a
small acceptance test was requested but had not arrived at this checkpoint.
The prepared test uses an administrator-owned key, six-second 720p output,
cross-host status polling, exact group-25 pricing and one durable usage record.
This acceptance step remains outstanding; the deployed code and regression
tests must not be described as proof of an observed live deduction.

## Remaining Errors And Operational Lessons

- Business traffic is not error-free. Group 9 continues to report upstream
  unavailable channels and platform routing capacity errors. At the final
  sample, old/new ingress counted 2994/920 business 503 responses and 26/4
  business 502 responses since full cutover, alongside 18512/8603 HTTP 200s.
  These counts are not all newly introduced platform errors.
- A tiny GPT probe initially showed old=upstream 400, new=routing 503, then
  both=upstream 400 on retry. The old response rejected input below 2000
  tokens, so it did not establish working inference. Repeating with a payload
  meeting that limit produced the same upstream `new_api_error` 503 on both
  330 and 331: no channel for gpt-5.6-sol. A version rollback would not repair
  that demonstrated provider failure. The routing-rate increase remains an
  availability issue to investigate separately, not evidence of zero errors.
- Compare valid equivalent requests when judging a regression. An upstream
  validation 400 proves reachability, not usable model capacity. Distinguish
  process-local cooldowns, provider errors and public health before rollback.
- Production JSON logs use uppercase error levels. Normalize level case in
  audit helpers; otherwise error counts can incorrectly appear empty.
- The watchdog remains armed and checks public/origin health, process identity,
  panic and memory margin. It restores healthy 330 endpoints on those failures;
  unrelated upstream business 502/503 do not independently force a rollback.
  Superseded guard timers were stopped; application processes were not stopped.
- No price, balance, account-model, subscription or database-schema changes
  were made by this rollout. Historical undercharges were not collected.
