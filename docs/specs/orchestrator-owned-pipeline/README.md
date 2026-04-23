# Orchestrator-Owned Pipeline Spec

This folder defines the **minimal Contrabass pipeline** where the orchestrator owns the workflow and OpenCode only provides the runtime for each stage.

If you are starting a fresh session, read this file first, then `phase-map.md`, then the phase-specific spec docs.

The core idea is simple:

- the orchestrator owns **plan → execute → verify → human review**
- each stage has a clear contract
- each stage writes durable artifacts
- the board stays coarse (`todo`, `in_progress`, `retry_queued`, `in_review`, `done`)
- pipeline state stays rich and debuggable

This spec is meant to make iteration fast:

- stage boundaries are explicit
- every decision is persisted
- retries are stage-scoped
- humans review from preserved evidence, not from memory

## Relationship to the repo

This spec is designed around the current minimal Contrabass-PI architecture and the Contrabass reference implementation.

Relevant code and docs:

- `../../internal/orchestrator/`
- `../../internal/agent/`
- `../../internal/diagnostics/`
- `../../internal/tracker/`
- `../../internal/workspace/`
- `../../internal/tui/`
- `../../context/what-contrabass-is.md`
- `../../context/minimal-contrabass.md`
- `../../references/contrabass/docs/team-mode-features.md`
- `../../references/contrabass/docs/local-board.md`
- `../../documentation/opencode-config.md`
- `../../documentation/opencode-server.md`

## Design principles

1. **Thin orchestrator**
   - the orchestrator coordinates
   - it does not hide stage logic inside one giant agent run

2. **Explicit stage boundaries**
   - plan, execute, verify, and review-ready handoff are separate
   - each stage can be debugged independently

3. **Durable artifacts first**
   - the file system is the source of truth for a run
   - logs and summaries outlive the process

4. **Typed failures**
   - failures should tell us whether they are transient, workspace-related, execution-related, or verification-related

5. **Human review is a gate**
   - success means `in_review`, not `done`
   - `done` is a human decision

6. **Fast replay**
   - if a stage fails, rerun that stage without destroying the rest of the attempt history

## Document index

- [`phase-map.md`](./phase-map.md) — fastest way to find the right spec doc for each phase
- [`stage-contracts.md`](./stage-contracts.md) — exact inputs, outputs, and success/failure rules for each stage
- [`artifact-schema.md`](./artifact-schema.md) — durable directory layout and file formats
- [`event-taxonomy.md`](./event-taxonomy.md) — event names and payloads emitted by the orchestrator
- [`test-matrix.md`](./test-matrix.md) — unit, integration, and snapshot coverage strategy
- [`lifecycle.md`](./lifecycle.md) — exact issue lifecycle from `todo` to `in_review` to `done`
- [`debugging-playbook.md`](./debugging-playbook.md) — fast inspection order when a run looks wrong

## High-level lifecycle

```text
todo -> in_progress -> plan -> execute -> verify -> in_review -> done
```

Failures branch into `retry_queued` and keep the issue and attempt history intact.

## What this spec does not do

- it does **not** introduce the full team runtime from the reference project
- it does **not** make hidden subagent delegation the primary orchestration model
- it does **not** auto-close issues after runtime success
- it does **not** delete worktrees or run records automatically


