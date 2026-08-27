# Agent Code Review instructions

This repository builds `acr`, a provider-free code review CLI. The active coding-agent model performs all review reasoning; `acr` only prepares deterministic packets, validates findings, manages local sessions, and renders results. Never request or configure a separate model API key for an ACR review.

## Running a review

1. Run `acr review prepare` for workspace changes. Use `--from/--to`, `--commit`, or `--path` when the user names another target.
2. For deep protocol-2 sessions, loop `acr review phase next` and `acr review phase submit` through intent, impact, candidates, critique, and finalize. Use a fresh same-model critic context when available; label the same-context fallback honestly.
3. Run `acr review draft`, preserve finalized candidate dispositions, add their findings and question resolutions, then run `acr review submit --render`.
4. Present successful Markdown directly. Repair rejection JSON once. Never leave rendering for the user.

When requested, `--caveman` uses the installed Caveman skill or the labeled compact fallback. Token economy never reduces evidence or phase coverage. Protocol-1 and standard sessions retain their legacy direct flow.

Review mode is read-only. Do not modify source files unless the user separately asks to fix findings.

## Development

- Preserve Apache-2.0 attribution. Source files (`.go`, `.sh`, `.js`, `.mjs`, `.ts`, `.tsx`) require the repository SPDX header.
- Keep source text in English and LF line endings.
- Format Go changes with `gofmt` and run `go vet ./...`.
- Run `make test`; tests that bind localhost sockets may require a less restrictive sandbox.
- Keep the public `acr` command provider-free. Legacy upstream provider code must not become reachable from the `acr` command tree.
- Add tests for behavior changes, especially protocol validation, path safety, hashes, line anchors, and source-file immutability.
