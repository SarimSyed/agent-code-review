# Agent Code Review plugin

This plugin lets Codex, Cursor, and Claude Code use their currently selected model to perform evidence-driven deep reviews prepared by the local `acr` CLI. ACR amplifies the model with deterministic risk questions for async ordering, API contracts, and lifecycle changes; the active model verifies those leads and remains the reviewer.

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
```

## Cursor

Copy this directory to Cursor's local plugin location. The `.cursor-plugin/plugin.json` manifest loads the bundled `agent-code-review` skill.

## Claude Code

The `claude-code` directory supplies `/agent-code-review:review`, backed by the same prepare–submit protocol.

When the current task also authored the change, the plugin prepares an immutable packet and produces an `acr review handoff` prompt for a separate reviewer task or host subagent. A task opened solely for review is already independent. All integrations remain read-only during review. Fixes require a separate explicit request.

Rendered reports include one copyable, narrowly scoped repair prompt per validated finding by default. A combined prompt remains available through `acr review render --fix-prompt combined`.
