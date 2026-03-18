# Legion Roadmap

> Legion's MVP is proven. The loop closes autonomously. What follows is the path from demo to tool.

---

## Phase 1 — MVP ✅ Complete

Legion's foundation is three binaries and a shared memory layer. `archon` watches for open Beads
issues and spawns Docker vessels to work them. `vessel-driver` bridges the vessel to the Copilot
ACP session, proxying prompts and collecting results. `lg` gives the developer a CLI surface: invoke
a task, watch its status, read its log. Beads ties them together — every state transition, every
branch name, every review verdict is a structured record that any process in the system can read.

The proof was PR #8: a Fibonacci function, implemented by Wraith, reviewed by Inquisitor, merged
by Legion — without a human touching the PR. The loop closes. What the MVP doesn't yet do is close
*reliably*, or tell you why it failed when it doesn't.

---

## Phase 2 — Legion Earns Trust

**Goal:** The developer assigns real issues to Legion before going to sleep. He wakes up to merged
PRs he'd actually ship.

**What this requires:** The loop must be reliable enough to trust and observable enough to debug.
This phase is not glamorous. There are no new capabilities — just the unglamorous infrastructure
that turns a working demo into a tool you can depend on. You cannot delegate to a system you cannot
inspect, and you cannot rely on a system that fails silently.

---

### `lg doctor` — Stack Validation in One Command

Inspired by `flutter doctor` and `brew doctor`: a single command that interrogates the full Legion
stack and tells you what's broken, what's missing, and what to do about it.

**What it checks:**
- Docker daemon reachable; vessel image present and tagged correctly
- `GH_TOKEN` set, scopes sufficient (repo + PR write)
- Dolt installed and `DOLT_DB_PATH` resolves to an initialized database
- Archon binary on PATH or in the expected location
- `bd` CLI present and responsive
- Git identity configured (user.name, user.email) — vessels commit, they need an identity

**Output:** colored pass/fail/warn per check, with an actionable hint for every failure. Not a wall
of JSON — a checklist a developer reads in ten seconds.

**Why it matters:** "Why doesn't this work" currently takes 30 minutes of log-diving. `lg doctor`
makes it a 3-second answer. Before any other Phase 2 work ships, this command exists. It is the
floor that everything else stands on.

---

### `lg init` — Proper Preflight

Right now `lg invoke` assumes the world is already set up. That assumption will be wrong at least
once per new machine and once per new developer. `lg init` makes the first-run experience explicit:

- Runs `bd init` if `.beads/` is absent
- Checks required env vars and fails with named, specific errors (not "exit code 1")
- Validates git remote connectivity before spawning anything
- Composable with `lg doctor` — `lg init` calls doctor internally, or doctor's checks are exposed
  as a library both can use

A developer cloning Legion for the first time should be able to reach a running system by following
a checklist, not by reading source code.

---

### Observability at the CLI

Legion already has Grafana, Loki, and Tempo. That's the power-user dashboard — useful for deep
investigation, not for the moment when you want to know why your PR didn't get created.

The CLI is the first-class interface. The observability goal for Phase 2 is: **every question a
developer would ask during a working session is answerable from `lg`.**

- `lg log <id>` — what did this vessel actually do, step by step? Clone, prompt, response, commit,
  push, PR creation — each step timestamped, each failure quoted verbatim.
- `lg status` — what is Archon currently doing? How many vessels are running? When did the last one
  finish, and did it succeed?
- `lg doctor` — is the stack healthy right now?

Grafana remains available for trend analysis and correlation across runs. But "why did this specific
task fail" should never require opening a browser.

---

### Reliability Hardening

These are not features. They are the floor that trust is built on. Each one is a known gap where
the current loop fails silently or produces a corrupt state:

- **`result.json` not written on success** — the worker `post-commit.sh` only writes `result.json`
  on failure paths. Archon's watcher loop cannot tell "succeeded and wrote nothing" from "hasn't
  finished yet." Fix: write `result.json` on every exit, success or failure.

- **Pre-run failure leaves orphaned state** — if the vessel fails before it does any meaningful
  work (bad image pull, missing env var), the post-run cleanup hook never fires. The Beads issue
  stays `in-progress` forever. Fix: hook into vessel exit regardless of exit code.

- **Hardcoded `--base main`** — PR creation assumes the default branch is `main`. This is wrong on
  any repo where it's `master`, or a release branch. Fix: read default branch from the remote at
  PR creation time.

- **`createReviewBead` captures wrong branch name** — the review bead is written with the branch
  name that was current at bead-creation time, which is not always the vessel's working branch.
  Inquisitor reads this to know what to review. Fix: write the branch name from the Beads issue,
  not from the local git state.

None of these require new concepts. All of them are required before the loop can be trusted with
real work.

---

### CI Green

Legion's own PRs are the first place the recursive property is tested. If CI is broken, Inquisitor
cannot approve its own work with confidence — and neither can the developer.

Phase 2 CI requirements:
- `go test ./...` passes on every push
- `go vet ./...` passes on every push
- Vessel image builds successfully on merge to main and is pushed to the registry
- Workflow failures block merge

This is the automated quality gate that makes Legion's own PRs trustable without reading every
line. Until CI is green and enforced, "the tests pass" is a claim, not a fact.

---

### QUICKSTART + `.env.example`

A new session — new machine, new developer, or just returning after a month — should reach a
running system by following a single document. Not a narrative. A checklist.

```
clone → copy .env.example → lg init → lg doctor → lg invoke "hello world" → watch it work
```

`.env.example` lists every required env var with a one-line description of where to get it.
`QUICKSTART.md` is the checklist — each step is either a command or a decision, and the expected
output of each command is stated so the developer knows when to proceed and when something is wrong.

The test for this document: hand it to someone who has never seen Legion and see if they reach a
running system without asking a question.

---

## Phase 3 — Legion Plans

**Goal:** The developer describes an outcome, not a task. Legion decomposes it, implements it, tests
it, and merges it.

Phase 2 proves Legion can execute a well-scoped task reliably. Phase 3 proves Legion can figure out
what the tasks are.

---

### Hierophant — The Planner

Wraith is a capable implementer. What Wraith cannot do is look at "refactor the auth layer" and
decide what sequence of changes that requires. That's the planner's job.

Hierophant is a vessel that takes a high-level goal — a GitHub issue, a prose description, or a
reference to a module — and produces a set of Beads issues that Archon can dispatch. It thinks
about dependencies: which tasks block which, which can run in parallel, what definition of done
looks like for each.

Without Hierophant, Legion is only useful for tasks the developer has already broken down himself.
With it, the developer can describe an outcome and Legion produces the plan. The issues in `.beads/`
stop being a to-do list the developer maintains and become a plan Legion owns.

---

### Gauntlet — The CI Vessel

Inquisitor reviews code. It does not run code. Currently, "APPROVE" from Inquisitor means "the code
looks correct to me." It does not mean "the tests pass."

Gauntlet is a vessel that runs the test suite against the PR branch before Inquisitor is invoked.
Its verdict — pass or fail, with the test output — is written to the Beads issue as a structured
record. Inquisitor reads that record as part of its review context.

When Gauntlet exists, Inquisitor's APPROVE means: "tests pass AND code is correct." That is a
meaningfully stronger guarantee.

---

### Formulas and Convoys

Right now, the lifecycle of a task — plan → implement → test → review — is assembled ad hoc from
individual Beads issues and vessel invocations. That works for one workflow. It does not scale to
many workflows, and it makes the common case no easier than the uncommon case.

Formulas are named, reusable workflow templates. "Implement and review" is a formula. "Plan,
implement, test, and review" is a formula. A Convoy is an instance of a formula in flight — a set
of coordinated vessels executing a named workflow against a specific goal.

Formulas make Legion's behavior inspectable and repeatable. They also make it possible for Legion
to acquire new workflows without changes to the core binaries — a Formula is data, not code.

---

## What's Explicitly Out (For Now)

These are decisions, not deferrals. They are recorded here so the team doesn't re-litigate them.

| Topic | Decision | Rationale |
|---|---|---|
| Kubernetes | Out | Docker Compose proves the loop. Orchestration complexity is not the constraint. |
| Wasteland / multi-rig federation | Out | No inter-rig work until single-rig is proven reliable. |
| Multiple ACP backends | Out | Copilot works. Swappable later, when there's a reason to swap. |
| Preflight validation beyond `lg doctor` | Out | Trust the image until it breaks. Don't validate what you're not going to fix. |
| UI / web dashboard | Out | The CLI is the interface. Grafana covers the power-user case. |

---

## The Recursive Property

Legion is being built by Legion. The issues in the backlog are not a to-do list for the developer —
they are a dispatch queue.

Phase 2 is complete when the developer can run `lg invoke` on any Phase 2 issue listed above and
trust the result enough to merge it without reading every line. The test is not whether the code
compiles. The test is whether the developer would ship it.

That bar — "I'd ship this" — is the only bar that matters.
