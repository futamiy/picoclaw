# Honcho Native Memory (tanpa MCP) — Investigasi & Rencana

Status: implementasi selesai + **terverifikasi live** di host uji.
Tanggal: 2026-10-18
Host uji: `root@192.168.1.20` (armbian, picoclaw gateway + launcher)

## Pertanyaan

Apakah picoclaw bisa membaca/mengambil memori dari memori eksternal (Honcho)
**tanpa MCP**, seperti Hermes Agent?

## Jawaban

**Ya.** Jalur native (HTTP langsung ke Honcho REST API v3) sudah ada di repo
(branch `memori`) dan sudah terbukti berjalan di host uji. MCP **tidak**
diperlukan untuk sync maupun recall.

## Bukti di kode

| Komponen | Lokasi | Fungsi |
|---|---|---|
| Honcho HTTP client (write) | `pkg/honcho/client.go` | `Ensure`, `AddMessages` |
| Honcho HTTP client (read) | `pkg/honcho/read.go` | `GetSessionContext`, `GetPeerContext`, `GetPeerCard`, `SearchPeer`, `Chat`, `CreateConclusion` |
| Pending batch (mode `ask`) | `pkg/honcho/pending.go` | staging approval di disk |
| Syncer + recall | `pkg/agent/memory_honcho.go` | `HonchoSyncer`, `RecallContext`, `fetchContext`, prefetch background |
| Wiring provider | `pkg/agent/instance.go:398-466` | pilih Honcho/Mem0/RetainDB, registrasi tool native `honcho` |
| Inject ke prompt | `pkg/agent/prompt_turn.go:44,112` → `pkg/agent/context.go:969` | `req.ExternalMemoryContext` → blok `EXTERNAL_MEMORY` |
| Sync akhir turn | `pkg/agent/pipeline_finalize.go:85` | `MemorySync.MaybeSync` |
| Tool native | `pkg/tools/honcho/tool.go` | actions: `context`, `profile`, `search`, `reason`, `conclude` |
| Config | `pkg/config/config.go:479-515, 746-782` | `HonchoMemoryConfig`, `EffectiveRecallMode`, `InjectsContext`, `ExposesTools` |

Commit asal: `12fcd90e feat(memory): pluggable external memory providers (Honcho recall, Mem0, RetainDB)`

### Alur recall native

```
turn start
  → recallExternalMemoryContext()    (prompt_turn.go:44)
  → HonchoSyncer.RecallContext()     (memory_honcho.go:126)
      → cache hit? return
      → maybePrefetch() → goroutine (timeout 8s)
          → GetSessionContext() → fallback GetPeerContext()
          → renderHonchoContext() → cache
  → req.ExternalMemoryContext → system prompt
turn end
  → MemorySync.MaybeSync() → AddMessages() (async)
```

Non-blocking: miss mengembalikan `""` sampai fetch background selesai.

## Bukti di host uji (`192.168.1.20`)

Config `/root/.picoclaw/config.json`:

```json
"memory": {
  "enabled": true,
  "provider": "honcho",
  "honcho": {
    "enabled": true,
    "base_url": "https://api.honcho.dev",
    "workspace_id": "miki-miki",
    "user_peer_id": "futami",
    "agent_peer_id": "miki-hoshi",
    "trigger": "auto",
    "sync_every_turns": 1,
    "recall_mode": "hybrid"
  }
}
```

- Backup `config.json.pre-native-honcho-20261015-033640` → menandai migrasi ke Honcho native.
- `hooks.enabled: false`, `hooks.processes: {}` → tidak ada bridge Python/hook aktif.
- Tidak ada service Honcho self-hosted / container Honcho → memakai `api.honcho.dev`.
- Log `gateway.log`:
  - `Registered core tool` `name:"honcho"` (tool native)
  - `Honcho context prefetched` — `memory_honcho.go:195`
  - `Runtime event: agent.memory.honcho.synced`

## Temuan / masalah

### 1. MCP Honcho masih aktif bersamaan dengan native (redundan + error)

`tools.mcp.servers.honcho` enabled → `https://mcp.honcho.dev`, ter-load
sebagai 20+ tool `mcp_honcho_*`. Tidak bentrok nama dengan tool native `honcho`,
tapi ada dua jalur baca/tulis ke Honcho yang sama.

Error nyata di log:

```
mcp_honcho_list_peers:   Missing workspace_id
mcp_honcho_create_peer:  invalid_format (regex peer ID)
```

Header `X-Honcho-Workspace-ID` sudah di-set di config MCP tetapi server tetap
meminta `workspace_id` per call → jalur MCP rapuh dan tidak diperlukan.

### 2. Dialectic cadence/level dormant — ~~sudah diperbaiki~~ (lihat "Implementasi lanjutan")

### 3. Mem0 / RetainDB hanya write-only — ~~sudah diperbaiki~~ (lihat "Implementasi lanjutan")

## Perubahan yang direncanakan

1. ~~**Verifikasi native tanpa MCP**~~ — **selesai 2026-10-18**, lihat
   "Verifikasi live" di bawah.
2. ~~**Rapikan config**~~ — `mcp.servers.honcho.enabled = false` **permanen**
   di host uji (backup: `config.json.bak-20261018-080719`). `recall_mode:
   hybrid` dipertahankan.
3. ~~**Paritas Hermes**~~ — dieksekusi, lihat "Implementasi lanjutan".

## Catatan

- Semua perubahan config ada di host remote (`/root/.picoclaw/config.json`);
  jangan commit secret (API key Honcho, token MCP).
- Referensi perilaku Hermes: `Memori Hermes.md` di root repo.

## Implementasi lanjutan (2026-10-18)

Perubahan #3 (paritas Hermes: recall untuk Mem0/RetainDB) sudah dieksekusi:

| Komponen | Lokasi | Fungsi |
|---|---|---|
| Mem0 read | `pkg/mem0/client.go` | `SearchMemories` (v3, fallback v2 + OSS `/search`), `ListMemories` |
| RetainDB read | `pkg/retaindb/client.go` | `SearchMemories` (`/v1/memory/search`), `GetProfile` (`/v1/memory/profile`) |
| Recall generik | `pkg/agent/memory_external.go` | `MemorySyncer.RecallContext` + cache prefetch background (mirror Honcho) |
| Tool native Mem0 | `pkg/tools/mem0/tool.go` | actions: `search`, `recent`, `add` |
| Tool native RetainDB | `pkg/tools/retaindb/tool.go` | actions: `search`, `profile`, `remember` |
| Wiring | `pkg/agent/instance.go` | Mem0/RetainDB kini menerima `memoryRecallConfig` + registrasi tool |
| Inject prompt | `pkg/agent/context.go` | blok `EXTERNAL_MEMORY` generik (label provider: honcho/mem0/retaindb) |
| Config | `pkg/config/config.go` | `recall_mode`, `context_cadence`, `recall_limit` (Mem0/RetainDB), `user_id` (RetainDB) |
| WebUI | `web/backend/api/memory.go`, `web/frontend/.../mem0-settings.tsx`, `retaindb-settings.tsx` | toggle recall + cadence + limit |

Default `recall_mode` provider non-Honcho adalah `hybrid`: inject konteks +
tool native (`mem0` / `retaindb`). Mode lain: `context` (inject saja), `tools`
(tool saja), `off` (write-only). Prefetch tetap non-blocking dengan timeout 8
detik seperti Honcho.

### Perbaikan rute tulis/baca (verifikasi skema)

- Mem0 tulis memakai pipeline V3 `POST /v3/memories/add/` (entity ID top-level),
  fallback lama `POST /v1/memories/`. Baca: V3 `POST /v3/memories/search/`
  (`filters` + `top_k`) → V2 (entity ID top-level + `limit`) → OSS `POST /search`;
  list: V3 `POST /v3/memories/` → V2/V1 `GET /v2|v1/memories/`.
- RetainDB tulis memakai `POST /v1/memory/ingest/session` dengan scope
  `project`/`user_id`/`session_id` + `write_mode: sync`, fallback lama
  `/v1/memories/`. Baca: `/v1/memory/search` dan `/v1/memory/profile/:user`
  (fallback `/v1/memories`). Simpan fakta: `POST /v1/memory`.

### Dialectic Honcho (paritas Hermes)

`dialecticCadence`/`dialecticLevel` kini dipakai: saat kadens terpenuhi dan ada
query, prefetch latar memanggil `peers/{ai}/chat` dan menambah blok "Reasoning
about what matters here" (maks 600 rune) ke konteks. Tetap non-blocking.

Catatan: verifikasi live ke api.mem0.ai / RetainDB cloud belum dijalankan (tidak
ada kredensial di mesin ini); skema dipatok lewat unit/contract test terhadap
bentuk payload resmi.

## Verifikasi live (2026-10-18, host `192.168.1.20`)

Deploy build `727007af-dirty` (gateway amd64 + launcher arm64), MCP honcho
dimatikan, `gateway.log_level: debug`, heartbeat interval dipendekkan sementara.

Hasil (semua di `gateway.log`):

| Cek | Bukti | Status |
|---|---|---|
| Tool native terdaftar tanpa MCP | `Registered core tool name:"honcho"`; tools 100 → 61 | ✅ |
| MCP honcho nonaktif | `Skipping disabled server honcho` (hanya vidiq tersisa) | ✅ |
| Prefetch jalan | `Honcho context prefetched chars:781` + event `agent.memory.honcho.prefetched` tiap turn | ✅ |
| Blok `EXTERNAL_MEMORY` masuk system prompt | task heartbeat: balasan `MEM_YES EXTERNAL_MEMORY: The following background memory was recalled from...` (3 turn berturut-turut, termasuk turn kedua dst. setelah cache terisi — bukti jalur recall → `context.go:978`) | ✅ |
| Sync (write) | jalur kode tak berubah (`MaybeSync`/`AddMessages`); terbukti jalan 15 Okt dengan binary lama; akan ter-fire otomatis di pesan Telegram sungguhan berikutnya (heartbeat session tak punya history → skip sync by design) | ⏳ |

Dialectic `Chat()` untuk session `heartbeat` 404 (`Session heartbeat not
found`) — expected, fallback ke peer context 781 char.

Setelah verifikasi, config dikembalikan: `gateway.log_level: warn`,
`heartbeat.interval: 30`, task heartbeat dihapus. MCP honcho tetap nonaktif
(permanen). Backup config: `config.json.bak-20261018-080719`.
