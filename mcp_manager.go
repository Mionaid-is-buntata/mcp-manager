// Copyright (c) 2026 Vincent Healy
// SPDX-License-Identifier: MIT
//
// MCP Manager — MCP server management CLI
//
// Install:
//   go build -o mcp-manager .
//   ln -sf $(pwd)/mcp-manager ~/.local/bin/mcp-manager
//
// Usage:
//   mcp-manager list [--enabled | --disabled] [--category <cat>] [--json]
//   mcp-manager enable <server> [<server>...]
//   mcp-manager disable <server> [<server>...]
//   mcp-manager status [<server>] [--json]
//   mcp-manager search <keyword> [--json]
//   mcp-manager save
//   mcp-manager restore
//   mcp-manager profile show [--json]
//   mcp-manager doctor [--fix] [--json]

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// ── Constants ────────────────────────────────────────────────────────────────

const (
	configFilename      = ".mcp-servers.json"
	backupSuffix        = ".mcp-servers.json.bak"
	settingsFilename    = "settings.json"
	settingsBackupName  = "settings.json.bak"
	profileFilename     = "mcp-profile.json"
)

// ── Types ────────────────────────────────────────────────────────────────────

// rawConfig is the top-level JSON structure.
type rawConfig struct {
	MCPServers orderedMap `json:"mcpServers"`
}

// orderedMap preserves JSON key insertion order, which is critical for
// keeping _comment_ section headers in the right positions on write-back.
type orderedMap struct {
	keys   []string
	values map[string]json.RawMessage
}

func (o *orderedMap) UnmarshalJSON(data []byte) error {
	o.values = make(map[string]json.RawMessage)
	dec := json.NewDecoder(strings.NewReader(string(data)))
	// consume '{'
	if _, err := dec.Token(); err != nil {
		return err
	}
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("expected string key, got %T", keyToken)
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return err
		}
		o.keys = append(o.keys, key)
		o.values[key] = val
	}
	// consume '}'
	_, err := dec.Token()
	return err
}

func (o orderedMap) MarshalJSON() ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("{")
	for i, key := range o.keys {
		if i > 0 {
			sb.WriteString(",")
		}
		keyBytes, _ := json.Marshal(key)
		sb.Write(keyBytes)
		sb.WriteString(":")
		sb.Write(o.values[key])
	}
	sb.WriteString("}")
	return []byte(sb.String()), nil
}

// serverEntry is a parsed MCP server entry.
type serverEntry struct {
	Name        string
	Category    string
	Description string
	Enabled     bool
}

// profile is the project-local mcp-profile.json file.
type profile struct {
	Servers []string `json:"servers"`
}

// serverRaw holds the mutable fields we need from a server's JSON.
type serverRaw struct {
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
}

// ── Config path ──────────────────────────────────────────────────────────────

func getConfigPath() string {
	if ch := os.Getenv("CLAUDE_HOME"); ch != "" {
		return filepath.Join(ch, configFilename)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fatalf("Cannot determine home directory: %v", err)
	}
	return filepath.Join(home, ".claude", configFilename)
}

// ── Config I/O ───────────────────────────────────────────────────────────────

func loadConfig(path string) rawConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fatalf("MCP config not found at %s", path)
		}
		fatalf("Cannot read %s: %v", path, err)
	}
	var cfg rawConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fatalf("Failed to parse MCP config: %v", err)
	}
	return cfg
}

func saveConfig(path string, cfg rawConfig) {
	// Backup first.
	backupPath := filepath.Join(filepath.Dir(path), backupSuffix)
	if err := copyFile(path, backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		fatalf("Cannot create backup: %v", err)
	}

	// Marshal with 2-space indent, preserving key order via orderedMap.
	raw, err := json.Marshal(cfg)
	if err != nil {
		fatalf("Cannot serialise config: %v", err)
	}
	// Re-indent for human readability.
	var indented strings.Builder
	if err := indentJSON(raw, &indented, "", "  "); err != nil {
		fatalf("Cannot format config: %v", err)
	}
	out := indented.String() + "\n"

	// Write with advisory lock.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			fatalf("Cannot write to %s: permission denied", path)
		}
		fatalf("Cannot open %s for writing: %v", path, err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		fatalf("Cannot acquire lock on %s: %v", path, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck

	if _, err := f.WriteString(out); err != nil {
		if errors.Is(err, os.ErrPermission) {
			fatalf("Cannot write to %s: permission denied", path)
		}
		fatalf("Cannot write config: %v", err)
	}
}

// getMtime returns the file modification time as a Unix nanosecond timestamp.
func getMtime(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// ── Settings I/O ──────────────────────────────────────────────────────────────

// settingsConfig represents the full settings.json structure.
// Only the mcpServers key is managed; all other keys are preserved verbatim.
type settingsConfig struct {
	keys   []string
	values map[string]json.RawMessage
}

func (s *settingsConfig) UnmarshalJSON(data []byte) error {
	s.values = make(map[string]json.RawMessage)
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if _, err := dec.Token(); err != nil {
		return err
	}
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("expected string key, got %T", keyToken)
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return err
		}
		s.keys = append(s.keys, key)
		s.values[key] = val
	}
	_, err := dec.Token()
	return err
}

func (s settingsConfig) MarshalJSON() ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("{")
	for i, key := range s.keys {
		if i > 0 {
			sb.WriteString(",")
		}
		keyBytes, _ := json.Marshal(key)
		sb.Write(keyBytes)
		sb.WriteString(":")
		sb.Write(s.values[key])
	}
	sb.WriteString("}")
	return []byte(sb.String()), nil
}

func getSettingsPath() string {
	if ch := os.Getenv("CLAUDE_HOME"); ch != "" {
		return filepath.Join(ch, settingsFilename)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fatalf("Cannot determine home directory: %v", err)
	}
	return filepath.Join(home, ".claude", settingsFilename)
}

func loadSettings(path string) settingsConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No settings.json yet — return empty with mcpServers key.
			return settingsConfig{
				keys:   []string{"mcpServers"},
				values: map[string]json.RawMessage{"mcpServers": []byte("{}")},
			}
		}
		fatalf("Cannot read %s: %v", path, err)
	}
	var sc settingsConfig
	if err := json.Unmarshal(data, &sc); err != nil {
		fatalf("Failed to parse settings: %v", err)
	}
	return sc
}

func saveSettings(path string, sc settingsConfig) {
	// Backup first.
	backupPath := filepath.Join(filepath.Dir(path), settingsBackupName)
	if err := copyFile(path, backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		fatalf("Cannot create settings backup: %v", err)
	}

	raw, err := json.Marshal(sc)
	if err != nil {
		fatalf("Cannot serialise settings: %v", err)
	}
	var indented strings.Builder
	if err := indentJSON(raw, &indented, "", "  "); err != nil {
		fatalf("Cannot format settings: %v", err)
	}
	out := indented.String() + "\n"

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		fatalf("Cannot open %s for writing: %v", path, err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		fatalf("Cannot acquire lock on %s: %v", path, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck

	if _, err := f.WriteString(out); err != nil {
		fatalf("Cannot write settings: %v", err)
	}
}

// getMCPServers extracts the mcpServers orderedMap from settings.json.
func getMCPServers(sc settingsConfig) orderedMap {
	raw, ok := sc.values["mcpServers"]
	if !ok {
		return orderedMap{values: make(map[string]json.RawMessage)}
	}
	var om orderedMap
	if err := json.Unmarshal(raw, &om); err != nil {
		return orderedMap{values: make(map[string]json.RawMessage)}
	}
	return om
}

// setMCPServers writes the mcpServers orderedMap back into settings.json.
func setMCPServers(sc *settingsConfig, om orderedMap) {
	raw, _ := json.Marshal(om)
	sc.values["mcpServers"] = raw
	// Ensure mcpServers key exists in the key list.
	found := false
	for _, k := range sc.keys {
		if k == "mcpServers" {
			found = true
			break
		}
	}
	if !found {
		sc.keys = append(sc.keys, "mcpServers")
	}
}

// stripEnabled removes the "enabled" field from a server's raw JSON for
// writing into settings.json (which doesn't use enabled flags).
func stripEnabled(raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	delete(m, "enabled")
	out, _ := json.Marshal(m)
	return out
}

// syncToSettings propagates enable/disable changes to settings.json.
// Enabled servers are added (without the enabled field); disabled servers are removed.
func syncToSettings(enabled, disabled []string, cfg rawConfig) {
	settingsPath := getSettingsPath()
	sc := loadSettings(settingsPath)
	om := getMCPServers(sc)

	for _, name := range enabled {
		raw, ok := cfg.MCPServers.values[name]
		if !ok {
			continue
		}
		stripped := stripEnabled(raw)
		// Add or update the server entry.
		if _, exists := om.values[name]; !exists {
			om.keys = append(om.keys, name)
		}
		om.values[name] = stripped
	}

	for _, name := range disabled {
		if _, exists := om.values[name]; exists {
			delete(om.values, name)
			// Remove from keys slice.
			for i, k := range om.keys {
				if k == name {
					om.keys = append(om.keys[:i], om.keys[i+1:]...)
					break
				}
			}
		}
	}

	setMCPServers(&sc, om)
	saveSettings(settingsPath, sc)
	fmt.Println("(Synced settings.json)")
}

// ── Server parsing ───────────────────────────────────────────────────────────

func parseServers(cfg rawConfig) []serverEntry {
	currentCategory := "uncategorized"
	var entries []serverEntry

	for _, key := range cfg.MCPServers.keys {
		if strings.HasPrefix(key, "_comment_") {
			currentCategory = strings.TrimPrefix(key, "_comment_")
			continue
		}
		raw := cfg.MCPServers.values[key]
		var sr serverRaw
		_ = json.Unmarshal(raw, &sr)
		entries = append(entries, serverEntry{
			Name:        key,
			Category:    currentCategory,
			Description: sr.Description,
			Enabled:     sr.Enabled,
		})
	}
	return entries
}

// setEnabled modifies the "enabled" field within an existing raw JSON value.
func setEnabled(raw json.RawMessage, enabled bool) json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	val, _ := json.Marshal(enabled)
	m["enabled"] = val
	out, _ := json.Marshal(m)
	return out
}

// ── Profile I/O ──────────────────────────────────────────────────────────────

func getProfilePath() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".claude", profileFilename)
}

func ensureProfileDir(profilePath string) {
	dir := filepath.Dir(profilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fatalf("Cannot create profile directory %s: %v", dir, err)
	}
}

func loadProfile(path string) profile {
	data, err := os.ReadFile(path)
	if err != nil {
		fatalf("Cannot read profile %s: %v", path, err)
	}
	var p profile
	if err := json.Unmarshal(data, &p); err != nil {
		fatalf("Failed to parse profile %s: %v", path, err)
	}
	return p
}

func saveProfile(path string, servers []string) {
	p := profile{Servers: servers}
	data, _ := json.MarshalIndent(p, "", "  ")
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		fatalf("Cannot write profile %s: %v", path, err)
	}
}

// updateProfileIfPresent adds newly-enabled servers to and removes
// newly-disabled servers from the project profile, if one exists in cwd.
func updateProfileIfPresent(enabled, disabled []string) {
	profilePath := getProfilePath()
	if _, err := os.Stat(profilePath); errors.Is(err, os.ErrNotExist) {
		return
	}
	p := loadProfile(profilePath)

	disableSet := make(map[string]bool, len(disabled))
	for _, n := range disabled {
		disableSet[n] = true
	}
	enableSet := make(map[string]bool, len(enabled))
	for _, n := range enabled {
		enableSet[n] = true
	}

	// Rebuild: keep existing (minus disabled), then append new enabled.
	var updated []string
	existing := make(map[string]bool)
	for _, n := range p.Servers {
		if !disableSet[n] {
			updated = append(updated, n)
			existing[n] = true
		}
	}
	for _, n := range enabled {
		if !existing[n] {
			updated = append(updated, n)
		}
	}

	saveProfile(profilePath, updated)
	fmt.Println("(Updated .claude/mcp-profile.json)")
}

// ── Output helpers ────────────────────────────────────────────────────────────

func printTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	printRow := func(cols []string) {
		var sb strings.Builder
		for i, col := range cols {
			if i > 0 {
				sb.WriteString("  ")
			}
			sb.WriteString(col)
			if i < len(cols)-1 {
				sb.WriteString(strings.Repeat(" ", widths[i]-len(col)))
			}
		}
		fmt.Println(sb.String())
	}

	printRow(headers)
	// Separator line.
	sep := make([]string, len(headers))
	for i, w := range widths {
		sep[i] = strings.Repeat("-", w)
	}
	printRow(sep)
	for _, row := range rows {
		printRow(row)
	}
}

func printJSON(v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

func normaliseCategory(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "-", "_"))
}

// ── Commands ─────────────────────────────────────────────────────────────────

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	fEnabled := fs.Bool("enabled", false, "Show only enabled servers")
	fDisabled := fs.Bool("disabled", false, "Show only disabled servers")
	fCategory := fs.String("category", "", "Filter by category")
	fJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args) //nolint:errcheck

	if *fEnabled && *fDisabled {
		fatalf("--enabled and --disabled are mutually exclusive")
	}

	cfg := loadConfig(getConfigPath())
	entries := parseServers(cfg)

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	var filtered []serverEntry
	for _, e := range entries {
		if *fEnabled && !e.Enabled {
			continue
		}
		if *fDisabled && e.Enabled {
			continue
		}
		if *fCategory != "" && normaliseCategory(e.Category) != normaliseCategory(*fCategory) {
			continue
		}
		filtered = append(filtered, e)
	}

	if *fJSON {
		type row struct {
			Name        string `json:"name"`
			Status      string `json:"status"`
			Category    string `json:"category"`
			Description string `json:"description"`
		}
		var out []row
		for _, e := range filtered {
			status := "disabled"
			if e.Enabled {
				status = "enabled"
			}
			out = append(out, row{e.Name, status, e.Category, e.Description})
		}
		printJSON(out)
		return
	}

	var rows [][]string
	for _, e := range filtered {
		status := "disabled"
		if e.Enabled {
			status = "enabled"
		}
		rows = append(rows, []string{e.Name, status, e.Category, e.Description})
	}
	printTable([]string{"SERVER", "STATUS", "CATEGORY", "DESCRIPTION"}, rows)
}

func cmdEnableDisable(targetEnabled bool, args []string) {
	cmd := "enable"
	if !targetEnabled {
		cmd = "disable"
	}
	if len(args) == 0 {
		fatalf("Usage: mcp-manager %s <server> [<server>...] | *", cmd)
	}

	configPath := getConfigPath()
	mtime := getMtime(configPath)
	cfg := loadConfig(configPath)
	entries := parseServers(cfg)

	nameSet := make(map[string]bool, len(entries))
	for _, e := range entries {
		nameSet[e.Name] = true
	}

	// Expand wildcard to all server names.
	if len(args) == 1 && args[0] == "*" {
		args = make([]string, 0, len(entries))
		for _, e := range entries {
			args = append(args, e.Name)
		}
	}

	var changed []string
	var alreadySet []string
	var unknown []string

	applyChanges := func(cfg *rawConfig) {
		for _, server := range args {
			if !nameSet[server] {
				unknown = append(unknown, server)
				continue
			}
			var sr serverRaw
			_ = json.Unmarshal(cfg.MCPServers.values[server], &sr)
			if sr.Enabled == targetEnabled {
				alreadySet = append(alreadySet, server)
				continue
			}
			cfg.MCPServers.values[server] = setEnabled(cfg.MCPServers.values[server], targetEnabled)
			changed = append(changed, server)
		}
	}

	applyChanges(&cfg)

	if len(changed) > 0 {
		// Check for concurrent modification.
		if getMtime(configPath) != mtime {
			// Re-read and re-apply.
			cfg = loadConfig(configPath)
			changed = nil
			alreadySet = nil
			applyChanges(&cfg)
			if getMtime(configPath) != mtime {
				fatalf("Config file changed during operation; aborting")
			}
		}
		saveConfig(configPath, cfg)
	}

	// Print results.
	verb := "Enabled"
	alreadyVerb := "already enabled"
	if !targetEnabled {
		verb = "Disabled"
		alreadyVerb = "already disabled"
	}
	for _, s := range changed {
		fmt.Printf("%s: %s\n", verb, s)
	}
	for _, s := range alreadySet {
		fmt.Printf("%s is %s\n", s, alreadyVerb)
	}
	for _, s := range unknown {
		fmt.Fprintf(os.Stderr, "Error: unknown server '%s'. Run 'mcp-manager list' to see available servers.\n", s)
	}

	if len(changed) > 0 {
		var nowEnabled, nowDisabled []string
		if targetEnabled {
			nowEnabled = changed
		} else {
			nowDisabled = changed
		}
		syncToSettings(nowEnabled, nowDisabled, cfg)
		updateProfileIfPresent(nowEnabled, nowDisabled)
	}

	if len(unknown) > 0 {
		os.Exit(1)
	}
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	fJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args) //nolint:errcheck

	remaining := fs.Args()
	configPath := getConfigPath()
	cfg := loadConfig(configPath)

	if len(remaining) > 0 {
		// Single server detail view.
		server := remaining[0]
		raw, ok := cfg.MCPServers.values[server]
		if !ok {
			fatalf("unknown server '%s'. Run 'mcp-manager list' to see available servers.", server)
		}

		entries := parseServers(cfg)
		category := "uncategorized"
		for _, e := range entries {
			if e.Name == server {
				category = e.Category
				break
			}
		}

		if *fJSON {
			var detail map[string]json.RawMessage
			_ = json.Unmarshal(raw, &detail)
			detail["name"] = mustMarshal(server)
			detail["category"] = mustMarshal(category)
			printJSON(detail)
			return
		}

		var detail map[string]any
		_ = json.Unmarshal(raw, &detail)
		fmt.Printf("Server:      %s\n", server)
		fmt.Printf("Category:    %s\n", category)
		if v, ok := detail["enabled"]; ok {
			fmt.Printf("Status:      %v\n", boolStatus(v.(bool)))
		}
		if v, ok := detail["description"]; ok {
			fmt.Printf("Description: %v\n", v)
		}
		if v, ok := detail["command"]; ok {
			fmt.Printf("Command:     %v\n", v)
		}
		if v, ok := detail["url"]; ok {
			fmt.Printf("URL:         %v\n", v)
		}
		if v, ok := detail["args"]; ok {
			argsRaw, _ := json.Marshal(v)
			fmt.Printf("Args:        %s\n", argsRaw)
		}
		if v, ok := detail["cwd"]; ok {
			fmt.Printf("CWD:         %v\n", v)
		}
		if v, ok := detail["timeout"]; ok {
			fmt.Printf("Timeout:     %vms\n", v)
		}
		if v, ok := detail["env"]; ok {
			envRaw, _ := json.Marshal(v)
			fmt.Printf("Env:         %s\n", envRaw)
		}
		return
	}

	// Summary view.
	entries := parseServers(cfg)
	total := len(entries)
	var enabledNames []string
	for _, e := range entries {
		if e.Enabled {
			enabledNames = append(enabledNames, e.Name)
		}
	}
	sort.Strings(enabledNames)
	enabledCount := len(enabledNames)
	disabledCount := total - enabledCount

	if *fJSON {
		printJSON(map[string]any{
			"total":           total,
			"enabled":         enabledCount,
			"disabled":        disabledCount,
			"enabled_servers": enabledNames,
		})
		return
	}

	fmt.Println("MCP Server Status:")
	fmt.Printf("  Enabled:  %d / %d\n", enabledCount, total)
	fmt.Printf("  Disabled: %d / %d\n", disabledCount, total)
	fmt.Println()
	if len(enabledNames) > 0 {
		fmt.Printf("Enabled servers: %s\n", strings.Join(enabledNames, ", "))
	} else {
		fmt.Println("Enabled servers: (none)")
	}
}

func cmdSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	fJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args) //nolint:errcheck

	remaining := fs.Args()
	if len(remaining) == 0 {
		fatalf("Usage: mcp-manager search <keyword>")
	}
	keyword := strings.ToLower(remaining[0])

	cfg := loadConfig(getConfigPath())
	entries := parseServers(cfg)

	var matches []serverEntry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name), keyword) ||
			strings.Contains(strings.ToLower(e.Description), keyword) {
			matches = append(matches, e)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})

	if *fJSON {
		type row struct {
			Name        string `json:"name"`
			Status      string `json:"status"`
			Category    string `json:"category"`
			Description string `json:"description"`
		}
		var out []row
		for _, e := range matches {
			out = append(out, row{e.Name, boolStatus(e.Enabled), e.Category, e.Description})
		}
		printJSON(out)
		return
	}

	if len(matches) == 0 {
		fmt.Println("No servers found matching:", remaining[0])
		return
	}
	var rows [][]string
	for _, e := range matches {
		rows = append(rows, []string{e.Name, boolStatus(e.Enabled), e.Category, e.Description})
	}
	printTable([]string{"SERVER", "STATUS", "CATEGORY", "DESCRIPTION"}, rows)
}

func cmdSave(_ []string) {
	configPath := getConfigPath()
	cfg := loadConfig(configPath)
	entries := parseServers(cfg)

	var names []string
	for _, e := range entries {
		if e.Enabled {
			names = append(names, e.Name)
		}
	}
	sort.Strings(names)

	profilePath := getProfilePath()
	ensureProfileDir(profilePath)
	saveProfile(profilePath, names)
	fmt.Printf("Saved %d enabled servers to %s\n", len(names), profilePath)
	for _, n := range names {
		fmt.Printf("  %s\n", n)
	}
}

func cmdRestore(_ []string) {
	profilePath := getProfilePath()
	if _, err := os.Stat(profilePath); errors.Is(err, os.ErrNotExist) {
		fatalf("No profile found at %s. Run 'mcp-manager save' first.", profilePath)
	}
	p := loadProfile(profilePath)

	configPath := getConfigPath()
	mtime := getMtime(configPath)
	cfg := loadConfig(configPath)
	entries := parseServers(cfg)

	validNames := make(map[string]bool, len(entries))
	for _, e := range entries {
		validNames[e.Name] = true
	}

	wantSet := make(map[string]bool, len(p.Servers))
	for _, s := range p.Servers {
		if !validNames[s] {
			fmt.Fprintf(os.Stderr, "Warning: profile server '%s' not found in config, skipping\n", s)
			continue
		}
		wantSet[s] = true
	}

	applyRestore := func(cfg *rawConfig) {
		for _, key := range cfg.MCPServers.keys {
			if strings.HasPrefix(key, "_comment_") {
				continue
			}
			cfg.MCPServers.values[key] = setEnabled(cfg.MCPServers.values[key], wantSet[key])
		}
	}

	applyRestore(&cfg)

	if getMtime(configPath) != mtime {
		cfg = loadConfig(configPath)
		applyRestore(&cfg)
		if getMtime(configPath) != mtime {
			fatalf("Config file changed during operation; aborting")
		}
	}

	saveConfig(configPath, cfg)

	// Build enabled/disabled lists for sync.
	var enabled, disabled []string
	for _, key := range cfg.MCPServers.keys {
		if strings.HasPrefix(key, "_comment_") {
			continue
		}
		if wantSet[key] {
			enabled = append(enabled, key)
		} else {
			disabled = append(disabled, key)
		}
	}
	sort.Strings(enabled)
	syncToSettings(enabled, disabled, cfg)

	fmt.Printf("Restored profile: enabled %d servers\n", len(enabled))
	for _, s := range enabled {
		fmt.Printf("  %s\n", s)
	}
}

func cmdProfileShow(args []string) {
	fs := flag.NewFlagSet("profile show", flag.ExitOnError)
	fJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args) //nolint:errcheck

	profilePath := getProfilePath()
	if _, err := os.Stat(profilePath); errors.Is(err, os.ErrNotExist) {
		fatalf("No profile at %s", profilePath)
	}
	p := loadProfile(profilePath)

	cfg := loadConfig(getConfigPath())
	entries := parseServers(cfg)
	globalStatus := make(map[string]string, len(entries))
	for _, e := range entries {
		globalStatus[e.Name] = boolStatus(e.Enabled)
	}

	if *fJSON {
		printJSON(map[string]any{
			"profile": profilePath,
			"servers": p.Servers,
		})
		return
	}

	fmt.Printf("Profile: %s\n\n", profilePath)
	if len(p.Servers) == 0 {
		fmt.Println("(empty)")
		return
	}
	var rows [][]string
	for _, s := range p.Servers {
		inGlobal := "yes"
		status := globalStatus[s]
		if status == "" {
			inGlobal = "no (unknown)"
			status = "n/a"
		}
		rows = append(rows, []string{s, inGlobal, status})
	}
	printTable([]string{"SERVER", "IN_GLOBAL_CONFIG", "CURRENTLY_ENABLED"}, rows)
}

// ── Utilities ────────────────────────────────────────────────────────────────

func boolStatus(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// indentJSON re-indents compact JSON preserving key order without HTML escaping.
func indentJSON(src []byte, dst *strings.Builder, prefix, indent string) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, src, prefix, indent); err != nil {
		return err
	}
	// json.Indent preserves HTML escaping from the source bytes (e.g. \u0026 for &).
	// Since our source bytes come from our own MarshalJSON which passes raw JSON
	// values through, we need to unescape HTML entities for human readability.
	result := buf.String()
	result = strings.ReplaceAll(result, `\u0026`, `&`)
	result = strings.ReplaceAll(result, `\u003c`, `<`)
	result = strings.ReplaceAll(result, `\u003e`, `>`)
	dst.WriteString(result)
	return nil
}

// ── Doctor ────────────────────────────────────────────────────────────────

func cmdDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	fFix := fs.Bool("fix", false, "Automatically fix divergence")
	fJSON := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args) //nolint:errcheck

	configPath := getConfigPath()
	cfg := loadConfig(configPath)
	entries := parseServers(cfg)

	settingsPath := getSettingsPath()
	sc := loadSettings(settingsPath)
	om := getMCPServers(sc)

	type issue struct {
		Server  string `json:"server"`
		Problem string `json:"problem"`
	}
	var issues []issue

	// Check each server in the registry.
	for _, e := range entries {
		_, inSettings := om.values[e.Name]
		if e.Enabled && !inSettings {
			issues = append(issues, issue{e.Name, "enabled in registry, missing from settings.json (won't run)"})
		}
		if !e.Enabled && inSettings {
			issues = append(issues, issue{e.Name, "disabled in registry, present in settings.json (will run despite being disabled)"})
		}
	}

	// Check for servers in settings.json not tracked in registry.
	registrySet := make(map[string]bool, len(entries))
	for _, e := range entries {
		registrySet[e.Name] = true
	}
	for _, key := range om.keys {
		if !registrySet[key] {
			issues = append(issues, issue{key, "present in settings.json, not tracked in registry (unmanaged)"})
		}
	}

	if *fJSON {
		printJSON(map[string]any{
			"issues": issues,
			"count":  len(issues),
			"status": map[bool]string{true: "diverged", false: "ok"}[len(issues) > 0],
		})
		if !*fFix {
			return
		}
	}

	if !*fJSON {
		if len(issues) == 0 {
			fmt.Println("No divergence detected. Registry and settings.json are in sync.")
			return
		}

		fmt.Printf("Divergence detected (%d issues):\n\n", len(issues))
		var rows [][]string
		for _, iss := range issues {
			rows = append(rows, []string{iss.Server, iss.Problem})
		}
		printTable([]string{"SERVER", "PROBLEM"}, rows)
	}

	if *fFix {
		fmt.Println()
		// Rebuild settings.json mcpServers from registry truth.
		var toEnable, toDisable []string
		for _, e := range entries {
			if e.Enabled {
				toEnable = append(toEnable, e.Name)
			} else {
				toDisable = append(toDisable, e.Name)
			}
		}
		// Also disable any unmanaged servers in settings.json.
		for _, key := range om.keys {
			if !registrySet[key] {
				toDisable = append(toDisable, key)
			}
		}
		syncToSettings(toEnable, toDisable, cfg)
		fmt.Printf("Fixed %d issues.\n", len(issues))
	} else if !*fJSON {
		fmt.Println("\nRun 'mcp-manager doctor --fix' to resolve.")
	}
}

// ── Main ─────────────────────────────────────────────────────────────────────

func usage() {
	fmt.Fprintf(os.Stderr, `MCP Manager — MCP server management CLI

Usage:
  mcp-manager list [--enabled | --disabled] [--category <cat>] [--json]
  mcp-manager enable <server> [<server>...]
  mcp-manager disable <server> [<server>...]
  mcp-manager status [<server>] [--json]
  mcp-manager search <keyword> [--json]
  mcp-manager save
  mcp-manager restore
  mcp-manager profile show [--json]
  mcp-manager doctor [--fix] [--json]

Environment:
  CLAUDE_HOME   Override directory containing .mcp-servers.json
`)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	cmd := os.Args[1]
	rest := os.Args[2:]

	switch cmd {
	case "list":
		cmdList(rest)
	case "enable":
		cmdEnableDisable(true, rest)
	case "disable":
		cmdEnableDisable(false, rest)
	case "status":
		cmdStatus(rest)
	case "search":
		cmdSearch(rest)
	case "save":
		cmdSave(rest)
	case "restore":
		cmdRestore(rest)
	case "profile":
		if len(rest) == 0 {
			usage()
		}
		switch rest[0] {
		case "show":
			cmdProfileShow(rest[1:])
		default:
			fatalf("Unknown profile subcommand '%s'", rest[0])
		}
	case "doctor":
		cmdDoctor(rest)
	case "--help", "-h", "help":
		usage()
	default:
		fatalf("Unknown command '%s'. Run 'mcp-manager --help' for usage.", cmd)
	}
}
