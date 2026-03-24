# Assessment: MCP Manager — settings.json Sync Feature

> **Date**: 2026-03-24
> **Issues**: Gitea git/mcp-manager #1, #2, #3
> **Scope**: Resolve config sync gap between `.mcp-servers.json` and `settings.json`

---

## 1. Requirements Summary

Three Gitea issues form a cohesive feature set:

| # | Issue | Core Requirement |
|---|-------|-----------------|
| 1 | Sync enabled/disabled state to `settings.json` | When `enable`/`disable` runs, propagate changes to Claude Code's config |
| 2 | No user warning when changes have no effect | Interim: warn users; permanent: fix via #1 |
| 3 | Config divergence is undetectable | Add `doctor` command to audit both files |

**Root cause**: mcp-manager only writes to `.mcp-servers.json`, but Claude Code reads MCP servers from `settings.json`. The PRD explicitly scoped this out (Section 7, line 142), but in practice it makes the tool ineffective.

---

## 2. Classification

| Requirement | Classification | Rationale |
|-------------|---------------|-----------|
| Issue #1 — settings.json sync on enable/disable | **Iterative Multi-Pass** | Core functional logic — must handle JSON merging, key preservation, backup, and concurrent modification safely |
| Issue #2 — User warning output | **Simulated** | Simple output formatting — becomes obsolete once #1 ships |
| Issue #3 — `doctor` command | **Iterative Multi-Pass** | Cross-file comparison logic, divergence reporting, optional `--fix` mode |
| PRD-01 update | **Simulated** | Documentation update to remove non-goal, add new scope |
| Test coverage | **Details-Matter** | High-stakes — sync bugs could corrupt `settings.json` and break Claude Code entirely |

---

## 3. Agent Recommendations

### Primary Agents

| Agent | Role in This Work | Justification |
|-------|-------------------|---------------|
| **@go-development-specialist** | Implement sync logic, `doctor` command | Go stdlib JSON manipulation, matching existing codebase patterns |
| **@go-build-deployment** | Build verification, binary deployment to `~/.local/bin/` | Ensure cross-compilation and install works |
| **@testing-qa** | Design test cases for sync and doctor features | 101 existing tests — new features need equivalent coverage |
| **@technical-documentation-specialist** | Update PRD-01, TECHNICAL.md, USAGE.md | Keep docs in sync with new capabilities |
| **@code-review** | Review sync implementation for edge cases | JSON corruption risk requires careful review |

### Supporting Agents

| Agent | Role | Justification |
|-------|------|---------------|
| **@security-analyst** | Review that sync doesn't leak credentials between files | `settings.json` contains tokens (Gitea), `.mcp-servers.json` has env vars |
| **@devops-infrastructure** | Validate backup/restore workflow | `.settings.json.bak` creation, rollback scenarios |

### MCP Servers Required

| MCP | Purpose |
|-----|---------|
| **filesystem** | Read/write config files during development |
| **sequential-thinking** | Design sync algorithm edge cases |
| **gitea** | Issue tracking, branch management, PR creation |

---

## 4. High-Stakes Decision Gates

### Gate 1: Sync direction — one-way or bidirectional?
- **DECIDED: One-way**. `.mcp-servers.json` is the single source of truth. `enable` adds to `settings.json`, `disable` removes from it.

### Gate 2: What happens to servers already in `settings.json` but not in `.mcp-servers.json`?
- **DECIDED: Consolidate into `.mcp-servers.json`**. All server definitions live in `.mcp-servers.json` as the canonical registry. Servers currently only in `settings.json` should be migrated into `.mcp-servers.json`.

### Gate 3: Should `disable` remove a server from `settings.json` entirely, or add `"disabled": true`?
- **DECIDED: Remove entirely**. Claude Code loads all servers listed in `settings.json` regardless of flags. Disabling = removing the entry.

### Gate 4: PRD scope change — does this remain a standalone tool or merge into `claude-project`?
- **DECIDED: Keep standalone**. The tool will be used across many projects, each with their own saved enable config (profile). Standalone is the correct architecture.

---

## 5. Implementation Plan

### Phase 1: Interim Warning (Issue #2) — Low risk, ship first
1. Modify `cmdEnableDisable()` output to include warning about `settings.json`
2. Output JSON snippet for manual copy-paste
3. Add tests for warning output
4. **Agents**: @go-development-specialist, @testing-qa

### Phase 2: Sync Logic (Issue #1) — Core feature
1. Add `loadSettings()` / `saveSettings()` functions (mirror existing config I/O pattern)
2. Add `syncToSettings()` — called after every `enable`/`disable` write
3. On enable: copy server entry (minus `enabled` field) from `.mcp-servers.json` into `settings.json` `mcpServers`
4. On disable: remove server entry from `settings.json` `mcpServers`
5. Backup `settings.json` before writes (`.settings.json.bak`)
6. Use same mtime-based concurrent modification detection
7. Preserve all non-`mcpServers` keys in `settings.json` (permissions, model, etc.)
8. Update profile `save`/`restore` commands to also sync
9. **Agents**: @go-development-specialist, @code-review, @security-analyst, @testing-qa

### Phase 3: Doctor Command (Issue #3) — Diagnostic tool
1. Implement `cmdDoctor()` — reads both files, reports divergence
2. Categories: missing-from-settings, disabled-but-present, unmanaged, matched
3. Optional `--fix` flag to resolve automatically
4. **Agents**: @go-development-specialist, @testing-qa

### Phase 4: Documentation & Deployment
1. Update PRD-01 Section 7 (remove non-goal, add settings.json sync as in-scope)
2. Update TECHNICAL.md with new functions
3. Update USAGE.md with `doctor` command docs
4. Rebuild binary, deploy to `~/.local/bin/`
5. **Agents**: @technical-documentation-specialist, @go-build-deployment

---

## 6. Protocols Required

| Protocol | Phase | Purpose |
|----------|-------|---------|
| **Iterative Multi-Pass** | Phase 2 (sync logic) | Multiple refinement cycles for JSON merge algorithm |
| **Details-Matter** | Phase 2 (testing) | High-stakes — sync bugs corrupt `settings.json` |
| **Simulated** | Phase 1 (warning), Phase 4 (docs) | Straightforward, non-controversial changes |

---

## 7. Risk Assessment

| Risk | Severity | Mitigation |
|------|----------|------------|
| Sync corrupts `settings.json` | **Critical** | Backup before write, atomic write, comprehensive tests |
| `settings.json` has keys mcp-manager doesn't understand | **Medium** | Only touch `mcpServers` key, preserve everything else verbatim |
| Concurrent modification by Claude Code and mcp-manager | **Medium** | mtime detection (already implemented pattern) |
| Credential leakage between files | **Low** | Security review — both files already contain tokens |
| Phase 1 warning becomes permanent (Phase 2 never ships) | **Low** | Gate warning behind version check, remove in Phase 2 |
