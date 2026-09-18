# 0.1.330 User Console Completion

## Intended Behavior

Channel status and recharge were still using their prior presentation in 329. This release extends the approved SIGNAL layout to monitoring, recharge, orders, subscriptions, redemption, profile, affiliate and IP allowlist pages. User routes are explicitly opted in; admin pages, authentication and the canvas editor retain their own layouts. Payment amounts, currencies, validation, order creation, provider integrations, channel metrics and polling must retain their existing behavior.

The recharge center can embed a separately hosted payment page. The gateway controls its own navigation, frame and actions; content inside that external frame belongs to the payment application.

## Validation And Rollout

- Runtime backend changes are limited to VERSION; no migration or production data mutation is needed for the design.
- Reuse the tested 329 rollout mechanism with candidate HTTP 8103 and loopback pprof 6081. Immediate fallback is retained 329/8101. During 1% canary, existing 327/8099 backup endpoints remain configured. No application process is stopped by rollout or rollback.
- Both gateways retain physical traffic weights old/new 40/60. Install release assets additively and update the new-host static index atomically after promotion.
- Promotion requires at least 180 seconds, twelve healthy guard checks, a recent check and fifty successful candidate business requests on each ingress. Full traffic observation requires at least 300 seconds and twenty-four healthy checks.
- Pure upstream 502/503 do not trigger rollback. Platform/origin/public health failures, panic and exhausted memory margin do.
- The new-host exact `/site-logo` route added during 329 remains compatible with both candidate and fallback. Verify image decoding as well as JS/CSS responses.

Execution evidence will be added after validation and deployment. This document is not a claim that 330 is already live.
