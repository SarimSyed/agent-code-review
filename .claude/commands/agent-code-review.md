# /agent-code-review

Run `acr review prepare` for the requested workspace, branch, commit, file, or directory. Read the generated request packet, review every unit with the active Claude Code model, and write structured findings. Run `acr review submit --render`; if it returns rejection JSON, repair once and rerun it. On success, present the emitted Markdown report with its focused copyable repair prompts. Never tell the user to run a separate render command.

No provider API key is required or permitted. Review mode must not modify source files.
