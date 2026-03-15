---
name: legion
description: >
  Legion task orchestration skill. Activate when: dispatching work via lg invoke,
  checking vessel status, routing tasks to specific agents, reading open issues,
  or monitoring active vessels. Covers: lg CLI commands, bd issue tracking,
  routing label conventions, agent roster, and status interpretation.
license: MIT
metadata:
  version: "0.1"
---

## Overview

Legion is an autonomous task orchestration system. You dispatch work with `lg invoke`,
Legion routes it to the right vessel, vessels execute and push branches.

## Routing Table

| Intent | Labels | Notes |
|---|---|---|
| Implement / fix / build | role:worker | Generic worker vessel |
| Route to named agent | agent:<name> | Archon spawns --agent <name> |
| Plan / decompose | role:hierophant | Vessel outputs sub-issues |
| Review (after branch) | role:inquisitor | Do NOT create manually — vessels create review beads |

## Commands

| Command | What it does |
|---|---|
| `lg invoke "<title>" [--agent <name>] [--model <tier\|name>]` | Create issue, dispatch to vessel |
| `lg status` | List open and in-progress issues |
| `lg log <id> [--follow]` | Print ACP execution traces |
| `lg watch` | Live-refreshing status dashboard |
| `bd list --status open` | Show all open beads |
| `bd show <id>` | Full bead detail |
| `bd ready` | Unblocked work ready to claim |

## Team Roster

| Agent | Role | Specialty |
|---|---|---|
| oracle | Face | Conversational intake, creates beads |
| hermes | Router | Reads bead, emits role: label |
| wraith | Worker | Writes code, pushes branch/PR |
| inquisitor | Reviewer | Diffs branch, merges or creates rework bead |
| hierophant | Planner | Vague intent → dependency graph |

## Model Tiers

| Tier | Model |
|---|---|
| fast | claude-haiku-4.5 |
| standard | claude-sonnet-4.6 |
| premium | claude-opus-4.6 |
