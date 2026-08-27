---
name: agent-code-review
description: Review workspace changes, branches, commits, files, or directories with the local acr CLI while the active coding agent performs all reasoning. Use for ACR reviews or provider-free, phased code review.
license: Apache-2.0
metadata:
  author: agent-code-review contributors
  version: "1.5.0"
---

# Agent Code Review

Use `acr` to enforce evidence-backed review phases. The active host model reasons; `acr` selects code, freezes evidence, controls barriers, validates results, and renders Markdown. Never request a model API key.

## Run the review

1. Verify `acr version`. Prepare the requested target. Deep is the default; use standard only when explicitly requested:

   ```bash
   acr review prepare --profile deep
   acr review prepare --profile deep --from <base> --to <target>
   acr review prepare --profile deep --commit <sha>
   acr review prepare --profile deep --path <file-or-directory>
   ```

   When the user requests token economy, add `--caveman` and optional `--caveman-level lite|full|ultra`. If the `caveman` skill is installed, invoke it at that intensity and record phase communication backend `skill`; otherwise obey the compact prompt and record `compact_fallback`. Compression must not reduce inspection, evidence, or critique.

2. For deep review, loop until `acr review phase status --session <id>` reports `ready`:

   ```bash
   acr review phase next --session <id> --worker <worker> --format json
   # Inspect code and fill the returned phase input file.
   acr review phase submit --session <id> --task <task-id> --input <phase-input>
   ```

   Complete barriers in order: `intent`, `impact`, `candidates`, `critique`, `finalize`. Keep the same host, model label, and opaque context ID for intent, impact, candidates, and finalize. Inspect every unit and answer every deterministic risk question. Candidate IDs must link known invariants/questions, anchor changed lines, and cite exact evidence. Empty candidate sets need explicit coverage.

3. Critique every candidate blind to primary identity and confidence. Use the same host/model in a fresh context ID when the host supports an isolated reviewer task or subagent. Return that task's phase JSON to the orchestrator for submission, then resume the primary context for finalize. If isolation is unavailable, use the primary context with `critic_mode: "same_context"`; never label it independent. Units without candidates use `critic_mode: "not_required"`.

4. After finalize, create the final transport:

   ```bash
   acr review draft --session <id>
   ```

   Preserve its candidate dispositions. Add exactly one finding for each `submit` disposition and none for `drop`. Every finding needs `candidate_id`, unit, safe relative path, changed-line range, allowed severity/category, clear explanation/evidence, and confidence. Resolve each generated question exactly once as `finding` with zero-based `finding_index`, or `no_finding`, with concrete evidence.

5. Validate and render immediately:

   ```bash
   acr review submit --session <id> --input <findings-path> --render
   ```

   Repair machine-readable rejection JSON once. On success, present the emitted Markdown directly; never leave a render command for the user. Per-finding fix prompts are default; use `--fix-prompt combined` only when requested.

Standard and protocol-1 sessions use the legacy brief/draft/submit flow. Review is read-only. Do not modify source unless the user separately asks to fix validated findings.
