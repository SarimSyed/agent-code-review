# Agent integration

Agent Code Review delegates all reasoning to the active coding-agent model. The local `acr` process performs no model or telemetry network calls.

## Universal workflow

```bash
acr review prepare
# Repeat through intent, impact, candidates, critique, finalize.
acr review phase next --session <id> --worker <worker>
acr review phase submit --session <id> --task <task> --input <phase-result>
acr review draft --session <id>
acr review submit --session <id> --input <findings_path> --render
```

Successful submission emits the complete Markdown report with one copyable prompt per finding. Present that output immediately; do not ask the user to run a separate render command. Rejected submissions remain JSON so the agent can repair them once. Use `--fix-prompt combined` only when one combined repair prompt is preferred.

Primary phases use one active-model context. Critique uses the same host/model in a fresh context when available, then finalize resumes the primary context. Same-context fallback is allowed but reported. Use `--caveman [--caveman-level lite|full|ultra]` for skill-backed or built-in compact communication without weakening phase requirements.

Use `--from/--to` for branch ranges, `--commit` for one commit, or `--path` for full-file scans. Protocol-1 and standard sessions retain the legacy brief/draft/submit flow. The canonical behavior is defined in [`skills/agent-code-review/SKILL.md`](skills/agent-code-review/SKILL.md).

## MCP

Configure the host to run `acr mcp` over stdio. It exposes prepare, `review_phase_next`, `review_phase_submit`, `review_phase_status`, briefing, drafts, request/unit retrieval, submit/validate, render, handoff, and sessions. MCP prepare accepts `caveman` and `caveman_level`. Set `render: true` on final submission so validated Markdown returns immediately.
