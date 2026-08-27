---
description: Benchmark native Claude Code review against ACR with isolated tasks and no model API key.
---

Use `acr benchmark prepare` with a canonical dataset and exactly one bounded selector. Pass requested `--caveman` flags; ACR applies them equally to both arms. Loop over `acr benchmark next`, assigning every prompt to a fresh same-model Claude reviewer or blinded judge. The ACR arm completes all five review phases. Submit exact communication backend and host token usage when available; never estimate. Continue until final Markdown appears and present it directly.
