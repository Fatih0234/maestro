# Test Matrix

This document describes the testing strategy for the orchestrator-owned pipeline.

## Testing goals

1. verify stage contracts quickly
2. keep tests deterministic
3. prefer fake runners over real network calls
4. assert on persisted artifacts, not only logs
5. cover the review gate explicitly

## Test pyramid

### 1. Fast unit tests

These should be pure and fast.

| What to test | Example assertion | Why it matters |
|---|---|---|
| prompt rendering | stage prompt contains issue title and workspace path | catches template regressions |
| stage contract validation | invalid stage config is rejected | avoids bad runtime setup |
| failure classification | errors map to the correct failure kind | ensures retry behavior is correct |
| event mapping | low-level agent events become the right orchestrator events | keeps TUI and recorder consistent |
| artifact serialization | JSON schemas round-trip cleanly | protects the run history format |

### 2. Component tests with a fake runner

Use a scripted fake OpenCode runner that emits known events and responses.

| Scenario | Expected outcome | What it proves |
|---|---|---|
| plan → execute → verify happy path | issue reaches `in_review` | the pipeline works end to end |
| plan failure | retry is queued with a typed failure | planning is isolated and recoverable |
| execute failure | retry is queued and workspace artifacts remain | code changes are recoverable |
| verify failure | issue is not marked review-ready | verification remains a hard gate |
| stall detection | run is aborted and queued for retry | watch-dog behavior works |
| timeout detection | run is aborted and queued for retry | time budgets are enforced |
| review rejection | issue does not advance to done | human review remains authoritative |

### 3. Filesystem integration tests

Use temp directories and real disk writes.

| Scenario | Expected outcome | What it proves |
|---|---|---|
| run directory creation | `issue.json`, `summary.json`, and attempt files exist | recorder layout is correct |
| stage artifact write order | prompt/result files exist before state update | the run is debuggable after crashes |
| retry creates a new attempt directory | `001`, `002`, ... are preserved | reruns remain auditable |
| review handoff persistence | `review/handoff.md` and `decision.json` are written | the human gate is durable |
| workspace preserved after success | worktree is still present after `in_review` | review can happen manually |

### 4. TUI snapshot tests

These verify visibility, not business logic.

| Screen area | Expected state | What it proves |
|---|---|---|
| session table | stage and issue rows are visible | users can see progress |
| review queue | review-ready issues are visible | human handoff is obvious |
| retry queue | queued retries show ETA and reason | recovery is transparent |
| event log | stage events and agent events render cleanly | debugging is readable |

### 5. End-to-end smoke tests

Keep these rare and valuable.

| Scenario | Expected outcome |
|---|---|
| one issue, one full attempt, real runner | issue reaches `in_review` and artifacts are written |
| one issue, human approval path | issue reaches `done` |
| one issue, rejection path | issue returns to work queue or retry state |

## Minimum must-pass coverage

The pipeline should not be considered stable unless all of these pass:

- plan failure is retried with the right failure kind
- execute failure preserves attempt artifacts
- verify failure blocks review handoff
- review-ready state is visible in the TUI and recorder
- human review can mark the issue done without deleting evidence
- restart can reconstruct the visible state from disk

## Fixture strategy

Use the smallest possible fixtures:

- a temp board directory
- a temp workspace root
- a fake agent runner
- a scripted event stream
- fixed timestamps where possible

## Practical rule

When in doubt, assert on **files and state transitions first**, then on **console output** and **TUI rendering**.

That keeps the feedback loop fast and the tests stable.


