---
name: agent-code-review-benchmark
description: Compare native review with phased ACR using the same active model in isolated reviewer and blinded judge contexts. Use to benchmark ACR bug-finding precision or token usage.
license: Apache-2.0
metadata:
  author: agent-code-review contributors
  version: "1.5.0"
---

# Agent Code Review Benchmark

Run paired experiments without a provider key. `acr` owns pinned checkouts, isolation metadata, scoring, and reports; the active host model reviews and judges.

1. Verify `acr version`. Fetch Qodo only when no manifest was supplied: `acr benchmark dataset fetch qodo --output <manifest>`.
2. Prepare exactly one bounded selector: `--pr`, `--limit`, or explicit `--all`. Pass `--repo` for a local checkout. When token economy is requested, add `--caveman` and optional `--caveman-level`; ACR applies the same policy to both arms.
3. Loop `acr benchmark next --run <id> --worker <unique-worker>`. Execute each prompt in a fresh context using the same host/model label. The ACR arm itself loops all five `acr review phase` barriers and renders its validated review. Do not share reviewer conclusions or expose ground truth.
4. Submit the exact host/model/context metadata. In Caveman mode include communication backend `skill` when that skill was used, otherwise `compact_fallback`. Include `usage` only when the host exposes exact input/output/total token counts; never estimate.
5. Run `acr benchmark submit --run <id> --task <task-id> --input <result.json>`. Repair malformed transport once. Judges remain blind and fresh; ACR schedules a third only after disagreement.
6. Continue automatically until the final submission emits Markdown, then present it directly. Use status only for interruption recovery.

Tasks are review-only and may not modify tracked source. If fresh contexts are unavailable, stop because the paired benchmark would be invalid. Never request model credentials.
