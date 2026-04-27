---
max_concurrency: 1
poll_interval_ms: 3000
max_retry_backoff_ms: 60000
agent_timeout_ms: 300000
stall_timeout_ms: 120000
tracker:
  type: internal
  board_dir: .maestro/projects/maestro-snake/board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 0
  profile: maestro
  agent: build
workspace:
  base_dir: /Volumes/T7/projects/maestro-snake
  branch_prefix: maestro/
---

# Task Assignment

## Issue: {{ issue.title }}

**Description:**
{{ issue.description }}

Labels: {{ issue.labels }}

## Instructions
1. Understand the issue above
2. Implement the solution in this issue worktree (not the parent checkout)
3. Leave your implementation **ready for human review** in this issue worktree
   - Do **not** merge into `main`
   - Do **not** delete or clean up the worktree
   - A commit is optional; if created, keep it scoped to this issue branch
4. Finish with a concise summary of what changed and how to verify it manually
