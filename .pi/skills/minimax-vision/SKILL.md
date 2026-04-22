---
name: minimax-vision
description: Analyze images using the MiniMax VLM (mmx vision). Use when the agent cannot read images natively and needs to describe, inspect, or extract information from image files, screenshots, URLs, or photos.
---

# MiniMax Vision

Use `mmx vision describe` to analyze images. The agent cannot read images natively, so use this skill whenever you need to see what's in an image.

## Quick Start

```bash
mmx vision describe --image <path-or-url> [--prompt "your question"]
```

## Workflows

### Describe an image (default prompt)

```bash
mmx vision describe --image photo.jpg
```

### Ask a specific question

```bash
mmx vision describe --image screenshot.png --prompt "What UI elements are visible?"
```

### Describe an image from a URL

```bash
mmx vision describe --image https://example.com/diagram.png --prompt "Explain the flow"
```

### JSON output (structured response)

```bash
mmx vision describe --image schema.png --prompt "List all fields and their types" --output json
```

## Input Options

Provide **one** of:

| Flag | Description |
|---|---|
| `--image <path-or-url>` | Local path or URL — auto base64-encodes local files |
| `--file-id <id>` | Pre-uploaded file ID (MiniMax hosted files, skips base64) |

## Tips

- For local files, the CLI auto base64-encodes them — no manual prep needed.
- URL images are fetched directly — no local copy required.
- Use `--prompt` to focus the analysis on what you need (e.g., "What breed?", "List the bugs", "Transcribe the text").
- Default prompt is `"Describe the image."` if `--prompt` is omitted.
- Prefer `--output json` when you need structured, machine-parseable output.
