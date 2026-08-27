# Agent Code Review plugin

This plugin lets Codex, Cursor, and Claude Code use their selected model in enforced intent, impact, candidate, blinded critique, and finalize phases. ACR validates evidence and lineage; the active model remains the reviewer.

No separate model provider or API key is required.

## Requirements

Build or install `acr`, ensure it is on `PATH`, and verify it with:

```bash
acr doctor
```

## Codex

Install this directory as a Codex plugin or marketplace entry, enable **Agent Code Review**, and start a new task. Example requests:

```text
@Agent Code Review perform an independent deep review of my current changes
@Agent Code Review perform an independent deep review of this branch against main
@Agent Code Review perform a deep review of src
@Agent Code Review benchmark ACR against a native review on this PR
```

## Cursor

Copy this directory to Cursor's local plugin location. The `.cursor-plugin/plugin.json` manifest loads the bundled `agent-code-review` skill.

## Claude Code

The `claude-code` directory supplies `/agent-code-review:review` and `/agent-code-review:benchmark`, backed by the same provider-free protocols.

When the current task also authored the change, the plugin prepares an immutable packet and produces an `acr review handoff` prompt for a separate reviewer task or host subagent. A task opened solely for review is already independent. All integrations remain read-only during review. Fixes require a separate explicit request.

Deep reviews automatically loop phase tasks. Critique uses a fresh same-model context when the host supports it; visible same-context fallback preserves compatibility. `--caveman` invokes the installed Caveman skill or a labeled compact fallback without reducing evidence requirements.

The plugin uses `acr review submit --render`, so successful validation immediately emits the complete report with one copyable, narrowly scoped repair prompt per finding. A combined prompt remains available through `--fix-prompt combined`.

The benchmark skill applies the same communication policy to both arms, uses fresh reviewer contexts and blinded judges, and reports exact host token usage when exposed. Its final submission emits the comparison report automatically.
