# AICodex Prompt Audit source reference manifest

- Frozen at: `2026-07-16 20:21:19 CST (+0800)`
- Source repository: `<SOURCE_REPO>`
- Source branch at capture: `yjb`
- Base commit: `7a50378851a80650cb0c086260b23abeb3469e6b`
- Public reference: the tracked patch below records the source changes reviewed during implementation.
- Local source snapshots and workstation metadata are excluded from the public repository.

## Public artifact

| Artifact | Size | SHA-256 |
| --- | ---: | --- |
| `aicodex-prompt-audit-tracked.patch` | 124674 bytes | `f751a13cce3f3a73cd60cae3aececcef6e1e76dcec8c551a7a4747f032234d2b` |

The tracked patch contains 38 files with 1306 insertions and 227 deletions. It is a source-review reference for the base commit above; it is not a standalone distribution of the original working tree.

## Historical verification

The source review used a detached worktree populated from the captured source state. That review passed `git diff --check`. The additional local snapshot used at review time is not a dependency of this repository and is not distributed here.

The following commands passed against the restored copy:

```text
cd ai-gateway
go test ./internal/service/promptaudit -count=1
ok github.com/mt21625457/aicodex/internal/service/promptaudit 2.081s

go test ./internal/router ./internal/relay ./internal/gatewayadapter/transport \
  -run 'PromptGuard|PromptAudit|ConcurrencyOrder' -count=1
ok github.com/mt21625457/aicodex/internal/router 1.184s
ok github.com/mt21625457/aicodex/internal/relay 2.201s
ok github.com/mt21625457/aicodex/internal/gatewayadapter/transport 3.233s
```

These are historical source-review results, not commands rerun during repository hygiene cleanup. The target OpenSpec specs remain authoritative if the reference implementation differs from the target architecture.
