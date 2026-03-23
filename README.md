# MCP Manager

A CLI tool for managing which MCP servers are active in `~/.claude/.mcp-servers.json` — without hand-editing JSON.

Toggle servers on and off, save per-project profiles, and restore them at the start of each session.

---

## Requirements

- Go 1.21+
- `~/.claude/.mcp-servers.json` (Claude Code's MCP config file)

---

## Installation

```bash
git clone <repo>
cd claude-mcp
go build -o mcp-manager mcp_manager.go
ln -sf $(pwd)/mcp-manager ~/.local/bin/mcp-manager
```

Verify:

```bash
mcp-manager --help
```

---

## Quick Start

```bash
# See all servers and their status
mcp-manager list

# Enable a server
mcp-manager enable serena

# Disable a server
mcp-manager disable serena

# Stop everything at once
mcp-manager disable "*"

# Check what's running
mcp-manager status
```

---

## Commands

| Command | Description |
|---|---|
| `list` | Show all servers — name, status, category, description |
| `enable <server>...` | Enable one or more servers |
| `disable <server>...` | Disable one or more servers. Pass `"*"` to disable all |
| `status [<server>]` | Summary of enabled/disabled counts, or full config for a named server |
| `search <keyword>` | Case-insensitive search across server names and descriptions |
| `save` | Save currently-enabled servers to `.claude/mcp-profile.json` |
| `restore` | Disable all servers, then re-enable from `.claude/mcp-profile.json` |
| `profile show` | Display the project profile and each server's current state |

All commands accept `--json` for machine-readable output. `list` also accepts `--enabled`, `--disabled`, and `--category <name>`.

---

## Project Profiles

The profile feature lets you pin a set of MCP servers to a project. Once saved, a single command restores your exact setup at the start of any session.

**First time:**

```bash
cd ~/my-project
mcp-manager enable serena playwright postgresql
mcp-manager save          # writes .claude/mcp-profile.json
```

**Every subsequent session:**

```bash
cd ~/my-project
mcp-manager restore       # disables all, enables only your saved servers
```

**Mid-session changes** (enable/disable automatically update the profile if one exists):

```bash
mcp-manager enable semgrep     # also added to .claude/mcp-profile.json
mcp-manager disable playwright # also removed from .claude/mcp-profile.json
```

`.claude/mcp-profile.json` is safe to commit to git.

---

## Environment Variables

| Variable | Description |
|---|---|
| `CLAUDE_HOME` | Override the directory where `.mcp-servers.json` is read from. Defaults to `~/.claude`. |

```bash
CLAUDE_HOME=/tmp/test-claude mcp-manager list
```

---

## Safety

- **Only the `enabled` field is ever modified.** Command, args, environment, cwd, and timeout are never touched.
- A backup is written to `~/.claude/.mcp-servers.json.bak` before every write operation.
- File locking (`flock`) prevents concurrent writes from corrupting the config.
- Key insertion order in the JSON is preserved exactly on every write.

---

## Running Tests

```bash
go test ./...
```

The test suite compiles the binary once, then drives it end-to-end using isolated temp directories. It never touches your real `~/.claude` config.

```bash
go test -v ./...          # verbose output per test
go test -run TestCmd_List # run a specific test
go test -cover            # with coverage
```

101 tests cover list filtering, enable/disable, wildcard disable, status, search, profiles, concurrent modification detection, JSON output, and error handling.

---

## Documentation

Full reference is in the [`documents/`](documents/) directory:

| File | Contents |
|---|---|
| [`USAGE.md`](documents/USAGE.md) | Complete command reference with examples and glossary |
| [`TECHNICAL.md`](documents/TECHNICAL.md) | Architecture, data flow, all types and functions |
| [`TESTING.md`](documents/TESTING.md) | Test catalogue, how to run, troubleshooting |
| [`PRD-01-claude-mcp.md`](documents/PRD-01-claude-mcp.md) | Original requirements document |
