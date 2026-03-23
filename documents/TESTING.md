# MCP Manager — Test Suite Guide

> Test file: `mcp_manager_test.go`
> Total tests: 101
> Framework: Go standard `testing` package — no external dependencies

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [Running the Tests](#2-running-the-tests)
3. [How the Test Suite Works](#3-how-the-test-suite-works)
4. [Test Groups and What They Cover](#4-test-groups-and-what-they-cover)
   - [orderedMap](#41-orderedmap-4-tests)
   - [parseServers](#42-parseservers-6-tests)
   - [setEnabled](#43-setenabled-4-tests)
   - [loadConfig / saveConfig](#44-loadconfig--saveconfig-7-tests)
   - [getMtime / copyFile](#45-getmtime--copyfile-5-tests)
   - [indentJSON](#46-indentjson-4-tests)
   - [Helpers](#47-helpers-6-tests)
   - [Profile I/O](#48-profile-io-7-tests)
   - [updateProfileIfPresent](#49-updateprofileifpresent-5-tests)
   - [getConfigPath](#410-getconfigpath-2-tests)
   - [Command: list](#411-command-list-7-tests)
   - [Command: enable](#412-command-enable-7-tests)
   - [Command: disable](#413-command-disable-5-tests)
   - [Command: status](#414-command-status-7-tests)
   - [Command: search](#415-command-search-6-tests)
   - [Command: save / restore](#416-command-save--restore-6-tests)
   - [Command: profile show](#417-command-profile-show-4-tests)
   - [Error handling](#418-error-handling-5-tests)
   - [Profile auto-update](#419-profile-auto-update-3-tests)
5. [Full Test Index](#5-full-test-index)
6. [Troubleshooting](#6-troubleshooting)

---

## 1. Prerequisites

Go 1.21 or later must be installed and on `PATH`.

```bash
go version
```

No external packages are required. The test suite uses only the Go standard library.

---

## 2. Running the Tests

All commands should be run from the project directory:

```bash
cd ~/Documents/projects/Personal/claude-config-reference/claude-mcp
```

### Run all tests

```bash
go test ./...
```

### Run all tests with verbose output (shows each test name and pass/fail)

```bash
go test -v ./...
```

### Run a single test by name

```bash
go test -v -run TestCmd_Enable_SingleServer ./...
```

### Run a group of related tests using a prefix

```bash
go test -v -run TestCmd_Enable ./...
go test -v -run TestParseServers ./...
go test -v -run TestOrderedMap ./...
```

### Run tests and show elapsed time per test

```bash
go test -v -count=1 ./...
```

> `-count=1` disables test result caching, ensuring every test runs from scratch. Recommended when verifying fixes.

### Run tests with a timeout (default is 10 minutes)

```bash
go test -timeout 60s ./...
```

### Check test coverage

```bash
go test -cover ./...
```

For a full HTML coverage report:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

---

## 3. How the Test Suite Works

### Two testing strategies

The suite uses two approaches depending on what is being tested:

**1. In-process unit tests** — Used for pure functions that do not call `os.Exit`. These tests call functions directly, pass real file paths into a `t.TempDir()` directory, and inspect return values. Examples: `orderedMap`, `parseServers`, `setEnabled`, `loadConfig`, `saveConfig`, all helpers.

**2. Subprocess integration tests** — Used for command functions (`cmdList`, `cmdEnableDisable`, etc.) which call `fatalf` → `os.Exit(1)` on failure. If called in-process, a failing code path would kill the entire test binary. Instead, the suite compiles the binary once in `TestMain`, then each test runs the binary as a subprocess via `os/exec`, capturing stdout, stderr, and exit code separately.

### TestMain — binary compilation

`TestMain` is a special function that runs before any test. It compiles `mcp_manager.go` into a temporary binary (`/tmp/mcp-manager-bin-*/mcp-manager`) and stores its path in `binaryPath`. All subprocess tests use this pre-compiled binary. The binary is deleted automatically when the test run ends.

If compilation fails, the entire test run aborts immediately with a panic message.

### Isolation

Every test that touches the filesystem uses `t.TempDir()` to create a private temporary directory. This directory is automatically deleted when the test finishes, whether it passes or fails. No test reads from or writes to `~/.claude/.mcp-servers.json` — the `CLAUDE_HOME` environment variable is set to the temp directory instead.

### The minimal fixture

Most tests use a shared JSON fixture called `minimalConfig`. It contains:

- Two categories: `core` (via `_comment_core`) and `external` (via `_comment_external`)
- Five servers:
  - `alpha` — core, **enabled**
  - `beta` — core, **disabled**
  - `gamma` — core, **enabled**, description contains "database"
  - `delta` — external, **disabled**
  - `epsilon` — external, **enabled**, description contains `&` and `<>` (for HTML escaping tests)

This fixture is small enough to reason about precisely but covers all the key cases: multiple categories, mixed enabled states, and special characters.

---

## 4. Test Groups and What They Cover

### 4.1 orderedMap (4 tests)

Tests the custom JSON type that preserves key insertion order.

| Test | What it checks |
|---|---|
| `TestOrderedMap_PreservesInsertionOrder` | Keys are stored in the order they appear in the JSON document, not alphabetically |
| `TestOrderedMap_MarshalPreservesOrder` | When marshaled back to JSON, keys appear in the original order |
| `TestOrderedMap_RoundTrip` | Unmarshal → Marshal produces byte-for-byte identical output |
| `TestOrderedMap_CommentKeysPreserved` | `_comment_core` and `_comment_external` keys appear at exactly the right positions after parsing `minimalConfig` |

---

### 4.2 parseServers (6 tests)

Tests the function that converts the raw ordered map into a slice of `serverEntry` structs.

| Test | What it checks |
|---|---|
| `TestParseServers_CategoryDerivedFromCommentKeys` | Each server inherits the category from the nearest preceding `_comment_*` key |
| `TestParseServers_CommentKeysExcluded` | No entry with a name starting with `_comment_` appears in the results |
| `TestParseServers_EnabledFlagParsed` | The `enabled` boolean is read correctly from each server's JSON |
| `TestParseServers_DescriptionParsed` | The `description` string is read correctly |
| `TestParseServers_UncategorizedFallback` | A config with no comment headers assigns category `"uncategorized"` to all servers |
| `TestParseServers_Count` | `minimalConfig` produces exactly 5 entries (the 2 comment keys are not counted) |

---

### 4.3 setEnabled (4 tests)

Tests the function that modifies the `enabled` field in a server's raw JSON without touching anything else.

| Test | What it checks |
|---|---|
| `TestSetEnabled_SetsTrue` | Sets `enabled` to `true` correctly |
| `TestSetEnabled_SetsFalse` | Sets `enabled` to `false` correctly |
| `TestSetEnabled_OtherFieldsUntouched` | `command`, `args`, `description`, and `timeout` are identical before and after |
| `TestSetEnabled_InvalidJSON_ReturnsOriginal` | If the raw JSON cannot be parsed, the original bytes are returned unchanged |

---

### 4.4 loadConfig / saveConfig (7 tests)

Tests reading from and writing to `.mcp-servers.json`.

| Test | What it checks |
|---|---|
| `TestLoadConfig_ReadsValidFile` | A valid config file is parsed without error |
| `TestLoadConfig_PreservesKeyOrder` | After loading `minimalConfig`, the keys are in the exact original document order |
| `TestSaveConfig_CreatesBackup` | A `.mcp-servers.json.bak` file is created in the same directory as the config before any write |
| `TestSaveConfig_PreservesKeyOrder` | After a save, reloading the file produces keys in the same original order |
| `TestSaveConfig_WritesValidJSON` | The file on disk after a save is parseable as valid JSON |
| `TestSaveConfig_ModificationPersists` | A change made in memory (enabling `beta`) is correctly reflected when the file is re-read |
| `TestSaveConfig_UnescapesHTMLEntities` | After saving, the file contains literal `&`, `<`, and `>` characters — not `\u0026`, `\u003c`, `\u003e` |

---

### 4.5 getMtime / copyFile (5 tests)

Tests the file-system utility functions used for concurrency detection and backup.

| Test | What it checks |
|---|---|
| `TestGetMtime_ExistingFile` | Returns a non-zero nanosecond timestamp for a real file |
| `TestGetMtime_MissingFile` | Returns `0` for a path that does not exist |
| `TestGetMtime_ChangesAfterWrite` | The timestamp is strictly greater after the file has been written |
| `TestCopyFile_ContentMatches` | The destination file contains exactly the same bytes as the source |
| `TestCopyFile_MissingSource` | Returns an error if the source file does not exist |

---

### 4.6 indentJSON (4 tests)

Tests the function that formats compact JSON with 2-space indentation and reverses HTML escaping.

| Test | What it checks |
|---|---|
| `TestIndentJSON_BasicFormatting` | Output contains newlines and indentation |
| `TestIndentJSON_UnescapesAmpersand` | `\u0026` is replaced with `&` in the output |
| `TestIndentJSON_UnescapesAngleBrackets` | `\u003c` and `\u003e` are replaced with `<` and `>` |
| `TestIndentJSON_InvalidInput` | Returns an error when given bytes that are not valid JSON |

---

### 4.7 Helpers (6 tests)

Tests the small utility functions.

| Test | What it checks |
|---|---|
| `TestNormaliseCategory_Lowercase` | `"CORE"` → `"core"` |
| `TestNormaliseCategory_HyphenToUnderscore` | `"external-plugins"` → `"external_plugins"` |
| `TestNormaliseCategory_AlreadyNormal` | `"core"` → `"core"` (no change) |
| `TestNormaliseCategory_Mixed` | `"External-Plugins"` → `"external_plugins"` |
| `TestBoolStatus_True` | `true` → `"enabled"` |
| `TestBoolStatus_False` | `false` → `"disabled"` |
| `TestMustMarshal_String` | A string value produces correct JSON (with quotes) |
| `TestMustMarshal_Bool` | A boolean produces `true` or `false` without quotes |

---

### 4.8 Profile I/O (7 tests)

Tests reading, writing, and directory creation for `.claude/mcp-profile.json`.

| Test | What it checks |
|---|---|
| `TestSaveAndLoadProfile_RoundTrip` | A list of server names written with `saveProfile` is returned identically by `loadProfile` |
| `TestSaveProfile_ValidJSON` | The file on disk is valid JSON |
| `TestSaveProfile_EmptyList` | An empty server list is saved and loaded correctly |
| `TestEnsureProfileDir_CreatesDirectory` | The `.claude/` subdirectory is created if it does not exist |
| `TestEnsureProfileDir_Idempotent` | Calling `ensureProfileDir` twice on the same path does not error |

---

### 4.9 updateProfileIfPresent (5 tests)

Tests the function that keeps the project profile in sync when servers are enabled or disabled.

| Test | What it checks |
|---|---|
| `TestUpdateProfileIfPresent_NoProfileIsNoop` | When no `.claude/mcp-profile.json` exists in the current directory, nothing happens and no file is created |
| `TestUpdateProfileIfPresent_AddsEnabled` | A newly enabled server is appended to the profile |
| `TestUpdateProfileIfPresent_RemovesDisabled` | A newly disabled server is removed from the profile |
| `TestUpdateProfileIfPresent_PreservesOrder` | The existing order of servers in the profile is maintained; new entries are appended at the end |
| `TestUpdateProfileIfPresent_NoDuplicates` | Enabling a server that is already in the profile does not add it a second time |

---

### 4.10 getConfigPath (2 tests)

Tests config path resolution.

| Test | What it checks |
|---|---|
| `TestGetConfigPath_ClaudeHomeOverride` | When `CLAUDE_HOME` is set, the config path uses that directory |
| `TestGetConfigPath_DefaultsToHomeDotClaude` | Without `CLAUDE_HOME`, the path resolves to `~/.claude/.mcp-servers.json` |

---

### 4.11 Command: list (7 tests)

Subprocess tests for the `list` command.

| Test | What it checks |
|---|---|
| `TestCmd_List_AllServers` | All 5 server names appear in the output |
| `TestCmd_List_SortedAlphabetically` | The server rows appear in alphabetical order by name |
| `TestCmd_List_FilterEnabled` | `--enabled` shows only `alpha`, `gamma`, `epsilon`; hides `beta`, `delta` |
| `TestCmd_List_FilterDisabled` | `--disabled` shows only `beta`, `delta`; hides the enabled servers |
| `TestCmd_List_FilterCategory` | `--category core` shows only the three core servers |
| `TestCmd_List_CategoryHyphenNormalised` | The same category with and without hyphens returns the same result |
| `TestCmd_List_JSON` | `--json` produces a valid JSON array; each object has `name`, `status`, `category`, `description` fields |

---

### 4.12 Command: enable (7 tests)

Subprocess tests for the `enable` command.

| Test | What it checks |
|---|---|
| `TestCmd_Enable_SingleServer` | Enabling `beta` prints `Enabled: beta` and sets `enabled: true` in the file |
| `TestCmd_Enable_MultipleServers` | Enabling `beta` and `delta` in one command enables both in the file |
| `TestCmd_Enable_AlreadyEnabled` | Enabling an already-enabled server prints `already enabled` and exits 0 |
| `TestCmd_Enable_UnknownServer` | An unrecognised server name prints an error to stderr and exits 1 |
| `TestCmd_Enable_PartialSuccess` | A mix of valid and invalid names enables the valid ones, errors on the invalid ones, and exits 1 |
| `TestCmd_Enable_Wildcard` | `enable "*"` enables every server in the config |
| `TestCmd_Enable_CreatesBackup` | A `.mcp-servers.json.bak` file is created after enabling |

---

### 4.13 Command: disable (5 tests)

Subprocess tests for the `disable` command.

| Test | What it checks |
|---|---|
| `TestCmd_Disable_SingleServer` | Disabling `alpha` prints `Disabled: alpha` and sets `enabled: false` in the file |
| `TestCmd_Disable_AlreadyDisabled` | Disabling an already-disabled server prints `already disabled` and exits 0 |
| `TestCmd_Disable_Wildcard` | `disable "*"` disables every server in the config |
| `TestCmd_Disable_UnknownServer` | An unrecognised server name exits 1 with an error |
| `TestCmd_EnableDisable_RoundTrip` | Enable then disable leaves the server in the disabled state |
| `TestCmd_Enable_OnlyModifiesEnabledField` | After enabling `beta`, its `command`, `args`, `description`, and `timeout` fields are byte-for-byte identical |

---

### 4.14 Command: status (7 tests)

Subprocess tests for the `status` command.

| Test | What it checks |
|---|---|
| `TestCmd_Status_Summary` | Output contains `Enabled:` and `Disabled:` labels |
| `TestCmd_Status_CorrectCounts` | `--json` reports `total: 5`, `enabled: 3`, `disabled: 2` matching the fixture |
| `TestCmd_Status_SingleServer` | `status alpha` shows the server name, category, and enabled status |
| `TestCmd_Status_SingleServer_JSON` | `status --json alpha` produces valid JSON with `name` and `category` fields |
| `TestCmd_Status_UnknownServer` | `status nosuchserver` exits 1 with an error |
| `TestCmd_Status_NoneEnabled` | After disabling everything, output contains `(none)` |
| `TestCmd_Status_JSON_EnabledServersList` | The `enabled_servers` array in JSON output matches exactly `[alpha, epsilon, gamma]` |

---

### 4.15 Command: search (6 tests)

Subprocess tests for the `search` command.

| Test | What it checks |
|---|---|
| `TestCmd_Search_ByName` | Searching `alpha` returns `alpha` |
| `TestCmd_Search_ByDescription` | Searching `database` returns `gamma` (whose description contains that word) |
| `TestCmd_Search_CaseInsensitive` | Searching `ALPHA` returns the same result as searching `alpha` |
| `TestCmd_Search_NoMatches` | Searching an unknown term prints `No servers found` and exits 0 |
| `TestCmd_Search_JSON` | `--json` produces valid JSON |
| `TestCmd_Search_ResultsSortedAlphabetically` | Multiple results are sorted by name |

---

### 4.16 Command: save / restore (6 tests)

Subprocess tests for the profile save and restore workflow.

| Test | What it checks |
|---|---|
| `TestCmd_Save_CreatesProfile` | `save` creates `.claude/mcp-profile.json` in the current directory |
| `TestCmd_Save_ProfileContainsEnabledServers` | The profile contains exactly `[alpha, epsilon, gamma]` — the enabled servers in the fixture |
| `TestCmd_Restore_EnablesProfileServers` | `restore` enables only `beta` and `delta` (from a hand-crafted profile) and disables everything else |
| `TestCmd_Restore_NoProfile` | `restore` without a profile file exits 1 with `No profile found` |
| `TestCmd_Save_Restore_RoundTrip` | Full cycle: save → switch away and scramble global config from a different directory → restore → config matches original saved state |
| `TestCmd_Restore_UnknownProfileServer_Warned` | A server name in the profile that does not exist in the global config prints a warning to stderr but the command still exits 0 |

---

### 4.17 Command: profile show (4 tests)

Subprocess tests for `profile show`.

| Test | What it checks |
|---|---|
| `TestCmd_ProfileShow_DisplaysServers` | Server names from the profile appear in the output |
| `TestCmd_ProfileShow_JSON` | `--json` produces valid JSON with `profile` and `servers` fields |
| `TestCmd_ProfileShow_NoProfile` | Without a profile file, exits 1 with `No profile` error |
| `TestCmd_ProfileShow_UnknownServerMarked` | A server in the profile that is not in the global config is shown as `no (unknown)` |

---

### 4.18 Error handling (5 tests)

Subprocess tests for global error conditions.

| Test | What it checks |
|---|---|
| `TestCmd_MissingConfig` | When the config file does not exist, exits 1 with `MCP config not found` |
| `TestCmd_MalformedConfig` | When the config file contains invalid JSON, exits 1 with `Failed to parse MCP config` |
| `TestCmd_UnknownCommand` | An unrecognised command exits 1 with `Unknown command` |
| `TestCmd_NoArguments` | Running with no arguments exits 1 and prints usage text |
| `TestCmd_ClaudeHomeOverride` | `CLAUDE_HOME` pointing to a valid directory reads from that directory's config |

---

### 4.19 Profile auto-update (3 tests)

Subprocess tests verifying that `enable` and `disable` keep the project profile in sync automatically.

| Test | What it checks |
|---|---|
| `TestCmd_Enable_UpdatesExistingProfile` | When a profile exists and `beta` is enabled, `beta` is added to the profile |
| `TestCmd_Disable_UpdatesExistingProfile` | When a profile exists and `alpha` is disabled, `alpha` is removed from the profile |
| `TestCmd_Enable_NoProfileNoUpdate` | When no profile exists, enabling a server does not create one |

---

## 5. Full Test Index

| # | Test name | Group |
|---|---|---|
| 1 | `TestOrderedMap_PreservesInsertionOrder` | orderedMap |
| 2 | `TestOrderedMap_MarshalPreservesOrder` | orderedMap |
| 3 | `TestOrderedMap_RoundTrip` | orderedMap |
| 4 | `TestOrderedMap_CommentKeysPreserved` | orderedMap |
| 5 | `TestParseServers_CategoryDerivedFromCommentKeys` | parseServers |
| 6 | `TestParseServers_CommentKeysExcluded` | parseServers |
| 7 | `TestParseServers_EnabledFlagParsed` | parseServers |
| 8 | `TestParseServers_DescriptionParsed` | parseServers |
| 9 | `TestParseServers_UncategorizedFallback` | parseServers |
| 10 | `TestParseServers_Count` | parseServers |
| 11 | `TestSetEnabled_SetsTrue` | setEnabled |
| 12 | `TestSetEnabled_SetsFalse` | setEnabled |
| 13 | `TestSetEnabled_OtherFieldsUntouched` | setEnabled |
| 14 | `TestSetEnabled_InvalidJSON_ReturnsOriginal` | setEnabled |
| 15 | `TestLoadConfig_ReadsValidFile` | loadConfig / saveConfig |
| 16 | `TestLoadConfig_PreservesKeyOrder` | loadConfig / saveConfig |
| 17 | `TestSaveConfig_CreatesBackup` | loadConfig / saveConfig |
| 18 | `TestSaveConfig_PreservesKeyOrder` | loadConfig / saveConfig |
| 19 | `TestSaveConfig_WritesValidJSON` | loadConfig / saveConfig |
| 20 | `TestSaveConfig_ModificationPersists` | loadConfig / saveConfig |
| 21 | `TestSaveConfig_UnescapesHTMLEntities` | loadConfig / saveConfig |
| 22 | `TestGetMtime_ExistingFile` | getMtime / copyFile |
| 23 | `TestGetMtime_MissingFile` | getMtime / copyFile |
| 24 | `TestGetMtime_ChangesAfterWrite` | getMtime / copyFile |
| 25 | `TestCopyFile_ContentMatches` | getMtime / copyFile |
| 26 | `TestCopyFile_MissingSource` | getMtime / copyFile |
| 27 | `TestIndentJSON_BasicFormatting` | indentJSON |
| 28 | `TestIndentJSON_UnescapesAmpersand` | indentJSON |
| 29 | `TestIndentJSON_UnescapesAngleBrackets` | indentJSON |
| 30 | `TestIndentJSON_InvalidInput` | indentJSON |
| 31 | `TestNormaliseCategory_Lowercase` | Helpers |
| 32 | `TestNormaliseCategory_HyphenToUnderscore` | Helpers |
| 33 | `TestNormaliseCategory_AlreadyNormal` | Helpers |
| 34 | `TestNormaliseCategory_Mixed` | Helpers |
| 35 | `TestBoolStatus_True` | Helpers |
| 36 | `TestBoolStatus_False` | Helpers |
| 37 | `TestMustMarshal_String` | Helpers |
| 38 | `TestMustMarshal_Bool` | Helpers |
| 39 | `TestSaveAndLoadProfile_RoundTrip` | Profile I/O |
| 40 | `TestSaveProfile_ValidJSON` | Profile I/O |
| 41 | `TestSaveProfile_EmptyList` | Profile I/O |
| 42 | `TestEnsureProfileDir_CreatesDirectory` | Profile I/O |
| 43 | `TestEnsureProfileDir_Idempotent` | Profile I/O |
| 44 | `TestUpdateProfileIfPresent_NoProfileIsNoop` | updateProfileIfPresent |
| 45 | `TestUpdateProfileIfPresent_AddsEnabled` | updateProfileIfPresent |
| 46 | `TestUpdateProfileIfPresent_RemovesDisabled` | updateProfileIfPresent |
| 47 | `TestUpdateProfileIfPresent_PreservesOrder` | updateProfileIfPresent |
| 48 | `TestUpdateProfileIfPresent_NoDuplicates` | updateProfileIfPresent |
| 49 | `TestGetConfigPath_ClaudeHomeOverride` | getConfigPath |
| 50 | `TestGetConfigPath_DefaultsToHomeDotClaude` | getConfigPath |
| 51 | `TestCmd_List_AllServers` | list |
| 52 | `TestCmd_List_SortedAlphabetically` | list |
| 53 | `TestCmd_List_FilterEnabled` | list |
| 54 | `TestCmd_List_FilterDisabled` | list |
| 55 | `TestCmd_List_FilterCategory` | list |
| 56 | `TestCmd_List_CategoryHyphenNormalised` | list |
| 57 | `TestCmd_List_JSON` | list |
| 58 | `TestCmd_Enable_SingleServer` | enable |
| 59 | `TestCmd_Enable_MultipleServers` | enable |
| 60 | `TestCmd_Enable_AlreadyEnabled` | enable |
| 61 | `TestCmd_Enable_UnknownServer` | enable |
| 62 | `TestCmd_Enable_PartialSuccess` | enable |
| 63 | `TestCmd_Enable_Wildcard` | enable |
| 64 | `TestCmd_Enable_CreatesBackup` | enable |
| 65 | `TestCmd_Disable_SingleServer` | disable |
| 66 | `TestCmd_Disable_AlreadyDisabled` | disable |
| 67 | `TestCmd_Disable_Wildcard` | disable |
| 68 | `TestCmd_Disable_UnknownServer` | disable |
| 69 | `TestCmd_EnableDisable_RoundTrip` | disable |
| 70 | `TestCmd_Enable_OnlyModifiesEnabledField` | disable |
| 71 | `TestCmd_Status_Summary` | status |
| 72 | `TestCmd_Status_CorrectCounts` | status |
| 73 | `TestCmd_Status_SingleServer` | status |
| 74 | `TestCmd_Status_SingleServer_JSON` | status |
| 75 | `TestCmd_Status_UnknownServer` | status |
| 76 | `TestCmd_Status_NoneEnabled` | status |
| 77 | `TestCmd_Status_JSON_EnabledServersList` | status |
| 78 | `TestCmd_Search_ByName` | search |
| 79 | `TestCmd_Search_ByDescription` | search |
| 80 | `TestCmd_Search_CaseInsensitive` | search |
| 81 | `TestCmd_Search_NoMatches` | search |
| 82 | `TestCmd_Search_JSON` | search |
| 83 | `TestCmd_Search_ResultsSortedAlphabetically` | search |
| 84 | `TestCmd_Save_CreatesProfile` | save / restore |
| 85 | `TestCmd_Save_ProfileContainsEnabledServers` | save / restore |
| 86 | `TestCmd_Restore_EnablesProfileServers` | save / restore |
| 87 | `TestCmd_Restore_NoProfile` | save / restore |
| 88 | `TestCmd_Save_Restore_RoundTrip` | save / restore |
| 89 | `TestCmd_Restore_UnknownProfileServer_Warned` | save / restore |
| 90 | `TestCmd_ProfileShow_DisplaysServers` | profile show |
| 91 | `TestCmd_ProfileShow_JSON` | profile show |
| 92 | `TestCmd_ProfileShow_NoProfile` | profile show |
| 93 | `TestCmd_ProfileShow_UnknownServerMarked` | profile show |
| 94 | `TestCmd_MissingConfig` | Error handling |
| 95 | `TestCmd_MalformedConfig` | Error handling |
| 96 | `TestCmd_UnknownCommand` | Error handling |
| 97 | `TestCmd_NoArguments` | Error handling |
| 98 | `TestCmd_ClaudeHomeOverride` | Error handling |
| 99 | `TestCmd_Enable_UpdatesExistingProfile` | Profile auto-update |
| 100 | `TestCmd_Disable_UpdatesExistingProfile` | Profile auto-update |
| 101 | `TestCmd_Enable_NoProfileNoUpdate` | Profile auto-update |

---

## 6. Troubleshooting

### Build failed in TestMain

```
panic: failed to build test binary: ...
```

The `go build` step inside `TestMain` failed. Common causes:

- **Syntax error in `mcp_manager.go`** — check the compiler output shown in the panic message, fix the error, and re-run.
- **Missing `go.mod`** — run `go mod init claude-mcp` from the project directory.
- **Wrong Go version** — run `go version` and confirm it is 1.21 or later.

---

### A single test fails unexpectedly

Run it in isolation to see the full output:

```bash
go test -v -count=1 -run TestName ./...
```

Replace `TestName` with the exact test name. The `-count=1` flag ensures the test is not served from cache.

---

### Tests that use `os.Chdir` interfere with each other

Several profile tests call `os.Chdir` to set the working directory for `updateProfileIfPresent`. If a test panics mid-run without restoring the directory, subsequent tests may run in an unexpected location.

To detect this, run the affected group in isolation:

```bash
go test -v -count=1 -run TestUpdateProfileIfPresent ./...
```

If the issue persists, check that every `os.Chdir` call in the test has a corresponding `defer os.Chdir(orig)` immediately after.

---

### Subprocess tests fail with "no such file or directory" for the binary

`binaryPath` is set in `TestMain`. If `TestMain` itself is not running (e.g., you are running tests from an IDE that bypasses it), the binary will not exist.

Always run tests via the command line:

```bash
go test -v ./...
```

---

### `TestGetMtime_ChangesAfterWrite` fails intermittently

This test checks that the file modification time increases after a write. On some filesystems (particularly those with 1-second mtime resolution, such as older ext3 mounts or certain network filesystems), the write may not produce a measurable mtime change within the same second.

If you see this on a standard Linux filesystem with ext4, it is a genuine bug — report it.

If you are working on a network or FUSE filesystem, the test may be unreliable in that environment.

---

### Config file is corrupted after a failed test

The test suite always uses `t.TempDir()` directories, never `~/.claude/.mcp-servers.json`. If you see corruption in your real config file, it was not caused by the tests.

If your real config needs restoring, the backup is at:

```
~/.claude/.mcp-servers.json.bak
```

Copy it back:

```bash
cp ~/.claude/.mcp-servers.json.bak ~/.claude/.mcp-servers.json
```

---

### A test prints `(Updated .claude/mcp-profile.json)` to the terminal

This is normal. `updateProfileIfPresent` prints this line to stdout when it modifies a profile. In tests that call this function in-process (the `TestUpdateProfileIfPresent_*` group), the line appears in the test runner output. It does not indicate a failure.

To suppress it during test runs:

```bash
go test -count=1 ./... 2>/dev/null
```

---

### Coverage is below 100%

Some code paths cannot be reached by automated tests:

- `fatalf` exit paths in `getConfigPath` (requires a system with no home directory)
- `saveConfig` permission-denied paths (requires running as root or modifying file permissions)
- The double-mtime-change retry path in `cmdEnableDisable` and `cmdRestore` (requires a concurrent writer)

These are acceptable gaps. The critical logic paths — JSON parsing, category derivation, enable/disable mutation, profile sync — are all covered.
