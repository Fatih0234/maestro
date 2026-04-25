---
max_concurrency: 1
poll_interval_ms: 3000
max_retry_backoff_ms: 60000
agent_timeout_ms: 300000
stall_timeout_ms: 120000
tracker:
  type: internal
  board_dir: /Volumes/T7/projects/contrabass-pi/.contrabass/projects/contrabass-snake/board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 0
  profile: contrabass
  agent: build
workspace:
  base_dir: /Volumes/T7/projects/contrabass-snake
  branch_prefix: contrabass/
---

# Task Assignment

## Issue: {{ issue.title }}

**Description:**
{{ issue.description }}

Labels: {{ issue.labels }}

## Instructions
1. Understand the issue above
2. Implement the solution
3. Leave your implementation **ready for human review** in this issue worktree
   - Do **not** merge into `main`
   - Do **not** delete or clean up the worktree
   - A commit is optional; if created, keep it scoped to this issue branch
4. Finish with a concise summary of what changed and how to verify it manually
