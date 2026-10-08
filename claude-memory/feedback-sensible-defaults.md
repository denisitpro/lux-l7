---
name: feedback-sensible-defaults
description: Decide trivial defaults yourself (e.g. default port for an IPv6 literal); never ask the owner about them
metadata:
  type: feedback
---

When a question is "should we default X the obvious way" (a port that follows the scheme, a lenient parse of a valid form), decide it and do it; do not put it in the owner's question batch.

**Why:** in the 2026-10-08 audit the orchestrator asked whether a bracketed IPv6 literal without a port should get the scheme's default port. The owner's answer: of course, and asking people about such things is junior behaviour that wastes their time.

**How to apply:** the question batch holds only decisions with real cost (behaviour changes for clients, security defaults, history rewrites). Everything else: pick the standard behaviour, record it in the report. See [[feedback-short-answers]].
