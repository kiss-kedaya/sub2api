The online regional gateway routing, heartbeat registration, streaming proxy,
country resolver and affinity tests are adapted from ranxi2001/sub2api commit
9620d315f72a7dbf04fa10fca02970e1e410805e under LGPL-3.0.

This integration retains existing online authentication and billing, disables
routing by default, and adds revision CAS, explicit private target trust,
connection-time DNS/IP validation and body-bound anti-replay forwarding.
It does not incorporate offline authorization, offline balance ledgers,
business multipliers, experimentation or model tracing.
