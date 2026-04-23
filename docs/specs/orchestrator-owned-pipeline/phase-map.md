# Phase Map

This is the quick-entry guide for a fresh session.

If you arrive here and want to implement the orchestrator-owned pipeline, read in this order:

1. `README.md`
2. this file
3. the phase-specific spec docs listed below
4. the matching phase todo in `.pi/todos`

## Phase 1 — Core contracts and artifact schema

### Read these spec docs
- `stage-contracts.md`
  - `Contract shape`
  - `Common invariants`
  - `Stage 1: Plan`
  - `Stage 4: Human review gate`
- `artifact-schema.md`
  - `Root layout`
  - `Canonical files`
  - `Stage folders`
  - `Review handoff`
- `lifecycle.md`
  - `Success path`
  - `What happens at in_review`

### Go packages
- `internal/types/types.go`
- `internal/diagnostics/recorder.go`
- `internal/diagnostics/*_test.go`

### What this phase should produce
- shared stage and review types
- recorder support for stage manifests and result files
- durable summary fields that can reconstruct a run

## Phase 2 — Stage-aware OpenCode runtime

### Read these spec docs
- `stage-contracts.md`
  - `Stage 1: Plan`
  - `Stage 2: Execute`
  - `Stage 3: Verify`
  - `Failure kinds`
- `README.md`
  - overall pipeline overview
- OpenCode docs
  - `docs/documentation/opencode-config.md`
  - `docs/documentation/opencode-server.md`
  - `docs/documentation/opencode-api-experiments.md`

### Go packages
- `internal/config/config.go`
- `internal/agent/opencode.go`
- `internal/agent/events.go`
- `internal/agent/*_test.go`

### What this phase should produce
- stage-aware agent selection
- prompt rendering hooks per stage
- request bodies that carry the selected agent
- isolated stage sessions that can be started, observed, and stopped

## Phase 3 — Orchestrator pipeline state machine

### Read these spec docs
- `lifecycle.md`
  - `Success path`
  - `Failure branches`
  - `What happens at in_review`
- `event-taxonomy.md`
  - `Control-plane events`
  - `Stage events`
  - `Review events`
  - `Recovery and retry events`
- `artifact-schema.md`
  - `Issue summary`
  - `Attempt metadata`
  - `Review handoff`
- `stage-contracts.md`
  - all stage contracts

### Go packages
- `internal/orchestrator/events.go`
- `internal/orchestrator/backoff.go`
- `internal/orchestrator/state.go`
- `internal/orchestrator/orchestrator.go`
- `internal/tracker/local.go`
- `internal/workspace/manager.go`

### What this phase should produce
- explicit plan → execute → verify → review transitions
- stage-scoped retry handling
- `in_review` handoff without deleting evidence
- typed orchestrator events for the TUI and recorder

## Phase 4 — TUI visibility and review queue

### Read these spec docs
- `event-taxonomy.md`
  - `Control-plane events`
  - `Stage events`
  - `Review events`
- `lifecycle.md`
  - `What happens at in_review`
  - `If the human rejects the change`
- `artifact-schema.md`
  - `summary.json`
  - `review handoff`

### Go packages
- `internal/tui/model.go`
- `internal/tui/events.go`
- `internal/tui/table.go`
- `internal/orchestrator/events.go`

### What this phase should produce
- stage-aware running rows
- a review-ready queue
- a retry queue with ETA and failure reason
- a clearer event log for stage transitions

## Phase 5 — Tests and smoke harness

### Read these spec docs
- `test-matrix.md`
- `debugging-playbook.md`
- `artifact-schema.md`
  - `Stage folders`
  - `Review handoff`
- `lifecycle.md`
  - the full lifecycle table

### Go packages
- `internal/agent/*_test.go`
- `internal/orchestrator/*_test.go`
- `internal/diagnostics/*_test.go`
- `internal/tui/*_test.go`
- `cmd/contrabass/*_test.go`

### What this phase should produce
- fake runner coverage for each stage
- artifact assertions for every stage
- retry / timeout / stall regressions
- a small end-to-end smoke path to `in_review` and `done`

## Phase 6 — Docs and migration notes

### Read these spec docs
- `README.md`
- `phase-map.md`
- every file in this folder

### Go / repo areas
- `README.md`
- `docs/context/`
- `docs/specs/orchestrator-owned-pipeline/`

### What this phase should produce
- onboarding notes for fresh sessions
- migration notes from the single-agent flow
- doc links that keep the spec and implementation aligned

## Shortcut rules

- If you are implementing **Phase 1**, start with `stage-contracts.md` and `artifact-schema.md`.
- If you are implementing **Phase 2**, start with `stage-contracts.md` and the OpenCode docs.
- If you are implementing **Phase 3**, start with `lifecycle.md` and `event-taxonomy.md`.
- If you are implementing **Phase 4**, start with `event-taxonomy.md` and `lifecycle.md`.
- If you are implementing **Phase 5**, start with `test-matrix.md`.
- If you are implementing **Phase 6**, start with the whole spec folder and write the migration notes last.
