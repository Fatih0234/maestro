# Orchestrator-Owned Pipeline — Phase 1 Findings

Date: 2026-04-23

## Summary

Phase 1 reached a stable checkpoint. The core pipeline contracts, durable artifact schema, and recorder support are now in place, and the full test suite passes.

The work did not follow the original straight-line plan exactly: two compatibility issues surfaced while wiring the new schema into the existing codebase, and both required small follow-up fixes before the new recorder model was stable.

## What was completed

### Shared pipeline contracts

Added explicit shared types for the orchestrator-owned pipeline:

- `Stage` and `StageState`
- `StageFailureKind`
- `ReviewState`
- `ReviewDecisionKind`
- `ReviewFollowUpState`
- `StageManifest`
- `StageResult`
- `ReviewDecision`

These live in `internal/types/pipeline.go`.

### Durable artifact schema support

Expanded the diagnostics recorder so it can write and reconstruct the run tree described by the spec:

- `issue.json`
- `summary.json`
- `attempts/<NNN>/meta.json`
- `attempts/<NNN>/stages/<stage>/manifest.json`
- `attempts/<NNN>/stages/<stage>/prompt.md`
- `attempts/<NNN>/stages/<stage>/response.md`
- `attempts/<NNN>/stages/<stage>/result.json`
- `attempts/<NNN>/stages/<stage>/events.jsonl`
- `attempts/<NNN>/stages/<stage>/stdout.log`
- `attempts/<NNN>/stages/<stage>/stderr.log`
- `attempts/<NNN>/review/handoff.md`
- `attempts/<NNN>/review/notes.md`
- `attempts/<NNN>/review/decision.json`

The recorder now also exposes read helpers so summary files and stage files can be reconstructed from disk after a restart.

### Summary state fields

The summary model now carries the fields needed by the spec:

- current stage
- review state
- reviewed at
- reviewed by
- last error

### Compatibility cleanup

One unexpected issue surfaced in the existing code path: issue snapshots were not guaranteed to serialize the board state in a stable, human-readable form. That would have made `issue.json` less useful for restart and review.

To fix that, `types.IssueState` now marshals to the canonical board strings and can still unmarshal legacy encodings.

### Deterministic dispatch ordering

A second issue surfaced in the orchestrator tests: map iteration order in the mock tracker made issue selection nondeterministic. I added a sort in `dispatchReady` so issue pickup is stable and testable.

## What went wrong / deviations from the original plan

The original phase description focused on core contracts and the artifact schema. In practice, the schema change needed a little more compatibility work than expected:

1. `issue.json` needed stable JSON encoding for `IssueState`.
2. The orchestrator needed deterministic issue ordering to keep behavior stable under tests.
3. Recorder reconstruction needed explicit read helpers, not just write paths.

Those were not blockers, but they did push the work beyond the purely additive schema change that the plan initially suggested.

## Verification

Ran the full suite successfully:

```bash
go test ./...
```

## Current boundary of this phase

What is in place now is the durable contract and recorder foundation.

What is **not** wired yet:

- stage-aware runtime execution in the orchestrator loop
- prompt/response generation per stage from the agent runtime
- phase 2 and phase 3 state-machine wiring

That work belongs to the next phases in the spec.

## Practical outcome

The repository now has a stable foundation for the orchestrator-owned pipeline:

- the contract types are explicit
- the persisted artifacts are structured
- the recorder can replay state from disk
- the storage layout is test-covered

This is enough to continue into stage-aware runtime wiring without revisiting the schema from scratch.
