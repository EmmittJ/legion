# ADR: VesselConfig Struct — Delivery Mechanism and Bool Defaults

**Refs**: lg-zjm (consolidates lg-7sv, lg-nkt)

## Status

Accepted

---

## Context

`VesselConfig` is the complete, validated configuration for a single vessel container run.
Archon assembles it at spawn time; `vessel-driver`, `pre-run.sh`, and hooks consume it.
Two decisions needed explicit documentation so future contributors understand the
shape of the struct and do not re-litigate settled choices.

### The struct (as of this ADR)

```go
type VesselConfig struct {
    // Core identity — all required
    IssueID  string  // bead ID (e.g. "lg-abc")
    RoleName string  // "worker" | "reviewer" | "dispatcher" | "planner"
    RepoURL  string  // git clone URL
    ACPSpec  ACPSpec // transport + backend + model + agent file

    // Agent overlay — optional
    AgentName string // bare agent name; informational

    // Review workflow
    ReviewEnabled        bool // default: false
    MaxRework            int  // default: 3
    DeleteBranchOnMerge  bool // default: true  ← non-zero default; see below
    DeleteBranchOnReject bool // default: false

    // Dispatcher config
    RouterAgent    string // agent filling dispatcher role
    DefaultRole    string // default: "worker"
    MaxDispatch    int    // default: 3
    DispatcherMode string // "keyword" | "llm"; default: "keyword"

    // Reviewer/rework context — required when RoleName == "reviewer"
    ReviewBranch        string
    ReviewWorkIssue     string
    ReviewOriginalIssue string
    ReviewReworkCount   int
}
```

### Decision 1: LEGION_CONFIG_JSON as the sole delivery vehicle

**Options considered:**

| Option | Pro | Con |
|--------|-----|-----|
| **`LEGION_CONFIG_JSON` env var (chosen)** | Atomic; structured; standard `docker run -e` injection; no file lifecycle | Value can be large; visible in `docker inspect` |
| File volume mount | Not visible in process env | Requires volume management; adds container-setup surface |
| Individual env vars | Simple for flat configs | VesselConfig has nested types (ACPSpec); many vars; error-prone assembly |

**Decision:** `LEGION_CONFIG_JSON` is the canonical delivery mechanism.
- Archon serializes `VesselConfig` with `json.Marshal` and injects it as a
  single `-e LEGION_CONFIG_JSON=<json>` argument to `docker run`.
- `vessel-driver` calls `config.Load()` on startup, which reads and validates
  the env var before any other work begins.
- Test override: `LEGION_CONFIG_FILE` (path to a JSON file) is accepted by
  `config.Load()` to avoid env var quoting issues in unit tests.
- Secrets (`GITHUB_TOKEN`, `DOLT_HOST/PORT`, `OTEL_*`) are **not** in
  VesselConfig — they remain as separate env vars and are never logged.

### Decision 2: Plain `bool` fields with `ApplyDefaults()` as the safety net

**The problem:** Go's zero value for `bool` is `false`. For most flags this is
safe (e.g. `ReviewEnabled: false` → reviews off, which is a safe default). But
`DeleteBranchOnMerge` should default to `true` — and `false` (the zero value)
would silently suppress branch cleanup.

**Options considered:**

| Option | Pro | Con |
|--------|-----|-----|
| `*bool` pointer for every flag | Distinguish "not set" from "set to false" | Nil dereference risk everywhere; verbose callers |
| Custom `UnmarshalJSON` + unexported sentinel (chosen for `DeleteBranchOnMerge`) | Handles one non-safe-zero bool cleanly; callers use plain `bool` | More code; pattern must be applied per-field |
| JSON schema with `required` | Enforces presence at parse time | Not idiomatic in Go stdlib |

**Decision:** Use plain `bool` fields. `ApplyDefaults()` is the explicit
safety net for any field whose zero value is not the correct default.

Rules that follow from this decision:

1. **`false` is the safe zero for most bools.** `ReviewEnabled`,
   `DeleteBranchOnReject` — if absent from JSON, `false` is correct.

2. **`DeleteBranchOnMerge` is the exception.** Its correct default is `true`.
   `UnmarshalJSON` uses a pointer + unexported `deleteBranchOnMergeExplicit`
   sentinel to detect whether the field was present in the JSON payload.
   `ApplyDefaults()` then sets it to `true` only if it was absent.

3. **Any new bool field must document its safe-zero assumption.** If `false`
   is NOT safe for a new bool field, the author must:
   - Add pointer-sentinel tracking in `UnmarshalJSON` (following the
     `DeleteBranchOnMerge` pattern), AND
   - Add an explicit default in `ApplyDefaults()`.
   Skipping either step will silently produce wrong behaviour when Archon
   emits a VesselConfig without that field set.

4. **Producer and consumer guard non-safe-zero defaults in different ways —
   both patterns are required for any new non-safe-zero bool field.**

   *Producer (Archon) side:* The primary safety net is `defaultArchonConfig()`
   in `internal/config/archon.go`, which initialises every non-safe-zero field
   before any TOML merge (e.g. `DeleteBranchOnMerge: true` at line 97).
   Archon then assigns fields explicitly from `acfg` after struct construction
   (e.g. `vc.DeleteBranchOnMerge = acfg.Review.DeleteBranchOnMerge`).
   `ApplyDefaults()` is also called on the assembled `VesselConfig` before
   `json.Marshal`, but because Archon immediately overwrites `DeleteBranchOnMerge`
   from `acfg` on the very next line, `ApplyDefaults()` has **zero effect** on
   that field in this path. On the producer side it is belt-and-suspenders, not
   the primary guard.

   *Consumer (vessel-driver) side:* `ApplyDefaults()` **is** the safety net.
   `config.Load()` (`internal/config/vessel.go`) calls it after `json.Unmarshal`
   so that any field absent from the JSON payload receives its correct default
   before `Validate()` runs. The pointer-sentinel in `UnmarshalJSON` makes this
   detection reliable.

   **Consequence for new non-safe-zero bool fields:** The author must satisfy
   BOTH sides:
   - *Producer:* initialise the default in `defaultArchonConfig()` and assign
     the field explicitly from `acfg` in Archon's spawn path.
   - *Consumer:* add pointer-sentinel tracking in `UnmarshalJSON` and an
     explicit case in `ApplyDefaults()` (following the `DeleteBranchOnMerge`
     pattern).

   Do not call `Validate()` on a freshly decoded `VesselConfig` without first
   calling `ApplyDefaults()`.

---

## Decision

Both decisions are accepted as the current design. No changes to Go source are
required at this time. This ADR documents intentional behaviour so contributors
understand:

- Why VesselConfig is delivered as a single JSON blob rather than per-field env vars
- Why adding a new `bool` field with a non-false default requires extra work
- That `LEGION_CONFIG_FILE` exists for testing and should never be set in production

---

## Consequences

**Positive:**
- Single source of truth for per-vessel config; `config.Load()` validates it once
  at startup, failing fast before any git or ACP operations begin.
- VesselConfig fields are type-safe and versioned in source; adding a field adds
  it everywhere (struct → marshal → unmarshal → validate) in one diff.

**Negative / ongoing constraints:**
- Every new `bool` field requires a comment stating whether `false` is the safe
  zero, and non-safe-zero bools require the pointer-sentinel pattern.
- `LEGION_CONFIG_JSON` values appear in `docker inspect` output; do not put
  secrets or tokens in VesselConfig fields.
- `LEGION_CONFIG_FILE` must remain a testing-only escape hatch; production
  deployments must not set it.
