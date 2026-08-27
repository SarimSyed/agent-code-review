# Agent Code Review instructions

This repository builds `acr`, a provider-free code review CLI. The active coding-agent model performs all review reasoning; `acr` only prepares deterministic packets, validates findings, manages local sessions, and renders results. Never request or configure a separate model API key for an ACR review.

## Running a review

1. Run `acr review prepare` for workspace changes. Use `--from/--to`, `--commit`, or `--path` when the user names another target.
2. Read the returned `request_path` and inspect every listed review unit and file against its supplied rule.
3. Write findings to the returned `findings_path` using protocol version `1` and the schema in `skills/agent-code-review/SKILL.md`.
4. Run `acr review submit --session <id> --input <findings_path> --render`. Successful validation emits the completed Markdown report with one copyable repair prompt per finding; present that output directly. If the command instead returns rejection JSON, repair it once and rerun the same command.
5. Never stop at a validation summary or tell the user to run a render command. Use `--fix-prompt combined` with `submit --render` only when the user requests one prompt for the complete result.

Review mode is read-only. Do not modify source files unless the user separately asks to fix findings.

## Development

- Preserve Apache-2.0 attribution. Source files (`.go`, `.sh`, `.js`, `.mjs`, `.ts`, `.tsx`) require the repository SPDX header.
- Keep source text in English and LF line endings.
- Format Go changes with `gofmt` and run `go vet ./...`.
- Run `make test`; tests that bind localhost sockets may require a less restrictive sandbox.
- Keep the public `acr` command provider-free. Legacy upstream provider code must not become reachable from the `acr` command tree.
- Add tests for behavior changes, especially protocol validation, path safety, hashes, line anchors, and source-file immutability.
