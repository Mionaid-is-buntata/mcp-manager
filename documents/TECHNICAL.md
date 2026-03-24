# MCP Manager — Technical Reference

> Source file: `mcp_manager.go`
> Language: Go 1.21+
> Dependencies: stdlib only

---

## Table of Contents

1. [Overview](#1-overview)
2. [Architecture](#2-architecture)
3. [Constants](#3-constants)
4. [Types](#4-types)
   - [rawConfig](#41-rawconfig)
   - [orderedMap](#42-orderedmap)
   - [serverEntry](#43-serverentry)
   - [serverRaw](#44-serverraw)
   - [profile](#45-profile)
   - [settingsConfig](#46-settingsconfig)
5. [Config I/O](#5-config-io)
   - [getConfigPath](#51-getconfigpath)
   - [loadConfig](#52-loadconfig)
   - [saveConfig](#53-saveconfig)
   - [getMtime](#54-getmtime)
   - [copyFile](#55-copyfile)
   - [getSettingsPath](#56-getsettingspath)
   - [loadSettings](#57-loadsettings)
   - [saveSettings](#58-savesettings)
   - [getMCPServers](#59-getmcpservers)
   - [setMCPServers](#510-setmcpservers)
   - [stripEnabled](#511-stripenabled)
   - [syncToSettings](#512-synctosettings)
6. [Server Parsing](#6-server-parsing)
   - [parseServers](#61-parseservers)
   - [setEnabled](#62-setenabled)
7. [Profile System](#7-profile-system)
   - [getProfilePath](#71-getprofilepath)
   - [ensureProfileDir](#72-ensureprofiledir)
   - [loadProfile](#73-loadprofile)
   - [saveProfile](#74-saveprofile)
   - [updateProfileIfPresent](#75-updateprofileifpresent)
8. [Commands](#8-commands)
   - [cmdList](#81-cmdlist)
   - [cmdEnableDisable](#82-cmdenabledisable)
   - [cmdStatus](#83-cmdstatus)
   - [cmdSearch](#84-cmdsearch)
   - [cmdSave](#85-cmdsave)
   - [cmdRestore](#86-cmdrestore)
   - [cmdProfileShow](#87-cmdprofileshow)
   - [cmdDoctor](#88-cmddoctor)
9. [Output Helpers](#9-output-helpers)
   - [printTable](#91-printtable)
   - [printJSON](#92-printjson)
   - [fatalf](#93-fatalf)
   - [indentJSON](#94-indentjson)
   - [normaliseCategory](#95-normalisecategory)
   - [boolStatus](#96-boolstatus)
   - [mustMarshal](#97-mustmarshal)
10. [Entry Point](#10-entry-point)
11. [Data Flow Diagrams](#11-data-flow-diagrams)
12. [Error Handling Reference](#12-error-handling-reference)

---

## 1. Overview

MCP Manager is a single-file Go program that manages the `enabled` flag on entries in `~/.claude/.mcp-servers.json`. It provides CLI commands to list, enable, disable, and search MCP servers, and implements a project-local profile system so server selections can be persisted per project.

The program has no external dependencies. All functionality is implemented using the Go standard library.

### Design Principles

- **Non-destructive writes.** The program only ever modifies the `enabled` field of server entries. It never changes `command`, `args`, `cwd`, `timeout`, or `env`.
- **Order preservation.** The MCP config file uses `_comment_*` keys as section headers, interleaved with server entries. Standard JSON parsers would sort or discard these. MCP Manager implements a custom ordered JSON structure to preserve their positions exactly.
- **Safe writes.** Every write operation creates a backup first, acquires an advisory file lock, and detects concurrent modifications using file mtime comparison.
- **Fail fast.** Any unrecoverable error (missing config, parse failure, permission denied) prints a clear message to stderr and exits with code 1 immediately.

---

## 2. Architecture

The program is structured as a flat set of functions in a single `main` package. There are no interfaces, no goroutines, and no global mutable state.

```
main()
  └── routes to cmd* function based on os.Args[1]
        ├── cmdList
        ├── cmdEnableDisable  (used by both enable and disable)
        ├── cmdStatus
        ├── cmdSearch
        ├── cmdSave
        ├── cmdRestore
        └── cmdProfileShow

Config I/O layer:
  getConfigPath → loadConfig → parseServers
  saveConfig ← setEnabled

Profile I/O layer:
  getProfilePath → loadProfile / saveProfile
  updateProfileIfPresent (called by cmdEnableDisable)
```

All commands follow the same pattern:

1. Parse flags from their argument slice using a command-specific `flag.FlagSet`.
2. Call `getConfigPath()` and `loadConfig()` to read the global config.
3. Apply the requested operation (read-only or mutating).
4. If mutating, call `saveConfig()`.
5. Print results and exit.

---

## 3. Constants

Defined at the top of the file. These are the only hardcoded strings that affect file paths.

| Constant | Value | Purpose |
|---|---|---|
| `configFilename` | `".mcp-servers.json"` | Name of the global MCP config file, resolved under `CLAUDE_HOME` or `~/.claude/` |
| `backupSuffix` | `".mcp-servers.json.bak"` | Name of the backup file written before every config write |
| `profileFilename` | `"mcp-profile.json"` | Name of the project profile file, stored inside `.claude/` in the project directory |

---

## 4. Types

### 4.1 rawConfig

```go
type rawConfig struct {
    MCPServers orderedMap `json:"mcpServers"`
}
```

The root structure of `.mcp-servers.json`. Contains a single field `MCPServers` of type `orderedMap`. Using `orderedMap` instead of `map[string]json.RawMessage` is the key decision that preserves section header positions during read and write.

---

### 4.2 orderedMap

```go
type orderedMap struct {
    keys   []string
    values map[string]json.RawMessage
}
```

A JSON object that preserves insertion order. This is the most architecturally significant type in the program.

**Why it exists.** Go's built-in `map` type is unordered. When the standard `encoding/json` package marshals a `map`, it sorts the keys alphabetically. The MCP config file uses `_comment_*` keys as in-place section headers; if keys were sorted, all comment keys would be grouped at the top and lose their meaning as category delimiters.

**Fields:**

| Field | Type | Purpose |
|---|---|---|
| `keys` | `[]string` | All keys in original document order, including `_comment_*` entries |
| `values` | `map[string]json.RawMessage` | Key-indexed raw JSON values |

#### UnmarshalJSON

Called by `json.Unmarshal` when decoding the `mcpServers` object. Instead of using the standard decoder (which would lose order), it creates a streaming `json.Decoder` directly on the raw bytes and reads one key-value token pair at a time.

Steps:
1. Consume the opening `{` token.
2. While `dec.More()` is true, read a string key token.
3. Decode the following value into `json.RawMessage` (preserves it verbatim).
4. Append the key to `o.keys` and store the value in `o.values`.
5. Consume the closing `}` token.

#### MarshalJSON

Called by `json.Marshal` when encoding the config back to JSON. Builds the output manually using a `strings.Builder` to enforce the order stored in `o.keys`.

Steps:
1. Write `{`.
2. For each key in `o.keys` (in original order), write the JSON-encoded key, a `:` separator, and the raw value bytes.
3. Write `}`.

This ensures `_comment_core` appears before `serena` in the output, just as it did in the input.

---

### 4.3 serverEntry

```go
type serverEntry struct {
    Name        string
    Category    string
    Description string
    Enabled     bool
}
```

A parsed, in-memory representation of a single MCP server. Produced by `parseServers`. Used for filtering, sorting, display, and profile operations. Read-only — mutations always go back through the raw `orderedMap`.

| Field | Source |
|---|---|
| `Name` | The JSON object key (e.g., `"serena"`) |
| `Category` | Derived from the nearest preceding `_comment_*` key |
| `Description` | The `description` field in the server's JSON value |
| `Enabled` | The `enabled` field in the server's JSON value |

---

### 4.4 serverRaw

```go
type serverRaw struct {
    Enabled     bool   `json:"enabled"`
    Description string `json:"description,omitempty"`
}
```

A minimal struct used when only the `enabled` and `description` fields need to be read from a server's raw JSON. Used inside `parseServers` and `cmdEnableDisable` to read the current enabled state before deciding whether a change is needed.

---

### 4.5 profile

```go
type profile struct {
    Servers []string `json:"servers"`
}
```

Represents the contents of a `.claude/mcp-profile.json` file. Contains a single field: an ordered list of server names that should be enabled for the project.

---

### 4.6 settingsConfig

```go
type settingsConfig struct {
    keys   []string
    values map[string]json.RawMessage
}
```

Represents the full `~/.claude/settings.json` file with ordered key preservation (same pattern as `orderedMap`). Only the `mcpServers` key is managed; all other keys (`permissions`, `model`, `enabledPlugins`, etc.) are preserved verbatim on write-back. Implements custom `MarshalJSON`/`UnmarshalJSON` to maintain key insertion order.

---

## 5. Config I/O

### 5.1 getConfigPath

```go
func getConfigPath() string
```

Returns the absolute path to `.mcp-servers.json`.

Resolution order:
1. If the `CLAUDE_HOME` environment variable is set, returns `$CLAUDE_HOME/.mcp-servers.json`.
2. Otherwise, resolves `~/.claude/.mcp-servers.json` using `os.UserHomeDir()`.

Calls `fatalf` if `os.UserHomeDir()` fails (only possible in very unusual system configurations).

---

### 5.2 loadConfig

```go
func loadConfig(path string) rawConfig
```

Reads and parses the MCP config file.

| Condition | Behaviour |
|---|---|
| File not found | `fatalf("MCP config not found at %s")` |
| Other read error | `fatalf("Cannot read %s: %v")` |
| JSON parse error | `fatalf("Failed to parse MCP config: %v")` |
| Success | Returns populated `rawConfig` |

The JSON parse invokes `orderedMap.UnmarshalJSON`, preserving key order.

---

### 5.3 saveConfig

```go
func saveConfig(path string, cfg rawConfig)
```

Writes a `rawConfig` back to disk. This is the only function that modifies the global config file on disk.

**Step 1 — Backup.** Copies the current file to the backup path (sibling file, `.mcp-servers.json.bak`) using `copyFile`. If the source doesn't exist (`os.ErrNotExist`), the backup step is skipped silently. Any other copy error is fatal.

**Step 2 — Marshal.** Calls `json.Marshal(cfg)`, which invokes `orderedMap.MarshalJSON` to produce compact, order-preserved JSON.

**Step 3 — Re-indent.** Calls `indentJSON` to add 2-space indentation and unescape HTML entities (`\u0026` → `&`, etc.). Appends a trailing newline.

**Step 4 — Lock and write.** Opens the file with `O_WRONLY|O_TRUNC` (overwrite in place), acquires `syscall.LOCK_EX` (exclusive advisory lock), writes the formatted string, and releases the lock when the file handle is closed.

| Error condition | Behaviour |
|---|---|
| Permission denied on open | `fatalf("Cannot write to %s: permission denied")` |
| Permission denied on write | `fatalf("Cannot write to %s: permission denied")` |
| Lock failure | `fatalf("Cannot acquire lock on %s: %v")` |
| Marshal/indent failure | `fatalf` with relevant message |

> **Note on advisory locking.** `syscall.Flock` provides advisory locking only. It prevents concurrent writes from other processes that also use `Flock`, but does not block a process that opens the file without locking.

---

### 5.4 getMtime

```go
func getMtime(path string) int64
```

Returns the file's modification time as a Unix nanosecond timestamp. Returns `0` if `os.Stat` fails.

Used by `cmdEnableDisable` and `cmdRestore` to detect whether the config file was modified externally between reading and writing. If the mtime changes between load and save, the operation re-reads the file and retries once.

---

### 5.5 copyFile

```go
func copyFile(src, dst string) error
```

Copies the contents of `src` to `dst` using `io.Copy`. Both files are opened via `os.Open` and `os.Create`. Returns any error encountered. Called by `saveConfig` and `saveSettings` for the backup step.

---

### 5.6 getSettingsPath

```go
func getSettingsPath() string
```

Returns the path to `settings.json`. Respects `CLAUDE_HOME` env var; defaults to `~/.claude/settings.json`.

---

### 5.7 loadSettings

```go
func loadSettings(path string) settingsConfig
```

Reads and parses `settings.json`. If the file does not exist, returns an empty `settingsConfig` with only a `mcpServers: {}` key (allowing sync to create the file on first use).

---

### 5.8 saveSettings

```go
func saveSettings(path string, sc settingsConfig)
```

Writes `settings.json` with 2-space indent, advisory file lock, and backup to `settings.json.bak` before writing. Uses `O_CREATE` so it can create the file if it doesn't exist.

---

### 5.9 getMCPServers

```go
func getMCPServers(sc settingsConfig) orderedMap
```

Extracts the `mcpServers` key from a `settingsConfig` and returns it as an `orderedMap`. Returns an empty `orderedMap` if the key is missing or unparseable.

---

### 5.10 setMCPServers

```go
func setMCPServers(sc *settingsConfig, om orderedMap)
```

Writes an `orderedMap` back into the `mcpServers` key of a `settingsConfig`. Adds the key if it doesn't exist in the key list.

---

### 5.11 stripEnabled

```go
func stripEnabled(raw json.RawMessage) json.RawMessage
```

Removes the `"enabled"` field from a server's raw JSON. Used when copying server entries from `.mcp-servers.json` (which has `enabled`) to `settings.json` (which does not use it).

---

### 5.12 syncToSettings

```go
func syncToSettings(enabled, disabled []string, cfg rawConfig)
```

Propagates enable/disable changes to `settings.json`:
- **Enabled servers**: Added to `settings.json` `mcpServers` with the `enabled` field stripped.
- **Disabled servers**: Removed from `settings.json` `mcpServers` entirely.
- Reads, modifies, and writes `settings.json` with backup.
- Called by `cmdEnableDisable` and `cmdRestore`.

---

## 6. Server Parsing

### 6.1 parseServers

```go
func parseServers(cfg rawConfig) []serverEntry
```

Converts the raw ordered map into a slice of `serverEntry` values. This is where category derivation happens.

**Category derivation algorithm:**

The function initialises `currentCategory` to `"uncategorized"`. It then iterates through `cfg.MCPServers.keys` in document order:

- If a key starts with `_comment_`, the suffix (e.g., `core` from `_comment_core`) becomes the new `currentCategory`. No entry is created.
- Otherwise, the key is a server name. A `serverEntry` is created with `Category` set to the current `currentCategory`.

This means each server inherits the category of the most recent comment key that preceded it in the file. The eight categories in the current config are:

| Comment key | Category name | Servers |
|---|---|---|
| `_comment_core` | `core` | serena, docker-agent, http-tester, semgrep, gitea, sandbox |
| `_comment_ai` | `ai` | sequential-thinking, memory, taskmaster-ai, magic |
| `_comment_dev` | `dev` | postgresql, typescript, react, filesystem |
| `_comment_testing` | `testing` | playwright, puppeteer |
| `_comment_data` | `data` | apache-doris, apache-pinot, algolia, alphavantage |
| `_comment_automation` | `automation` | apify |
| `_comment_external_plugins` | `external_plugins` | context7, firebase, github, stripe, supabase, gitlab, figma, slack, notion, linear, atlassian, sentry, vercel, asana |
| `_comment_reacher` | `reacher` | local-filesystem, local-database, local-docprocessor, local-orchestration |

---

### 6.2 setEnabled

```go
func setEnabled(raw json.RawMessage, enabled bool) json.RawMessage
```

Modifies the `"enabled"` field within a raw JSON server entry without touching any other fields.

Steps:
1. Unmarshal `raw` into a `map[string]json.RawMessage`.
2. Marshal the `enabled` boolean to a `json.RawMessage`.
3. Set `m["enabled"]` to the marshaled value.
4. Re-marshal the map.

On unmarshal error (malformed entry), returns the original `raw` unchanged.

> **Important.** Because step 4 re-marshals a `map`, the key order within an individual server entry is not preserved. This is acceptable because server entries are small and their internal key order does not affect correctness or category derivation. Only the order of entries within `mcpServers` matters, which is maintained by `orderedMap`.

---

## 7. Profile System

The profile system stores a per-project list of server names in `.claude/mcp-profile.json`. When `restore` is run, it sets the global config to exactly match the profile — disabling everything not listed and enabling everything that is.

### 7.1 getProfilePath

```go
func getProfilePath() string
```

Returns `<cwd>/.claude/mcp-profile.json` where `<cwd>` is the current working directory at the time the command is invoked. This means the profile is always relative to wherever the user ran `mcp-manager` from.

---

### 7.2 ensureProfileDir

```go
func ensureProfileDir(profilePath string)
```

Calls `os.MkdirAll` on the directory component of `profilePath`. Creates `.claude/` (and any parent directories) with permission `0755`. Called by `cmdSave` before writing a profile. Fatal on error.

---

### 7.3 loadProfile

```go
func loadProfile(path string) profile
```

Reads and parses a `.claude/mcp-profile.json` file. Fatal if the file cannot be read or if the JSON is invalid. The caller is responsible for checking existence before calling (using `os.Stat`).

---

### 7.4 saveProfile

```go
func saveProfile(path string, servers []string)
```

Marshals a `profile` struct with 2-space indent and writes it to `path`. Appends a trailing newline. Fatal on write error. Does not create a backup (profile files are low-risk and recoverable by running `save` again).

---

### 7.5 updateProfileIfPresent

```go
func updateProfileIfPresent(enabled, disabled []string)
```

Called at the end of every `enable` and `disable` operation to keep the project profile in sync with changes made to the global config.

**Returns immediately** if no `.claude/mcp-profile.json` exists in the current directory. This ensures the function is a no-op for users who are not using the profile feature.

**Algorithm when a profile exists:**

1. Load the existing profile.
2. Build a set of disabled names for O(1) lookup.
3. Iterate the existing profile's server list, keeping all names that are not in the disabled set.
4. Append any newly enabled names that are not already in the list.
5. Save the updated list back to the profile.
6. Print `(Updated .claude/mcp-profile.json)` to inform the user.

This preserves the original ordering of servers already in the profile and only appends new ones at the end.

---

## 8. Commands

Each command function takes `[]string` (the arguments after the command name) and parses them using a command-local `flag.FlagSet` with `flag.ExitOnError`.

### 8.1 cmdList

```go
func cmdList(args []string)
```

**Flags:** `--enabled`, `--disabled`, `--category <cat>`, `--json`

Loads the config, parses all servers, applies filters, sorts alphabetically by name, and prints a table or JSON array.

`--enabled` and `--disabled` are mutually exclusive — passing both results in `fatalf`.

`--category` matching calls `normaliseCategory` on both the argument and the server's category before comparing, allowing `external-plugins` and `external_plugins` to match the same servers.

**JSON output schema (array of objects):**
```json
[
  {
    "name": "serena",
    "status": "disabled",
    "category": "core",
    "description": "Intelligent code analysis with semantic symbol operations"
  }
]
```

---

### 8.2 cmdEnableDisable

```go
func cmdEnableDisable(targetEnabled bool, args []string)
```

Shared implementation for both `enable` and `disable`. The `targetEnabled` parameter determines which direction the change goes.

**Wildcard expansion.** If `args` contains exactly one element and that element is `"*"`, it is expanded to the full list of server names from `parseServers`. This allows `mcp-manager disable "*"` to stop all servers.

**`applyChanges` closure.** Defined internally and called up to twice (once normally, once on retry). For each requested server name:

| Condition | Result |
|---|---|
| Name not in `nameSet` | Added to `unknown`, error printed to stderr |
| Already in target state | Added to `alreadySet`, notice printed |
| State will change | `setEnabled` called, added to `changed` |

**Concurrent modification retry.** Before the first call to `applyChanges`, the file mtime is captured. After the changes are applied (but before saving), the mtime is checked again. If it differs:

1. The config is re-read from disk.
2. `changed` and `alreadySet` are cleared.
3. `applyChanges` is called again on the freshly loaded config.
4. If the mtime changed *again*, `fatalf` is called — the file is too active to write safely.

**Exit codes:**

| Scenario | Exit code |
|---|---|
| All servers changed successfully | 0 |
| All servers already in desired state | 0 |
| Any unknown server names | 1 |

**Profile sync.** After saving, calls `updateProfileIfPresent` with the lists of newly enabled and disabled servers.

---

### 8.3 cmdStatus

```go
func cmdStatus(args []string)
```

**Flags:** `--json`

**Summary mode** (no positional argument): Counts all servers, lists enabled ones by name (sorted).

**JSON output schema (summary):**
```json
{
  "total": 39,
  "enabled": 6,
  "disabled": 33,
  "enabled_servers": ["context7", "docker-agent", "filesystem", "gitea", "http-tester", "sequential-thinking"]
}
```

**Detail mode** (one positional argument — server name): Looks up the server in the raw config and prints all its fields. The category is determined by running `parseServers` and finding the matching entry.

Fields printed in detail mode: `name`, `category`, `status` (enabled/disabled), `description`, `command` or `url`, `args`, `cwd`, `timeout`, `env`.

---

### 8.4 cmdSearch

```go
func cmdSearch(args []string)
```

**Flags:** `--json`

**Positional argument:** `<keyword>` (required)

Performs case-insensitive substring search across all server names and descriptions. Uses `strings.ToLower` for normalisation. Returns results sorted alphabetically. Output format is identical to `cmdList`.

---

### 8.5 cmdSave

```go
func cmdSave(_ []string)
```

No flags. No positional arguments.

Collects all currently-enabled server names from the global config (sorted), calls `ensureProfileDir` to create `.claude/` if needed, and writes the list to `.claude/mcp-profile.json` in the current directory. Prints confirmation with the count and individual server names.

---

### 8.6 cmdRestore

```go
func cmdRestore(_ []string)
```

No flags. No positional arguments.

**Pre-conditions checked:**
- `.claude/mcp-profile.json` must exist in the current directory.
- Each server name in the profile must exist in the global config (unknown names print a warning and are skipped).

**`applyRestore` closure.** Iterates all keys in the global config. For each non-comment key:
- If the name is in `wantSet` (loaded from the profile), sets `enabled: true`.
- Otherwise, sets `enabled: false`.

This is an atomic reset — all servers not in the profile are disabled in the same write operation.

Applies the same concurrent modification detection as `cmdEnableDisable`.

---

### 8.7 cmdProfileShow

```go
func cmdProfileShow(args []string)
```

**Flags:** `--json`

Reads `.claude/mcp-profile.json` from the current directory and cross-references each entry against the global config. For each server in the profile, shows:

- Whether it exists in the global config (`yes` / `no (unknown)`)
- Its current enabled/disabled state in the global config

**JSON output schema:**
```json
{
  "profile": "/path/to/project/.claude/mcp-profile.json",
  "servers": ["context7", "docker-agent"]
}
```

---

### 8.8 cmdDoctor

```go
func cmdDoctor(args []string)
```

**Flags:** `--fix`, `--json`

Audits `.mcp-servers.json` against `settings.json` and reports three categories of divergence:

1. **Enabled but missing**: Server is `enabled: true` in registry but not present in `settings.json` (Claude Code won't load it).
2. **Disabled but present**: Server is `enabled: false` in registry but still in `settings.json` (Claude Code will load it despite being "disabled").
3. **Unmanaged**: Server exists in `settings.json` but has no entry in `.mcp-servers.json` (not tracked by mcp-manager).

With `--fix`, rebuilds `settings.json` `mcpServers` from registry truth: adds all enabled servers, removes all disabled and unmanaged servers.

**JSON output schema:**
```json
{
  "issues": [{"server": "playwright", "problem": "enabled in registry, missing from settings.json (won't run)"}],
  "count": 1,
  "status": "diverged"
}
```

---

## 9. Output Helpers

### 9.1 printTable

```go
func printTable(headers []string, rows [][]string)
```

Prints a plain-text table with no borders. Column widths are calculated as the maximum of the header width and the widest value in that column. Columns are separated by two spaces. A separator row of dashes is printed between the header and data rows.

---

### 9.2 printJSON

```go
func printJSON(v any)
```

Marshals `v` with `json.MarshalIndent` (2-space indent) and prints to stdout. Used by all commands when `--json` is passed.

---

### 9.3 fatalf

```go
func fatalf(format string, args ...any)
```

Prints `Error: <formatted message>` to `os.Stderr` and calls `os.Exit(1)`. The program does not attempt to clean up or recover. Used for all unrecoverable error conditions.

---

### 9.4 indentJSON

```go
func indentJSON(src []byte, dst *strings.Builder, prefix, indent string) error
```

Re-indents compact JSON bytes into human-readable form using `json.Indent`. After indenting, performs three string replacements to reverse HTML escaping:

| Escaped | Unescaped |
|---|---|
| `\u0026` | `&` |
| `\u003c` | `<` |
| `\u003e` | `>` |

**Why this is necessary.** `orderedMap.MarshalJSON` writes raw JSON values verbatim from the `values` map. Those raw values may contain strings with `&`, `<`, or `>` characters (e.g., server descriptions). The standard `json` package encodes these as `\u0026`, `\u003c`, and `\u003e` when a value passes through any marshal call. After `json.Indent` processes the compact output, these escaped sequences remain. Replacing them restores the characters to their readable form in the file on disk.

---

### 9.5 normaliseCategory

```go
func normaliseCategory(s string) string
```

Lowercases the string and replaces all hyphens with underscores. Used to make `--category` matching tolerant of both `external-plugins` and `external_plugins`.

---

### 9.6 boolStatus

```go
func boolStatus(b bool) string
```

Returns `"enabled"` if `b` is true, `"disabled"` otherwise. Used wherever a server's state is printed as text.

---

### 9.7 mustMarshal

```go
func mustMarshal(v any) json.RawMessage
```

Marshals `v` to JSON, discarding the error. Used in `cmdStatus` detail mode to build JSON map values (e.g., adding `"name"` and `"category"` fields to a server's raw JSON). Safe to use here because the inputs are always simple Go values (strings, booleans).

---

## 10. Entry Point

```go
func main()
```

Reads `os.Args`. If fewer than two arguments are provided, calls `usage()` and exits.

Routes `os.Args[1]` to the appropriate command function:

| Argument | Function called |
|---|---|
| `list` | `cmdList(rest)` |
| `enable` | `cmdEnableDisable(true, rest)` |
| `disable` | `cmdEnableDisable(false, rest)` |
| `status` | `cmdStatus(rest)` |
| `search` | `cmdSearch(rest)` |
| `save` | `cmdSave(rest)` |
| `restore` | `cmdRestore(rest)` |
| `profile show` | `cmdProfileShow(rest[1:])` |
| `doctor` | `cmdDoctor(rest)` |
| `--help`, `-h`, `help` | `usage()` |
| anything else | `fatalf("Unknown command...")` |

`usage()` prints the full command reference to stderr and exits with code 1.

---

## 11. Data Flow Diagrams

### Read path (list, status, search)

```
os.Args
  → main()
    → cmdList / cmdStatus / cmdSearch
      → getConfigPath()               # resolve ~/.claude/.mcp-servers.json
        → loadConfig(path)            # os.ReadFile + json.Unmarshal
          → orderedMap.UnmarshalJSON  # streaming token decode, preserves order
        → parseServers(cfg)           # linear scan, derives category from _comment_ keys
      → filter / sort / format
      → printTable / printJSON
```

### Write path (enable, disable)

```
os.Args
  → main()
    → cmdEnableDisable(targetEnabled, args)
      → getConfigPath()
      → getMtime(path)                # capture timestamp for concurrency check
      → loadConfig(path)
      → parseServers(cfg)             # build nameSet for validation
      → [wildcard expansion if args == ["*"]]
      → applyChanges(&cfg)            # setEnabled() on raw JSON values
      → [mtime check → retry if changed]
      → saveConfig(path, cfg)
          → copyFile(path, backupPath)        # backup
          → json.Marshal(cfg)                 # orderedMap.MarshalJSON
          → indentJSON(compact, &buf, ...)    # format + unescape
          → flock(LOCK_EX) + WriteString      # lock and write
      → syncToSettings(enabled, disabled, cfg)
          → getSettingsPath()                 # resolve ~/.claude/settings.json
          → loadSettings(path)                # read or create empty
          → getMCPServers(sc)                 # extract mcpServers orderedMap
          → [add enabled entries with stripEnabled]
          → [remove disabled entries]
          → setMCPServers(&sc, om)            # write back mcpServers key
          → saveSettings(path, sc)            # backup + lock + write
      → updateProfileIfPresent(enabled, disabled)
          → [return if .claude/mcp-profile.json not found]
          → loadProfile / rebuild list / saveProfile
```

### Restore path

```
os.Args
  → main()
    → cmdRestore()
      → getProfilePath()              # <cwd>/.claude/mcp-profile.json
      → loadProfile(path)             # read wanted server list
      → getConfigPath()
      → getMtime(path)
      → loadConfig(path)
      → parseServers(cfg)             # build validNames set
      → applyRestore(&cfg)            # set all servers enabled/disabled to match wantSet
      → [mtime check → retry if changed]
      → saveConfig(path, cfg)
      → syncToSettings(enabled, disabled, cfg)   # sync to settings.json
```

### Doctor path

```
os.Args
  → main()
    → cmdDoctor(args)
      → getConfigPath() → loadConfig()     # read registry
      → parseServers(cfg)                  # build server list
      → getSettingsPath() → loadSettings() # read settings.json
      → getMCPServers(sc)                  # extract settings mcpServers
      → [compare: enabled vs in-settings, disabled vs in-settings, untracked]
      → [report issues as table/JSON]
      → [if --fix: syncToSettings(allEnabled, allDisabled, cfg)]
```

---

## 12. Error Handling Reference

| Error condition | Location | Behaviour |
|---|---|---|
| Config file not found | `loadConfig` | `fatalf("MCP config not found at %s")` — exit 1 |
| Config file unreadable | `loadConfig` | `fatalf("Cannot read %s: %v")` — exit 1 |
| Config file malformed JSON | `loadConfig` | `fatalf("Failed to parse MCP config: %v")` — exit 1 |
| Cannot determine home directory | `getConfigPath` | `fatalf("Cannot determine home directory: %v")` — exit 1 |
| Backup copy fails | `saveConfig` | `fatalf("Cannot create backup: %v")` — exit 1 |
| Config file not writable | `saveConfig` | `fatalf("Cannot write to %s: permission denied")` — exit 1 |
| Cannot acquire file lock | `saveConfig` | `fatalf("Cannot acquire lock on %s: %v")` — exit 1 |
| Config modified twice during operation | `cmdEnableDisable`, `cmdRestore` | `fatalf("Config file changed during operation; aborting")` — exit 1 |
| Unknown server name (single) | `cmdEnableDisable` | Error printed to stderr, other valid names still processed — exit 1 |
| Unknown server name (restore) | `cmdRestore` | Warning printed to stderr, server skipped — continues |
| Profile file not found | `cmdRestore`, `cmdProfileShow` | `fatalf("No profile found at %s...")` — exit 1 |
| Profile file unreadable or malformed | `loadProfile` | `fatalf` — exit 1 |
| Cannot create `.claude/` directory | `ensureProfileDir` | `fatalf("Cannot create profile directory...")` — exit 1 |
| `--enabled` and `--disabled` both passed | `cmdList` | `fatalf("--enabled and --disabled are mutually exclusive")` — exit 1 |
| Settings file unreadable | `loadSettings` | `fatalf("Cannot read %s: %v")` — exit 1 |
| Settings file malformed JSON | `loadSettings` | `fatalf("Failed to parse settings: %v")` — exit 1 |
| Settings file not found | `loadSettings` | Returns empty `settingsConfig` with `mcpServers: {}` — sync creates the file |
| Settings backup fails | `saveSettings` | `fatalf("Cannot create settings backup: %v")` — exit 1 |
| Settings file not writable | `saveSettings` | `fatalf("Cannot open %s for writing: %v")` — exit 1 |
| Unknown command | `main` | `fatalf("Unknown command '%s'...")` — exit 1 |
| No arguments | `main` | `usage()` — exit 1 |
