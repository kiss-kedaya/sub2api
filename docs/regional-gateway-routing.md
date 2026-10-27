# Regional gateways

Regional gateway routing is disabled by default. It routes only synchronous
`/v1/messages`, `/v1/chat/completions`, `/v1/responses`, their bare aliases, and
the Responses WebSocket handshake. Management, asynchronous media tasks and
ordinary downloads remain on their existing paths.

All nodes use the existing online database, Redis, authentication, subscription,
rate and balance controls. The ingress authenticates a key, then proxies before
business concurrency and billing. The destination authenticates again and runs
the ordinary handler. An ingress must not independently settle the proxied call.
There is no offline authorization, offline balance ledger, budget replica or
automatic model mapping.

The primary administrator configures approved node origins and country-to-node
assignments under Regional gateway routing. Changes require existing sensitive
operation verification and a revision check. No secret is accepted or returned
by this API. Setup uses process environment variables:

| Variable | Meaning |
| --- | --- |
| `SUB2API_SERVERLESS_CLUSTER_SECRET` | Same cryptographically random secret of at least 32 bytes on the ingress and approved nodes; keep it in the existing secret store. |
| `SUB2API_SERVERLESS_NODE_ID` | Unique identity for this running gateway process; omit on the ingress. |
| `SUB2API_SERVERLESS_ENDPOINT` | Node origin without credentials, paths, query strings or fragments. |
| `SUB2API_SERVERLESS_REGION` | Optional two-letter uppercase country code identifying the node. |
| `SUB2API_SERVERLESS_TRUSTED_PRIVATE_CIDRS` | Explicit operator-owned private or loopback networks; empty blocks private target connections. |
| `SUB2API_DISABLE_BACKGROUND_JOBS` | Enable on gateway nodes to leave shared scheduled work to the designated primary worker. |

Public destinations require HTTPS. HTTP is accepted only for a private or
loopback IP explicitly covered by the process allowlist. Metadata, link-local,
CGNAT, unspecified and multicast destinations remain forbidden. Every new
connection resolves DNS, rejects an unsafe result set, then dials a vetted literal
address. Redirects are not followed by readiness probes. Requests are never
replayed or transparently failed over after a proxy attempt.

Country lookup uses the fixed GeoJS provider on the server, from the trusted
client IP. Browser country fields and arbitrary request headers are not routing
input. Forwarding proofs bind the destination boot, request method, URI, Host,
authentication, Origin, content type, body digest and client IP. They expire
quickly and require a Redis nonce reservation. Only verified proofs can set the
request-local client IP used by destination security checks.

Key affinity is retained for 24 hours and renewed on use. Disabling new
assignments leaves existing bindings intact. An unavailable bound node returns
an explicit service error instead of silently losing conversation state.
When rolling nodes, assign a fresh process identity and retain old nodes until
existing requests and bindings are drained. Reusing an active identity is
intentionally rejected by signed heartbeat registration.

The proxy flushes streaming data immediately and has no response-header deadline
that could cut off a long-running answer. Client cancellation reaches the node.
The runtime refreshes configuration in a background loop; ordinary requests do
not read the PostgreSQL settings table.

Ported from `ranxi2001/sub2api` commit
`9620d315f72a7dbf04fa10fca02970e1e410805e` under LGPL-3.0, with local safety and
online billing integration changes. Automated tests cover affinity, SSE and
WebSocket transport, cancellation, proof tampering, replay, endpoint trust,
country lookup validation and observer access restrictions.
