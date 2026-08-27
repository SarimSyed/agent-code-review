---
name: agent-code-review
description: Review workspace changes, branches, commits, files, or directories with the local acr CLI while the active coding agent performs all LLM reasoning. Use when the user asks for an ACR review or a provider-free structured code review.
license: Apache-2.0
metadata:
  author: agent-code-review contributors
  version: "1.3.1"
---

# Agent Code Review

Use `acr` as an evidence-driven, independent code-review workflow. `acr` performs file selection, rule resolution, snapshotting, validation, and rendering; it never supplies or calls a model.

1. Run `acr review prepare --profile deep` for workspace changes, adding `--from/--to`, `--commit`, or `--path` when requested. Use `--profile standard` only for an explicitly requested lightweight review.
2. If this task also authored or fixed the change, run `acr review handoff --session <id>` and use its prompt in a separate reviewer task or host subagent when available. Do not reuse conclusions from the author task. If this task was opened solely to review the change and has no authoring context, it is already independent; review directly rather than creating another task. If the host cannot launch a separate reviewer, disclose that limitation.
3. Run `acr review brief --session <id>`, then `acr review draft --session <id>`. The draft supplies the exact non-overwriting submission form, so do not print the raw packet or inspect older sessions to rediscover its schema. Treat each focused risk question as a required investigation, not as a presumed defect. Inspect every listed file plus the relevant callee/callers and tests. Complete the invariants, dependency-order, API-contract, lifecycle, verification, and critique passes.
4. Fill `question_resolutions` and `findings` in the draft. Resolve every question exactly once as `finding` with a zero-based `finding_index`, or `no_finding`; include concrete evidence either way. Use only categories from the brief and use `bug` for correctness defects. Findings about removals may use the nearest target-side anchor listed by the brief. An empty findings array is valid only when every question is resolved as `no_finding`.
5. Run `acr review submit --session <id> --input <findings_path>`. Repair rejected entries from their error codes and retry at most once.
6. Run `acr review render --session <id> --format markdown --fix-prompt per-finding` and present that readable report with one narrowly scoped repair prompt per validated finding. Use `--fix-prompt combined` only when the user requests one prompt for all findings. Present the report rather than the transport JSON unless the user asks for JSON.

The active host model performs all reasoning. Never configure or request a provider API key. Do not modify source files unless the user separately requests fixes. This workflow improves evidence and independence, but cannot guarantee a model stronger than the host reviewer.
