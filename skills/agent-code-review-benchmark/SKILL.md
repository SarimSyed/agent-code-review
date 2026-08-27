---
name: agent-code-review-benchmark
description: Compare a coding agent's native PR review with ACR using the same active model in isolated reviewer and blinded judge contexts. Use when the user asks to benchmark ACR review quality or measure whether ACR finds bugs more accurately.
license: Apache-2.0
metadata:
  author: agent-code-review contributors
  version: "1.4.0"
---

# Agent Code Review Benchmark

Run paired experiments without configuring a model provider. `acr` owns immutable checkouts, task state, matching, and reports; every review and adjudication is performed by the model already active in the host.

1. Verify `acr version`. Obtain a canonical dataset with `acr benchmark dataset fetch qodo --output <manifest>` when the user has not supplied one.
2. Prepare an explicitly bounded run with `acr benchmark prepare --dataset <manifest> --pr <url>`, `--limit <n>`, or explicit `--all`. Use one trial unless the user requests repetitions. Pass `--repo` for a supplied local checkout.
3. Repeatedly claim work with `acr benchmark next --run <id> --worker <unique-worker>`. Give the returned prompt to a fresh reviewer or judge task using the currently selected model. Do not perform two paired tasks in the orchestrator context and do not share conclusions between tasks. If the host cannot create isolated contexts, stop and explain that the comparison would not be valid.
4. Each worker must return the requested protocol-versioned JSON with its actual host/model label and a unique context ID. Reviewer tasks return `findings`; judge tasks return one `judgment` for every requested pair. Never expose dataset ground truth to a reviewer task or reveal experiment labels to a judge.
5. Submit each result with `acr benchmark submit --run <id> --task <task-id> --input <result.json>`. Repair malformed transport once. Continue claiming tasks: ACR creates two blinded judges for ambiguous matches and a third only when they disagree.
6. The final successful submission emits the completed Markdown report automatically. Present that output directly. Use `acr benchmark status` only to diagnose an interrupted run and `acr benchmark report` to re-render an existing result; do not leave either command as work for the user.

Review and judge tasks are read-only. They may inspect or test their disposable checkout but must not modify tracked source. Never request an OpenAI, Anthropic, or other model API key; optional GitHub authentication is only for repository access.
