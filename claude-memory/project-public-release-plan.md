---
name: project-public-release-plan
description: lux-l7 is PUBLIC since 2026-10-14 (same repo, history kept); everything written here is world-readable
metadata:
  type: project
---

Done 2026-10-14 (supersedes the 2026-10-08 "new repo without history" plan): this repository itself is **public** on GitHub, with its full git history. A pre-publication audit (#110) found no secrets, real hosts or IPs; names of the owner's other repositories and the personal e-mail in old commit metadata are accepted as trivial. Issue bodies that named the owner's private client apps (#23, #24) were rewritten generically.

**How to apply:** keep writing code, docs, issues and PR text as public (no private identifiers, no names of the owner's private apps, no raw chat quotes in claude-memory). Check every diff, issue, PR and comment against the CLAUDE.md "Public repository" list before publishing; issue edit history stays public. Do not rewrite history. New commits use the GitHub noreply author address. Related: [[feedback-sensible-defaults]].
