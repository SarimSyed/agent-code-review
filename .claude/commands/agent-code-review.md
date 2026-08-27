# /agent-code-review

Run `acr review prepare --profile deep` for the requested target. Loop `acr review phase next` and `acr review phase submit` through intent, impact, candidates, critique, and finalize. Keep primary phases in this context. Use a fresh same-model Claude subagent for blinded critique when available; otherwise record `same_context`. Then run `acr review draft` and `acr review submit --render`; repair rejection JSON once and present successful Markdown directly.

When the user requests token economy, pass `--caveman` and optional level, invoke the installed Caveman skill when available, and record the actual backend. Never reduce evidence or phase coverage.

No provider API key is required or permitted. Review mode must not modify source files.
