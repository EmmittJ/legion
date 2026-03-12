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

| Agent    | Role                     | File                | Use for                                                                   |
| -------- | ------------------------ | ------------------- | ------------------------------------------------------------------------- |
| Mephisto | Orchestrator             | `mephisto.agent.md` | Default — all requests start here                                         |
| Diablo   | Archon Binary Engineer   | `diablo.agent.md`   | Archon Go binary: pulse/watcher loops, Docker spawning                    |
| Baal     | Vessel Driver Engineer   | `baal.agent.md`     | Vessel-driver binary: ACP client, git ops, container entrypoint           |
| Azmodan  | Platform/DevOps Engineer | `azmodan.agent.md`  | Docker Compose, Dockerfiles, vessel image hierarchy                       |
| Belial   | Operator CLI Engineer    | `belial.agent.md`   | `lg` CLI binary (invoke/status/log) + cross-domain peer reviewer          |
| Andariel | QA/Test Engineer         | `andariel.agent.md` | Test harness, build validation, bug hunting; peer reviewer for any domain |
| Duriel   | Scribe                   | `duriel.agent.md`   | Commits, branches, pull requests                                          |

---

## Routing Rules

| Pattern                                                           | Role                                                                                      |
| ----------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| archon, pulse loop, watcher loop, docker spawn, controller        | Archon Binary Engineer (agent: Diablo)                                                    |
| vessel driver, ACP, JSON-RPC, stdio, acp client, copilot session  | Vessel Driver Engineer (agent: Baal)                                                      |
| docker compose, dockerfile, container, image, vessel image, infra | Platform/DevOps Engineer (agent: Azmodan)                                                 |
| lg, lg invoke, lg status, lg log, operator CLI                    | Operator CLI Engineer (agent: Belial)                                                     |
| test, tests, testing, build validation, go test, vet, coverage    | QA/Test Engineer (agent: Andariel)                                                        |
| peer review, review wisp, `review:` wisp                          | Route to builder who did NOT author the change; Belial and Andariel can review any domain |
| commit, PR, branch, push, git, merge                              | Scribe (agent: Duriel)                                                                    |

---

## Default Flow

```
Mephisto → Diablo/Baal/Azmodan/Andariel (parallel where possible)
         → builder creates `review:` wisp in Beads
         → Mephisto routes wisp to peer reviewer (not the author)
         → peer approves (`bd close`) or blocks (`bd update --status=blocked`)
         → Duriel commits
         → Belial only on escalation (architecture, security, breaking changes)
```

---

## Installed Skills

| Order | Skill directory | Session-start action                                                       |
| ----- | --------------- | -------------------------------------------------------------------------- |
| 1     | `beads/`        | `memory:context:read` — load context, decisions, insights, per-agent notes |
| 2     | `beads/`        | `issue:ready` — surface actionable work                                    |
| 3     | `beads/`        | `inbox:message:read` — check waiting messages from other agents            |

## Reference Skills (applied on demand, not at session start)

| Skill                    | Who uses it                    | When to apply                                   |
| ------------------------ | ------------------------------ | ----------------------------------------------- |
| `go-best-practices/`     | Diablo, Baal, Belial, Andariel | Any Go implementation or peer review task       |
| `acp-protocol/`          | Baal, Belial                   | Writing or reviewing ACP client / vessel-driver |
| `docker-best-practices/` | Azmodan                        | Any Dockerfile or docker-compose change         |
| `conventional-commits/`  | Duriel                         | Every commit and PR                             |
| `git-best-practices/`    | Diablo, Baal, Duriel           | Branch ops, conflict resolution, push decisions |
| `github-actions/`        | Azmodan, Andariel              | Writing or reviewing `.github/workflows/` files |

---

## Model Tiers

Used by Mephisto when spawning tasks via the Copilot CLI.

| Tier     | Model               | Use for                             |
| -------- | ------------------- | ----------------------------------- |
| Fast     | `claude-haiku-4.5`  | Research, exploration, narrow tasks |
| Standard | `claude-sonnet-4.5` | Typical implementation and review   |
| Premium  | `claude-opus-4.5`   | Architecture, high-stakes reasoning |
