# 0.1.327 Release Audit

## Scope

- Smart routes validate candidate group authorization and status, use candidate
  model mappings, and bind billing, subscription, reservation, and forwarding
  policy to the selected route. Snapshot-only selection no longer falls back to
  repeated account database scans.
- Gemini bridges honor the configured upstream protocol and preserve exclusive
  normal/cache input usage, partial usage, cancellation, and terminal errors.
- The API-key editor groups available choices by provider, disables empty
  providers, clears stale single-group selections, and preserves ordered
  cross-provider smart routes. Existing quota and permission APIs are unchanged.
- No schema migration, account credentials, user balances, or group rates change.

## Verification

- Smart-route backend fixes passed full GitHub unit/integration, lint, frontend,
  and security checks at 25f896f712ce99745decba6f817e794e1fb8e00d.
- Provider editor: 19 focused Vue/i18n tests, TypeScript and ESLint passed.
- Playwright checked 1280, 375, and 320 pixel viewports with mocked APIs; provider
  filtering, empty categories, stale selection clearing, and layout passed.
- Release commit CI and immutable release artifact checks are required before
  traffic changes. Earlier test results alone do not approve production rollout.

## Rollout Contract

- Confirmed live baseline: the old-host ingress serves 0.1.326-grok600 on 8097
  with 326/8095 as backup. The new-host ingress rolled back to 326/8095 after
  memory headroom dropped below its guard threshold. Both versions remain alive.
  Physical weighting is old host 40%, new host 60%; preserve each ingress's
  actual initial configuration as its rollback target.
- Preserve the current 600-second Grok header timeout and other application
  settings. Candidate startup uses database migration validate mode.
- Back up configuration and units before changes; retain the serving 326-grok600
  processes, their connections, and a tested rollback target throughout rollout.
- Start with 1% candidate traffic, inspect real request/usage errors, then promote
  only after sufficient successful business traffic and healthy probes.
- Keep the health watchdog active through at least five minutes after promotion.
  Upstream business 502/503 alone are not rollback triggers. New platform faults,
  failed public/origin health, process restarts, or exhausted memory margins are.
- This document records the release plan, not a claim of successful deployment.
  Production evidence is retained in each host's versioned rollout directory.
