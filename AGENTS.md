# AGENTS.md — Contrabass-PI

## Project Overview

This is a **learning exercise** in building an AI coding agent orchestrator from scratch. The goal is to understand every line of code, every function, and every design decision — not to produce a production-ready system.

**Reference**: The [contrabass](https://github.com/junhoyeo/contrabass) repo is studied for understanding concepts only. It lives in `.gitignore` and should never be modified or committed.

**Language**: Go (learned alongside the project)

---

## What We're Building

A **minimal orchestrator for OpenCode agents** with:

- A `WORKFLOW.md` config parser
- A local file-based issue tracker
- Git worktree-based workspace per issue
- An OpenCode agent runner (HTTP + SSE)
- A simple orchestrator loop (poll → claim → dispatch → retry)

The team/phase system from contrabass is deferred until the core single-agent flow is solid.

---

## User Goals

1. **Learn Go** by writing it — every file should be understandable
2. **Learn Git** by using it — commit regularly, understand the history
3. **Understand agent orchestration** — what happens when, and why

---

## Git Workflow

### Commit Messages

Use this format: `<type>: <description>`

| Type | Purpose |
|------|---------|
| `init` | Project initialization |
| `feat` | New feature |
| `fix` | Bug fix |
| `refactor` | Code restructuring |
| `docs` | Documentation |
| `test` | Tests |

### When to Commit

- After every working piece of functionality
- After any configuration file
- Before exploring or trying risky changes

### AI Agent Instructions

When using Git, **explain your action** and give a takeaway:

- "I'm committing the config parser because it now reads YAML front matter"
- "Takeaway: `git add -p` lets you stage parts of a file, useful for review"

---

## Rules

1. **No magic** — every function must have a clear purpose
2. **Test as we build** — run code early, run code often
3. **Slow is fine** — understanding trumps speed
4. **Contrabass is reference only** — never copy code directly from it
5. **Git is a teacher** — use it to show the history of decisions