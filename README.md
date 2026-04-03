<p align="center">
  <img width="1024" height="340" alt="image" src="https://github.com/user-attachments/assets/32ed8985-841d-49c3-81f7-2aabc7c7c564" />
</p>

<p align="center">
  <strong>Persistent memory for AI coding agents</strong><br>
  <em>Agent-agnostic. Single binary. Zero dependencies.</em>
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> &bull;
  <a href="#install-on-windows">Windows</a> &bull;
  <a href="#how-it-works">How It Works</a> &bull;
  <a href="#agent-setup">Agent Setup</a> &bull;
  <a href="CONTRIBUTING.md">Contributing</a> &bull;
  <a href="#why-not-claude-mem">Why Not claude-mem?</a> &bull;
  <a href="#tui">Terminal UI</a> &bull;
  <a href="DOCS.md">Full Docs</a>
</p>

---

> **mnemo** `/ˈen.ɡræm/` — *neuroscience*: the physical trace of a memory in the brain.

Your AI coding agent forgets everything when the session ends. Mnemo gives it a brain.

A **Go binary** with SQLite + FTS5 full-text search, exposed via CLI, HTTP API, MCP server, and an interactive TUI. Works with **any agent** that supports MCP — Claude Code, OpenCode, Gemini CLI, Codex, VS Code (Copilot), Antigravity, Cursor, Windsurf, or anything else.

```
Agent (Claude Code / OpenCode / Gemini CLI / Codex / VS Code / Antigravity / ...)
    ↓ MCP stdio
Mnemo (single Go binary)
    ↓
SQLite + FTS5 (~/.mnemo/mnemo.db)
```

## Quick Start

### Install via Homebrew (macOS / Linux)

```bash
brew install MiguelAguiarDEV/tap/mnemo
```

Upgrade to latest:

```bash
brew update && brew upgrade mnemo
```

> **Migrating from Cask?** If you installed mnemo before v1.0.1, it was distributed as a Cask. Uninstall first, then reinstall:
> ```bash
> brew uninstall --cask mnemo 2>/dev/null; brew install MiguelAguiarDEV/tap/mnemo
> ```

### Install on Windows

**Option A: Download the binary (recommended)**

1. Go to [GitHub Releases](https://github.com/MiguelAguiarDEV/mnemo/releases)
2. Download `mnemo_<version>_windows_amd64.zip` (or `arm64` for ARM devices)
3. Extract `mnemo.exe` to a folder in your `PATH` (e.g. `C:\Users\<you>\bin\`)

```powershell
# Example: extract and add to PATH (PowerShell)
Expand-Archive mnemo_*_windows_amd64.zip -DestinationPath "$env:USERPROFILE\bin"
# Add to PATH permanently (run once):
[Environment]::SetEnvironmentVariable("Path", "$env:USERPROFILE\bin;" + [Environment]::GetEnvironmentVariable("Path", "User"), "User")
```

**Option B: Install from source**

```powershell
git clone https://github.com/MiguelAguiarDEV/mnemo.git
cd mnemo
go install ./cmd/mnemo
# Binary goes to %GOPATH%\bin\mnemo.exe (typically %USERPROFILE%\go\bin\)

# Optional: build with version stamp (otherwise `mnemo version` shows "dev")
$v = git describe --tags --always
go build -ldflags="-X main.version=local-$v" -o mnemo.exe ./cmd/mnemo
```

> **Windows notes:**
> - Data is stored in `%USERPROFILE%\.mnemo\mnemo.db`
> - Override with `MNEMO_DATA_DIR` environment variable
> - All core features work natively: CLI, MCP server, TUI, HTTP API, Git Sync
> - No WSL required for the core binary — it's a native Windows executable

### Install from source (macOS / Linux)

```bash
git clone https://github.com/MiguelAguiarDEV/mnemo.git
cd mnemo
go install ./cmd/mnemo

# Optional: build with version stamp (otherwise `mnemo version` shows "dev")
go build -ldflags="-X main.version=local-$(git describe --tags --always)" -o mnemo ./cmd/mnemo
```

### Download binary (all platforms)

Grab the latest release for your platform from [GitHub Releases](https://github.com/MiguelAguiarDEV/mnemo/releases).

| Platform | File |
|----------|------|
| macOS (Apple Silicon) | `mnemo_<version>_darwin_arm64.tar.gz` |
| macOS (Intel) | `mnemo_<version>_darwin_amd64.tar.gz` |
| Linux (x86_64) | `mnemo_<version>_linux_amd64.tar.gz` |
| Linux (ARM64) | `mnemo_<version>_linux_arm64.tar.gz` |
| Windows (x86_64) | `mnemo_<version>_windows_amd64.zip` |
| Windows (ARM64) | `mnemo_<version>_windows_arm64.zip` |

Then set up your agent's plugin:

```bash
# Claude Code — via marketplace
claude plugin marketplace add MiguelAguiarDEV/mnemo
claude plugin install mnemo

# OpenCode — via mnemo setup
mnemo setup opencode

# Gemini CLI — MCP auto-registration
mnemo setup gemini-cli

# Codex — MCP auto-registration
mnemo setup codex

# VS Code — add MCP server via CLI
code --add-mcp "{\"name\":\"mnemo\",\"command\":\"mnemo\",\"args\":[\"mcp\"]}"

# Antigravity — add via MCP Store (see Agent Setup below)

# Or interactive (asks which agent)
mnemo setup
```

See [Agent Setup](#agent-setup) for manual configuration or other agents (VS Code, Antigravity, Cursor, Windsurf).

That's it. No Node.js, no Python, no Bun, no Docker, no ChromaDB, no vector database, no worker processes. **One binary, one SQLite file.**

## How It Works

<p align="center">
  <img src="assets/agent-save.png" alt="Agent saving a memory via mem_save" width="800" />
  <br />
  <em>The agent proactively calls <code>mem_save</code> after significant work — structured, searchable, no noise.</em>
</p>

Mnemo trusts the **agent** to decide what's worth remembering — not a firehose of raw tool calls.

### The Agent Saves, Mnemo Stores

```
1. Agent completes significant work (bugfix, architecture decision, etc.)
2. Agent calls mem_save with a structured summary:
   - title: "Fixed N+1 query in user list"
   - type: "bugfix"
   - content: What/Why/Where/Learned format
3. Mnemo persists to SQLite with FTS5 indexing
4. Next session: agent searches memory, gets relevant context
```

### Session Lifecycle

```
Session starts → Agent works → Agent saves memories proactively
                                    ↓
Session ends → Agent writes session summary (Goal/Discoveries/Accomplished/Files)
                                    ↓
Next session starts → Previous session context is injected automatically
```

### 13 MCP Tools

| Tool | Purpose |
|------|---------|
| `mem_save` | Save a structured observation (decision, bugfix, pattern, etc.) |
| `mem_update` | Update an existing observation by ID |
| `mem_delete` | Delete an observation (soft-delete by default, hard-delete optional) |
| `mem_suggest_topic_key` | Suggest a stable `topic_key` for evolving topics before saving |
| `mem_search` | Full-text search across all memories |
| `mem_session_summary` | Save end-of-session summary |
| `mem_context` | Get recent context from previous sessions |
| `mem_timeline` | Chronological context around a specific observation |
| `mem_get_observation` | Get full content of a specific memory |
| `mem_save_prompt` | Save a user prompt for future context |
| `mem_stats` | Memory system statistics |
| `mem_session_start` | Register a session start |
| `mem_session_end` | Mark a session as completed |

### Progressive Disclosure (3-Layer Pattern)

Token-efficient memory retrieval — don't dump everything, drill in:

```
1. mem_search "auth middleware"     → compact results with IDs (~100 tokens each)
2. mem_timeline observation_id=42  → what happened before/after in that session
3. mem_get_observation id=42       → full untruncated content
```

### Memory Hygiene

- `mem_save` now supports `scope` (`project` default, `personal` optional)
- `mem_save` also supports `topic_key`; with a topic key, saves become upserts (same project+scope+topic updates the existing memory)
- Exact dedupe prevents repeated inserts in a rolling window (hash + project + scope + type + title)
- Duplicates update metadata (`duplicate_count`, `last_seen_at`, `updated_at`) instead of creating new rows
- Topic upserts increment `revision_count` so evolving decisions stay in one memory
- `mem_delete` uses soft-delete by default (`deleted_at`), with optional hard delete
- `mem_search`, `mem_context`, recent lists, and timeline ignore soft-deleted observations

### Topic Key Workflow (recommended)

Use this when a topic evolves over time (architecture, long-running feature decisions, etc.):

```text
1. mem_suggest_topic_key(type="architecture", title="Auth architecture")
2. mem_save(..., topic_key="architecture-auth-architecture")
3. Later change on same topic -> mem_save(..., same topic_key)
   => existing observation is updated (revision_count++)
```

Different topics should use different keys (e.g. `architecture/auth-model` vs `bug/auth-nil-panic`) so they never overwrite each other.

`mem_suggest_topic_key` now applies a family heuristic for consistency across sessions:

- `architecture/*` for architecture/design/ADR-like changes
- `bug/*` for fixes, regressions, errors, panics
- `decision/*`, `pattern/*`, `config/*`, `discovery/*`, `learning/*` when detected

## Agent Setup

Mnemo works with **any MCP-compatible agent**. Add it to your agent's MCP config:

### OpenCode

> **Prerequisite**: Install the `mnemo` binary first (via [Homebrew](#install-via-homebrew-macOS--linux), [Windows binary](#install-on-windows), [binary download](#download-binary-all-platforms), or [source](#install-from-source-macos--linux)). The plugin needs it for the MCP server and session tracking.

**Recommended: Full setup with one command** — installs the plugin AND registers the MCP server in `opencode.json` automatically:

```bash
mnemo setup opencode
```

This does two things:
1. Copies the plugin to `~/.config/opencode/plugins/mnemo.ts` (session tracking, Memory Protocol, compaction recovery)
2. Adds the `mnemo` MCP server entry to your `opencode.json` (the 13 memory tools)

The plugin also needs the HTTP server running for session tracking:

```bash
mnemo serve &
```

> **Windows**: On Windows, `mnemo setup opencode` writes to `%APPDATA%\opencode\plugins\` and `%APPDATA%\opencode\opencode.json` automatically. To run the server in the background: `Start-Process mnemo -ArgumentList "serve" -WindowStyle Hidden` (PowerShell) or just run `mnemo serve` in a separate terminal.

**Alternative: Manual MCP-only setup** (no plugin, just the 13 memory tools):

Add to your `opencode.json` (global: `~/.config/opencode/opencode.json` or project-level; on Windows: `%APPDATA%\opencode\opencode.json`):

```json
{
  "mcp": {
    "mnemo": {
      "type": "local",
      "command": ["mnemo", "mcp"],
      "enabled": true
    }
  }
}
```

See [OpenCode Plugin](#opencode-plugin) for details on what the plugin provides beyond bare MCP.

### Claude Code

> **Prerequisite**: Install the `mnemo` binary first (via [Homebrew](#install-via-homebrew-macOS--linux), [Windows binary](#install-on-windows), [binary download](#download-binary-all-platforms), or [source](#install-from-source-macos--linux)). The plugin needs it for the MCP server and session tracking scripts.

**Option A: Plugin via marketplace (recommended)** — full session management, auto-import, compaction recovery, and Memory Protocol skill:

```bash
claude plugin marketplace add MiguelAguiarDEV/mnemo
claude plugin install mnemo
```

That's it. The plugin registers the MCP server, hooks, and Memory Protocol skill automatically.

**Option B: Plugin via `mnemo setup`** — same plugin, installed from the embedded binary:

```bash
mnemo setup claude-code
```

During setup, you'll be asked whether to add mnemo tools to `~/.claude/settings.json` permissions allowlist — this prevents Claude Code from prompting for confirmation on every memory operation.

**Option C: Bare MCP** — just the 13 memory tools, no session management:

Add to your `.claude/settings.json` (project) or `~/.claude/settings.json` (global):

```json
{
  "mcpServers": {
    "mnemo": {
      "command": "mnemo",
      "args": ["mcp"]
    }
  }
}
```

With bare MCP, add a [Surviving Compaction](#surviving-compaction-recommended) prompt to your `CLAUDE.md` so the agent remembers to use Mnemo after context resets.

> **Windows note:** The Claude Code plugin hooks use bash scripts. On Windows, Claude Code runs hooks through Git Bash (bundled with [Git for Windows](https://gitforwindows.org/)) or WSL. If hooks don't fire, ensure `bash` is available in your `PATH`. Alternatively, use **Option C (Bare MCP)** which works natively on Windows without any shell dependency.

See [Claude Code Plugin](#claude-code-plugin) for details on what the plugin provides.

### Gemini CLI

Recommended: one command to set up MCP + compaction recovery instructions:

```bash
mnemo setup gemini-cli
```

`mnemo setup gemini-cli` now does three things:
- Registers `mcpServers.mnemo` in `~/.gemini/settings.json` (Windows: `%APPDATA%\gemini\settings.json`)
- Writes `~/.gemini/system.md` with the Mnemo Memory Protocol (includes post-compaction recovery)
- Ensures `~/.gemini/.env` contains `GEMINI_SYSTEM_MD=1` so Gemini actually loads that system prompt

> `mnemo setup gemini-cli` automatically writes the full Memory Protocol to `~/.gemini/system.md`, so the agent knows exactly when to save, search, and close sessions. No additional configuration needed.

Manual alternative: add to your `~/.gemini/settings.json` (global) or `.gemini/settings.json` (project); on Windows: `%APPDATA%\gemini\settings.json`:


```json
{
  "mcpServers": {
    "mnemo": {
      "command": "mnemo",
      "args": ["mcp"]
    }
  }
}
```

Or via the CLI:

```bash
gemini mcp add mnemo mnemo mcp
```

### Codex

Recommended: one command to set up MCP + compaction recovery instructions:

```bash
mnemo setup codex
```

`mnemo setup codex` now does three things:
- Registers `[mcp_servers.mnemo]` in `~/.codex/config.toml` (Windows: `%APPDATA%\codex\config.toml`)
- Writes `~/.codex/mnemo-instructions.md` with the Mnemo Memory Protocol
- Writes `~/.codex/mnemo-compact-prompt.md` and points `experimental_compact_prompt_file` to it, so compaction output includes a required memory-save instruction

> `mnemo setup codex` automatically writes the full Memory Protocol to `~/.codex/mnemo-instructions.md` and a compaction recovery prompt to `~/.codex/mnemo-compact-prompt.md`. No additional configuration needed.

Manual alternative: add to your `~/.codex/config.toml` (Windows: `%APPDATA%\codex\config.toml`):

```toml
model_instructions_file = "~/.codex/mnemo-instructions.md"
experimental_compact_prompt_file = "~/.codex/mnemo-compact-prompt.md"

[mcp_servers.mnemo]
command = "mnemo"
args = ["mcp"]
```

### VS Code (Copilot / Claude Code Extension)

VS Code supports MCP servers natively in its chat panel (Copilot agent mode). This works with **any** AI agent running inside VS Code — Copilot, Claude Code extension, or any other MCP-compatible chat provider.

**Option A: Workspace config** (recommended for teams — commit to source control):

Add to `.vscode/mcp.json` in your project:

```json
{
  "servers": {
    "mnemo": {
      "command": "mnemo",
      "args": ["mcp"]
    }
  }
}
```

**Option B: User profile** (global, available across all workspaces):

1. Open Command Palette (`Cmd+Shift+P` / `Ctrl+Shift+P`)
2. Run **MCP: Open User Configuration**
3. Add the same `mnemo` server entry above to VS Code User `mcp.json`:
   - macOS: `~/Library/Application Support/Code/User/mcp.json`
   - Linux: `~/.config/Code/User/mcp.json`
   - Windows: `%APPDATA%\Code\User\mcp.json`

**Option C: CLI one-liner:**

```bash
code --add-mcp "{\"name\":\"mnemo\",\"command\":\"mnemo\",\"args\":[\"mcp\"]}"
```

> **Using Claude Code extension in VS Code?** The Claude Code extension runs inside VS Code but uses its own MCP config. Follow the [Claude Code](#claude-code) instructions above — the `.claude/settings.json` config works whether you use Claude Code as a CLI or as a VS Code extension.

> **Windows**: Make sure `mnemo.exe` is in your `PATH`. VS Code resolves MCP commands from the system PATH.

**Adding the Memory Protocol** (recommended — teaches the agent when to save and search memories):

Without the Memory Protocol, the agent has the tools but doesn't know WHEN to use them. Add these instructions to your agent's prompt:

**For Copilot:** Create a `.instructions.md` file in the VS Code User `prompts/` folder and paste the Memory Protocol from [DOCS.md](DOCS.md#memory-protocol-full-text).

Recommended file path:
- macOS: `~/Library/Application Support/Code/User/prompts/mnemo-memory.instructions.md`
- Linux: `~/.config/Code/User/prompts/mnemo-memory.instructions.md`
- Windows: `%APPDATA%\Code\User\prompts\mnemo-memory.instructions.md`

**For any VS Code chat extension:** Add the Memory Protocol text to your extension's custom instructions or system prompt configuration.

The Memory Protocol tells the agent:
- **When to save** — after bugfixes, decisions, discoveries, config changes, patterns
- **When to search** — reactive ("remember", "recall") + proactive (overlapping past work)
- **Session close** — mandatory `mem_session_summary` before ending
- **After compaction** — recover state with `mem_context`

See [Surviving Compaction](#surviving-compaction-recommended) for the minimal version, or [DOCS.md](DOCS.md#memory-protocol-full-text) for the full Memory Protocol text you can copy-paste.

### Antigravity

[Antigravity](https://antigravity.google) is Google's AI-first IDE with native MCP and skill support.

**Add the MCP server** — open the MCP Store (`...` dropdown in the agent panel) → **Manage MCP Servers** → **View raw config**, and add to `~/.gemini/antigravity/mcp_config.json`:

```json
{
  "mcpServers": {
    "mnemo": {
      "command": "mnemo",
      "args": ["mcp"]
    }
  }
}
```

**Adding the Memory Protocol** (recommended):

Add the Memory Protocol as a global rule in `~/.gemini/GEMINI.md`, or as a workspace rule in `.agent/rules/`. See [DOCS.md](DOCS.md#memory-protocol-full-text) for the full text, or use the minimal version from [Surviving Compaction](#surviving-compaction-recommended).

> **Note:** Antigravity has its own skill, rule, and MCP systems separate from VS Code. Do not use `.vscode/mcp.json`.

### Cursor

Add to your `.cursor/mcp.json` (same path on all platforms — it's project-relative):

```json
{
  "mcpServers": {
    "mnemo": {
      "command": "mnemo",
      "args": ["mcp"]
    }
  }
}
```

> **Windows**: Make sure `mnemo.exe` is in your `PATH`. Cursor resolves MCP commands from the system PATH.

> **Memory Protocol:** Add the Memory Protocol instructions to your `.cursorrules` file. See [DOCS.md](DOCS.md#memory-protocol-full-text) for the full text, or use the minimal version from [Surviving Compaction](#surviving-compaction-recommended).

### Windsurf

Add to your `~/.windsurf/mcp.json` (Windows: `%USERPROFILE%\.windsurf\mcp.json`):

```json
{
  "mcpServers": {
    "mnemo": {
      "command": "mnemo",
      "args": ["mcp"]
    }
  }
}
```

> **Memory Protocol:** Add the Memory Protocol instructions to your `.windsurfrules` file. See [DOCS.md](DOCS.md#memory-protocol-full-text) for the full text.

### Any other MCP agent

The pattern is always the same — point your agent's MCP config to `mnemo mcp` via stdio transport.

### Surviving Compaction (Recommended)

When your agent compacts (summarizes long conversations to free context), it starts fresh — and might forget about Mnemo. To make memory truly resilient, add this to your agent's system prompt or config file:

**For Claude Code** (`CLAUDE.md`):
```markdown
## Memory
You have access to Mnemo persistent memory via MCP tools (mem_save, mem_search, mem_session_summary, etc.).
- Save proactively after significant work — don't wait to be asked.
- After any compaction or context reset, call `mem_context` to recover session state before continuing.
```

**For OpenCode** (agent prompt in `opencode.json`):
```
After any compaction or context reset, call mem_context to recover session state before continuing.
Save memories proactively with mem_save after significant work.
```

**For Gemini CLI** (`GEMINI.md`):
```markdown
## Memory
You have access to Mnemo persistent memory via MCP tools (mem_save, mem_search, mem_session_summary, etc.).
- Save proactively after significant work — don't wait to be asked.
- After any compaction or context reset, call `mem_context` to recover session state before continuing.
```

**For VS Code** (`Code/User/prompts/*.instructions.md` or custom instructions):
```markdown
## Memory
You have access to Mnemo persistent memory via MCP tools (mem_save, mem_search, mem_session_summary, etc.).
- Save proactively after significant work — don't wait to be asked.
- After any compaction or context reset, call `mem_context` to recover session state before continuing.
```

**For Antigravity** (`~/.gemini/GEMINI.md` or `.agent/rules/`):
```markdown
## Memory
You have access to Mnemo persistent memory via MCP tools (mem_save, mem_search, mem_session_summary, etc.).
- Save proactively after significant work — don't wait to be asked.
- After any compaction or context reset, call `mem_context` to recover session state before continuing.
```

**For Cursor/Windsurf** (`.cursorrules` or `.windsurfrules`):
```
You have access to Mnemo persistent memory (mem_save, mem_search, mem_context).
Save proactively after significant work. After context resets, call mem_context to recover state.
```

This is the **nuclear option** — system prompts survive everything, including compaction.

## Why Not claude-mem?

[claude-mem](https://github.com/thedotmack/claude-mem) is a great project (28K+ stars!) that inspired Mnemo. But we made fundamentally different design decisions:

| | **Mnemo** | **claude-mem** |
|---|---|---|
| **Language** | Go (single binary, zero runtime deps) | TypeScript + Python (needs Node.js, Bun, uv) |
| **Agent lock-in** | None. Works with any MCP agent | Claude Code only (uses Claude plugin hooks) |
| **Search** | SQLite FTS5 (built-in, zero setup) | ChromaDB vector database (separate process) |
| **What gets stored** | Agent-curated summaries only | Raw tool calls + AI compression |
| **Compression** | Agent does it inline (it already has the LLM) | Separate Claude API calls via agent-sdk |
| **Dependencies** | `go install` and done | Node.js 18+, Bun, uv, Python, ChromaDB |
| **Processes** | One binary (or none — MCP stdio) | Worker service on port 37777 + ChromaDB |
| **Database** | Single `~/.mnemo/mnemo.db` file | SQLite + ChromaDB (two storage systems) |
| **Web UI** | Terminal TUI (`mnemo tui`) | Web viewer on localhost:37777 |
| **Privacy** | `<private>` tags stripped at 2 layers | `<private>` tags stripped |
| **Auto-capture** | No. Agent decides what matters | Yes. Captures all tool calls then compresses |
| **License** | MIT | AGPL-3.0 |

### The Core Philosophy Difference

**claude-mem** captures *everything* and then compresses it with AI. This means:

- Extra API calls for compression (costs money, adds latency)
- Raw tool calls pollute search results until compressed
- Requires a worker process, ChromaDB, and multiple runtimes
- Locked to Claude Code's plugin system

**Mnemo** lets the agent decide what's worth remembering. The agent already has the LLM, the context, and understands what just happened. Why run a separate compression pipeline?

- `mem_save` after a bugfix: *"Fixed N+1 query — added eager loading in UserList"*
- `mem_session_summary` at session end: structured Goal/Discoveries/Accomplished/Files
- No noise, no compression step, no extra API calls
- Works with ANY agent via standard MCP

**The result**: cleaner data, faster search, no infrastructure overhead, agent-agnostic.

## TUI

Interactive terminal UI for browsing your memory. Built with [Bubbletea](https://github.com/charmbracelet/bubbletea).

```bash
mnemo tui
```
<p align="center">
<img src="assets/tui-dashboard.png" alt="TUI Dashboard" width="400" />
  <img width="400" alt="image" src="https://github.com/user-attachments/assets/0308991a-58bb-4ad8-9aa2-201c059f8b64" />
  <img src="assets/tui-detail.png" alt="TUI Observation Detail" width="400" />
  <img src="assets/tui-search.png" alt="TUI Search Results" width="400" />
</p>

**Screens**: Dashboard, Search, Recent Observations, Observation Detail, Timeline, Sessions, Session Detail

**Navigation**: `j/k` vim keys, `Enter` to drill in, `t` for timeline, `/` to search, `Esc` to go back

**Features**:
- Catppuccin Mocha color palette
- Scroll indicators for long lists
- Full FTS5 search from the TUI
- Live data refresh on back-navigation

## Git Sync

Share memories across machines and team members by committing them to your repo. Uses compressed chunks with a manifest index — no merge conflicts, no huge files.

```bash
# Export new memories as a compressed chunk
# (automatically filters by current directory name as project)
mnemo sync

# Export ALL memories from every project (useful for a shared notes repo)
mnemo sync --all

# Commit to git
git add .mnemo/ && git commit -m "sync mnemo memories"

# On another machine / clone: import new chunks
mnemo sync --import

# Check sync status
mnemo sync --status

# Override project detection if needed
mnemo sync --project other-name
```

**How it works:**

```
.mnemo/
├── manifest.json          ← small index (git diffs this)
├── chunks/
│   ├── a3f8c1d2.jsonl.gz ← chunk by Alan (compressed, ~2KB)
│   ├── b7d2e4f1.jsonl.gz ← chunk by Juan
│   └── c9f1a2b3.jsonl.gz ← chunk by Alan (next day)
└── mnemo.db              ← gitignored (local working DB)
```

- Each `mnemo sync` creates a **new chunk** — never modifies old ones
- Chunks are **gzipped JSONL** — small files, git treats as binary (no diff noise)
- The **manifest** is the only file git diffs — it's small and append-only
- Each chunk has a **content hash ID** — imported only once, no duplicates
- **No merge conflicts** on data — each dev creates independent chunks

**Auto-import**: The OpenCode plugin automatically runs `mnemo sync --import` when it detects `.mnemo/manifest.json` in the project directory. Clone a repo, open OpenCode, and the team's memories are loaded.

## Cloud Sync

Sync memories across machines via a Postgres-backed cloud server. **Auto-sync is on by default** — when you run `mnemo serve` or `mnemo mcp` with cloud credentials configured, every local write automatically pushes/pulls in the background. No manual sync needed.

- Cloud failures never block local reads or writes — the sync manager degrades gracefully with exponential backoff
- Manual one-off sync: `mnemo cloud sync` (push + pull, then exit)
- Check sync health: `mnemo cloud sync-status` (pending mutations, degraded state)
- Project-scoped sync: `mnemo cloud enroll <project>` to choose which projects sync to the cloud
- Cloud-managed org policy: admins can pause/resume sync per project from the dashboard, with reason + audit trail
- Autosync batches pushes by project, so one paused project does not block unrelated project mutations
- **Web Dashboard**: Browse knowledge, projects, and contributor stats in the browser at `/dashboard/`
- Legacy chunk-based sync: `mnemo cloud sync --legacy` (deprecated, preserved for backward compatibility)
- Client contract stays simple: one reachable base URL + one token

See `DOCS.md` for the full cloud workflow, dashboard setup, security notes, and local two-machine testing guidance.

### Cloud Dashboard

A server-rendered web UI embedded in `mnemo cloud serve`. Navigate to `http://<server>/dashboard/` and log in with your cloud credentials.

- **Dashboard** — Shared-memory overview with synced project stats
- **Browser** — Search and browse observations, sessions, and prompts with linked detail pages
- **Projects** — Per-project detail views, pause status, and recent activity
- **Contributors** — Per-developer stats plus drill-down into sessions, observations, and prompts
- **Admin** — System health, user management, and project sync controls (requires `MNEMO_CLOUD_ADMIN` env var)

Built with templ + htmx — zero JS build step, ships inside the single binary.

## CLI

```
mnemo setup [agent]      Install/setup agent integration (opencode, claude-code, gemini-cli, codex)
mnemo serve [port]       Start HTTP API server (default: 7437)
mnemo mcp                Start MCP server (stdio transport)
mnemo tui                Launch interactive terminal UI
mnemo search <query>     Search memories
mnemo save <title> <msg> Save a memory
mnemo timeline <obs_id>  Chronological context around an observation
mnemo context [project]  Recent context from previous sessions
mnemo stats              Memory statistics
mnemo export [file]      Export all memories to JSON
mnemo import <file>      Import memories from JSON
mnemo sync               Export new memories as compressed chunk to .mnemo/
mnemo sync --all         Export ALL projects (ignore directory-based filter)
mnemo cloud serve        Start cloud server (Postgres backend)
mnemo cloud register     Register a cloud account
mnemo cloud login        Login to a cloud account
mnemo cloud sync         Sync local mutations to cloud (push + pull)
mnemo cloud sync-status  Show local sync journal state
mnemo cloud status       Show cloud sync status (legacy chunks)
mnemo cloud api-key      Generate an API key for cloud access
mnemo cloud enroll <p>   Enroll a project for cloud sync
mnemo cloud unenroll <p> Unenroll a project from cloud sync
mnemo cloud projects     List enrolled projects
mnemo version            Show version
```

## OpenCode Plugin

For [OpenCode](https://opencode.ai) users, a thin TypeScript plugin adds enhanced session management on top of the MCP tools:

```bash
# Install via mnemo (recommended — works from Homebrew or binary install)
mnemo setup opencode

# Or manually: cp plugin/opencode/mnemo.ts ~/.config/opencode/plugins/
```

The plugin auto-starts the HTTP server if it's not already running — no manual `mnemo serve` needed.

> **Local model compatibility:** The plugin works with all models, including local ones served via llama.cpp, Ollama, or similar. The Memory Protocol is concatenated into the existing system prompt (not added as a separate system message), so models with strict Jinja templates (Qwen, Mistral/Ministral) work correctly.

The plugin:
- **Auto-starts** the mnemo server if not running
- **Auto-imports** git-synced memories from `.mnemo/manifest.json` if present in the project
- **Creates sessions** on-demand via `ensureSession()` (resilient to restarts/reconnects)
- **Injects the Memory Protocol** into the agent's system prompt via `chat.system.transform` — strict rules for when to save, when to search, and a mandatory session close protocol. The protocol is concatenated into the existing system message (not pushed as a separate one), ensuring compatibility with models that only accept a single system block (Qwen, Mistral/Ministral via llama.cpp, etc.)
- **Injects previous session context** into the compaction prompt
- **Instructs the compressor** to tell the new agent to persist the compacted summary via `mem_session_summary`
- **Strips `<private>` tags** before sending data

**No raw tool call recording** — the agent handles all memory via `mem_save` and `mem_session_summary`.

### Memory Protocol (injected via system prompt)

The plugin injects a strict protocol into every agent message:

- **WHEN TO SAVE**: Mandatory after bugfixes, decisions, discoveries, config changes, patterns, preferences
- **WHEN TO SEARCH**: Reactive (user says "remember"/"recordar") + proactive (starting work that might overlap past sessions)
- **SESSION CLOSE**: Mandatory `mem_session_summary` before ending — "This is NOT optional. If you skip this, the next session starts blind."
- **AFTER COMPACTION**: Immediately call `mem_context` to recover state

### Three Layers of Memory Resilience

The OpenCode plugin uses a defense-in-depth strategy to ensure memories survive compaction:

| Layer | Mechanism | Survives Compaction? |
|-------|-----------|---------------------|
| **System Prompt** | `MEMORY_INSTRUCTIONS` concatenated into existing system prompt via `chat.system.transform` | Always present |
| **Compaction Hook** | Auto-saves checkpoint + injects context + reminds compressor | Fires during compaction |
| **Agent Config** | "After compaction, call `mem_context`" in agent prompt | Always present |

## Claude Code Plugin

For [Claude Code](https://docs.anthropic.com/en/docs/claude-code) users, a plugin adds enhanced session management using Claude's native hook and skill system:

```bash
# Install via Claude Code marketplace (recommended)
claude plugin marketplace add MiguelAguiarDEV/mnemo
claude plugin install mnemo

# Or via mnemo binary (works from Homebrew or binary install)
mnemo setup claude-code

# Or for local development/testing from the repo
claude --plugin-dir ./plugin/claude-code
```

### What the Plugin Provides (vs bare MCP)

| Feature | Bare MCP | Plugin |
|---------|----------|--------|
| 13 memory tools | ✓ | ✓ |
| Session tracking (auto-start) | ✗ | ✓ |
| Auto-import git-synced memories | ✗ | ✓ |
| Compaction recovery | ✗ | ✓ |
| Memory Protocol skill | ✗ | ✓ |
| Previous session context injection | ✗ | ✓ |

### Plugin Structure

```
plugin/claude-code/
├── .claude-plugin/plugin.json     # Plugin manifest
├── .mcp.json                      # Registers mnemo MCP server
├── hooks/hooks.json               # SessionStart + SubagentStop + Stop lifecycle hooks
├── scripts/
│   ├── session-start.sh           # Ensures server, creates session, imports chunks, injects context
│   ├── post-compaction.sh         # Injects previous context + recovery instructions
│   ├── subagent-stop.sh           # Passive capture trigger on subagent completion
│   └── session-stop.sh            # Logs end-of-session event
└── skills/memory/SKILL.md         # Memory Protocol (when to save, search, close, recover)
```

### How It Works

**On session start** (`startup`):
1. Ensures the mnemo HTTP server is running
2. Creates a new session via the API
3. Auto-imports git-synced chunks from `.mnemo/manifest.json` (if present)
4. Injects previous session context into Claude's initial context

**On compaction** (`compact`):
1. Injects the previous session context + compacted summary
2. Tells the agent: "FIRST ACTION REQUIRED — call `mem_session_summary` with this content before doing anything else"
3. This ensures no work is lost when context is compressed

**Memory Protocol skill** (always available):
- Strict rules for **when to save** (mandatory after bugfixes, decisions, discoveries)
- **When to search** memory (reactive + proactive)
- **Session close protocol** — mandatory `mem_session_summary` before ending
- **After compaction** — 3-step recovery: persist summary → load context → continue

## Privacy

Wrap sensitive content in `<private>` tags — it gets stripped at TWO levels:

```
Set up API with <private>sk-abc123</private> key
→ Set up API with [REDACTED] key
```

1. **Plugin layer** — stripped before data leaves the process
2. **Store layer** — `stripPrivateTags()` in Go before any DB write

## Project Structure

```
mnemo/
├── cmd/mnemo/main.go              # CLI entrypoint
├── internal/
│   ├── store/store.go              # Core: SQLite + FTS5 + all data ops
│   ├── server/server.go            # HTTP REST API (port 7437)
│   ├── mcp/mcp.go                  # MCP stdio server (13 tools)
│   ├── setup/setup.go              # Agent plugin installer (go:embed)
│   ├── sync/sync.go                # Git sync: manifest + compressed chunks
│   ├── cloud/
│   │   ├── autosync/manager.go     # Background auto-sync manager (lease + backoff)
│   │   ├── cloudstore/             # Postgres storage (schema, CRUD, search, project controls)
│   │   ├── cloudserver/            # Cloud HTTP API (auth, push/pull, mutations)
│   │   ├── dashboard/              # Embedded web dashboard (templ + htmx)
│   │   └── remote/transport.go     # HTTP client for cloud sync
│   └── tui/                        # Bubbletea terminal UI
│       ├── model.go                # Screen constants, Model, Init()
│       ├── styles.go               # Lipgloss styles (Catppuccin Mocha)
│       ├── update.go               # Input handling, per-screen handlers
│       └── view.go                 # Rendering, per-screen views
├── plugin/
│   ├── opencode/mnemo.ts          # OpenCode adapter plugin
│   └── claude-code/                # Claude Code plugin (hooks + skill)
│       ├── .claude-plugin/plugin.json
│       ├── .mcp.json
│       ├── hooks/hooks.json
│       ├── scripts/                # session-start, post-compaction, subagent-stop, session-stop
│       └── skills/memory/SKILL.md
├── skills/                         # Contributor AI skills (repo-wide standards + Mnemo-specific guardrails)
├── setup.sh                        # Links repo skills into .claude/.codex/.gemini (project-local)
├── assets/                         # Screenshots and media
├── DOCS.md                         # Full technical documentation
├── CONTRIBUTING.md                 # Contribution workflow and standards
├── go.mod
└── go.sum
```

## Requirements

- **Go 1.25+** to build from source (not needed if installing via Homebrew or downloading a binary)
- That's it. No runtime dependencies.

The binary includes SQLite (via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — pure Go, no CGO). Works natively on **macOS**, **Linux**, and **Windows** (x86_64 and ARM64).

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `MNEMO_DATA_DIR` | Data directory | `~/.mnemo` (Windows: `%USERPROFILE%\.mnemo`) |
| `MNEMO_PORT` | HTTP server port | `7437` |

### Windows Config Paths

When using `mnemo setup`, config files are written to platform-appropriate locations:

| Agent | macOS / Linux | Windows |
|-------|---------------|---------|
| OpenCode | `~/.config/opencode/` | `%APPDATA%\opencode\` |
| Gemini CLI | `~/.gemini/` | `%APPDATA%\gemini\` |
| Codex | `~/.codex/` | `%APPDATA%\codex\` |
| Claude Code | Managed by `claude` CLI | Managed by `claude` CLI |
| VS Code | `.vscode/mcp.json` (workspace) or `~/Library/Application Support/Code/User/mcp.json` (user) | `.vscode\mcp.json` (workspace) or `%APPDATA%\Code\User\mcp.json` (user) |
| Antigravity | `~/.gemini/antigravity/mcp_config.json` | `%USERPROFILE%\.gemini\antigravity\mcp_config.json` |
| Data directory | `~/.mnemo/` | `%USERPROFILE%\.mnemo\` |

## License

MIT

---

**Inspired by [claude-mem](https://github.com/thedotmack/claude-mem)** — but agent-agnostic, simpler, and built different.
