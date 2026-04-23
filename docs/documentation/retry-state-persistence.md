# Retry State Persistence Note

## Context

The current orchestrator keeps retry/backoff intent in memory. That is fine for a single uninterrupted process, but it means retry state is not durable across orchestrator restarts.

## Current behavior

- failed runs enqueue a retry in `BackoffManager`
- ready retries are dispatched from the in-memory queue
- review handoff issues remain on the board in `in_review`

## Future consideration

If restart resilience becomes important, we may want to persist retry intent into board state or a small durable runtime record so the orchestrator can reconstruct pending retries after a crash or restart.

Possible directions:

1. store retry metadata in the local board issue record
2. add a small runtime state file beside the board
3. rebuild backoff entries from persisted run summaries on startup

## Notes

This is intentionally **not** part of the human-review handoff refactor.
It is a follow-up hardening idea for later.
