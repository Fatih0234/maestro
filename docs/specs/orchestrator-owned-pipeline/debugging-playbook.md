# Debugging Playbook

When a run looks wrong, debug it in this order.

## 1. Open `summary.json`

Start with the issue-level summary.

Check:

- current issue state
- current attempt number
- current stage
- last error
- review state
- workspace path
- final commit or start commit

If the summary is already wrong, the problem is usually in the orchestrator transition logic.

## 2. Inspect the latest attempt metadata

Open:

```text
attempts/<NNN>/meta.json
```

Look for:

- `session_id`
- `pid`
- `server_url`
- `current_stage`
- `error`
- `retry_at`
- `workspace_path`

If this is missing or stale, the problem is usually in the recorder or stage handoff.

## 3. Inspect the stage manifest

Open:

```text
attempts/<NNN>/stages/<stage>/manifest.json
```

This should tell you:

- which agent was used
- when the stage started and ended
- where the prompt and response files are
- whether the stage was retryable

If the manifest is missing, the stage was never fully entered.

## 4. Read the prompt and response

Open:

- `prompt.md`
- `response.md`
- `result.json`

This is the fastest way to understand whether the model or the orchestrator is at fault.

## 5. Check the event log

Open:

- `attempts/<NNN>/events.jsonl`
- `_orchestrator/events.jsonl`

Look for:

- stage start/completion events
- tokens or output updates
- timeout or stall events
- review-ready events

If the event log and the artifact files disagree, the bug is in the event wiring.

## 6. Check the workspace

Inspect:

- `git status`
- `git diff`
- `git log`
- `git worktree list`

This tells you whether the issue is in the actual code changes or only in the runner state.

## 7. Rerun only the failed stage

Do not restart the whole issue unless you need to.

Examples:

- plan failed → rerun plan
- execute failed → rerun execute
- verify failed → rerun verify
- review was rejected → reopen from the review decision

This is the fastest way to keep iteration tight.

## Symptom cheat sheet

| Symptom | Likely place to look |
|---|---|
| issue never reaches `in_review` | verify stage artifacts or review handoff |
| workspace changes exist but review is blocked | verification result or diff snapshot |
| repeated retries with no progress | stage manifest and failure kind |
| TUI shows stale state | event log and summary refresh logic |
| human cannot review confidently | review handoff package |

## Rule of thumb

Always prefer the file system over the terminal scrollback.

If the file system tells the story clearly, the run is debuggable.


