---
name: routing
description: >
  Maps thematic agent names to functional roles. Team roster and routing rules for Legion.
  Applied by Mephisto at session start. Edit this file to change who is on the team,
  what they do, and how work gets routed.
license: MIT
metadata:
  version: "0.2"
---

## Team

| Agent | Role | File | Use for |
|---|---|---|---|
| Mephisto | Orchestrator | `mephisto.agent.md` | Default — all requests start here |
| Diablo | Archon Binary Engineer | `diablo.agent.md` | Archon Go binary: pulse/watcher loops, Docker spawning |
| Baal | Vessel Driver Engineer | `baal.agent.md` | Vessel-driver binary: ACP client, git ops, container entrypoint |
| Azmodan | Platform/DevOps Engineer | `azmodan.agent.md` | Docker Compose, Dockerfiles, vessel image hierarchy |
| Belial | Code Reviewer | `belial.agent.md` | Code review against acceptance criteria before any merge |
| Andariel | QA/Test Engineer | `andariel.agent.md` | Test harness, build validation, bug hunting |
| Duriel | Scribe | `duriel.agent.md` | Commits, branches, pull requests |

---

## Routing Rules

| Pattern | Role |
|---|---|
| archon, pulse loop, watcher loop, docker spawn, controller | Archon Binary Engineer (agent: Diablo) |
| vessel driver, ACP, JSON-RPC, stdio, acp client, copilot session | Vessel Driver Engineer (agent: Baal) |
| docker compose, dockerfile, container, image, vessel image, infra | Platform/DevOps Engineer (agent: Azmodan) |
| review, correctness, spec check, acceptance criteria, quality gate | Code Reviewer (agent: Belial) |
| test, tests, testing, build validation, go test, vet, coverage | QA/Test Engineer (agent: Andariel) |
| commit, PR, branch, push, git, merge | Scribe (agent: Duriel) |
| lg invoke, lg status, lg log, CLI, lg CLI | Vessel Driver Engineer (agent: Baal) — lg CLI is thin; route to Baal |

---

## Default Flow

```
Mephisto → Diablo/Baal/Azmodan/Andariel (implementation, parallel where possible) → Belial (review) → Duriel (commit)
```

---

## Installed Skills

| Order | Skill directory | Session-start action |
|---|---|---|
| 1 | `beads/` | `memory:context:read` — load context, decisions, insights, per-agent notes |
| 2 | `beads/` | `issue:ready` — surface actionable work |
| 3 | `beads/` | `inbox:message:read` — check waiting messages from other agents |

---

## Model Tiers

Used by Mephisto when spawning tasks via the Copilot CLI.

| Tier | Model | Use for |
|---|---|---|
| Fast | `` | Research, exploration, narrow tasks |
| Standard | `` | Typical implementation and review |
| Premium | `` | Architecture, high-stakes reasoning |
