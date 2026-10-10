[NousResearch/hermes-agent](https://github.com/NousResearch/hermes-agent)

dari code ini, bagaimana hermes agent menyimpan memori?

Saya akan mencari informasi tentang bagaimana Hermes Agent menyimpan memori dengan melihat kode yang relevan.

Hermes Agent menyimpan memori melalui **dua mekanisme utama**:

## 1\. Bounded Persistent Memory Files (untuk Ingatan Personal)

Hermes menyimpan ingatan Agent dan profil pengguna dalam **dua file teks sederhana** di `~/.hermes/memories/`:

| File | Fungsi | Kapasitas |
| --- | --- | --- |
| **MEMORY.md** | Catatan pribadi Agent — fakta lingkungan, konvensi, hal yang dipelajari | 2.200 karakter (~800 tokens) |
| **USER.md** | Profil pengguna — preferensi, gaya komunikasi, ekspektasi | 1.375 karakter (~500 tokens) |

**Cara kerjanya:**

- Kedua file diinjeksi ke system prompt sebagai **snapshot yang terbeku** saat awal session
- Agent mengelola memorinya sendiri melalui tool `memory` — bisa menambah, mengganti, atau menghapus entri
- Ingatan disimpan **per profil**: profile `work` memiliki direktorinya sendiri (`~/.hermes/profiles/work/memories/`) terpisah dari default profile

---

## 2\. Session Database (SQLite) — Penyimpanan Transkrip & Metadata

Semua percakapan dan metadata session disimpan di **`~/.hermes/state.db`** — basis data SQLite dengan full-text search (FTS5):

```
# Dari hermes_state.py
class SessionDB(
    SessionSessionsMixin, SessionFtsSetupMixin, SessionSearchMixin, 
    SessionSchemaMixin, ...
):
    """SQLite-backed session storage with FTS5 search; 
    many reader threads, one writer (WAL)."""
```

**Apa yang disimpan di SQLite:**

- Session metadata (ID, timestamp, sumber platform: CLI/Telegram/Discord)
- **Riwayat pesan lengkap** (role, content, tool calls, tool results)
- Jumlah token input/output dan biaya estimasi
- Snapshot system prompt
- Titel session dan linase kompresi

---

## 3\. Session Search Tool — Akses Lintas-Session

Agent dapat mencari percakapan masa lalu menggunakan tool `session_search`:

- FTS5 query syntax mendukung pencarian frasa, boolean operators, prefix matching
- Hasil mencakup snippet pesan asli dengan context
- Agent dapat menemukan diskusi dari berminggu-minggu lalu meskipun tidak tersimpan di MEMORY.md

```
hermes sessions list    # Browse semua session
hermes --resume latest  # Resume session terakhir
hermes -c "project name"  # Resume by title
```

---

## 4\. Isolated by Profile

Setiap profil Hermes adalah **island terpisah**:

- `~/.hermes/memories/` — default profile
- `~/.hermes/profiles/work/memories/` — profile "work"
- `~/.hermes/state.db` — satu database tapi dipartisi per profile

Dua agent pada profil berbeda **tidak berbagi memori**; untuk shared memory lintas-agent diperlukan **external memory provider** (Honcho, RetainDB, Mem0, dll).

---

## 5\. Memory Write Pipeline

Di akhir setiap session:

1. Pertanyaan dikirim ke model untuk "extraction" memory (apa yang penting?)
2. Background review (bisa di model cheaper) memverifikasi entries
3. Duplikat dicegah, security scan dilakukan
4. Perubahan ditulis ke MEMORY.md/USER.md dan state.db

Ingatan baru **hanya terlihat di session BERIKUTNYA**, bukan session yang sama (snapshot frozen saat start).

bagaimana cara hermes menyimpan memori ke honcho?

Sekarang saya akan mencari file plugin Honcho yang sebenarnya untuk melihat implementasi detail:

Hermes menyimpan memori ke **Honcho** melalui **plugin memory provider** yang terintegrasi dengan lifecycle agent. Berikut cara kerjanya:

## 1\. Setup Awal

```
# Install plugin dari plugin catalog
hermes plugins install honcho

# Setup interaktif
hermes memory setup
# Pilih "honcho" → Cloud atau Self-hosted
```

**Konfigurasi minimal** (`~/.hermes/honcho.json`):

```
{
  "apiKey": "your-key-from-app.honcho.dev",
  "hosts": {
    "hermes": {
      "enabled": true,
      "aiPeer": "hermes",
      "peerName": "your-name",
      "workspace": "hermes"
    }
  }
}
```

Atau self-hosted:

```
{
  "baseUrl": "http://localhost:8000",
  "hosts": {
    "hermes": {
      "enabled": true,
      "aiPeer": "hermes",
      "peerName": "your-name",
      "workspace": "hermes"
    }
  }
}
```

---

## 2\. Pipeline Penyimpanan Memori (3 tahap)

Ketika Honcho provider aktif, Hermes otomatis:

### Tahap 1: Sebelum Setiap Turn (Prefetch)

- **`queue_prefetch()`** — menjadwalkan background fetch memori relevan
- Honcho API dipanggil untuk:
	- **Session summary** — ringkasan singkat sesi sebelumnya
		- **User representation** — model user yang terakumulasi (preferensi, pola)
		- **AI peer card** — kartu identitas AI untuk profile ini
- Hasil di-inject ke system prompt

### Tahap 2: Setelah Setiap Respons (Sync Turn)

- **`sync_turn(user_content, assistant_content)`** — flush messages non-blocking
- Dikirim ke Honcho API (background thread):
	- User message (role=user)
		- Assistant response (role=assistant)
		- Tool calls dan results
		- Metadata sesi
- **Write frequency** configurable:
	- `async` (default) — background thread
		- `turn` — sync (blocking)
		- `session` — batch pada akhir sesi
		- `N` (integer) — setiap N turns

```
# Default: async thread
writeFrequency: 'async'

# Messages chunked jika > messageMaxChars (default 25k)
messageMaxChars: 25000
```

### Tahap 3: Akhir Sesi (Memory Extraction)

- **`on_session_end(messages)`** — Honcho melakukan **dialectic reasoning**:
	- LLM-powered **peer.chat()** meng-analyze riwayat percakapan
		- Extract insights, preferences, patterns
		- Update **user representation** secara otomatis
		- Simpan **conclusions** persistent ke Honcho
- Depth reasoning configurable:
	- `dialecticDepth: 1-3` (pass 0=prompt, pass 1=self-audit, pass 2=reconciliation)

---

## 3\. Arsitektur Context Injection

Honcho menyusun context dalam **dua layer**:

### Base Layer (Per contextCadence):

```
1. Session summary (current session so far)
2. User representation (accumulated model)
3. AI peer card (hermes.coder / hermes.writer, etc.)
```

### Dialectic Layer (Per dialecticCadence):

```
LLM reasoning over queries, added to system prompt
```

**Cold vs Warm Start:**

| Kondisi | Strategi | Apa terjadi |
| --- | --- | --- |
| Tidak ada session sebelumnya | Cold start | Lightweight intro prompt, skip summary |
| Ada representation/history | Warm start | Penuh context injection (summary → representation → card) |

---

## 4\. Peer Model (Multi-Profile)

Honcho models conversations sebagai **peers** yang saling bertukar pesan:

| Konsep | Apa itu |
| --- | --- |
| **Workspace** | Lingkungan shared — semua profiles Hermes melihat user yang sama |
| **User peer** (`peerName`) | Manusia (shared across all profiles) |
| **AI peer** (`aiPeer`) | Satu per Hermes profile; default `hermes`, atau `hermes.coder`, `hermes.writer` |
| **Observation** | Toggles per-peer (directional vs unified) |

**Create new profile dengan peer sendiri:**

```
hermes profile create coder --clone
# Otomatis membuat hermes.coder host block di honcho.json
```

**Untuk existing profiles:**

```
hermes honcho sync
# Scan semua profiles, create host blocks untuk yang belum ada
```

---

## 5\. Config Options (Cost Tuning)

Tiga knobs orthogonal untuk mengontrol cost & depth independen:

```
# ~/.hermes/config.yaml atau honcho.json
contextCadence: 1           # Refresh base layer setiap N turns (default 1)
dialecticCadence: 2         # Refresh LLM reasoning setiap N turns
dialecticDepth: 1           # Berapa passes per invocation (1-3)
dialecticReasoningLevel: 'low'  # minimal/low/medium/high/max
dialecticMaxChars: 600      # Max chars dialectic result di system prompt

recallMode: 'hybrid'        # hybrid (auto+tools) | context (inject only) | tools (tools only)
writeFrequency: 'async'     # async | turn | session | N (integer)
saveMessages: true          # Persist messages to Honcho API
messageMaxChars: 25000      # Max chars per message (chunked if exceeded)
sessionStrategy: 'per-directory'  # per-directory | per-repo | per-session | global
```

---

## 6\. Lifecycle di Gateway

Ketika gateway menjalankan agent dengan Honcho enabled:

```
# 1. Gateway creates AIAgent per message
AIAgent(session_id, memory_provider=honcho_provider)

# 2. MemoryManager initializes provider
manager.initialize_all(session_id, platform="telegram", user_id="123", ...)

# 3. Sebelum turn: prefetch context
context = honcho.prefetch(query="relevant memories")
system_prompt += context  # inject ke system prompt

# 4. Setelah response: sync turn
honcho.sync_turn(
    user_content="...",
    assistant_content="...",
    messages=[...],  # OpenAI-style list
    turn_author={"id": "123", "name": "alice", "is_bot": False}
)

# 5. Session end: extract memory
honcho.on_session_end(messages=[...])
# LLM runs peer.chat() → updates user representation
```

---

## 7\. Tools yang Disediakan Honcho (5 tools)

- **`honcho_profile`** — read/update peer card
- **`honcho_search`** — semantic search memories
- **`honcho_context`** — get session context (summary + representation + card)
- **`honcho_reasoning`** — LLM-powered reasoning over memories
- **`honcho_save`** — explicitly save facts

---

## 8\. Per-Profile Memory Isolation

Setiap profile memiliki **workspace** dan **ai peer** sendiri, tapi **user peer** shared:

```
// Multi-profile example
"hosts": {
  "hermes": {
    "aiPeer": "hermes",
    "workspace": "hermes",
    "peerName": "eri"
  },
  "hermes.coder": {
    "aiPeer": "coder",
    "workspace": "hermes",
    "peerName": "eri"  // Same user
  },
  "hermes.writer": {
    "aiPeer": "writer",
    "workspace": "hermes",
    "peerName": "eri"
  }
}
```

Hasilnya:

- **User `eri`** (Shared) — Honcho builds one model of the user across all profiles
- **AI peer `hermes`** — Code-focused observations
- **AI peer `coder`** — Same user, AI perspective berbeda (domain-specific)
- **AI peer `writer`** — Editor-focused untuk same user

---

## Ringkasan Flow

```
Turn Start
    ↓
Prefetch from Honcho ← (session summary + representation + card)
    ↓
Inject into system prompt
    ↓
Agent processes message
    ↓
Sync turn to Honcho (background) ← (user msg + assistant response)
    ↓
Response sent to user
    ↓
[Repeat]
    ↓
Session End
    ↓
Honcho dialectic reasoning
    ↓
Extract insights, update user representation
    ↓
Persist to Honcho Cloud / Self-hosted
```

**Kunci:** Semua writes adalah **non-blocking** (background thread). `sync_turn()` tidak menunda respons.

apakah hermes akan selalu simpan ke honco?, dan apakah harmes memanggil honco sebelum yang lain kaya web search

Berdasarkan kode source, berikut jawaban lengkapnya:

## 1\. Apakah Hermes SELALU simpan ke Honcho?

**TIDAK.** Honcho hanya menyimpan jika:

### Kondisi Provider Honcho Menyimpan:

```
# Dari memory_manager.py
def add_provider(self, provider: MemoryProvider) -> None:
    """Register a provider; builtin always accepted, only ONE external allowed."""
    if provider.name != "builtin" and self._has_external:
        existing = next((p.name for p in self._providers 
                        if p.name != "builtin"), "unknown")
        logger.warning(
            "Rejected memory provider '%s' — external provider '%s' is "
            "already registered. Only one external memory provider is "
            "allowed at a time.",
            provider.name, existing,
        )
        return
```

**Kunci:**

- Hanya **1 external provider** yang boleh aktif sekaligus
- Jika Honcho di-set sebagai `memory.provider: honcho` dalam config → simpan ke Honcho
- Jika memory provider **tidak dikonfigurasi atau dinonaktifkan** → hanya simpan ke built-in MEMORY.md/USER.md (SQLite)
- Jika provider lain aktif (Hindsight, Mem0, OpenViking) → simpan ke provider itu, BUKAN Honcho

---

## 2\. Apakah Hermes Memanggil Honcho SEBELUM Tools Lain (seperti web search)?

**JAWABANNYA: YA, prefetch memory dipanggil PERTAMA, tapi sistemnya non-blocking.**

### Urutan Eksekusi Per Turn:

```
# Dari turn_context.py
def _memory_turn_start_and_prefetch(
    agent: Any, original_user_message: Any, turn_author: Optional[dict[str, Any]] = None,
) -> str:
    """Notify memory providers of the new turn, then prefetch external memory 
    ONCE before the tool loop (skipped on trivial prompts)."""
    
    # STEP 1: Tell provider turn is starting
    agent._memory_manager.on_turn_start(
        agent._user_turn_count, _query, ...
    )
    
    # STEP 2: Prefetch memory (background thread untuk external providers)
    if not is_trivial_prompt(_query):
        ext_prefetch_cache = agent._memory_manager.prefetch_all(
            _query, session_id=agent.session_id
        ) or ""
    
    # STEP 3: Inject ke system prompt
    return ext_prefetch_cache
```

**Konteks timeline:**

```
Turn Start
    ↓
[FIRST] on_turn_start() dipanggil ← Memory provider notified
    ↓
[THEN] prefetch_all() — Background thread untuk external (Honcho)
       Built-in providers: blocking (synchronous)
       External (Honcho): spawned in daemon thread, timeout 8 seconds
    ↓
Inject hasil prefetch ke system prompt
    ↓
[THEN] Agent tools loop runs
    ├── memory provider tools (honcho_search, honcho_profile)
    ├── web search tools
    ├── code execution
    └── semua tools lain
    ↓
[FINALLY] sync_turn() — Background flush ke Honcho (async)
```

---

## 3\. Urutan Tool Execution (Priority)

```
# Dari tool_executor.py
def _resolve_sequential_dispatch(agent, ref: _ToolCallRef, messages: list):
    """Precedence untuk tool execution:"""
    
    # Priority 1: Inline agent-level tools
    # Priority 2: delegate_task
    # Priority 3: context-engine tools
    # Priority 4: **MEMORY-PROVIDER TOOLS** ← Honcho tools (honcho_search, honcho_profile)
    if agent._memory_manager and agent._memory_manager.has_tool(function_name):
        return _SequentialDispatch(
            execute=lambda next_args: 
                agent._memory_manager.handle_tool_call(function_name, next_args),
            ...
        )
    
    # Priority 5: Registry tools (web search, terminal, browser, etc.)
    return _SequentialDispatch(
        execute=_execute,  # ← Web search, code execution, dll
        ...
    )
```

**Artinya:**

- Memory provider tools (Honcho) **lebih prioritas** dari registry tools (web search)
- TAPI prefetch memory injected ke system prompt **SEBELUM agent mulai tools loop**
- Jadi agent **sudah punya konteks Honcho** sebelum decide memakai tools

---

## 4\. Timeline Lengkap dengan Config

```
# ~/.hermes/config.yaml
memory:
  provider: honcho           # ← Honcho enabled

# honcho.json
{
  "apiKey": "your-key",
  "writeFrequency": "async",  # ← Background, non-blocking
  "contextCadence": 1,        # ← Prefetch setiap turn
  "saveMessages": true        # ← Menyimpan messages ke Honcho
}
```

**Eksekusi per turn:**

```
T=0.0s: Prefetch dimulai (background thread spawned)
T=0.1s: Built-in memory injected (instant)
T=0.5s: [timeout 8s] Honcho prefetch selesai (atau skip jika stuck)
T=0.6s: System prompt + Honcho context diinjeksi
T=0.7s: Agent mulai tools loop
        - Jika agent call honcho_search → langsung ke provider (Priority 4)
        - Jika agent call web_search → registry tools (Priority 5)
T=2.0s: Response siap
T=2.1s: sync_turn() queued (background, serialized)
T=2.2s: Response sent ke user
T=2.5s: [background] sync_turn() finally writes ke Honcho
```

---

## 5\. Timeout & Error Handling

```
# Dari memory_manager.py
_EXTERNAL_PREFETCH_TIMEOUT_S = 8.0

def _prefetch_provider(self, provider, query, *, session_id=""):
    # External providers run in thread + timeout
    thread = spawn_context_thread(_run, name=f"memory-prefetch-{provider.name}")
    thread.join(self._external_prefetch_timeout)  # ← 8 second wait
    
    if thread.is_alive():
        logger.warning(
            "Memory provider '%s' prefetch timed out after %.1fs; "
            "skipping it until the stuck call returns", 
            provider.name, self._external_prefetch_timeout,
        )
        return ""  # ← Skip this turn, agent continues
```

**Jika Honcho API slow/down:**

- Timeout 8 detik → skip injeksi Honcho untuk turn itu
- Agent tetap jalan dengan built-in memory
- Next turn coba lagi (thread mungkin finished sekarang)

---

## Kesimpulan

| Pertanyaan | Jawaban |
| --- | --- |
| **Apakah selalu simpan ke Honcho?** | **TIDAK** — hanya jika `memory.provider: honcho` dan Honcho provider aktif. Hanya 1 external provider diizinkan. |
| **Apakah prefetch Honcho dipanggil sebelum tools?** | **YA** — prefetch memory adalah FIRST yang dipanggil per turn, di-inject ke system prompt sebelum tools loop |
| **Apakah prefetch blocking?** | **TIDAK** untuk external (Honcho) — spawned di daemon thread, timeout 8s. Built-in: synchronous. |
| **Apakah Honcho tools prioritas atas web search?** | **YA** — memory provider tools (Priority 4) > registry tools (Priority 5). Tapi agent decide mana yang mau dipake. |
| **Jika Honcho API slow?** | Timeout 8s → skip Honcho itu turn, retry next turn. Agent tidak blocked. |

Messages beyond this point will start a new private chat