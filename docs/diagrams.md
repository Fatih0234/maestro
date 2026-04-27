# Maestro Diagrams

## How It Works — The Pipeline Lifecycle

```mermaid
flowchart TB
    subgraph IN[" "]
        ISSUES["📋 Issues<br/>(file-based board)"]
    end

    subgraph ORCH["Orchestrator Loop"]
        POLL["poll tracker"]
        CLAIM["claim issue"]
    end

    ISSUES -->|"todo"| POLL
    POLL --> CLAIM

    CLAIM --> WORKSPACE["📁 git worktree<br/>(isolated branch)"]

    WORKSPACE --> PLAN
    PLAN -->|"✓"| EXEC
    EXEC -->|"✓"| VERIF

    PLAN -->|"✗"| RETRY
    EXEC -->|"✗"| RETRY
    VERIF -->|"✗"| RETRY

    RETRY["⏳ retry with backoff<br/>resume from failed stage"] -->|"jitter + exponential"| WORKSPACE

    VERIF -->|"✓ all stages pass"| REVIEW["👁️ human review<br/>(worktree preserved)"]

    REVIEW --> APPROVE["✔ approve → merge + done"]
    REVIEW --> REJECT["↩ reject → back to todo"]
    REJECT --> ISSUES
```

**Key points:**

- **Stage-scoped retry.** If `verify` fails, only `verify` is retried — not `plan` or `execute`.
- **Exponential backoff** with ±20% jitter, capped at 4 minutes.
- **No auto-merge.** On success, the issue enters `in_review`. A human inspects the worktree, then approves or rejects.
- **Everything is persisted.** Every stage writes prompt, response, diff, and result to `.maestro/runs/`.

---

## Architecture — How Packages Connect

```mermaid
flowchart LR
    subgraph ENTRY["Entry"]
        CLI["cmd/maestro<br/>CLI + TUI startup"]
    end

    subgraph CORE["Core"]
        direction TB
        ORCH2["orchestrator<br/>poll → claim → dispatch → retry"]
        PIPE["pipeline<br/>one stage at a time"]
        AGENT["agent<br/>OpenCode HTTP + SSE"]
        DIAG["diagnostics<br/>persistent run records"]
    end

    subgraph INFRA["Infrastructure"]
        direction TB
        CFG["config<br/>WORKFLOW.md parser"]
        TRK["tracker<br/>local file-based board"]
        WS2["workspace<br/>git worktree manager"]
    end

    subgraph UI["UI"]
        TUI2["tui<br/>Bubble Tea terminal UI"]
    end

    TYPES["types<br/>shared contracts"]

    CLI --> CFG
    CLI --> ORCH2

    ORCH2 --> TRK
    ORCH2 --> WS2
    ORCH2 --> PIPE
    ORCH2 --> DIAG
    ORCH2 -.->|"events"| TUI2

    PIPE --> AGENT
    PIPE --> WS2
    PIPE --> DIAG

    AGENT --> DIAG

    TRK -.-> TYPES
    WS2 -.-> TYPES
    PIPE -.-> TYPES
    ORCH2 -.-> TYPES
    AGENT -.-> TYPES

    style TYPES stroke-dasharray: 5 5
```

**Dependency rule:** `types` has zero internal dependencies — everything depends on it. Each other package depends only on what's below it in the stack: `config → tracker → workspace → agent → diagnostics → pipeline → orchestrator → tui`.

---

## Data Flow — One Attempt

```mermaid
sequenceDiagram
    participant Orch as Orchestrator
    participant Pipe as Pipeline Runner
    participant WS as Workspace
    participant Agent as OpenCode
    participant Diag as Diagnostics
    participant TUI as Terminal UI

    Orch->>Orch: poll tracker → claim issue

    Orch->>Diag: begin attempt (preflight git state)
    Orch->>TUI: agent.started

    loop for each stage (plan → execute → verify)
        Orch->>Pipe: Run(issue, attempt, stage)
        Pipe->>WS: create git worktree
        Pipe->>Diag: begin stage (manifest + prompt)
        Pipe->>Agent: start + submit prompt
        Agent-->>Pipe: SSE events (tokens, output)
        Pipe->>Diag: append events
        Pipe->>TUI: tokens.updated, agent.output
        Agent-->>Pipe: session complete
        Pipe->>Diag: finish stage (response + result + diff)
        Pipe-->>Orch: Result{Success, Error}
        Orch->>TUI: stage.completed / stage.failed
    end

    alt all stages passed
        Orch->>Diag: record review handoff
        Orch->>Orch: mark issue in_review
        Orch->>TUI: issue.ready_for_review
    else stage failed
        Orch->>Orch: enqueue retry with backoff
        Orch->>TUI: backoff.queued
    end
```
