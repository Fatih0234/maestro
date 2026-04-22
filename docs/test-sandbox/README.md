# Test Sandbox Setup

## Overview

Contrabass-PI uses a dedicated **test sandbox** environment to safely test the orchestrator, agent integration, and git workflow without polluting the main project.

| Property | Value |
|----------|-------|
| Path | `/Volumes/T7/projects/contrabass-pi-test` |
| Git branch | `main` |
| Purpose | Integration testing of orchestrator flow |
| Relationship | Copy of main project, separate git history |

---

## Why a Separate Sandbox?

### Problems Solved

| Problem | Without Sandbox | With Sandbox |
|---------|-----------------|--------------|
| **Git history** | Test commits mixed with real commits | Clean history in sandbox |
| **Board state** | Issues marked done multiple times | Fresh board per test |
| **Binary confusion** | Stale binaries, old code versions | Fresh build, known state |
| **Risk** | Testing could break project | Sandbox is disposable |
| **Worktrees** | Orphaned branches everywhere | Isolated to sandbox |

### The Testing Problem We Had

```
# In the main project, we ran tests and ended up with:
- Polluted git history (test commits, merge artifacts)
- Board with issues marked done/created multiple times
- Orphaned worktrees (workspaces/CB-* directories)
- Confusion about which binary was running
- Stale code mixed with fresh code
```

---

## Sandbox Structure

```
/Volumes/T7/projects/contrabass-pi-test/
├── .contrabass/board/           # Local issue board
│   ├── manifest.json           # Board manifest
│   └── issues/
│       └── CB-1.json           # Test issue
├── .git/                        # Separate git repo
├── cmd/contrabass/             # Main CLI
├── internal/                    # Core packages
│   ├── agent/                   # OpenCode runner
│   ├── config/                  # Config parser
│   ├── orchestrator/            # Main orchestrator
│   ├── tracker/                 # Local board tracker
│   ├── tui/                     # Bubble Tea UI
│   ├── types/                   # Shared types
│   └── workspace/               # Git worktree manager
├── workspaces/                # Test worktrees (created during tests)
├── WORKFLOW.md                 # Test configuration
├── contrabass                  # Built binary
├── go.mod / go.sum             # Go modules
└── README.md / AGENTS.md       # Documentation
```

---

## Setup Process

### 1. Create Directory and Initialize Git

```bash
mkdir -p /Volumes/T7/projects/contrabass-pi-test
cd /Volumes/T7/projects/contrabass-pi-test
git init -b main
```

### 2. Archive Main Project Files

```bash
# From main project:
cd /Volumes/T7/projects/contrabass-pi
git archive HEAD | tar -x -C /Volumes/T7/projects/contrabass-pi-test
```

### 3. Update Module Name

```bash
cd /Volumes/T7/projects/contrabass-pi-test

# Update go.mod module name
sed -i '' 's|contrabass-pi|contrabass-pi-test|g' go.mod

# Update all imports in .go files
find . -name "*.go" -exec sed -i '' 's|contrabass-pi|contrabass-pi-test|g' {} \;

# Tidy and build
go mod tidy
go build -o contrabass ./cmd/contrabass
```

### 4. Configure WORKFLOW.md

```yaml
---
max_concurrency: 1
poll_interval_ms: 3000
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
  profile: contrabass      # Use the profile we configured
  agent: build
workspace:
  base_dir: .
  branch_prefix: opencode/
---

# Task Assignment
## Issue: {{ issue.title }}
{{ issue.description }}
```

### 5. Reset Board and Add Test Issues

```bash
# Clear existing issues
rm -rf .contrabass/board/issues
mkdir -p .contrabass/board/issues

# Create fresh manifest
cat > .contrabass/board/manifest.json << 'EOF'
{
  "schema_version": "2",
  "issue_prefix": "CB",
  "next_issue_number": 1,
  "created_at": "2026-04-22T00:00:00Z",
  "updated_at": "2026-04-22T00:00:00Z"
}
EOF

# Create test issue
cat > .contrabass/board/issues/CB-1.json << 'EOF'
{
  "id": "CB-1",
  "identifier": "CB-1",
  "title": "Create test file",
  "description": "Create hello.txt with content 'hello world'",
  "state": "todo",
  "labels": ["test"],
  "created_at": "2026-04-22T00:00:00Z",
  "updated_at": "2026-04-22T00:00:00Z"
}
EOF
```

### 6. Initial Commit

```bash
git add .
git commit -m "Initial: Contrabass-PI test sandbox"
```

---

## Git History

The sandbox has its own clean git history:

```
bbcbe24 chore: add MergeToMain functionality from main project
0aecfbb chore: setup test sandbox with WORKFLOW.md and CB-1 test issue
17c4524 Initial: Contrabass-PI test sandbox from 2026-04-22
```

### Commit Messages

| Commit | Purpose |
|--------|---------|
| `17c4524` | Initial archive of main project |
| `0aecfbb` | Setup with WORKFLOW.md and test issue |
| `bbcbe24` | Added MergeToMain from main project |

---

## Copying Code from Main Project

When the main project has updates you want in the sandbox:

### Step 1: Export Files

```bash
# In main project:
cd /Volumes/T7/projects/contrabass-pi
git show HEAD:internal/workspace/manager.go > /tmp/manager.go
git show HEAD:internal/orchestrator/orchestrator.go > /tmp/orchestrator.go
git show HEAD:internal/orchestrator/events.go > /tmp/events.go
```

### Step 2: Copy to Sandbox

```bash
cd /Volumes/T7/projects/contrabass-pi-test
cp /tmp/manager.go internal/workspace/manager.go
cp /tmp/orchestrator.go internal/orchestrator/orchestrator.go
cp /tmp/events.go internal/orchestrator/events.go
```

### Step 3: Update Imports

```bash
# Fix module name in imports
find . -name "*.go" -exec sed -i '' 's|contrabass-pi-test-test|contrabass-pi-test|g' {} \;

# Rebuild and commit
go build -o contrabass ./cmd/contrabass
git add .
git commit -m "chore: sync MergeToMain from main project"
```

---

## Running Tests

### Basic Test Run

```bash
cd /Volumes/T7/projects/contrabass-pi-test

# Run headless (logs to stdout)
./contrabass --config WORKFLOW.md --no-tui

# Run with TUI
./contrabass --config WORKFLOW.md

# Dry run (single poll cycle)
./contrabass --config WORKFLOW.md --dry-run
```

### Monitoring During Test

```bash
# Watch worktree creation
watch -n 1 'git worktree list'

# Watch for new files
watch -n 5 'ls -la hello.txt 2>/dev/null || echo "Not created yet"'

# Watch git log
watch -n 5 'git log --oneline -3'
```

### After Test

```bash
# Check results
ls -la hello.txt                    # Did the file get created?
git log --oneline -10               # Is there a merge commit?
git worktree list                   # Is worktree cleaned up?
git branch -a | grep opencode       # Any orphan branches?
```

---

## Resetting the Sandbox

When you want a clean state:

### Option 1: Soft Reset

```bash
cd /Volumes/T7/projects/contrabass-pi-test

# Reset issues to todo state
echo '{"id":"CB-1","state":"todo"}' > .contrabass/board/issues/CB-1.json

# Clean worktrees
git worktree list
git worktree prune

# Remove orphan branches
git branch -D opencode/CB-1
```

### Option 2: Full Reset (Nuclear)

```bash
# Remove everything except .git and docs
cd /Volumes/T7/projects/contrabass-pi-test
rm -rf workspaces .contrabass/board/internal/* cmd internal .pi go.* contraband WORKFLOW.md README.md AGENTS.md test.txt

# Recreate from main project
cd /Volumes/T7/projects/contrabass-pi
git archive HEAD | tar -x -C /Volumes/T7/projects/contrabass-pi-test

# Reconfigure and commit
# ... (repeat setup steps) ...
```

---

## Test Issues

### CB-1: Basic File Creation (Recommended First Test)

```json
{
  "id": "CB-1",
  "title": "Create test file",
  "description": "Create hello.txt in the root directory with the exact content 'hello world' (without quotes).",
  "state": "todo",
  "labels": ["test"]
}
```

**Expected behavior:**
1. Worktree created at `workspaces/CB-1`
2. Agent runs in worktree
3. Agent creates `hello.txt` with content
4. Agent commits changes
5. Branch merged to main
6. `hello.txt` appears in main branch

### CB-2: Verify Agent Behavior

```json
{
  "id": "CB-2",
  "title": "Verify git commands",
  "description": "Run git status, git log, and git branch in the worktree. Report what you see.",
  "state": "todo",
  "labels": ["debug"]
}
```

**Expected behavior:**
- Agent runs git commands
- Shows output in agent output
- Verifies worktree is set up correctly

### CB-3: Test Merge Conflict

```json
{
  "id": "CB-3",
  "title": "Create CHANGELOG.md",
  "description": "Create a CHANGELOG.md file with # Changelog header",
  "state": "todo",
  "labels": ["test"]
}
```

**To create conflict:**
1. First, manually add CHANGELOG.md to main with different content
2. Run CB-3
3. Agent creates CHANGELOG.md with its content
4. Merge should fail with conflict
5. `merge.failed` event should be emitted

---

## Debugging

### Enable Verbose Logging

```bash
cd /Volumes/T7/projects/contrabass-pi-test

# Run with debug logging
RUST_LOG=debug ./contrabass --config WORKFLOW.md --no-tui 2>&1 | tee /tmp/test.log
```

### Check Agent Commands

Look in the agent's worktree for evidence of git commands:

```bash
cd /Volumes/T7/projects/contrabass-pi-test/workspaces/CB-1
git log --oneline -5
git status
ls -la
```

### Common Issues

#### Issue: Agent finishes immediately

**Symptoms:** `agent.finished` within 1-2 seconds

**Likely cause:** Agent didn't receive the task or failed to start

**Debug:** Check the agent logs, verify the worktree exists

#### Issue: merge.failed event

**Symptoms:** `merge.failed` appears in logs

**Likely cause:** Worktree was cleaned up before merge, or no commits were made

**Debug:** Check if agent made commits:
```bash
cd workspaces/CB-1
git log --oneline -1
```

#### Issue: Worktree not cleaned up

**Symptoms:** Orphaned `workspaces/CB-*` directories

**Likely cause:** Cleanup failed or race condition

**Fix:**
```bash
git worktree prune
git branch -D opencode/CB-1
rm -rf workspaces/CB-1
```

---

## Profile Configuration

The sandbox uses the `contrabass` profile from your local config:

```
~/.config/opencode/profiles/contrabass/
├── agents/
│   └── build.md      # Instructions for the agent
├── instructions/
├── ocx.jsonc
└── opencode.jsonc
```

### Key Profile Settings

The `contrabass` profile instructs the agent to:

1. **Work in the git worktree** - Isolated from main
2. **Commit before finishing** - Critical for merge-back
3. **Use descriptive messages** - Include issue ID

See [Profile Configuration](../documentation/ocx-profiles-configuration.md) for details.

---

## Maintenance

### Update Sandbox with Main Project Changes

```bash
# In main project:
cd /Volumes/T7/projects/contrabass-pi

# Export key files
git show HEAD:internal/workspace/manager.go > /tmp/manager.go
git show HEAD:internal/orchestrator/orchestrator.go > /tmp/orchestrator.go
git show HEAD:internal/orchestrator/events.go > /tmp/events.go

# In sandbox:
cd /Volumes/T7/projects/contrabass-pi-test
cp /tmp/*.go internal/*/
find . -name "*.go" -exec sed -i '' 's|contrabass-pi-test-test|contrabass-pi-test|g' {} \;
go build -o contrabass ./cmd/contrabass
git add .
git commit -m "sync: update from main project $(date +%Y-%m-%d)"
```

### Keep Test Issues Current

After tests, reset issues for next run:

```bash
cd /Volumes/T7/projects/contrabass-pi-test

# Update issue state back to todo
jq '.state = "todo" | .updated_at = "'$(date -Iseconds)'"' .contrabass/board/issues/CB-1.json > tmp.json
mv tmp.json .contrabass/board/issues/CB-1.json

# Or delete and recreate
rm .contrabass/board/issues/CB-*.json
```

---

## Related Documentation

| Document | Purpose |
|----------|---------|
| [Minimal Contrabass](./context/minimal-contrabass.md) | Project overview |
| [Phase 3: Orchestrator](./context/phase-3-orchestrator.md) | Orchestrator implementation |
| [Profile Configuration](../documentation/ocx-profiles-configuration.md) | OpenCode profile setup |
| [Contrabass Reference](../references/contrabass/README.md) | Reference implementation |
