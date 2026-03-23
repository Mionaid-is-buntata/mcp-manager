# PRD-01: `claude-mcp` — MCP Server Management CLI

> Status: Draft
> Author: Technical Reviewer
> Date: 2026-03-23
> Decomposed from: [08-cli-tool-design.md](08-cli-tool-design.md)

---

## 1. Problem Statement

Developers currently hand-edit `~/.claude/.mcp-servers.json` to toggle MCP servers on and off. This file contains 30+ server entries. A single misplaced comma or bracket silently breaks the session. There is no validation, no feedback, and no way to see what's enabled without reading raw JSON.

This is the most frequent configuration action — it happens at every project start and end.

---

## 2. Proposed Solution

A single-purpose CLI tool: `claude-mcp`.

It does one thing: manage the `enabled` flag on MCP server entries in `~/.claude/.mcp-servers.json`.

---

## 3. User Personas

| Persona | Need |
|---------|------|
| **Multi-project developer** | Quickly toggle MCPs between projects without JSON editing |
| **New Claude Code user** | Discover what MCPs are available and what they do |
| **DevOps/security user** | Audit which MCPs are currently active |

---

## 4. Command Surface

```bash
claude-mcp list                          # Show all MCPs with enabled/disabled status
claude-mcp list --enabled                # Show only enabled MCPs
claude-mcp list --disabled               # Show only disabled MCPs
claude-mcp list --category <cat>         # Filter by category (core, ai, dev, testing, data, external, reacher)

claude-mcp enable <server> [<server>...] # Enable one or more MCPs
claude-mcp disable <server> [<server>...]# Disable one or more MCPs

claude-mcp status                        # Summary: N enabled, M disabled, K total
claude-mcp status <server>               # Show full config for one server

claude-mcp search <keyword>              # Search MCPs by name or description
```

---

## 5. Behaviour Specification

### 5.1 `claude-mcp list`

Reads `~/.claude/.mcp-servers.json`. Outputs a table:

```
SERVER              STATUS     CATEGORY    DESCRIPTION
serena              enabled    core        Code-aware navigation and editing
docker-agent        enabled    core        Docker container management
postgresql          disabled   dev         PostgreSQL database operations
playwright          disabled   testing     Browser automation testing
stripe              disabled   external    Stripe payment integration
```

Sorted alphabetically by default. `--category` filters.

### 5.2 `claude-mcp enable <server>`

1. Reads `.mcp-servers.json`
2. Validates `<server>` exists as a key
3. Sets `"enabled": true`
4. Writes back with preserved formatting (use JSON with 2-space indent)
5. Prints confirmation: `Enabled: postgresql`
6. If already enabled: `postgresql is already enabled` (no error, exit 0)
7. If server not found: `Error: unknown server 'foo'. Run 'claude-mcp list' to see available servers.` (exit 1)

Multiple servers in one command: `claude-mcp enable postgresql serena http-tester`
- Enables each sequentially
- Prints one line per server
- If any server name is invalid, reports the error but still enables valid ones (partial success)

### 5.3 `claude-mcp disable <server>`

Mirror of `enable` with `"enabled": false`.

### 5.4 `claude-mcp status`

```
MCP Server Status:
  Enabled:  8 / 34
  Disabled: 26 / 34

Enabled servers: serena, docker-agent, http-tester, semgrep, sequential-thinking, memory, context7, sandbox
```

### 5.5 `claude-mcp search <keyword>`

Searches server names and descriptions. Case-insensitive.

```
$ claude-mcp search database
postgresql          disabled   dev     PostgreSQL database operations
local-database      disabled   reacher State persistence and caching
```

---

## 6. Config File Contract

### Input/Output
- **File**: `~/.claude/.mcp-servers.json`
- **Read/write**: Tool reads the full file, modifies in-place, writes back
- **Format preservation**: 2-space JSON indent, sorted keys preserved
- **Backup**: Before any write, copy current file to `~/.claude/.mcp-servers.json.bak`
- **Locking**: Advisory file lock during write to prevent concurrent modification

### Schema assumptions
Each server entry has at minimum:
```json
{
  "serverName": {
    "command": "string",
    "args": ["array"],
    "enabled": true | false
  }
}
```

The tool ONLY modifies the `enabled` field. It never touches `command`, `args`, `cwd`, `timeout`, or any other field.

---

## 7. Non-Goals

- Does NOT manage credentials (that's `claude-creds`)
- Does NOT manage profiles or named groups (that's `claude-profile`)
- Does NOT manage `settings.json` or `additionalDirectories`
- Does NOT start/stop MCP server processes
- Does NOT validate that an MCP server's dependencies are installed
- Does NOT modify orchestration files (`mcp-index.json`, `context-manager.js`)

---

## 8. Technical Constraints

| Constraint | Detail |
|------------|--------|
| Language | Python 3.10+ (already present for `uv`/serena ecosystem) |
| Dependencies | stdlib only (`json`, `argparse`, `pathlib`) |
| Install | Single file, symlinked into `~/.local/bin/` or installed via `uv tool install` |
| Config path | Hardcoded to `~/.claude/.mcp-servers.json`, overridable via `CLAUDE_HOME` env var |
| Output | Plain text by default, `--json` flag for machine-readable output |

---

## 9. Error Handling

| Scenario | Behaviour |
|----------|-----------|
| `.mcp-servers.json` missing | Error: "MCP config not found at ~/.claude/.mcp-servers.json" (exit 1) |
| `.mcp-servers.json` malformed | Error: "Failed to parse MCP config: {json error}" (exit 1) |
| Unknown server name | Error with suggestion (exit 1) |
| Permission denied on write | Error: "Cannot write to {path}: permission denied" (exit 1) |
| File changed during operation | Re-read and retry once, then error |

---

## 10. Success Metrics

- **Adoption**: Replaces >90% of manual `.mcp-servers.json` edits within 2 weeks
- **Error reduction**: Zero malformed JSON incidents from MCP toggling
- **Speed**: Any command completes in <100ms

---

## 11. Phasing

This is **Phase 1** — the minimal viable tool. It has no dependencies on other proposed tools (`claude-creds`, `claude-profile`, `claude-project`). It can ship independently.

---

## 12. Relationship to Oracle Pattern

In the Oracle routing model, this tool is a **worker-tier specialist** — it does exactly one thing (MCP toggling) and does it well. The `claude-project` tool (PRD-03) acts as the **Oracle/coordinator**, deciding which MCPs to enable based on project context and delegating the actual toggling to this tool.

```
claude-project start .          ← Oracle (classifies, decides)
  └── claude-mcp enable X Y Z  ← Worker (executes)
```
