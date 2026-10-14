---
name: feedback-standard-conventions
description: Use standard mechanisms (log levels debug/info/warn/error) instead of inventing new knobs or profiles
metadata:
  node_type: memory
  type: feedback
  originSessionId: 56037643-15bf-4b76-a300-309a050367d4
  modified: 2026-10-06T15:48:20.037Z
---

Use the standard, well-known mechanism instead of inventing a custom one. Example: for per-mode logging behaviour the owner rejected a new `LOG_PROFILE` variable (auto/private/ops) and wants plain `LOG_LEVEL` debug/info/warn/error, with the proxy mode deciding what each level emits.

**Why:** owner (2026-10-06): take the standard log levels (debug/info/warn), do not invent custom ones.

**How to apply:** before proposing a new env variable or setting, check whether an existing standard knob already expresses it; derive behaviour from existing config (mode, level) first. See also [[feedback-short-answers]].
