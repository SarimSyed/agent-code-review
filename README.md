# Agent Code Review (`acr`)

Agent Code Review is a provider-free fork of [Alibaba Open Code Review](https://github.com/alibaba/open-code-review). It keeps the upstream deterministic Git, filtering, rule-resolution, and line-positioning foundations while delegating all model reasoning to the coding agent already active in Codex, Cursor, Claude Code, or another compatible host.

No OpenAI, Anthropic, or other model API key is required by `acr`.

## How it works

```text
acr prepares immutable review units
              ↓
acr detects risky async, contract, and lifecycle changes
              ↓
active agent completes intent and impact barriers
              ↓
active agent proposes candidates; blinded critic challenges them
              ↓
primary agent finalizes survivors
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

# Optional compact agent communication (full is the default level)
acr review prepare --profile deep --caveman --caveman-level full

# Claim and submit every unit through intent, impact, candidates, critique,
# and finalize. Critique uses a fresh same-model context when available.
acr review phase next --session <id> --worker <worker>
acr review phase submit --session <id> --task <task-id> --input <phase-result>
acr review phase status --session <id>

# After every phase is complete, create the final findings transport.
acr review draft --session <id>

# After the active agent writes findings.json, validate and immediately render
# the completed report with one focused repair prompt per finding.
acr review submit --session <id> --input <findings-path> --render

# Optional: emit one prompt containing all validated findings.
acr review submit --session <id> --input <findings-path> --render --fix-prompt combined

# Re-render an existing validated result (per-finding prompts are the default).
acr review render --session <id> --format markdown

# Read the compact agent-safe manifest (legacy/diagnostic flow)
acr review brief --session <id>

# Create a prompt for a separate reviewer task or host subagent
acr review handoff --session <id>
```

Prepared sessions live in the ignored `.acr/sessions/` directory. Review preparation and validation do not modify source files.

Fix prompts are generated deterministically from validated findings and do not call a model. Per-finding mode is the default because it keeps each repair narrowly scoped; combined and `none` modes remain available. A successful `submit --render` returns the complete report in the same command, preventing an agent from leaving rendering as a user follow-up.

Deep protocol-2 sessions persist five barriers. Per-unit tasks use expiring atomic claims and idempotent submissions. Evidence files are frozen when first cited. Candidates must anchor changed lines and retain lineage through critique and final disposition. A critic-rejected candidate cannot survive without an explicit override and additional evidence. Protocol-1 sessions and the standard profile remain compatible with direct submission.

`--caveman` shortens prompts and agent chatter through the installed Caveman skill, or through a labeled compact fallback when unavailable. Paths, symbols, numbers, evidence, review coverage, critic independence, and final user-facing Markdown remain unchanged. ACR never estimates token usage.

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

The benchmark runner compares a neutral review with an ACR deep review performed by the same active host model in separate contexts. Ambiguous semantic matches are decided by two blinded judge contexts, with a third only when they disagree. The CLI never calls a model.

```bash
# Fetch the pinned public corpus and record its revision, license, and checksum
acr benchmark dataset fetch qodo --output qodo-benchmark.json

# Prepare one bounded paired experiment
acr benchmark prepare \
  --dataset qodo-benchmark.json \
  --pr https://github.com/owner/repository/pull/123 \
  --caveman --caveman-level full

# Agent integrations loop over these commands in fresh contexts
acr benchmark next --run <id> --worker <unique-worker>
acr benchmark submit --run <id> --task <task-id> --input <result.json>

# Inspect or re-render an interrupted/completed run
acr benchmark status --run <id>
acr benchmark report --run <id> --format markdown
```

The final submission automatically emits the Markdown comparison report. Runs live under `.acr/benchmarks/runs/`; Git mirrors use the OS user cache. Use `--repo` for an existing one-case checkout or `--checkout-map` for batch overrides.

The original one-result scorer remains available:

```bash
acr benchmark score \
  --dataset /path/to/pr-review-bench.jsonl \
  --pr https://github.com/owner/repository/pull/123 \
  --findings .acr/sessions/<id>/result.json
```

Reports include micro precision, recall, F1, macro F1, paired outcomes, matched/missed/extra findings, setup coverage, communication backend, exact host-reported token totals when available, and a deterministic bootstrap confidence interval when at least ten pairs complete. Caveman policy applies equally to both arms. The compatibility scorer accepts both ACR `result.json` and a minimal `{ "findings": [...] }` document.

## License

Apache-2.0. Copyright and attribution from the upstream Alibaba project are preserved.
