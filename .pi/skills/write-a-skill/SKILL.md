---
name: write-a-skill
description: Create new Pi Coding Agent skills with proper structure, progressive disclosure, and bundled resources. Use when user wants to create, write, or build a new skill, skill template, or skill structure.
---

# Writing Skills

## Process

### 1. Gather Requirements

Ask the user about:
- What task/domain does the skill cover?
- What specific use cases should it handle?
- Does it need executable scripts or just instructions?
- Any reference materials to include?

### 2. Draft the Skill

Create:
- `SKILL.md` with concise instructions
- Additional reference files if content exceeds ~100 lines
- Utility scripts if deterministic operations needed

### 3. Review with User

Present draft and ask:
- Does this cover your use cases?
- Anything missing or unclear?
- Should any section be more/less detailed?

## Skill Structure

```
skill-name/
├── SKILL.md           # Main instructions (required)
├── REFERENCE.md       # Detailed docs (if needed)
├── EXAMPLES.md        # Usage examples (if needed)
└── scripts/           # Utility scripts (if needed)
    └── helper.sh
```

## SKILL.md Template

```markdown
---
name: skill-name
description: Brief description of capability. Use when [specific triggers].
---

# Skill Name

## Quick Start

[Minimal working example]

## Workflows

[Step-by-step processes for complex tasks]

## Advanced Features

[Link to separate files: See [REFERENCE.md](REFERENCE.md)]
```

## Frontmatter Requirements

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Max 64 chars. Lowercase a-z, 0-9, hyphens only. Must match parent directory. |
| `description` | Yes | Max 1024 chars. What it does and when to use it. |

### Name Rules
- 1-64 characters
- Lowercase letters, numbers, hyphens only
- No leading/trailing hyphens
- No consecutive hyphens
- Must match parent directory name

**Valid:** `pdf-processing`, `data-analysis`, `code-review`
**Invalid:** `PDF-Processing`, `-pdf`, `pdf--processing`

## Description Best Practices

The description is the **only thing the agent sees** when deciding which skill to load. Be specific.

### Format
- Max 1024 chars
- Write in third person
- First sentence: what it does
- Second sentence: "Use when [specific triggers]"

### Good Example
```
Extract text and tables from PDF files, fill forms, merge documents. Use when working with PDF files or when user mentions PDFs, forms, or document extraction.
```

### Bad Example
```
Helps with PDFs.
```

## When to Add Scripts

Add utility scripts when:
- Operation is deterministic (validation, formatting)
- Same code would be generated repeatedly
- Errors need explicit handling

Scripts save tokens and improve reliability vs generated code.

## When to Split Files

Split into separate files when:
- SKILL.md exceeds ~100 lines
- Content has distinct domains
- Advanced features are rarely needed

## Skill Locations

Place skills in:
- **Project:** `.pi/skills/` (or `.agents/skills/`)
- **Global:** `~/.pi/agent/skills/`

Skills can also be loaded from npm packages via `packages` in settings.json.

## Review Checklist

After drafting, verify:
- [ ] Description includes triggers ("Use when...")
- [ ] SKILL.md under ~100 lines
- [ ] No time-sensitive info
- [ ] Consistent terminology
- [ ] Concrete examples included
- [ ] References one level deep
- [ ] Name matches directory and uses lowercase/hyphens only
