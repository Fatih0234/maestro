---
max_concurrency: 1
poll_interval_ms: 3000
max_retry_backoff_ms: 60000
agent_timeout_ms: 300000
stall_timeout_ms: 120000
tracker:
  type: internal
  board_dir: .contrabass/board
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
2. Implement the solution in the project at `/Volumes/T7/projects/contrabass-snake`
3. **IMPORTANT: Commit your changes before finishing**
   - Run: `git add . && git commit -m "feat({{ issue.id }}): {{ issue.title }}"`
4. Verify your work is committed with `git log --oneline -1`
