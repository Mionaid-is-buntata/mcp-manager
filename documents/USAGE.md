# MCP Manager — Usage Guide

MCP Manager is a CLI tool for managing which MCP servers are enabled in `~/.claude/.mcp-servers.json`, with automatic sync to `~/.claude/settings.json` (the file Claude Code reads) and support for project-local profiles.

---

## Installation

### 1. Build the binary

```bash
cd ~/Documents/projects/Personal/claude-config-reference/mcp-manager
go build -o mcp-manager mcp_manager.go
```

### 2. Install to PATH

```bash
ln -sf $(pwd)/mcp-manager ~/.local/bin/mcp-manager
```

### 3. Verify

```bash
mcp-manager --help
```

### Uninstall

```bash
rm ~/.local/bin/mcp-manager
```

---

## Quick Start

```bash
# See what's available and what's currently on
mcp-manager list

# Turn on a server
mcp-manager enable serena

# Turn off a server
mcp-manager disable serena

# Turn off everything at once
mcp-manager disable "*"

# Check overall status
mcp-manager status

# Save your current setup for this project
mcp-manager save

# Restore that setup next time you open the project
mcp-manager restore
```

---

## Commands

### `list`

Shows all MCP servers with their current status, category, and description.

```bash
mcp-manager list
mcp-manager list --enabled
mcp-manager list --disabled
mcp-manager list --category core
mcp-manager list --json
```

Output is sorted alphabetically by server name.

---

### `enable`

Enables one or more MCP servers. Accepts multiple server names in a single command.

```bash
mcp-manager enable serena
mcp-manager enable serena semgrep playwright
```

- If a server is already enabled, prints a notice and exits 0 (no error).
- If any server name is unknown, prints an error for that name but still enables the valid ones.
- Automatically syncs the server entry to `~/.claude/settings.json` so Claude Code picks it up immediately.
- If a `.claude/mcp-profile.json` profile exists in the current directory, it is updated automatically.

---

### `disable`

Disables one or more MCP servers. Mirror of `enable`. The server entry is removed from `settings.json` so Claude Code stops loading it.

```bash
mcp-manager disable serena
mcp-manager disable serena semgrep playwright
```

Pass `*` to disable every server at once:

```bash
mcp-manager disable "*"
```

> The quotes around `*` prevent the shell from expanding it as a glob.

---

### `status`

Without arguments: prints a summary of how many servers are enabled, disabled, and total, followed by the names of all currently-enabled servers.

```bash
mcp-manager status
```

With a server name: prints the full configuration for that server.

```bash
mcp-manager status serena
mcp-manager status --json serena
```

---

### `search`

Searches server names and descriptions. Case-insensitive.

```bash
mcp-manager search database
mcp-manager search docker
mcp-manager search "browser automation"
```

---

### `save`

Snapshots the currently-enabled servers into `.claude/mcp-profile.json` in the current directory. Use this once per project after you have the right servers enabled.

```bash
cd ~/Documents/projects/my-project
mcp-manager save
```

The profile is saved to `.claude/mcp-profile.json` inside the project directory. The `.claude/` directory is created automatically if it doesn't exist. This file can be committed to git to share the server profile with your team.

---

### `restore`

Reads `.claude/mcp-profile.json` in the current directory, disables all servers globally, then enables only the servers listed in the profile. Run this at the start of a work session on a project.

```bash
cd ~/Documents/projects/my-project
mcp-manager restore
```

Requires `.claude/mcp-profile.json` to exist in the current directory. If none is found, it will error with a message directing you to run `save` first.

---

### `profile show`

Displays the contents of `.claude/mcp-profile.json` in the current directory, cross-referenced against the global config to show whether each listed server currently exists and is enabled.

```bash
mcp-manager profile show
mcp-manager profile show --json
```

---

### `doctor`

Checks for divergence between `.mcp-servers.json` (the registry) and `settings.json` (what Claude Code reads). Reports servers that are enabled but missing from settings, disabled but still present, or present but untracked.

```bash
mcp-manager doctor
mcp-manager doctor --json
mcp-manager doctor --fix
```

With `--fix`, automatically resolves all divergence by rebuilding the `mcpServers` section in `settings.json` to match the registry.

---

## Project Profile Workflow

The profile feature lets you persist MCP server selections per project so you never have to re-configure them manually.

**First time on a project:**

```bash
cd ~/Documents/projects/my-project
mcp-manager enable serena postgresql playwright
mcp-manager save                 # creates .claude/mcp-profile.json
```

**Every subsequent session:**

```bash
cd ~/Documents/projects/my-project
mcp-manager restore              # resets globals and enables your project's servers
```

**Adjusting mid-session:**

```bash
mcp-manager enable semgrep       # also updates .claude/mcp-profile.json automatically
mcp-manager disable playwright   # also updates .claude/mcp-profile.json automatically
```

**Resetting to a clean state when leaving a project:**

```bash
mcp-manager disable "*"          # stop everything instantly
```

---

## Environment Variables

| Variable | Description |
|---|---|
| `CLAUDE_HOME` | Override the directory where `.mcp-servers.json` is looked up. Defaults to `~/.claude`. |

Example:

```bash
CLAUDE_HOME=/tmp/test-claude mcp-manager list
```

---

## Glossary

| Term / Flag | Command(s) | Description |
|---|---|---|
| `--category <cat>` | `list` | Filter the server list to a single category. Category names match the section headers in `.mcp-servers.json`: `core`, `ai`, `dev`, `testing`, `data`, `automation`, `external_plugins`, `reacher`. Hyphens and underscores are interchangeable (`external-plugins` and `external_plugins` both work). |
| `--disabled` | `list` | Show only servers whose `enabled` field is `false`. Mutually exclusive with `--enabled`. |
| `--enabled` | `list` | Show only servers whose `enabled` field is `true`. Mutually exclusive with `--disabled`. |
| `--json` | `list`, `status`, `search`, `profile show` | Output results as a JSON array or object instead of a plain-text table. Useful for piping to other tools (`jq`, `python3 -m json.tool`). |
| `.claude/mcp-profile.json` | `save`, `restore`, `profile show` | A project-local profile stored inside the `.claude/` directory at your project root. The `.claude/` directory is created automatically on first save. Contains a JSON object with a `servers` array listing the server names to enable for that project. Safe to commit to git. |
| `.mcp-servers.json` | all commands | The global MCP server configuration file at `~/.claude/.mcp-servers.json`. The registry and single source of truth. The tool only ever modifies the `enabled` field of each entry — it never changes `command`, `args`, `cwd`, `timeout`, or `env`. |
| `.mcp-servers.json.bak` | `enable`, `disable`, `restore` | A backup of `.mcp-servers.json` written to `~/.claude/` before every write operation. Overwritten each time. Useful for recovering if something goes wrong. |
| `settings.json` | `enable`, `disable`, `restore`, `doctor` | Claude Code's configuration file at `~/.claude/settings.json`. The `mcpServers` key is automatically synced by `enable`/`disable`/`restore` commands. Enabled servers are added (without the `enabled` field); disabled servers are removed. Non-MCP keys (`permissions`, `model`, etc.) are preserved. |
| `settings.json.bak` | `enable`, `disable`, `restore`, `doctor --fix` | A backup of `settings.json` written before every sync operation. |
| `CLAUDE_HOME` | all commands | Environment variable that overrides the directory where `.mcp-servers.json` is found. Defaults to `~/.claude`. |
| `category` | (display) | Each server belongs to a category derived from the `_comment_*` section headers in `.mcp-servers.json`. Categories are: `core` (local servers), `ai` (cognitive tools), `dev` (language/framework servers), `testing` (browser automation), `data` (analytics/search), `automation` (scraping/workflow), `external_plugins` (third-party cloud MCPs), `reacher` (remote servers on 192.168.30.2). |
| `disable <server>...` | `disable` | Sets `"enabled": false` for one or more named servers and removes them from `settings.json`. Multiple names can be passed in a single command. Pass `*` (quoted) to disable every server at once: `mcp-manager disable "*"`. Already-disabled servers are reported but not treated as errors. |
| `doctor` | `doctor` | Audits `.mcp-servers.json` against `settings.json` and reports divergence. With `--fix`, automatically resolves all issues. With `--json`, outputs structured results. |
| `enable <server>...` | `enable` | Sets `"enabled": true` for one or more named servers and syncs them to `settings.json`. Multiple names can be passed in a single command. Already-enabled servers are reported but not treated as errors. |
| `list` | `list` | Displays all 39 servers in a formatted table sorted alphabetically, showing name, enabled/disabled status, category, and description. |
| `profile show` | `profile show` | Shows the contents of `.claude/mcp-profile.json` in the current directory. Each server is shown with whether it exists in the global config and its current enabled/disabled state. |
| `restore` | `restore` | Atomically applies a project profile: disables every server in the global config, then enables only the servers listed in `.claude/mcp-profile.json`. Retries once if the config file is modified concurrently. |
| `save` | `save` | Reads the current set of enabled servers from the global config and writes their names to `.claude/mcp-profile.json` in the current directory. The `.claude/` directory is created automatically if it doesn't exist. Creates or overwrites the file. |
| `search <keyword>` | `search` | Case-insensitive substring search across server names and descriptions. Returns a filtered table in the same format as `list`. |
| `status` | `status` | Without arguments: shows total/enabled/disabled counts and lists enabled server names. With a server name argument: shows the full configuration for that server including command, args, cwd, timeout, and env. |
