# OpenCode Config Documentation

> Source: https://opencode.ai/docs/config/
> Last Updated: Apr 22, 2026

## Overview

Using the OpenCode JSON config. You can configure OpenCode using a JSON config file.

---

## Format

OpenCode supports both **JSON** and **JSONC** (JSON with Comments) formats.

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "model": "anthropic/claude-sonnet-4-5",
  "autoupdate": true,
  "server": {
    "port": 4096,
  },
}
```

---

## Locations

You can place your config in a couple of different locations and they have a different order of precedence.

> **Note:** Configuration files are **merged together**, not replaced.
>
> Configuration files are merged together, not replaced. Settings from the following config locations are combined. Later configs override earlier ones only for conflicting keys. Non-conflicting settings from all configs are preserved.

### Precedence Order

Config sources are loaded in this order (later sources override earlier ones):

1. **Remote config** (from `.well-known/opencode`) — organizational defaults
2. **Global config** (`~/.config/opencode/opencode.json`) — user preferences
3. **Custom config** (`OPENCODE_CONFIG` env var) — custom overrides
4. **Project config** (`opencode.json` in project) — project-specific settings
5. **`.opencode` directories** — agents, commands, plugins
6. **Inline config** (`OPENCODE_CONFIG_CONTENT` env var) — runtime overrides
7. **Managed config files** (`/Library/Application Support/opencode/` on macOS) — admin-controlled
8. **macOS managed preferences** (`.mobileconfig` via MDM) — highest priority, not user-overridable

### Remote

Organizations can provide default configuration via the `.well-known/opencode` endpoint. This is fetched automatically when you authenticate with a provider that supports it.

Remote config is loaded first, serving as the base layer. All other config sources (global, project) can override these defaults.

### Global

Place your global OpenCode config in `~/.config/opencode/opencode.json`. Use global config for user-wide server/runtime preferences like providers, models, and permissions.

For TUI-specific settings, use `~/.config/opencode/tui.json`.

### Per Project

Add `opencode.json` in your project root. Project config has the highest precedence among standard config files — it overrides both global and remote configs.

When OpenCode starts up, it looks for a config file in the current directory or traverse up to the nearest Git directory.

### Custom Path

Specify a custom config file path using the `OPENCODE_CONFIG` environment variable:

```
export OPENCODE_CONFIG=/path/to/my/custom-config.json
opencode run "Hello world"
```

### Custom Directory

Specify a custom config directory using the `OPENCODE_CONFIG_DIR` environment variable. This directory will be searched for agents, commands, modes, and plugins just like the standard `.opencode` directory:

```
export OPENCODE_CONFIG_DIR=/path/to/my/config-directory
opencode run "Hello world"
```

---

## Schema

The server/runtime config schema is defined in **`opencode.ai/config.json`**.

TUI config uses **`opencode.ai/tui.json`**.

---

## Server

You can configure server settings for the `opencode serve` and `opencode web` commands through the `server` option:

```json
{
  "server": {
    "port": 4096,
    "hostname": "0.0.0.0",
    "mdns": true,
    "mdnsDomain": "myproject.local",
    "cors": ["http://localhost:5173"]
  }
}
```

Available options:
- `port` — Port to listen on
- `hostname` — Hostname to listen on
- `mdns` — Enable mDNS service discovery
- `mdnsDomain` — Custom domain name for mDNS service
- `cors` — Additional origins to allow for CORS

---

## Models

You can configure the providers and models you want to use:

```json
{
  "model": "anthropic/claude-sonnet-4-5",
  "small_model": "anthropic/claude-haiku-4-5"
}
```

The `small_model` option configures a separate model for lightweight tasks like title generation.

Provider options:
```json
{
  "provider": {
    "anthropic": {
      "options": {
        "timeout": 600000,
        "chunkTimeout": 30000,
        "setCacheKey": true
      }
    }
  }
}
```

- `timeout` — Request timeout in milliseconds (default: 300000)
- `chunkTimeout` — Timeout in milliseconds between streamed response chunks
- `setCacheKey` — Ensure a cache key is always set

---

## Agents

You can configure specialized agents:

```jsonc
{
  "agent": {
    "code-reviewer": {
      "description": "Reviews code for best practices and potential issues",
      "model": "anthropic/claude-sonnet-4-5",
      "prompt": "You are a code reviewer. Focus on security, performance, and maintainability.",
      "tools": {
        "write": false,
        "edit": false,
      },
    },
  },
}
```

---

## Default Agent

Set the default agent using `default_agent`:

```json
{
  "default_agent": "plan"
}
```

The default agent must be a primary agent (not a subagent).

---

## Permissions

By default, opencode **allows all operations** without requiring explicit approval:

```json
{
  "permission": {
    "edit": "ask",
    "bash": "ask"
  }
}
```

---

## Variables

You can use variable substitution in config files:

### Env vars

```json
{
  "model": "{env:OPENCODE_MODEL}",
  "provider": {
    "anthropic": {
      "options": {
        "apiKey": "{env:ANTHROPIC_API_KEY}"
      }
    }
  }
}
```

### Files

```json
{
  "instructions": ["./custom-instructions.md"],
  "provider": {
    "openai": {
      "options": {
        "apiKey": "{file:~/.secrets/openai-key}"
      }
    }
  }
}
```

---

## Other Config Options

### MCP Servers

```json
{
  "mcp": {}
}
```

### Plugins

```json
{
  "plugin": ["opencode-helicone-session", "@my-org/custom-plugin"]
}
```

### Instructions

```json
{
  "instructions": ["CONTRIBUTING.md", "docs/guidelines.md", ".cursor/rules/*.md"]
}
```

### Disabled Providers

```json
{
  "disabled_providers": ["openai", "gemini"]
}
```

### Enabled Providers

```json
{
  "enabled_providers": ["anthropic", "openai"]
}
```
