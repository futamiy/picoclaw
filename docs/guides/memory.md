# Memory Guide

PicoClaw keeps a small, editable long-term memory in the workspace and can
optionally mirror conversation history to an external memory backend. This
guide covers the local files, the configuration, external providers, and
backup/restore.

## Local memory files

All local memory lives under `<workspace>/memory/`:

| File | Purpose |
| --- | --- |
| `memory/MEMORY.md` | Long-term memory: durable world facts the agent should keep. |
| `memory/PROFILE.md` | Bounded user profile: durable facts about the user (see below). |
| `memory/YYYY-MM-DD.md` | Daily notes for a specific day. |

Edits to these files are detected automatically at runtime via mtime tracking,
so you do not need to restart the gateway after editing them by hand.

`PROFILE.md` is distinct from the workspace `USER.md` definition file: `USER.md`
is a static prompt document you write, while `PROFILE.md` is memory the agent
maintains about the user. When the profile is enabled, a `## User Profile`
block is injected into the prompt ahead of long-term memory.

### Bounded writes

Every file is capped, and a write that would exceed its budget is rejected so
the agent has to condense first. This keeps memory from growing without bound.

## Configuration

Memory is configured under `agents.defaults.memory`:

```json
{
  "agents": {
    "defaults": {
      "memory": {
        "enabled": true,
        "provider": "local",
        "long_term_char_limit": 2200,
        "profile_enabled": true,
        "profile_char_limit": 1375,
        "daily_notes_enabled": true,
        "daily_notes_days": 3,
        "daily_note_char_limit": 2200,
        "extract_enabled": false,
        "extract_every_turns": 10
      }
    }
  }
}
```

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `true` | Register the `memory` tool and inject memory into prompts. |
| `provider` | `local` | External backend: `local`, `honcho`, `mem0`, or `retaindb`. |
| `long_term_char_limit` | `2200` | Cap for `MEMORY.md`. |
| `profile_enabled` | `true` | Enable the bounded `PROFILE.md` user profile. |
| `profile_char_limit` | `1375` | Cap for `PROFILE.md` (runes). |
| `daily_notes_enabled` | `true` | Write and read dated daily notes. |
| `daily_notes_days` | `3` | How many recent days of notes are injected. |
| `daily_note_char_limit` | `2200` | Cap for each daily note file. |
| `extract_enabled` | `false` | Run the end-of-turn memory extraction pipeline. |
| `extract_every_turns` | `10` | Extract once every N completed turns. |
| `extract_model` | (unset) | Optional cheaper model used for extraction. |

Every key has a matching environment variable, e.g.
`PICOCLAW_AGENTS_DEFAULTS_MEMORY_ENABLED` and
`PICOCLAW_AGENTS_DEFAULTS_MEMORY_LONG_TERM_CHAR_LIMIT`.

The web UI exposes the same settings under the agent's **Memory** tool tab.

## External providers

Set `provider` to mirror transcripts to an external backend. Exactly one
backend is selected at a time; local memory always stays on. Sync is one-way
(conversation transcripts are pushed out) and recall is injected back into the
prompt or exposed as tools, depending on the backend.

Use `base_url` to point any provider at a self-hosted instance. Leaving it
empty uses the managed cloud default.

### Honcho

```json
{
  "memory": {
    "provider": "honcho",
    "honcho": {
      "enabled": true,
      "base_url": "https://api.honcho.dev",
      "api_key": "YOUR_KEY",
      "workspace_id": "",
      "user_peer_id": "user",
      "trigger": "ask",
      "sync_every_turns": 10,
      "recall_mode": "hybrid"
    }
  }
}
```

- `base_url` — defaults to `https://api.honcho.dev`; set it to your self-hosted
  Honcho URL to keep data on your own infrastructure.
- `workspace_id` — scopes Honcho data; empty uses the agent ID.
- `trigger` — `ask` stages each batch for manual approval; `auto` pushes without
  asking.
- `recall_mode` — `hybrid` (inject context and expose tools), `context` (inject
  only), `tools` (tools only), or `off`.

Honcho also supports `context_cadence`, `dialectic_cadence`, and
`dialectic_level` for cost tuning.

### Mem0

```json
{
  "memory": {
    "provider": "mem0",
    "mem0": {
      "enabled": true,
      "base_url": "https://api.mem0.ai",
      "api_key": "YOUR_KEY",
      "user_id": "you",
      "agent_id": "picoclaw",
      "sync_every_turns": 10
    }
  }
}
```

`base_url` defaults to `https://api.mem0.ai`; point it at a self-hosted Mem0
server to keep data local.

### RetainDB

```json
{
  "memory": {
    "provider": "retaindb",
    "retaindb": {
      "enabled": true,
      "base_url": "http://localhost:8000",
      "api_key": "YOUR_KEY",
      "sync_every_turns": 10
    }
  }
}
```

RetainDB has no cloud default — set `base_url` to your server.

## Backup and restore

Memory backup has two independent layers, and manual operations always work:

- **Drift snapshots** — before a memory file is rewritten, its previous content
  is copied to `<file>.bak.<unix_ts>` in the same directory (bounded to the last
  few). This is the fine-grained safety net for a bad edit.
- **Archives** — a zip of the whole `memory/` directory, written outside the
  workspace so a wiped workspace does not take its backups with it.

### Toggle

Both automatic layers are controlled by a single master switch,
`agents.defaults.memory.backup.enabled`, which defaults to **off** so backups
cannot silently fill the disk. Turn it on in the config or with the
**Enable Memory Backup** toggle in the web UI.

```json
{
  "memory": {
    "backup": {
      "enabled": true,
      "dir": "~/.picoclaw/backups",
      "keep": 20,
      "interval_hours": 24,
      "drift_keep": 3
    }
  }
}
```

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | Master switch for drift snapshots and scheduled archives. |
| `dir` | `~/.picoclaw/backups` | Where archives are written (outside the workspace). |
| `keep` | `20` | Archives to retain; older ones are pruned. |
| `interval_hours` | `24` | How often to archive while the toggle is on. |
| `drift_keep` | `3` | Per-file `.bak` snapshots to retain. |

Disabling the toggle turns off automatic snapshots and scheduled archives.
Running a manual backup, listing, or restoring is unaffected.

### Full backup (whole PicoClaw home)

A separate **Full backup** archives the entire PicoClaw home directory
(`~/.picoclaw/` by default), covering config, workspace (including memory),
sessions, state, and auth. It is distinct from the memory-only archive above.

**Configuration (Config settings)**

```json
{
  "backup": {
    "enabled": true,
    "dir": "~/.picoclaw/backups",
    "keep": 10,
    "interval_hours": 24,
    "include_secrets": true
  }
}
```

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | Scheduled full-home backups. Manual triggers are not dependent on this. |
| `dir` | `~/.picoclaw/backups` | Where full-backup archives are written (shared with memory archives). |
| `keep` | `10` | Archives to retain; oldest are pruned. |
| `interval_hours` | `24` | Interval for scheduled runs. |
| `include_secrets` | `true` | Include secret-bearing files (`.security.yml`, `auth.json`) when true. |

Manual full-backup commands/triggers can be added in future iterations. For now,
the full-home scheduled backups use this top-level `backup` key in Config
settings. The memory-only backup uses `agents.defaults.memory.backup` in Memory
settings.

### CLI

```bash
picoclaw memory backup                 # archive memory/ into the backup dir
picoclaw memory backup --include-config  # also include config.json (holds secrets)
picoclaw memory list                   # list archives, newest first
picoclaw memory restore <archive.zip> --force
```

`config.json` is only archived with `--include-config`, because it contains
plaintext API keys. Archives store it inside the zip.

### Web API

- `GET /api/tools/memory/backups` — list archives.
- `POST /api/tools/memory/backups` — create one (body `{"include_config": false}`).
- `POST /api/tools/memory/backups/restore` — restore (`{"path": "...", "confirm": true}`).

### Restore safety

Restoring overwrites current memory. It requires explicit confirmation
(`--force` on the CLI, `confirm: true` via the API), and before overwriting
each file it saves the current content to `<file>.bak.<unix_ts>`, so a wrong
restore is reversible.
