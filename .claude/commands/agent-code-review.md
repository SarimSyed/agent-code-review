# /agent-code-review

Run `acr review prepare` for the requested workspace, branch, commit, file, or directory. Read the generated request packet, review every unit with the active Claude Code model, write structured findings, validate them with `acr review submit`, and present `acr review render --format markdown --fix-prompt per-finding` so each validated finding has a focused copyable repair prompt.

No provider API key is required or permitted. Review mode must not modify source files.
