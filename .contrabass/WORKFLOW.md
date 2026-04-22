---
max_concurrency: 2
poll_interval_ms: 5000
max_retry_backoff_ms: 30000
agent_timeout_ms: 120000
stall_timeout_ms: 60000
tracker:
  type: internal
  board_dir: .contrabass/board
  issue_prefix: CB
agent:
  type: opencode
opencode:
  binary_path: opencode serve
  port: 0
  profile: ws
  agent: scribe
workspace:
  base_dir: .
  branch_prefix: opencode/
---

# Task Assignment

## Issue: {{ issue.title }}

**Description:**
{{ issue.description }}

## Your Task
1. Understand the issue above
2. Create a simple implementation
3. Verify your work

---
Labels: {{ issue.labels }}