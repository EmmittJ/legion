---
name: routing
description: >
  Maps thematic agent names to functional roles. Team roster and routing rules for Legion.
  Applied by Mephisto at session start. Edit this file to change who is on the team,
  what they do, and how work gets routed.
license: MIT
metadata:
  version: "0.3"
---

## Team

| Agent    | Role                     | File                | Use for                                                                        |
| -------- | ------------------------ | ------------------- | ------------------------------------------------------------------------------ |
| Mephisto | Orchestrator             | `mephisto.agent.md` | Default — all requests start here                                              |
| Andariel | Architect/Design Lead    | `andariel.agent.md` | Architecture, acceptance criteria, component contracts, peer review            |
| Diablo   | Archon Binary Engineer   | `diablo.agent.md`   | Archon Go binary: pulse/watcher loops, Docker spawning                         |
| Baal     | Vessel Driver Engineer   | `baal.agent.md`     | Vessel-driver binary: ACP client, git ops, container entrypoint                |
| Azmodan  | Platform/DevOps Engineer | `azmodan.agent.md`  | Docker Compose, Dockerfiles, vessel image hierarchy                            |
| Belial   | Operator CLI Engineer    | `belial.agent.md`   | `lg` CLI binary: invoke, status, log subcommands                               |
| Duriel   | Scribe                   | `duriel.agent.md`   | Commits, branches, pull requests                                               |

---

## Routing Rules

| Pattern                                                           | Role                                                                                          |
| ----------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| architecture, design, spec, acceptance criteria, contracts, ADR   | Architect/Design Lead (agent: Andariel)                                                       |
| archon, pulse loop, watcher loop, docker spawn, controller        | Archon Binary Engineer (agent: Diablo)                                                        |
| vessel driver, ACP, JSON-RPC, stdio, acp client, copilot session  | Vessel Driver Engineer (agent: Baal)                                                          |
| docker compose, dockerfile, container, image, vessel image, infra | Platform/DevOps Engineer (agent: Azmodan)                                                     |
| lg, lg invoke, lg status, lg log, operator CLI                    | Operator CLI Engineer (agent: Belial)                                                         |
| test, build validation, go test, vet, coverage                    | Each specialist owns their own domain; peer reviewer validates during review phase            |
| peer review, review wisp, `review:` wisp                          | Route to any specialist who did NOT author the change; Andariel preferred for cross-domain    |
| commit, PR, branch, push, git, merge                              | Scribe (agent: Duriel)                                                                        |

---

## SDLC Phases

Map your agents to each phase in the Team table. The orchestrator routes work through these stages.

| Phase         | Responsibility                                                      | Who            | When                                                |
| ------------- | ------------------------------------------------------------------- | -------------- | --------------------------------------------------- |
| **Design**    | Requirements, acceptance criteria, architecture, trade-off analysis | Andariel       | New features, ambiguous requests, significant scope |
| **Implement** | Code, files, configuration, scripts, artifacts                      | Diablo/Baal/Azmodan/Belial | Spec is clear; design phase complete or skipped |
| **Verify**    | Build, vet, tests — each specialist validates their own domain      | Implementing specialist (self) | After every implementation; never skipped |
| **Review**    | Peer validation of output against acceptance criteria               | Non-author specialist | Behavioral changes, shared-contract impact   |
| **Integrate** | Commit, branch, pull request, version history                       | Duriel         | Verify passes; review passes or is skipped          |

## Default Flow

**Full path** — new features, behavioral changes, shared-contract impact:

```
Mephisto (route, brief)
  ↓
Andariel (Design — architecture, acceptance criteria, component contracts)
  ↓
Diablo / Baal / Azmodan / Belial (Implement — routed by domain; each self-tests)
  ↓
Non-author specialist (Verify + Review — any of the 4 can review any domain; Andariel preferred for cross-domain)
  ↓
Duriel (Integrate — commit, branch, push)
```

**Fast path** — bounded tasks, no shared-contract impact:

```
Mephisto (brief) → specialist (Implement + self-verify) → Duriel (Integrate)
```

> Peer review: route the `review:` wisp to any specialist who did NOT author the change. Andariel is the preferred reviewer for cross-domain or architectural impact.

See the Maker-Checker skip criteria in the `orchestrate` skill to determine which path applies.

---

## Installed Skills

| Order | Skill directory | Session-start action                                                       |
| ----- | --------------- | -------------------------------------------------------------------------- |
| 1     | `work-cycle/`   | `session:start` — orient: context, inbox, ready work, claim task           |
| 2     | `beads/`        | `context:read` — load context, decisions, insights, per-agent notes        |
| 3     | `beads/`        | `issue:ready` — surface actionable work                                    |
| 4     | `beads/`        | `message:read` — check waiting messages from other agents                  |

## Reference Skills (applied on demand, not at session start)

| Skill                    | Who uses it                         | When to apply                                   |
| ------------------------ | ----------------------------------- | ----------------------------------------------- |
| `work-cycle/`            | All agents                          | Session start/end discipline, issue lifecycle   |
| `go-best-practices/`     | Diablo, Baal, Belial, Andariel      | Any Go implementation or peer review task       |
| `acp-protocol/`          | Baal, Andariel                      | Writing or reviewing ACP client / vessel-driver |
| `docker-best-practices/` | Azmodan, Andariel                   | Any Dockerfile or docker-compose change         |
| `conventional-commits/`  | Duriel                              | Every commit and PR                             |
| `git-best-practices/`    | Diablo, Baal, Duriel                | Branch ops, conflict resolution, push decisions |
| `github-actions/`        | Azmodan, Andariel                   | Writing or reviewing `.github/workflows/` files |

---

## Model Tiers

Used by Mephisto when spawning tasks via the Copilot CLI.

| Tier     | Model               | Use for                             |
| -------- | ------------------- | ----------------------------------- |
| Fast     | `claude-haiku-4.5`  | Research, exploration, narrow tasks |
| Standard | `claude-sonnet-4.5` | Typical implementation and review   |
| Premium  | `claude-opus-4.5`   | Architecture, high-stakes reasoning |
