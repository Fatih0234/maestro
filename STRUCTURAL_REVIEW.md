# Structural Simplicity Review — Contrabass-PI

## Overall Verdict: **can be simplified**

The codebase has no structural rot (S0), but there are several shallow modules, some dead code, and a handful of thin wrappers that should be consolidated. The deep modules — `orchestrator.go`, `opencode.go`, `recorder.go`, `runner.go`, `local.go`, `github.go` — are well-structured. The issues are concentrated in small satellite files and accessor-heavy abstractions.

---

## Findings

### [S1] `internal/tui/events.go` — Shallow module with no standalone purpose
**Location:** `internal/tui/events.go` (12 lines)

Contains only two type definitions (`OrchestratorEventMsg`, `tickMsg`) used exclusively by `model.go`. A file that exists solely to host two message wrappers adds a package boundary with no abstraction benefit.

**Concrete fix:** Merge into `internal/tui/model.go` at the top.

---

### [S1] `internal/web/events.go` — Shallow module with no standalone purpose
**Location:** `internal/web/events.go` (12 lines)

Contains only the `WebEvent` struct, used by `hub.go`, `server.go`, and `sse.go`. There is no behavior or protocol logic here — just a JSON wrapper type that could live in any of its three consumers.

**Concrete fix:** Merge `WebEvent` into `server.go` (where `Snapshot` and related types already live). Then `sse.go` becomes just protocol helpers next to the server.

---

### [S1] `internal/agent/events.go` — Six identical-shape extraction helpers hiding an untyped payload
**Location:** `internal/agent/events.go`

`IsIdle`, `IsError`, `IsHeartbeat`, `ExtractSessionID`, `ExtractTextContent`, and `ExtractTokens` all follow the exact same pattern: type-assert `map[string]interface{}`, traverse 2–3 nested map levels, return a value. Each function is 5–15 lines of identical structural code. They don't abstract a concept — they paper over the fact that `AgentEvent.Payload` is `interface{}`.

This is interface bloat: six public functions to do what one structured payload type would achieve with field access. The only caller of these helpers is `opencode.go` (inside `streamSessionEvents` and `Start`).

**Concrete fix:** Define a typed `EventPayload` struct (or unmarshal into one) when parsing the SSE JSON. Then delete all six helpers and access fields directly. If keeping the untyped design, inline the three extraction calls in `opencode.go` and delete the file.

---

### [S2] `internal/tui/table.go` — Shallow data holder with trivial accessors
**Location:** `internal/tui/table.go`

`Table` is a struct with 5 fields and 7 public methods (`Update`, `SetWidth`, `SetSelected`, `SetFocused`, `RowCount`, `Selected`, `View`). Six of the seven methods are trivial pass-throughs or one-liners. The only real logic is `View()`, which renders rows. All state that Table holds (`rows`, `selected`, `focused`, `width`) is derived from or mirrored in `Model`.

The rendering logic for the session table already lives in `model.go`'s `renderTable()`. `table.go` exists only to hold a slice of `SessionRow` and a selected index that `Model` could hold directly.

**Concrete fix:** Fold `Table` into `Model` as a private `sessionRows []SessionRow` field. Move `View()` rendering into `model.go`'s `renderTable()`. Delete `table.go`.

---

### [S2] `internal/orchestrator/state.go` — Accessor-heavy wrapper with dead code
**Location:** `internal/orchestrator/state.go`

`StateManager` exposes 12 public methods over a `map[string]*RunState`. Six of them (`UpdateStage`, `UpdatePhase`, `UpdateTokens`, `UpdateLastEvent`, `SetError`, `SetProcess`) are 3-line field mutators. Two are unused:

- `SetError` — never called in `orchestrator.go`. Error tracking is done through backoff events and `finalizeAttempt`, not through run state.
- `GetByPhase` — never called in production code (may be used in tests, but that doesn't justify a public method in the core API).

The remaining four (`Add`, `Get`, `Remove`, `Len`, `GetAll`) are legitimate map operations with mutex protection.

**Concrete fix:**
1. Delete `SetError` and `GetByPhase` (dead code).
2. Collapse the six field mutators into a single `Mutate(issueID string, fn func(*RunState))` method. This reduces the public surface from 12 to 6 methods while preserving thread safety. Callers become slightly more verbose but the abstraction boundary stays clean.

---

### [S2] `internal/orchestrator/events.go` — 20 payload structs, no behavior
**Location:** `internal/orchestrator/events.go`

Contains 20+ event payload structs (`IssueClaimedPayload`, `WorkspaceCreatedPayload`, `AgentStartedPayload`, etc.) totaling ~200 lines. Each is a plain data bag with 2–6 fields and no methods. They are used only for type assertions in `model.go` and `server.go`.

This is configuration-parameter-style bloat: many types that exist only to give names to event payloads. They could be replaced with 3–4 generic payload types or even `map[string]interface{}` without losing clarity, since the event type string (`event.Type`) already discriminates the payload shape.

**Concrete fix:** Consolidate into 4 generic payload types:
- `IssuePayload` (IssueID + Title)
- `StagePayload` (IssueID + Stage + Attempt + Error)
- `AgentPayload` (IssueID + PID + SessionID)
- `BackoffPayload` (IssueID + Attempt + Stage + RetryAt + Error)

Then use these in event definitions. This would reduce ~20 types to 4. Alternatively, accept the current design as domain-documentation-by-types and leave it alone — this is a judgment call. I flag it as S2 because the interface surface is large relative to the behavior provided.

---

### [S2] `cmd/contrabass/main.go` and `cmd/contrabass/board.go` — Trivial `truncate` duplication
**Location:** `cmd/contrabass/main.go`, `cmd/contrabass/board.go`, `internal/tui/model.go`

`truncate(s string, maxLen int) string` is defined independently in three places:
- `main.go` (not shown in read but `formatEvent` uses truncation)
- `board.go` line ~220
- `model.go` line ~380

Each is a 3-line function. Per the guidelines, "Prefer duplication in an existing deep module over a new shallow abstraction for trivial DRY (3–10 lines)." This is borderline — three copies of the same trivial function across the CLI surface is slightly noisy but not a structural problem.

**Concrete fix:** [S3] polish — leave alone or inline one-liner at each call site.

---

### [S3] `internal/web/hub.go` — Could be merged into `server.go`
**Location:** `internal/web/hub.go` (45 lines)

`Hub` is a standalone fan-out broadcast mechanism. It's self-contained and correct, but it's only used by `Server`. It doesn't have independent test value or reusable semantics. Merging it into `server.go` would reduce the package from 4 files to 3 with no loss of clarity.

**Concrete fix:** Optional — merge into `server.go` as a private `eventHub` struct.

---

### [S3] `cmd/demo/` — Build-ignored scripts in source tree
**Location:** `cmd/demo/main.go`, `cmd/demo/integration.go`

Both files have `//go:build ignore`. They are not compiled, not tested by CI, and duplicate helper functions (`runGit`, `humanDuration`, `truncate`) from the main codebase. They clutter the `cmd/` tree and drift from the actual code.

**Concrete fix:** Move to `scripts/` or `examples/` outside `cmd/`, or delete and document how to run integration tests in `docs/`.

---

## Simplification Roadmap (ordered by impact)

1. **Delete dead code** (`StateManager.SetError`, `StateManager.GetByPhase`, `tui.displayIssueID`, `orchestrator.EventMergeFailed` if unused) — zero-risk, immediate win.
2. **Merge shallow files** (`tui/events.go` → `model.go`, `web/events.go` → `server.go`) — reduces file count with no logic change.
3. **Consolidate agent event helpers** — replace 6 map-traversal functions with a typed payload or inline extraction in `opencode.go`.
4. **Collapse StateManager mutators** — `Mutate()` instead of 6 separate update methods.
5. **Fold `Table` into `Model`** — delete `table.go`, move rendering to `model.go`.
6. **Consolidate event payload types** — 4 generic types instead of 20+.
7. **Relocate build-ignored demo scripts** — out of `cmd/`.

---

## Leave-These-Alone

These modules are deep, coherent, and well-structured. Length is not a smell here.

- **`internal/orchestrator/orchestrator.go`** (~450 lines) — The `startRun` goroutine is long but it tells a single narrative: stage loop → event wrapping → success/failure → handoff. Splitting it would create a tangled call graph.
- **`internal/diagnostics/recorder.go`** (~350 lines) — Complex file-tree management, atomic JSON writes, cross-referenced updates. Every line serves the artifact-persistence contract.
- **`internal/agent/opencode.go`** (~400 lines) — SSE parsing, server lifecycle, HTTP session management, process supervision. Rich implementation, simple interface.
- **`internal/pipeline/runner.go`** (~250 lines) — Stage lifecycle: workspace → prompt → agent → postflight → commit. One clear narrative.
- **`internal/tracker/local.go`** and **`github.go`** — Two deep tracker implementations behind one interface. Good abstraction.
- **`internal/config/config.go`** — YAML front matter, validation, path resolution. All config concerns in one place.
- **`cmd/contrabass/board.go`** — All human-review CLI commands in one file. Deep and focused.
- **`internal/types/`** — Type definitions are domain complexity, not structural bloat. The `IssueTracker` and `AgentRunner` interfaces are appropriately sized.

---

## Complexity Metrics

| Metric | Count |
|--------|-------|
| Files analyzed (`.go`, excluding tests) | 28 |
| Shallow modules detected | 4 (`tui/events.go`, `web/events.go`, `agent/events.go` helpers, `tui/table.go`) |
| Pass-through methods | ~18 (StateManager: 6, Table: 6, agent extractors: 6) |
| Dead public symbols | 3 (`StateManager.SetError`, `StateManager.GetByPhase`, `displayIssueID`) |
| Public API surface (types + functions + methods, approximate) | ~140 symbols across 12 packages |
| Packages under `internal/` | 12 |
| TUI files that could merge to 1 | 3 → 1 |
| Web files that could merge to 2 | 4 → 2 |
