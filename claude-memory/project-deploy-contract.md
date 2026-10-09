---
name: project-deploy-contract
description: lux-l7 deploy contract — pull image + set env and it works; never require host-side tweaks (sysctl etc.)
metadata:
  node_type: memory
  type: project
  originSessionId: 3abd1edf-fa6f-4077-b7b6-848aecbdb94e
  modified: 2026-10-09T09:11:15.529Z
---

Deploying lux-l7 must be: pull the image, set env vars, run (incl. `network_mode: host`, any port such as :443/:449). Any solution that needs host-side changes (e.g. `net.ipv4.ip_unprivileged_port_start` sysctl, root user, extra compose tricks) is rejected outright — don't even propose it as an option.

**Why:** owner (2026-10-09, issue #99): "nobody will fiddle with the host; the whole job is pull the image, give it env, it works". The current high-port setup was a workaround because :443 under host mode failed.

**How to apply:** fixes for runtime limits go into the image/binary itself (e.g. `setcap cap_net_bind_service` in the Dockerfile). Don't use a user's current workaround config as evidence a feature isn't needed. Related: [[feedback-short-answers]], [[feedback-standard-conventions]].
