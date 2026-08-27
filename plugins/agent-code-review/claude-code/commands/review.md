---
description: Review code with ACR while the active Claude Code model performs all reasoning; no provider API key is required.
---

Run `acr review prepare --profile deep` with the requested target. Loop `acr review phase next` and `acr review phase submit` through intent, impact, candidates, critique, and finalize. Keep primary phases in one context; use a fresh same-model Claude subagent for blinded critique when available, otherwise label `same_context`. Resolve every risk question and preserve exact evidence. Run `acr review draft`, add one finding per submitted disposition, then `acr review submit --render`. Repair rejection JSON once and present successful Markdown directly.

If token economy is requested, pass `--caveman` and optional level. Use the installed Caveman skill and report backend `skill`, or use the compact fallback and report it; never reduce review depth.

The active Claude Code model is the reviewer. Never configure or request a separate model API key. Do not modify source files unless the user explicitly asks for fixes after the review.
