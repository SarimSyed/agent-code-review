---
name: agent-code-review
description: Review workspace changes, branches, commits, files, or directories with the local acr CLI while the active coding agent performs all LLM reasoning. Use when the user asks for an ACR review or a provider-free structured code review.
license: Apache-2.0
metadata:
  author: agent-code-review contributors
  version: "1.4.0"
---

# Agent Code Review

Use `acr` as an evidence-driven, independent code-review workflow. `acr` performs file selection, rule resolution, snapshotting, validation, and rendering; it never supplies or calls a model.

## Workflow

1. Verify `acr` is available with `acr version`. If unavailable, report that the local CLI must be installed; do not request an API key.
2. Prepare the requested target with the default `deep` profile. Use `standard` only when the user explicitly wants a fast, lightweight review:

   ```bash
   acr review prepare --profile deep
   acr review prepare --profile deep --from <base> --to <target>
   acr review prepare --profile deep --commit <sha>
   acr review prepare --profile deep --path <file-or-directory>
   ```

3. If this task also authored or fixed the change, keep it separate from the reviewer. Generate a portable reviewer prompt:

   ```bash
   acr review handoff --session <session_id>
   ```

   Give that prompt to a separate reviewer task or host subagent when available. The reviewer must not rely on conclusions from the author task. If this task was opened solely to review the change and has no authoring context, it is already the independent reviewer; do not create another task. If the host cannot create a separate task, disclose that the review is not independent and still complete all deep-profile passes.
4. Run `acr review brief --session <session_id>` instead of printing the raw `request.json`, then run `acr review draft --session <session_id>` to create the non-overwriting submission form. The brief is the compact checklist of unit IDs, paths, changed/deleted-line anchors, rules, focused risk questions, categories, and passes. A risk question is a search lead, not a presumed defect. Fill the draft by inspecting the changed code, its callee/callers, relevant tests, and nearby invariants. Complete every required pass: invariants, async/dependency order, API contracts, lifecycle/ownership, verification, and false-positive critique.
5. Report only concrete defects. Create the returned `findings_path` as JSON using this shape:

   ```json
   {
     "protocol_version": "1",
     "session_id": "<session_id>",
     "question_resolutions": [
       {
         "question_id": "question-0001",
         "outcome": "finding",
         "evidence": "The callee rejects a missing apiUrl and this boot path always executes.",
         "finding_index": 0
       },
       {
         "question_id": "question-0002",
         "outcome": "no_finding",
         "evidence": "The removed registration is replaced by module initialization at server/start.ts:44."
       }
     ],
     "findings": [
       {
         "unit_id": "unit-0001",
         "file": "relative/path.go",
         "start_line": 12,
         "end_line": 12,
         "severity": "high",
         "category": "bug",
         "explanation": "Concise defect description.",
         "evidence": "Why this code produces the defect.",
         "suggested_fix": "Optional remediation.",
         "confidence": 0.9
       }
     ]
   }
   ```

   Resolve each generated question exactly once. Outcomes are `finding` (with a zero-based `finding_index`) or `no_finding`; both require concrete evidence. Use one of the categories listed in the brief: `bug`, `security`, `performance`, `maintainability`, `test`, `style`, `documentation`, or `other`. Classify correctness defects as `bug`; ACR also normalizes `correctness` to `bug`. Findings about removed behavior may anchor to the nearest target-side line identified by the brief. An empty `findings` array is valid when every question is resolved as `no_finding`.

6. Validate and immediately render the response:

   ```bash
   acr review submit --session <session_id> --input <findings_path> --render
   ```

   Rejected submissions return machine-readable JSON; repair only those entries and rerun the same command once. Successful submission returns the complete Markdown report with one repair prompt beneath each finding by default. Present that command output directly. Never stop at a validation summary, show a render command as the next step, or ask the user to generate the report. Use `--fix-prompt combined` only when the user explicitly prefers one prompt for all findings. Stop and report remaining validation errors after the second failed submission.

Do not configure OpenAI, Anthropic, or another provider. The model active in Codex, Cursor, Claude Code, or the current host is the reviewer. Do not modify source files unless the user separately asks to fix validated findings. This workflow improves review discipline and independence; it does not guarantee a stronger model than the host's reviewer.
