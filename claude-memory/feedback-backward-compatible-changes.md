---
name: feedback-backward-compatible-changes
description: "Never ship breaking changes; add new behavior alongside the old, deprecate, remove later via an issue"
metadata:
  node_type: memory
  type: feedback
  originSessionId: 8a4ecc6a-bf61-4a6c-83fc-044dc4d44367
  modified: 2026-10-06T12:59:12.926Z
---

Changes must not break existing configs or behavior. Add the new behavior next to the old (new field/option, old default kept), mark the old one deprecated (startup warning + docs), and open a GitHub issue to remove it a few versions later. "Pre-alpha, so breaking is fine" is not an excuse.

**Why:** in #43 (2026-10-06, release 0.2.6) the route-mode default outgoing Host was changed from the route host to the upstream host as a breaking change. Owner: breaking on every edit is amateur work; proper engineering keeps the past working and removes the old path later on purpose.

**How to apply:** when a change alters a default, a field meaning, an env var or a response, design an opt-in or a compatible fallback first; put the deprecation and the removal issue into the same PR. Subagent briefs must state this. Related: [[feedback-short-answers]].
