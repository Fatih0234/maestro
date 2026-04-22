# Remote Project Orchestration

## Concept

Contrabass-PI runs from **this directory** (Contrabass-PI) but spawns agents that work on **a remote project**. The agents operate in the remote project's git history, creating worktrees and committing there.

```
┌─────────────────────────────────┐
│  Contrabass-PI (this directory) │  ← Orchestrator lives here
│  ├── Board                      │  ← Issue tracking here
│  ├── Orchestrator               │  ← Manages everything
│  └── Agent runner               │  ← Spawns agents
└─────────────────────────────────┘
                │
                │ orchestrator tells agent:
                │ "work in /Volumes/T7/projects/contrabass-snake"
                ▼
┌─────────────────────────────────┐
│  Contrabass-Snake (remote)      │  ← Agents work here
│  ├── Git history                │  ← Commits go here
│  ├── index.html, script.js      │  ← Source code
│  └── workspaces/CB-1/           │  ← Agent worktrees
└─────────────────────────────────┘
```

## How It Works

### Configuration

In `WORKFLOW.md`:

```yaml
workspace:
  base_dir: /Volumes/T7/projects/contrabass-snake  # ← Remote project
  branch_prefix: contrabass/                        # ← Branch naming
```

### What Happens When You Run

1. **Contrabass-PI polls the board** (its own `.contrabass/board/`)
2. **Claims an issue** from the board
3. **Creates a worktree** in the remote project:
   ```
   /Volumes/T7/projects/contrabass-snake/workspaces/CB-1
   ```
4. **Spawns agent** in that worktree directory
5. **Agent works** on the remote project (sees its files, commits to its git)
6. **Contrabass-PI merges** the agent's commits back to the remote's main branch
7. **Cleans up** the worktree

### Git History Location

| What | Where |
|------|-------|
| Contrabass-PI commits | `contrabass-pi` git history |
| Agent commits | `contrabass-snake` git history |
| Contrabass-PI board | `contrabass-pi/.contrabass/board/` |
| Remote project board | `contrabass-snake/.contrabass/board/` (optional) |

## Current Setup

| Component | Path |
|-----------|------|
| Orchestrator | `/Volumes/T7/projects/contrabass-pi` |
| Remote project | `/Volumes/T7/projects/contrabass-snake` |
| Board (issues) | `/Volumes/T7/projects/contrabass-pi/.contrabass/board/` |

## Running the Orchestrator

```bash
cd /Volumes/T7/projects/contrabass-pi
./contrabass --config WORKFLOW.md --no-tui
```

The orchestrator runs here, but agents do work in `contrabass-snake`.

## Adding Issues

Create issues in **Contrabass-PI's board**:

```bash
cd /Volumes/T7/projects/contrabass-pi

cat > .contrabass/board/issues/CB-1.json << 'EOF'
{
  "id": "CB-1",
  "title": "Add score display",
  "description": "Add a score counter that increments when the snake eats food",
  "state": "todo",
  "labels": ["feature"]
}
EOF
```

The agent will implement this issue **in the snake project**.

## Switching Remote Projects

To work on a different project, just update `WORKFLOW.md`:

```yaml
workspace:
  base_dir: /Volumes/T7/projects/my-other-project
  branch_prefix: contrabass/
```

## Benefits

1. **Clean separation** - Contrabass-PI git history stays clean
2. **Single orchestrator** - Manage multiple projects by changing config
3. **Isolated history** - Each project has its own git history
4. **Real projects** - Work on actual projects, not test clones
