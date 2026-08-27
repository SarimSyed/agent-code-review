# Agent integration

Agent Code Review delegates all reasoning to the active coding-agent model. The local `acr` process performs no model or telemetry network calls.

## Universal workflow

```bash
acr review prepare
acr review brief --session <id>
acr review draft --session <id>
# Inspect every unit and resolve every focused risk question.
# Write question_resolutions plus findings to the returned findings_path.
acr review submit --session <id> --input <findings_path> --render
```

Successful submission emits the complete Markdown report with one copyable prompt per finding. Present that output immediately; do not ask the user to run a separate render command. Rejected submissions remain JSON so the agent can repair them once. Use `--fix-prompt combined` only when one combined repair prompt is preferred.

Use `--from/--to` for branch ranges, `--commit` for one commit, or `--path` for full-file scans. The canonical finding schema and retry behavior are defined in [`skills/agent-code-review/SKILL.md`](skills/agent-code-review/SKILL.md).

## MCP

Configure the host to run `acr mcp` over stdio. It exposes prepare, compact briefing, submission-draft creation, request/unit retrieval, submit/validate, result/render, and session-list operations. The concrete tool names include `review_prepare`, `review_get_briefing`, `review_create_draft`, `review_get_request`, `review_get_unit`, `review_submit_findings`, `review_validate_findings`, `review_get_result`, `review_render`, `review_handoff`, and `review_session_list`. Set `render: true` on `review_submit_findings` or `review_validate_findings` so successful validation returns the completed Markdown report in that call; per-finding prompts are the default. Use `fix_prompt: "combined"` for one complete repair prompt or `"none"` to omit prompts.
