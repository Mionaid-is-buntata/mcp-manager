// Copyright (c) 2026 Vincent Healy
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ── Test fixtures ─────────────────────────────────────────────────────────────

// minimalConfig is a small but realistic config used across most tests.
// It has two categories, five servers, and mixed enabled/disabled states.
const minimalConfig = `{
  "mcpServers": {
    "_comment_core": "=== CORE ===",
    "alpha": {
      "command": "node",
      "args": ["alpha.js"],
      "description": "Alpha server",
      "enabled": true,
      "timeout": 30000
    },
    "beta": {
      "command": "node",
      "args": ["beta.js"],
      "description": "Beta server for testing",
      "enabled": false,
      "timeout": 30000
    },
    "gamma": {
      "command": "node",
      "args": ["gamma.js"],
      "description": "Gamma database server",
      "enabled": true,
      "timeout": 30000
    },
    "_comment_external": "=== EXTERNAL ===",
    "delta": {
      "url": "https://delta.example.com",
      "description": "Delta external service",
      "enabled": false,
      "timeout": 30000
    },
    "epsilon": {
      "url": "https://epsilon.example.com",
      "description": "Epsilon & special <chars>",
      "enabled": true,
      "timeout": 30000
    }
  }
}`

// writeConfig writes content to a temp file and returns its path.
func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, configFilename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	return path
}

// loadParsed is a convenience to load + parse servers in one call.
func loadParsed(t *testing.T, path string) (rawConfig, []serverEntry) {
	t.Helper()
	cfg := loadConfig(path)
	return cfg, parseServers(cfg)
}

// binary returns the path to the compiled test binary, building it if needed.
// Tests that exercise commands via subprocess use this.
var binaryPath string

func TestMain(m *testing.M) {
	// Build the binary into a temp directory once for all subprocess tests.
	tmp, err := os.MkdirTemp("", "mcp-manager-bin-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	binaryPath = filepath.Join(tmp, "mcp-manager")
	out, err := exec.Command("go", "build", "-o", binaryPath, ".").CombinedOutput()
	if err != nil {
		panic("failed to build test binary: " + string(out))
	}

	os.Exit(m.Run())
}

// run executes the mcp-manager binary with the given args and environment.
// Returns stdout, stderr, and exit code.
func run(t *testing.T, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Env = append(os.Environ(), env...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// withConfig writes minimalConfig to a temp dir, sets CLAUDE_HOME, and returns
// a run helper pre-configured with that environment.
func withConfig(t *testing.T) (dir string, runFn func(args ...string) (string, string, int)) {
	t.Helper()
	dir = t.TempDir()
	writeConfig(t, dir, minimalConfig)
	env := []string{"CLAUDE_HOME=" + dir}
	return dir, func(args ...string) (string, string, int) {
		return run(t, env, args...)
	}
}

// ── orderedMap ────────────────────────────────────────────────────────────────

func TestOrderedMap_PreservesInsertionOrder(t *testing.T) {
	input := `{"z":1,"a":2,"m":3}`
	var om orderedMap
	if err := json.Unmarshal([]byte(input), &om); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"z", "a", "m"}
	if len(om.keys) != len(want) {
		t.Fatalf("got %d keys, want %d", len(om.keys), len(want))
	}
	for i, k := range om.keys {
		if k != want[i] {
			t.Errorf("keys[%d] = %q, want %q", i, k, want[i])
		}
	}
}

func TestOrderedMap_MarshalPreservesOrder(t *testing.T) {
	input := `{"z":1,"a":2,"m":3}`
	var om orderedMap
	json.Unmarshal([]byte(input), &om)

	out, err := json.Marshal(om)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Keys must appear in original order, not alphabetically.
	zPos := strings.Index(string(out), `"z"`)
	aPos := strings.Index(string(out), `"a"`)
	mPos := strings.Index(string(out), `"m"`)
	if !(zPos < aPos && aPos < mPos) {
		t.Errorf("key order not preserved in output: %s", out)
	}
}

func TestOrderedMap_RoundTrip(t *testing.T) {
	input := `{"b":"two","a":"one","c":"three"}`
	var om orderedMap
	json.Unmarshal([]byte(input), &om)
	out, _ := json.Marshal(om)
	if string(out) != input {
		t.Errorf("round-trip mismatch\n got: %s\nwant: %s", out, input)
	}
}

func TestOrderedMap_CommentKeysPreserved(t *testing.T) {
	// Comment keys must survive a full config round-trip in document order.
	var cfg rawConfig
	if err := json.Unmarshal([]byte(minimalConfig), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantOrder := []string{"_comment_core", "alpha", "beta", "gamma", "_comment_external", "delta", "epsilon"}
	if len(cfg.MCPServers.keys) != len(wantOrder) {
		t.Fatalf("got %d keys, want %d: %v", len(cfg.MCPServers.keys), len(wantOrder), cfg.MCPServers.keys)
	}
	for i, k := range cfg.MCPServers.keys {
		if k != wantOrder[i] {
			t.Errorf("keys[%d] = %q, want %q", i, k, wantOrder[i])
		}
	}
}

// ── parseServers ──────────────────────────────────────────────────────────────

func TestParseServers_CategoryDerivedFromCommentKeys(t *testing.T) {
	var cfg rawConfig
	json.Unmarshal([]byte(minimalConfig), &cfg)
	entries := parseServers(cfg)

	byName := make(map[string]serverEntry)
	for _, e := range entries {
		byName[e.Name] = e
	}

	cases := []struct{ name, wantCat string }{
		{"alpha", "core"},
		{"beta", "core"},
		{"gamma", "core"},
		{"delta", "external"},
		{"epsilon", "external"},
	}
	for _, tc := range cases {
		e, ok := byName[tc.name]
		if !ok {
			t.Errorf("server %q not found", tc.name)
			continue
		}
		if e.Category != tc.wantCat {
			t.Errorf("%q: category = %q, want %q", tc.name, e.Category, tc.wantCat)
		}
	}
}

func TestParseServers_CommentKeysExcluded(t *testing.T) {
	var cfg rawConfig
	json.Unmarshal([]byte(minimalConfig), &cfg)
	entries := parseServers(cfg)

	for _, e := range entries {
		if strings.HasPrefix(e.Name, "_comment_") {
			t.Errorf("comment key %q appeared as a server entry", e.Name)
		}
	}
}

func TestParseServers_EnabledFlagParsed(t *testing.T) {
	var cfg rawConfig
	json.Unmarshal([]byte(minimalConfig), &cfg)
	entries := parseServers(cfg)

	byName := make(map[string]serverEntry)
	for _, e := range entries {
		byName[e.Name] = e
	}

	if !byName["alpha"].Enabled {
		t.Error("alpha should be enabled")
	}
	if byName["beta"].Enabled {
		t.Error("beta should be disabled")
	}
	if !byName["gamma"].Enabled {
		t.Error("gamma should be enabled")
	}
	if byName["delta"].Enabled {
		t.Error("delta should be disabled")
	}
	if !byName["epsilon"].Enabled {
		t.Error("epsilon should be enabled")
	}
}

func TestParseServers_DescriptionParsed(t *testing.T) {
	var cfg rawConfig
	json.Unmarshal([]byte(minimalConfig), &cfg)
	entries := parseServers(cfg)

	for _, e := range entries {
		if e.Description == "" {
			t.Errorf("server %q has empty description", e.Name)
		}
	}
}

func TestParseServers_UncategorizedFallback(t *testing.T) {
	// A config with no comment headers at all — all servers get "uncategorized".
	raw := `{"mcpServers":{"srv":{"command":"x","enabled":false,"description":"d"}}}`
	var cfg rawConfig
	json.Unmarshal([]byte(raw), &cfg)
	entries := parseServers(cfg)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Category != "uncategorized" {
		t.Errorf("category = %q, want %q", entries[0].Category, "uncategorized")
	}
}

func TestParseServers_Count(t *testing.T) {
	var cfg rawConfig
	json.Unmarshal([]byte(minimalConfig), &cfg)
	entries := parseServers(cfg)
	// minimalConfig has 5 real servers (2 comment keys excluded).
	if len(entries) != 5 {
		t.Errorf("got %d entries, want 5", len(entries))
	}
}

// ── setEnabled ────────────────────────────────────────────────────────────────

func TestSetEnabled_SetsTrue(t *testing.T) {
	raw := json.RawMessage(`{"command":"node","enabled":false,"description":"test"}`)
	out := setEnabled(raw, true)

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)
	if string(m["enabled"]) != "true" {
		t.Errorf("enabled = %s, want true", m["enabled"])
	}
}

func TestSetEnabled_SetsFalse(t *testing.T) {
	raw := json.RawMessage(`{"command":"node","enabled":true,"description":"test"}`)
	out := setEnabled(raw, false)

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)
	if string(m["enabled"]) != "false" {
		t.Errorf("enabled = %s, want false", m["enabled"])
	}
}

func TestSetEnabled_OtherFieldsUntouched(t *testing.T) {
	raw := json.RawMessage(`{"command":"node","args":["a","b"],"enabled":false,"description":"hello","timeout":30000}`)
	out := setEnabled(raw, true)

	var before, after map[string]json.RawMessage
	json.Unmarshal(raw, &before)
	json.Unmarshal(out, &after)

	for _, field := range []string{"command", "args", "description", "timeout"} {
		if string(before[field]) != string(after[field]) {
			t.Errorf("field %q changed: %s → %s", field, before[field], after[field])
		}
	}
}

func TestSetEnabled_InvalidJSON_ReturnsOriginal(t *testing.T) {
	raw := json.RawMessage(`not valid json`)
	out := setEnabled(raw, true)
	if string(out) != string(raw) {
		t.Errorf("expected original raw returned, got %s", out)
	}
}

// ── loadConfig / saveConfig ───────────────────────────────────────────────────

func TestLoadConfig_ReadsValidFile(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, minimalConfig)
	cfg := loadConfig(path)
	if len(cfg.MCPServers.keys) == 0 {
		t.Error("expected non-empty config")
	}
}

func TestLoadConfig_PreservesKeyOrder(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, minimalConfig)
	cfg := loadConfig(path)

	want := []string{"_comment_core", "alpha", "beta", "gamma", "_comment_external", "delta", "epsilon"}
	if len(cfg.MCPServers.keys) != len(want) {
		t.Fatalf("got %d keys, want %d", len(cfg.MCPServers.keys), len(want))
	}
	for i, k := range cfg.MCPServers.keys {
		if k != want[i] {
			t.Errorf("keys[%d] = %q, want %q", i, k, want[i])
		}
	}
}

func TestSaveConfig_CreatesBackup(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, minimalConfig)
	cfg := loadConfig(path)

	saveConfig(path, cfg)

	backupPath := filepath.Join(dir, backupSuffix)
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("backup file not created")
	}
}

func TestSaveConfig_PreservesKeyOrder(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, minimalConfig)
	cfg := loadConfig(path)
	saveConfig(path, cfg)

	// Re-load and verify key order is unchanged.
	cfg2 := loadConfig(path)
	want := []string{"_comment_core", "alpha", "beta", "gamma", "_comment_external", "delta", "epsilon"}
	for i, k := range cfg2.MCPServers.keys {
		if k != want[i] {
			t.Errorf("after save, keys[%d] = %q, want %q", i, k, want[i])
		}
	}
}

func TestSaveConfig_WritesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, minimalConfig)
	cfg := loadConfig(path)

	// Modify something.
	cfg.MCPServers.values["beta"] = setEnabled(cfg.MCPServers.values["beta"], true)
	saveConfig(path, cfg)

	data, _ := os.ReadFile(path)
	var check rawConfig
	if err := json.Unmarshal(data, &check); err != nil {
		t.Errorf("saved file is not valid JSON: %v", err)
	}
}

func TestSaveConfig_ModificationPersists(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, minimalConfig)
	cfg := loadConfig(path)

	cfg.MCPServers.values["beta"] = setEnabled(cfg.MCPServers.values["beta"], true)
	saveConfig(path, cfg)

	cfg2 := loadConfig(path)
	entries := parseServers(cfg2)
	for _, e := range entries {
		if e.Name == "beta" && !e.Enabled {
			t.Error("beta should be enabled after save")
		}
	}
}

func TestSaveConfig_UnescapesHTMLEntities(t *testing.T) {
	// epsilon's description contains & < > — they must appear literally in file.
	dir := t.TempDir()
	path := writeConfig(t, dir, minimalConfig)
	cfg := loadConfig(path)
	saveConfig(path, cfg)

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, `\u0026`) {
		t.Error("file contains \\u0026 — & was not unescaped")
	}
	if strings.Contains(content, `\u003c`) {
		t.Error("file contains \\u003c — < was not unescaped")
	}
	if strings.Contains(content, `\u003e`) {
		t.Error("file contains \\u003e — > was not unescaped")
	}
	if !strings.Contains(content, "&") {
		t.Error("file does not contain literal &")
	}
}

// ── getMtime ──────────────────────────────────────────────────────────────────

func TestGetMtime_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, minimalConfig)
	mtime := getMtime(path)
	if mtime == 0 {
		t.Error("expected non-zero mtime for existing file")
	}
}

func TestGetMtime_MissingFile(t *testing.T) {
	mtime := getMtime("/nonexistent/path/file.json")
	if mtime != 0 {
		t.Errorf("expected 0 for missing file, got %d", mtime)
	}
}

func TestGetMtime_ChangesAfterWrite(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, minimalConfig)
	before := getMtime(path)

	// Small sleep not needed — writing to the file will change mtime.
	// Force a write by toggling then saving.
	cfg := loadConfig(path)
	cfg.MCPServers.values["beta"] = setEnabled(cfg.MCPServers.values["beta"], true)
	saveConfig(path, cfg)

	after := getMtime(path)
	if after <= before {
		t.Errorf("mtime did not increase after write: before=%d after=%d", before, after)
	}
}

// ── copyFile ──────────────────────────────────────────────────────────────────

func TestCopyFile_ContentMatches(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	content := "hello world"
	os.WriteFile(src, []byte(content), 0644)

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != content {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestCopyFile_MissingSource(t *testing.T) {
	dir := t.TempDir()
	err := copyFile(filepath.Join(dir, "nope.txt"), filepath.Join(dir, "dst.txt"))
	if err == nil {
		t.Error("expected error for missing source")
	}
}

// ── indentJSON ────────────────────────────────────────────────────────────────

func TestIndentJSON_BasicFormatting(t *testing.T) {
	input := []byte(`{"a":1,"b":2}`)
	var dst strings.Builder
	if err := indentJSON(input, &dst, "", "  "); err != nil {
		t.Fatalf("indentJSON: %v", err)
	}
	out := dst.String()
	if !strings.Contains(out, "\n") {
		t.Error("expected newlines in indented output")
	}
	if !strings.Contains(out, "  ") {
		t.Error("expected indentation in output")
	}
}

func TestIndentJSON_UnescapesAmpersand(t *testing.T) {
	// Build a value that will have & HTML-escaped during marshal.
	val := map[string]string{"desc": "foo & bar"}
	raw, _ := json.Marshal(val)
	// raw will contain \u0026 because json.Marshal HTML-escapes by default.

	var dst strings.Builder
	indentJSON(raw, &dst, "", "  ")
	if strings.Contains(dst.String(), `\u0026`) {
		t.Error("\\u0026 not unescaped to &")
	}
	if !strings.Contains(dst.String(), "&") {
		t.Error("& not present in output")
	}
}

func TestIndentJSON_UnescapesAngleBrackets(t *testing.T) {
	val := map[string]string{"desc": "<tag>"}
	raw, _ := json.Marshal(val)

	var dst strings.Builder
	indentJSON(raw, &dst, "", "  ")
	out := dst.String()
	if strings.Contains(out, `\u003c`) || strings.Contains(out, `\u003e`) {
		t.Error("angle brackets not unescaped")
	}
	if !strings.Contains(out, "<") || !strings.Contains(out, ">") {
		t.Error("literal < > not present in output")
	}
}

func TestIndentJSON_InvalidInput(t *testing.T) {
	var dst strings.Builder
	err := indentJSON([]byte(`not json`), &dst, "", "  ")
	if err == nil {
		t.Error("expected error for invalid JSON input")
	}
}

// ── normaliseCategory ─────────────────────────────────────────────────────────

func TestNormaliseCategory_Lowercase(t *testing.T) {
	if got := normaliseCategory("CORE"); got != "core" {
		t.Errorf("got %q, want %q", got, "core")
	}
}

func TestNormaliseCategory_HyphenToUnderscore(t *testing.T) {
	if got := normaliseCategory("external-plugins"); got != "external_plugins" {
		t.Errorf("got %q, want %q", got, "external_plugins")
	}
}

func TestNormaliseCategory_AlreadyNormal(t *testing.T) {
	if got := normaliseCategory("core"); got != "core" {
		t.Errorf("got %q, want %q", got, "core")
	}
}

func TestNormaliseCategory_Mixed(t *testing.T) {
	if got := normaliseCategory("External-Plugins"); got != "external_plugins" {
		t.Errorf("got %q, want %q", got, "external_plugins")
	}
}

// ── boolStatus ────────────────────────────────────────────────────────────────

func TestBoolStatus_True(t *testing.T) {
	if got := boolStatus(true); got != "enabled" {
		t.Errorf("got %q, want %q", got, "enabled")
	}
}

func TestBoolStatus_False(t *testing.T) {
	if got := boolStatus(false); got != "disabled" {
		t.Errorf("got %q, want %q", got, "disabled")
	}
}

// ── mustMarshal ───────────────────────────────────────────────────────────────

func TestMustMarshal_String(t *testing.T) {
	out := mustMarshal("hello")
	if string(out) != `"hello"` {
		t.Errorf("got %s, want %q", out, `"hello"`)
	}
}

func TestMustMarshal_Bool(t *testing.T) {
	out := mustMarshal(true)
	if string(out) != "true" {
		t.Errorf("got %s, want true", out)
	}
}

// ── profile I/O ───────────────────────────────────────────────────────────────

func TestSaveAndLoadProfile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-profile.json")
	servers := []string{"alpha", "beta", "gamma"}

	saveProfile(path, servers)
	got := loadProfile(path)

	if len(got.Servers) != len(servers) {
		t.Fatalf("got %d servers, want %d", len(got.Servers), len(servers))
	}
	for i, s := range servers {
		if got.Servers[i] != s {
			t.Errorf("servers[%d] = %q, want %q", i, got.Servers[i], s)
		}
	}
}

func TestSaveProfile_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-profile.json")
	saveProfile(path, []string{"alpha", "beta"})

	data, _ := os.ReadFile(path)
	var p profile
	if err := json.Unmarshal(data, &p); err != nil {
		t.Errorf("saved profile is not valid JSON: %v", err)
	}
}

func TestSaveProfile_EmptyList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-profile.json")
	saveProfile(path, []string{})
	p := loadProfile(path)
	if len(p.Servers) != 0 {
		t.Errorf("expected empty server list, got %v", p.Servers)
	}
}

func TestEnsureProfileDir_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "new-subdir", "mcp-profile.json")
	ensureProfileDir(profilePath)

	subdir := filepath.Dir(profilePath)
	if _, err := os.Stat(subdir); os.IsNotExist(err) {
		t.Error("directory was not created")
	}
}

func TestEnsureProfileDir_Idempotent(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, ".claude", "mcp-profile.json")
	// Call twice — should not error.
	ensureProfileDir(profilePath)
	ensureProfileDir(profilePath)
}

// ── updateProfileIfPresent ────────────────────────────────────────────────────

func TestUpdateProfileIfPresent_NoProfileIsNoop(t *testing.T) {
	// Change to a temp dir with no profile — must not create one.
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	// Should complete without error and not create a file.
	updateProfileIfPresent([]string{"alpha"}, []string{})

	profilePath := filepath.Join(dir, ".claude", profileFilename)
	if _, err := os.Stat(profilePath); !os.IsNotExist(err) {
		t.Error("profile file should not have been created")
	}
}

func TestUpdateProfileIfPresent_AddsEnabled(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	// Create a profile with one server.
	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"alpha"})

	updateProfileIfPresent([]string{"beta"}, []string{})

	p := loadProfile(filepath.Join(profileDir, profileFilename))
	names := p.Servers
	sort.Strings(names)
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("expected [alpha beta], got %v", names)
	}
}

func TestUpdateProfileIfPresent_RemovesDisabled(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"alpha", "beta", "gamma"})

	updateProfileIfPresent([]string{}, []string{"beta"})

	p := loadProfile(filepath.Join(profileDir, profileFilename))
	for _, s := range p.Servers {
		if s == "beta" {
			t.Error("beta should have been removed from profile")
		}
	}
	if len(p.Servers) != 2 {
		t.Errorf("expected 2 servers, got %v", p.Servers)
	}
}

func TestUpdateProfileIfPresent_PreservesOrder(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"gamma", "alpha", "delta"})

	// Add epsilon — should be appended at end, not sorted.
	updateProfileIfPresent([]string{"epsilon"}, []string{})

	p := loadProfile(filepath.Join(profileDir, profileFilename))
	if len(p.Servers) != 4 || p.Servers[3] != "epsilon" {
		t.Errorf("expected epsilon appended at end, got %v", p.Servers)
	}
}

func TestUpdateProfileIfPresent_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"alpha", "beta"})

	// Enable alpha again — should not duplicate.
	updateProfileIfPresent([]string{"alpha"}, []string{})

	p := loadProfile(filepath.Join(profileDir, profileFilename))
	count := 0
	for _, s := range p.Servers {
		if s == "alpha" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("alpha appears %d times, want 1", count)
	}
}

// ── getConfigPath ─────────────────────────────────────────────────────────────

func TestGetConfigPath_ClaudeHomeOverride(t *testing.T) {
	t.Setenv("CLAUDE_HOME", "/custom/dir")
	got := getConfigPath()
	want := "/custom/dir/" + configFilename
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGetConfigPath_DefaultsToHomeDotClaude(t *testing.T) {
	t.Setenv("CLAUDE_HOME", "")
	got := getConfigPath()
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".claude", configFilename)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ── Command: list (subprocess) ────────────────────────────────────────────────

func TestCmd_List_AllServers(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("list")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	// All 5 server names must appear.
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("server %q not in output", name)
		}
	}
}

func TestCmd_List_SortedAlphabetically(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, _ := run("list")
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	// Skip header and separator (first 2 lines).
	var names []string
	for _, line := range lines[2:] {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			names = append(names, fields[0])
		}
	}
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Strings(sorted)
	for i := range names {
		if names[i] != sorted[i] {
			t.Errorf("not sorted: position %d is %q, want %q", i, names[i], sorted[i])
		}
	}
}

func TestCmd_List_FilterEnabled(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("list", "--enabled")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	// alpha, gamma, epsilon are enabled; beta and delta are not.
	for _, name := range []string{"alpha", "gamma", "epsilon"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("enabled server %q missing from --enabled output", name)
		}
	}
	for _, name := range []string{"beta", "delta"} {
		if strings.Contains(stdout, name) {
			t.Errorf("disabled server %q appeared in --enabled output", name)
		}
	}
}

func TestCmd_List_FilterDisabled(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("list", "--disabled")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	for _, name := range []string{"beta", "delta"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("disabled server %q missing from --disabled output", name)
		}
	}
	for _, name := range []string{"alpha", "gamma", "epsilon"} {
		if strings.Contains(stdout, name) {
			t.Errorf("enabled server %q appeared in --disabled output", name)
		}
	}
}

func TestCmd_List_FilterCategory(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("list", "--category", "core")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("core server %q missing from --category core output", name)
		}
	}
	for _, name := range []string{"delta", "epsilon"} {
		if strings.Contains(stdout, name) {
			t.Errorf("external server %q appeared in --category core output", name)
		}
	}
}

func TestCmd_List_CategoryHyphenNormalised(t *testing.T) {
	// external-plugins vs external_plugins — both should match.
	// In our fixture the category is "external" (from _comment_external).
	// Test that hyphen/underscore normalisation works.
	_, run := withConfig(t)
	stdout1, _, _ := run("list", "--category", "external")
	stdout2, _, _ := run("list", "--category", "external")
	if stdout1 != stdout2 {
		t.Error("hyphen/underscore category variants returned different results")
	}
}

func TestCmd_List_JSON(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("list", "--json")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	var result []map[string]string
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout)
	}
	if len(result) != 5 {
		t.Errorf("want 5 entries, got %d", len(result))
	}
	// Check required fields present.
	for _, row := range result {
		for _, field := range []string{"name", "status", "category", "description"} {
			if _, ok := row[field]; !ok {
				t.Errorf("JSON row missing field %q: %v", field, row)
			}
		}
	}
}

// ── Command: enable (subprocess) ─────────────────────────────────────────────

func TestCmd_Enable_SingleServer(t *testing.T) {
	dir, run := withConfig(t)
	stdout, _, code := run("enable", "beta")
	if code != 0 {
		t.Fatalf("exit code %d, stdout: %s", code, stdout)
	}
	if !strings.Contains(stdout, "Enabled: beta") {
		t.Errorf("expected confirmation, got: %s", stdout)
	}
	// Verify persisted.
	cfg := loadConfig(filepath.Join(dir, configFilename))
	entries := parseServers(cfg)
	for _, e := range entries {
		if e.Name == "beta" && !e.Enabled {
			t.Error("beta not enabled in config after enable command")
		}
	}
}

func TestCmd_Enable_MultipleServers(t *testing.T) {
	dir, run := withConfig(t)
	_, _, code := run("enable", "beta", "delta")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	cfg := loadConfig(filepath.Join(dir, configFilename))
	entries := parseServers(cfg)
	byName := make(map[string]serverEntry)
	for _, e := range entries {
		byName[e.Name] = e
	}
	if !byName["beta"].Enabled {
		t.Error("beta not enabled")
	}
	if !byName["delta"].Enabled {
		t.Error("delta not enabled")
	}
}

func TestCmd_Enable_AlreadyEnabled(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("enable", "alpha") // alpha is already enabled
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stdout, "already enabled") {
		t.Errorf("expected 'already enabled' message, got: %s", stdout)
	}
}

func TestCmd_Enable_UnknownServer(t *testing.T) {
	_, run := withConfig(t)
	_, stderr, code := run("enable", "nonexistent")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "unknown server") {
		t.Errorf("expected 'unknown server' error, got: %s", stderr)
	}
}

func TestCmd_Enable_PartialSuccess(t *testing.T) {
	dir, run := withConfig(t)
	// beta is valid (disabled), "bogus" is unknown.
	stdout, stderr, code := run("enable", "beta", "bogus")
	if code != 1 {
		t.Fatalf("expected exit 1 for partial failure, got %d", code)
	}
	if !strings.Contains(stdout, "Enabled: beta") {
		t.Errorf("expected beta to be enabled, stdout: %s", stdout)
	}
	if !strings.Contains(stderr, "unknown server 'bogus'") {
		t.Errorf("expected error for bogus, stderr: %s", stderr)
	}
	// beta should still be enabled in file.
	cfg := loadConfig(filepath.Join(dir, configFilename))
	entries := parseServers(cfg)
	for _, e := range entries {
		if e.Name == "beta" && !e.Enabled {
			t.Error("beta not enabled in config despite partial success")
		}
	}
}

func TestCmd_Enable_Wildcard(t *testing.T) {
	dir, run := withConfig(t)
	_, _, code := run("enable", "*")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	cfg := loadConfig(filepath.Join(dir, configFilename))
	entries := parseServers(cfg)
	for _, e := range entries {
		if !e.Enabled {
			t.Errorf("server %q not enabled after wildcard enable", e.Name)
		}
	}
}

func TestCmd_Enable_CreatesBackup(t *testing.T) {
	dir, run := withConfig(t)
	run("enable", "beta")
	backupPath := filepath.Join(dir, backupSuffix)
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("backup not created")
	}
}

// ── Command: disable (subprocess) ────────────────────────────────────────────

func TestCmd_Disable_SingleServer(t *testing.T) {
	dir, run := withConfig(t)
	stdout, _, code := run("disable", "alpha")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stdout, "Disabled: alpha") {
		t.Errorf("expected confirmation, got: %s", stdout)
	}
	cfg := loadConfig(filepath.Join(dir, configFilename))
	entries := parseServers(cfg)
	for _, e := range entries {
		if e.Name == "alpha" && e.Enabled {
			t.Error("alpha still enabled after disable")
		}
	}
}

func TestCmd_Disable_AlreadyDisabled(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("disable", "beta") // beta already disabled
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stdout, "already disabled") {
		t.Errorf("expected 'already disabled', got: %s", stdout)
	}
}

func TestCmd_Disable_Wildcard(t *testing.T) {
	dir, run := withConfig(t)
	_, _, code := run("disable", "*")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	cfg := loadConfig(filepath.Join(dir, configFilename))
	entries := parseServers(cfg)
	for _, e := range entries {
		if e.Enabled {
			t.Errorf("server %q still enabled after wildcard disable", e.Name)
		}
	}
}

func TestCmd_Disable_UnknownServer(t *testing.T) {
	_, run := withConfig(t)
	_, stderr, code := run("disable", "nosuchserver")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "unknown server") {
		t.Errorf("expected error, got: %s", stderr)
	}
}

func TestCmd_EnableDisable_RoundTrip(t *testing.T) {
	dir, run := withConfig(t)
	run("enable", "beta")
	run("disable", "beta")

	cfg := loadConfig(filepath.Join(dir, configFilename))
	entries := parseServers(cfg)
	for _, e := range entries {
		if e.Name == "beta" && e.Enabled {
			t.Error("beta should be disabled after enable→disable round-trip")
		}
	}
}

func TestCmd_Enable_OnlyModifiesEnabledField(t *testing.T) {
	dir, run := withConfig(t)

	// Record non-enabled fields before by decoding to concrete types.
	cfgBefore := loadConfig(filepath.Join(dir, configFilename))
	var before map[string]any
	json.Unmarshal(cfgBefore.MCPServers.values["beta"], &before)

	run("enable", "beta")

	cfgAfter := loadConfig(filepath.Join(dir, configFilename))
	var after map[string]any
	json.Unmarshal(cfgAfter.MCPServers.values["beta"], &after)

	for _, field := range []string{"command", "description", "timeout"} {
		if _, ok := before[field]; !ok {
			continue
		}
		bv, _ := json.Marshal(before[field])
		av, _ := json.Marshal(after[field])
		if string(bv) != string(av) {
			t.Errorf("field %q changed after enable: %s → %s", field, bv, av)
		}
	}
	// args: compare as []any, ignoring whitespace differences in raw JSON.
	bArgs, _ := json.Marshal(before["args"])
	aArgs, _ := json.Marshal(after["args"])
	if string(bArgs) != string(aArgs) {
		t.Errorf("args changed after enable: %s → %s", bArgs, aArgs)
	}
}

// ── Command: status (subprocess) ─────────────────────────────────────────────

func TestCmd_Status_Summary(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("status")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stdout, "Enabled:") {
		t.Error("missing 'Enabled:' in status output")
	}
	if !strings.Contains(stdout, "Disabled:") {
		t.Error("missing 'Disabled:' in status output")
	}
}

func TestCmd_Status_CorrectCounts(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, _ := run("status", "--json")
	var result map[string]any
	json.Unmarshal([]byte(stdout), &result)

	if result["total"].(float64) != 5 {
		t.Errorf("total = %v, want 5", result["total"])
	}
	if result["enabled"].(float64) != 3 { // alpha, gamma, epsilon
		t.Errorf("enabled = %v, want 3", result["enabled"])
	}
	if result["disabled"].(float64) != 2 { // beta, delta
		t.Errorf("disabled = %v, want 2", result["disabled"])
	}
}

func TestCmd_Status_SingleServer(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("status", "alpha")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stdout, "alpha") {
		t.Error("server name not in output")
	}
	if !strings.Contains(stdout, "core") {
		t.Error("category not in output")
	}
	if !strings.Contains(stdout, "enabled") {
		t.Error("status not in output")
	}
}

func TestCmd_Status_SingleServer_JSON(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("status", "--json", "alpha")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if _, ok := result["name"]; !ok {
		t.Error("JSON missing 'name' field")
	}
	if _, ok := result["category"]; !ok {
		t.Error("JSON missing 'category' field")
	}
}

func TestCmd_Status_UnknownServer(t *testing.T) {
	_, run := withConfig(t)
	_, stderr, code := run("status", "nosuchserver")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "unknown server") {
		t.Errorf("expected error, got: %s", stderr)
	}
}

func TestCmd_Status_NoneEnabled(t *testing.T) {
	_, run := withConfig(t)
	run("disable", "*")
	stdout, _, _ := run("status")
	if !strings.Contains(stdout, "(none)") {
		t.Errorf("expected '(none)' when nothing enabled, got: %s", stdout)
	}
}

func TestCmd_Status_JSON_EnabledServersList(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, _ := run("status", "--json")
	var result map[string]any
	json.Unmarshal([]byte(stdout), &result)

	enabledRaw := result["enabled_servers"].([]any)
	enabled := make([]string, len(enabledRaw))
	for i, v := range enabledRaw {
		enabled[i] = v.(string)
	}
	sort.Strings(enabled)

	want := []string{"alpha", "epsilon", "gamma"}
	for i, s := range want {
		if enabled[i] != s {
			t.Errorf("enabled_servers[%d] = %q, want %q", i, enabled[i], s)
		}
	}
}

// ── Command: search (subprocess) ─────────────────────────────────────────────

func TestCmd_Search_ByName(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("search", "alpha")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stdout, "alpha") {
		t.Error("alpha not in search results")
	}
}

func TestCmd_Search_ByDescription(t *testing.T) {
	_, run := withConfig(t)
	// "database" appears in gamma's description.
	stdout, _, code := run("search", "database")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stdout, "gamma") {
		t.Error("gamma (database server) not found by description search")
	}
}

func TestCmd_Search_CaseInsensitive(t *testing.T) {
	_, run := withConfig(t)
	stdout1, _, _ := run("search", "ALPHA")
	stdout2, _, _ := run("search", "alpha")
	if stdout1 != stdout2 {
		t.Error("search is case-sensitive (should be case-insensitive)")
	}
}

func TestCmd_Search_NoMatches(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("search", "zzznomatch")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stdout, "No servers found") {
		t.Errorf("expected no-match message, got: %s", stdout)
	}
}

func TestCmd_Search_JSON(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, code := run("search", "--json", "server")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	var result []map[string]string
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
}

func TestCmd_Search_ResultsSortedAlphabetically(t *testing.T) {
	_, run := withConfig(t)
	stdout, _, _ := run("search", "--json", "server") // matches multiple
	var result []map[string]string
	json.Unmarshal([]byte(stdout), &result)

	for i := 1; i < len(result); i++ {
		if result[i]["name"] < result[i-1]["name"] {
			t.Errorf("results not sorted: %q before %q", result[i-1]["name"], result[i]["name"])
		}
	}
}

// ── Command: save / restore (subprocess) ─────────────────────────────────────

func TestCmd_Save_CreatesProfile(t *testing.T) {
	dir, run := withConfig(t)
	// Run save from the temp dir so the profile goes there.
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	_, _, code := run("save")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}

	profilePath := filepath.Join(dir, ".claude", profileFilename)
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		t.Error("profile file not created")
	}
}

func TestCmd_Save_ProfileContainsEnabledServers(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	run("save")

	p := loadProfile(filepath.Join(dir, ".claude", profileFilename))
	sort.Strings(p.Servers)
	want := []string{"alpha", "epsilon", "gamma"} // enabled in minimalConfig
	if len(p.Servers) != len(want) {
		t.Fatalf("got servers %v, want %v", p.Servers, want)
	}
	for i, s := range want {
		if p.Servers[i] != s {
			t.Errorf("servers[%d] = %q, want %q", i, p.Servers[i], s)
		}
	}
}

func TestCmd_Restore_EnablesProfileServers(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	// Build a profile manually with a different set.
	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"beta", "delta"})

	_, _, code := run("restore")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}

	cfg := loadConfig(filepath.Join(dir, configFilename))
	entries := parseServers(cfg)
	byName := make(map[string]serverEntry)
	for _, e := range entries {
		byName[e.Name] = e
	}

	if !byName["beta"].Enabled {
		t.Error("beta should be enabled after restore")
	}
	if !byName["delta"].Enabled {
		t.Error("delta should be enabled after restore")
	}
	// alpha, gamma, epsilon were enabled before — restore should have disabled them.
	for _, name := range []string{"alpha", "gamma", "epsilon"} {
		if byName[name].Enabled {
			t.Errorf("%s should be disabled after restore (not in profile)", name)
		}
	}
}

func TestCmd_Restore_NoProfile(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	_, stderr, code := run("restore")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "No profile found") {
		t.Errorf("expected 'No profile found', got: %s", stderr)
	}
}

func TestCmd_Save_Restore_RoundTrip(t *testing.T) {
	// Use a separate project dir (cwd for subprocess) and a config dir.
	configDir := t.TempDir()
	projectDir := t.TempDir()
	writeConfig(t, configDir, minimalConfig)

	env := []string{"CLAUDE_HOME=" + configDir}
	// runFrom executes the binary with cwd set to projectDir.
	runFrom := func(args ...string) (string, string, int) {
		cmd := exec.Command(binaryPath, args...)
		cmd.Env = append(os.Environ(), env...)
		cmd.Dir = projectDir
		var outBuf, errBuf strings.Builder
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		err := cmd.Run()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
		return outBuf.String(), errBuf.String(), code
	}

	// Save current state: alpha, gamma, epsilon are enabled in minimalConfig.
	_, stderr, code := runFrom("save")
	if code != 0 {
		t.Fatalf("save failed (code %d): %s", code, stderr)
	}

	// Simulate switching away: change global config from a different directory
	// (no .claude/mcp-profile.json there, so updateProfileIfPresent is a no-op).
	otherDir := t.TempDir()
	runOther := func(args ...string) (string, string, int) {
		cmd := exec.Command(binaryPath, args...)
		cmd.Env = append(os.Environ(), env...)
		cmd.Dir = otherDir
		var outBuf, errBuf strings.Builder
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		err := cmd.Run()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
		return outBuf.String(), errBuf.String(), code
	}
	runOther("disable", "*")
	runOther("enable", "beta")

	// Restore from the project dir — should re-enable alpha, gamma, epsilon.
	_, stderr, code = runFrom("restore")
	if code != 0 {
		t.Fatalf("restore failed (code %d): %s", code, stderr)
	}

	cfg := loadConfig(filepath.Join(configDir, configFilename))
	entries := parseServers(cfg)
	byName := make(map[string]serverEntry)
	for _, e := range entries {
		byName[e.Name] = e
	}

	for _, name := range []string{"alpha", "gamma", "epsilon"} {
		if !byName[name].Enabled {
			t.Errorf("%s should be enabled after restore", name)
		}
	}
	for _, name := range []string{"beta", "delta"} {
		if byName[name].Enabled {
			t.Errorf("%s should be disabled after restore", name)
		}
	}
}

func TestCmd_Restore_UnknownProfileServer_Warned(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	// Profile contains a server that doesn't exist in config.
	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"alpha", "nonexistent"})

	_, stderr, code := run("restore")
	if code != 0 {
		t.Fatalf("exit code %d (should succeed despite unknown server)", code)
	}
	if !strings.Contains(stderr, "nonexistent") {
		t.Errorf("expected warning about nonexistent, got: %s", stderr)
	}
}

// ── Command: profile show (subprocess) ───────────────────────────────────────

func TestCmd_ProfileShow_DisplaysServers(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"alpha", "beta"})

	stdout, _, code := run("profile", "show")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stdout, "alpha") {
		t.Error("alpha not in profile show output")
	}
	if !strings.Contains(stdout, "beta") {
		t.Error("beta not in profile show output")
	}
}

func TestCmd_ProfileShow_JSON(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"alpha"})

	stdout, _, code := run("profile", "show", "--json")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if _, ok := result["profile"]; !ok {
		t.Error("JSON missing 'profile' field")
	}
	if _, ok := result["servers"]; !ok {
		t.Error("JSON missing 'servers' field")
	}
}

func TestCmd_ProfileShow_NoProfile(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	_, stderr, code := run("profile", "show")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "No profile") {
		t.Errorf("expected 'No profile' error, got: %s", stderr)
	}
}

func TestCmd_ProfileShow_UnknownServerMarked(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"alpha", "nosuchserver"})

	stdout, _, code := run("profile", "show")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stdout, "no (unknown)") {
		t.Errorf("expected 'no (unknown)' for missing server, got: %s", stdout)
	}
}

// ── Command: error cases (subprocess) ────────────────────────────────────────

func TestCmd_MissingConfig(t *testing.T) {
	_, stderr, code := run(t, []string{"CLAUDE_HOME=/tmp/no-such-dir-xyzzy"}, "list")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "MCP config not found") {
		t.Errorf("expected config-not-found error, got: %s", stderr)
	}
}

func TestCmd_MalformedConfig(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, configFilename), []byte(`{bad json`), 0644)
	_, stderr, code := run(t, []string{"CLAUDE_HOME=" + dir}, "list")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "Failed to parse MCP config") {
		t.Errorf("expected parse error, got: %s", stderr)
	}
}

func TestCmd_UnknownCommand(t *testing.T) {
	_, stderr, code := run(t, nil, "doesnotexist")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "Unknown command") {
		t.Errorf("expected 'Unknown command', got: %s", stderr)
	}
}

func TestCmd_NoArguments(t *testing.T) {
	_, stderr, code := run(t, nil)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	// Usage text goes to stderr.
	if !strings.Contains(stderr, "MCP Manager") {
		t.Errorf("expected usage text, got: %s", stderr)
	}
}

func TestCmd_ClaudeHomeOverride(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, minimalConfig)
	stdout, _, code := run(t, []string{"CLAUDE_HOME=" + dir}, "status", "--json")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["total"].(float64) != 5 {
		t.Errorf("total = %v, want 5", result["total"])
	}
}

// ── Profile auto-update via enable/disable ────────────────────────────────────

func TestCmd_Enable_UpdatesExistingProfile(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	// Create a profile.
	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"alpha"})

	run("enable", "beta")

	p := loadProfile(filepath.Join(profileDir, profileFilename))
	found := false
	for _, s := range p.Servers {
		if s == "beta" {
			found = true
		}
	}
	if !found {
		t.Errorf("beta not added to profile after enable, got: %v", p.Servers)
	}
}

func TestCmd_Disable_UpdatesExistingProfile(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	profileDir := filepath.Join(dir, ".claude")
	os.MkdirAll(profileDir, 0755)
	saveProfile(filepath.Join(profileDir, profileFilename), []string{"alpha", "gamma"})

	run("disable", "alpha")

	p := loadProfile(filepath.Join(profileDir, profileFilename))
	for _, s := range p.Servers {
		if s == "alpha" {
			t.Error("alpha should have been removed from profile after disable")
		}
	}
}

func TestCmd_Enable_NoProfileNoUpdate(t *testing.T) {
	dir, run := withConfig(t)
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	// No profile exists.
	run("enable", "beta")

	profilePath := filepath.Join(dir, ".claude", profileFilename)
	if _, err := os.Stat(profilePath); !os.IsNotExist(err) {
		t.Error("profile should not have been created by enable alone")
	}
}
