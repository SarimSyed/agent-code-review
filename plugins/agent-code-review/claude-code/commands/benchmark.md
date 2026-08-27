---
description: Benchmark native Claude Code review against ACR with isolated tasks and no model API key.
---

Use `acr benchmark prepare` with a user-supplied canonical dataset and an explicit `--pr`, `--limit`, or `--all` selector. Loop over `acr benchmark next`, assigning every returned prompt to a fresh Claude reviewer or blinded judge subagent using the current model. Submit each task's protocol JSON with `acr benchmark submit`. Do not share reviewer conclusions, expose ground truth, modify tracked source, or configure a provider API key. Continue until the final submission emits the Markdown comparison report, then present it directly.
