# Agent Code Review (`acr`)

Agent Code Review is a provider-free fork of [Alibaba Open Code Review](https://github.com/alibaba/open-code-review). It keeps the upstream deterministic Git, filtering, rule-resolution, and line-positioning foundations while delegating all model reasoning to the coding agent already active in Codex, Cursor, Claude Code, or another compatible host.

No OpenAI, Anthropic, or other model API key is required by `acr`.

## How it works

```text
acr prepares immutable review units
              ↓
acr detects risky async, contract, and lifecycle changes
              ↓
active coding agent reviews every unit
              ↓
acr validates paths, snapshots, lines, and schema
              ↓
acr renders the accepted findings
```

## Build

Requires Go 1.25.5 or newer and Git 2.41 or newer.

```bash
make build
./dist/acr version
./dist/acr doctor
```

## Review with the active agent

The normal user experience is to ask the coding agent to run an ACR review. The bundled skill performs this protocol:

```bash
# Workspace changes
acr review prepare --profile deep

# Branch range
acr review prepare --profile deep --from main --to HEAD

# Commit
acr review prepare --profile deep --commit HEAD

# Full-file scan
acr review prepare --profile deep --path src

# After the active agent writes findings.json
acr review submit --session <id> --input <findings-path>

# Recommended: readable report with one focused repair prompt per finding
acr review render --session <id> --format markdown --fix-prompt per-finding

# Optional: one prompt containing all validated findings
acr review render --session <id> --format markdown --fix-prompt combined

# Read the compact agent-safe manifest; do not print the full request packet
acr review brief --session <id>

# Create the non-overwriting question-resolution and findings form
acr review draft --session <id>

# Create a prompt for a separate reviewer task or host subagent
acr review handoff --session <id>
```

Prepared sessions live in the ignored `.acr/sessions/` directory. Review preparation and validation do not modify source files.

Fix prompts are generated deterministically from validated findings and do not call a model. Per-finding mode is recommended because it keeps each repair narrowly scoped; combined mode is available for agents that can safely manage a larger context.

Deep briefs contain focused review questions for risky diff shapes such as sequential work moved into concurrency, removed call arguments, and removed listener or initialization calls. These questions are leads for the active model to verify, not automatic findings. Submission requires an evidence-backed resolution for every generated question, which makes skipped risk areas visible while preserving model judgment.

## Agent integrations

- Canonical portable skill: [`skills/agent-code-review/SKILL.md`](skills/agent-code-review/SKILL.md)
- Codex and Cursor plugin: [`plugins/agent-code-review`](plugins/agent-code-review)
- Claude Code command: [`plugins/agent-code-review/claude-code`](plugins/agent-code-review/claude-code)
- Generic integration and MCP reference: [`AGENT_INTEGRATION.md`](AGENT_INTEGRATION.md)

Run the MCP server over stdio with:

```bash
acr mcp
```

## Validation guarantees

`acr review submit` verifies that findings reference prepared units and files, use supported severities/categories, stay within line ranges, match unchanged content snapshots, and anchor diff comments to changed lines. Invalid findings are returned with machine-readable rejection codes.

## Benchmarking review quality

`acr benchmark score` compares a review result with anchored ground truth from a compatible JSONL dataset, without calling a model or changing source files:

```bash
acr benchmark score \
  --dataset /path/to/pr-review-bench.jsonl \
  --pr https://github.com/owner/repository/pull/123 \
  --findings .acr/sessions/<id>/result.json
```

It reports precision, recall, F1, missed issues, and extra findings. The command accepts both ACR `result.json` files and a minimal `{ "findings": [...] }` document for a direct-agent baseline.

## License

Apache-2.0. Copyright and attribution from the upstream Alibaba project are preserved.
