---
description: Review code with ACR while the active Claude Code model performs all reasoning; no provider API key is required.
---

Run `acr review prepare --profile deep` with any user-supplied `--from/--to`, `--commit`, or `--path` target. If this task authored or fixed the change, run `acr review handoff --session <id>` and give its output to a separate Claude reviewer subagent when available. A task opened solely to review the change is already independent. Run `acr review brief --session <id>`, then `acr review draft --session <id>`; use that form rather than printing the raw packet. Resolve every focused risk question by inspecting the changed code, relevant contracts, callers, invariants, and tests. Questions are search leads, not presumed defects. Fill each question as `finding` with a zero-based `finding_index`, or `no_finding`, with concrete evidence. Run `acr review submit --render`; repair rejection JSON once and rerun it. On success, present the emitted Markdown report with its narrowly scoped repair prompts. Never stop at validation or tell the user to run a separate render command.

The active Claude Code model is the reviewer. Never configure or request a separate model API key. Do not modify source files unless the user explicitly asks for fixes after the review.
