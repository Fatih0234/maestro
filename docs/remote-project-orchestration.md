# Remote Project Orchestration

## Concept

Contrabass-PI runs from **this directory** (Contrabass-PI) but spawns agents that work on **a remote project**. The agents operate in the remote project's git history, creating worktrees and committing there.

```
┌─────────────────────────────────────────────────────────────┐
│  Contrabass-PI (this directory)                             │
│  .contrabass/                                              │
│  ├── orchestrator/                                         │
│  │   ├── WORKFLOW.md          # Active config              │
│  │   └── board/               # Orchestrator's board      │
│  └── projects/                                              │
│      └── contrabass-snake/     # Project: snake game        │
│          ├── WORKFLOW.md       # Project-specific config    │
│          └── board/            # Snake's issues             │
└─────────────────────────────────────────────────────────────┘
                │
                │ orchestrator tells agent:
                │ "work in /Volumes/T7/projects/contrabass-snake"
                ▼
┌─────────────────────────────────────────────────────────────┐
│  Contrabass-Snake (remote)                                  │
│  /Volumes/T7/projects/contrabass-snake                       │
│  ├── Git history              # Agent commits here          │
│  ├── index.html, script.js     # Source code                │
│  └── workspaces/             # Agent worktrees             │
│      └── contrabass/CB-1/     # Created by orchestrator     │
└─────────────────────────────────────────────────────────────┘
```

## Folder Structure

```
.contrabass/
├── orchestrator/
│   └── WORKFLOW.md              # Points to active project
├── projects/
│   └── <project-name>/           # One folder per managed project
│       ├── WORKFLOW.md          # Project's own config
│       └── board/               # Project's issues
│           ├── manifest.json
│           └── issues/
│               └── CB-*.json
└── configs/
    └── ws-minimax.json          # OpenCode profile configs
```

## How It Works

### 1. Configure Which Project to Work On

Edit `.contrabass/orchestrator/WORKFLOW.md`:

```yaml
workspace:
  base_dir: /Volumes/T7/projects/contrabass-snake  # ← Remote project
  branch_prefix: contrabass/
```

The orchestrator's board_dir points to the project's board:
```yaml
tracker:
  board_dir: .contrabass/projects/contrabass-snake/board
```

### 2. Create Issues for That Project

Issues live in the project's board directory:
```
.contrabass/projects/contrabass-snake/board/issues/CB-1.json
```

### 3. Run the Orchestrator

```bash
cd /Volumes/T7/projects/contrabass-pi
./contrabass --config .contrabass/orchestrator/WORKFLOW.md --no-tui
```

### What Happens When You Run

1. **Contrabass-PI polls the project's board** (`.contrabass/projects/<project>/board/`)
2. **Claims an issue** from that board
3. **Creates a worktree** in the remote project:
   ```
   /Volumes/T7/projects/contrabass-snake/workspaces/contrabass/CB-1
   ```
4. **Spawns agent** in that worktree directory
5. **Agent works** on the remote project (sees its files, commits to its git)
6. On success, orchestrator moves the issue to **`in_review`**
7. Worktree is **kept intact** for human inspection
8. Human decides when to merge to main and when to mark the issue done

> **Important:** orchestrator does not auto-merge, auto-cleanup, or auto-close issues after runtime success.

## Git History Location

| What | Where |
|------|-------|
| Contrabass-PI commits | `contrabass-pi` git history |
| Agent commits | `contrabass-snake` git history |
| Contrabass-PI config | `contrabass-pi/.contrabass/orchestrator/WORKFLOW.md` |
| Project issues | `contrabass-pi/.contrabass/projects/<project>/board/` |

## Adding a New Project

### Step 1: Create Project Directory

```bash
cd /Volumes/T7/projects/contrabass-pi
mkdir -p .contrabass/projects/my-new-project/board
```

### Step 2: Create Project's WORKFLOW.md

```bash
cat > .contrabass/projects/my-new-project/WORKFLOW.md << 'EOF'
---
workspace:
  base_dir: /Volumes/T7/projects/my-new-project
  branch_prefix: contrabass/
---

# Task: {{ issue.title }}
{{ issue.description }}
EOF
```

### Step 3: Create Board Manifest

```bash
cat > .contrabass/projects/my-new-project/board/manifest.json << 'EOF'
{
  "schema_version": "2",
  "issue_prefix": "CB",
  "next_issue_number": 1
}
EOF
mkdir -p .contrabass/projects/my-new-project/board/issues
```

### Step 4: Create First Issue

```bash
cat > .contrabass/projects/my-new-project/board/issues/CB-1.json << 'EOF'
{
  "id": "CB-1",
  "title": "First task",
  "description": "Do something awesome",
  "state": "todo",
  "labels": ["feature"]
}
EOF
```

### Step 5: Update Orchestrator Config

Edit `.contrabass/orchestrator/WORKFLOW.md`:

```yaml
tracker:
  board_dir: .contrabass/projects/my-new-project/board
workspace:
  base_dir: /Volumes/T7/projects/my-new-project
```

### Step 6: Run

```bash
./contrabass --config .contrabass/orchestrator/WORKFLOW.md --no-tui
```

## Switching Projects

To work on a different project, update the orchestrator's WORKFLOW.md:

```bash
# Edit to point to different project
cat > .contrabass/orchestrator/WORKFLOW.md << 'EOF'
---
tracker:
  board_dir: .contrabass/projects/other-project/board
workspace:
  base_dir: /Volumes/T7/projects/other-project
---
...
EOF

# Run
./contrabass --config .contrabass/orchestrator/WORKFLOW.md --no-tui
```

## Current Setup

| Component | Path |
|-----------|------|
| Orchestrator | `/Volumes/T7/projects/contrabass-pi` |
| Orchestrator config | `/Volumes/T7/projects/contrabass-pi/.contrabass/orchestrator/WORKFLOW.md` |
| Remote project | `/Volumes/T7/projects/contrabass-snake` |
| Project board | `/Volumes/T7/projects/contrabass-pi/.contrabass/projects/contrabass-snake/board/` |

## Benefits

1. **Organized per project** - Each project has its own config and issues
2. **Clean separation** - Contrabass-PI git history stays clean
3. **Single orchestrator** - Manage multiple projects by changing config
4. **Isolated history** - Each project has its own git history
5. **Easy to switch** - Just update one WORKFLOW.md to work on different project
