# Web Search di picoclaw — Daftar Lengkap & Panduan Edit

Dokumen ini merangkum kemampuan **web search** picoclaw: tool yang diekspos ke LLM,
daftar provider, **klasifikasi conventional vs research**, opsi native search, logika
pemilihan provider, dan kondisi aktual di server.

> **Tujuan utama file ini (untuk edit kode di masa depan):**
> 1. Peta cepat "provider ini outputnya apa" supaya tidak salah ganti/tambah provider.
> 2. Titik-titik kode (file:line) yang harus disentuh saat menambah/mengubah provider.
> 3. Peringatan selisih **desain provider vs implementasi picoclaw** (lihat §3) — jangan
>    mengasumsikan sebuah provider "synthesis" kalau kode picoclaw sebenarnya mencoerce
>    jadi daftar link.

## 1. Tool yang diekspos ke LLM (2 buah)

| Tool | Nama | Deskripsi | Params |
|---|---|---|---|
| Pencarian | **`web_search`** | "Search the web for current information" | `query` (wajib), `count` (1–10, default 10), `range` (`d`/`w`/`m`/`y`) |
| Ambil halaman | **`web_fetch`** | Fetch/ekstrak isi URL | lihat `pkg/tools/integration/web.go:2073` |

Implementasi: `pkg/tools/integration/web.go` (`WebSearchTool`:1923, `WebFetchTool`:2073),
di-bridge lewat `pkg/tools/integration_facade.go`.

Interface inti (semua provider wajib implement ini):

```go
// pkg/tools/integration/web.go:102
type SearchProvider interface {
    Search(ctx context.Context, query string, count int, rangeCode string) (string, error)
}
```

Perhatikan: return-nya `string` — jadi **bentuk output ditentukan oleh masing-masing
implementasi**, bukan oleh interface. Inilah sumber perbedaan conventional vs research (§3).

## 2. Klasifikasi: Conventional vs Research (taxonomy desain)

Secara **desain** provider (bukan perilaku picoclaw), ada dua keluarga:

### 🔗 Conventional search — sumber link / hasil mentah

Mengembalikan **daftar link + snippet** hasil indeks; LLM membaca sendiri lalu menyimpulkan.

| Provider | Jenis |
|---|---|
| `duckduckgo` | scraper indeks (gratis) |
| `sogou` | scraper indeks China (gratis) |
| `searxng` | meta-search self-host |
| `brave` | API indeks komersial |
| `kagi` | API indeks komersial |
| `baidu_search` | API indeks China |

→ "search engine" klasik. Output = daftar URL, tanpa sintesis.

### 🧠 Research search — sintesis jawaban

Membaca halaman lalu **menyusun jawaban dengan sitasi**.

| Provider | Jenis |
|---|---|
| `tavily` | API research (dirancang untuk LLM agent) |
| `perplexity` | API answer-engine |
| `gemini` | Google Search grounding |
| `glm_search` | Zhipu answer-engine (`search_pro` = versi riset) |

→ "answer engine". Output = jawaban jadi, bukan daftar link.

### Catatan taxonomy

1. `auto` **bukan kategori sendiri** — dia pemilih. Urutan primary-nya condong ke research
   (`perplexity → brave → kagi → searxng → tavily → gemini`), jadi `auto` cenderung berakhir
   di research-style kalau perplexity/tavily ada.
2. Batas tak selalu tegas (brave/kagi/tavily punya mode snippet + ringkasan). Tapi desainnya:
   **Brave & Kagi = index-first**, **Tavily & Perplexity = synthesis-first**.

## 3. ⚠️ Realita implementasi picoclaw (SELISIH dengan taxonomy §2)

**Ini bagian paling penting untuk edit kode.** Di `pkg/tools/integration/web.go`, bentuk output
per-provider **tidak selalu sama dengan desain provider**:

| Provider | Desain (§2) | **Output aktual di picoclaw** | Bukti kode |
|---|---|---|---|
| `duckduckgo` | conventional | daftar link | `web.go:977` |
| `sogou` | conventional | daftar link | `web.go:879` |
| `searxng` | conventional | daftar link | `web.go:1200` |
| `brave` | conventional | daftar link | `web.go:278` |
| `kagi` | conventional | daftar link | `web.go:511` |
| `baidu_search` | conventional | daftar link | `web.go:1374` |
| `tavily` | research | **daftar link** (dipaksa!) | `web.go:394` — kirim `"include_answer": false`, output `title+url+content` |
| `glm_search` | research | **daftar link** | `web.go:1281` — output `title+link+content` dari `search_result` |
| `perplexity` | research | **daftar link** (dipaksa!) | `web.go:1081` — system prompt menyuruh model output `1. Title / URL / Description` |
| `gemini` | research | **sintesis + sitasi** | `web.go:779` — `parts.text` + `groundingMetadata.groundingChunks` |

**Kesimpulan:** di picoclaw sekarang, **hanya `gemini` yang benar-benar synthesis-style**.
`tavily`, `perplexity`, `glm_search` secara implementasi **dikembalikan sebagai daftar link**.
Jadi jangan mengandalkan provider research untuk mendapat jawaban jadi — LLM tetap yang
menyimpulkan dari daftar link (kecuali `gemini`).

### Kalau nanti mau menjadikan Tavily/Perplexity benar-benar "research"
- **Tavily**: ubah `"include_answer": false` → `true` (atau `"basic"`/`"advanced"`) di
  `web.go:422`, lalu baca field `answer` dari response dan render sebagai teks jawaban
  (sekarang hanya `results[]` yang dibaca, `web.go:470`).
- **Perplexity**: hapus/ubah system prompt yang memaksa format daftar link di
  `web.go:1102`, biarkan model mengembalikan sintesis (saat ini `choices[0].message.content`
  tetap dikembalikan utuh, tapi prompt memaksa format list).

> Simpan perilaku ini konsisten: kalau menambah provider baru, tentukan dulu ia **conventional
> atau research**, lalu pastikan fungsi `Search()` benar-benar menghasilkan bentuk yang
> dimaksud (jangan cuma ikut default provider).

## 4. Daftar lengkap provider (10 + mode auto)

Sumber otoritatif: `knownWebSearchProviders` (`pkg/tools/integration/web.go:1555`)
& label UI di `web/backend/api/tools.go:655`. Config struct: `pkg/config/config.go:1409`
(`WebToolsConfig`), field per-provider di :1350–1420.

| ID | Label | Butuh auth/key | Kategori desain |
|---|---|---|---|
| `auto` | Auto | — | pemilih (lihat §5) |
| `sogou` | Sogou | ❌ (gratis) | conventional |
| `duckduckgo` | DuckDuckGo | ❌ (gratis) | conventional |
| `gemini` | Gemini (Google Search) | ✅ api_key | research |
| `brave` | Brave Search | ✅ api_keys | conventional |
| `tavily` | Tavily | ✅ api_keys | research (→ link di picoclaw, §3) |
| `kagi` | Kagi Search | ✅ api_keys | conventional |
| `perplexity` | Perplexity | ✅ api_keys | research (→ link di picoclaw, §3) |
| `searxng` | SearXNG | ❌ (butuh base_url) | conventional (self-host) |
| `glm_search` | GLM Search | ✅ api_key | research (→ link di picoclaw, §3) |
| `baidu_search` | Baidu Search | ✅ api_key | conventional |

Keys **disimpan di `.security.yml`**, bukan `config.json`.

## 5. Logika pemilihan provider (mode auto)

`resolveProviderName` (`pkg/tools/integration/web.go:1618`):

1. Provider eksplisit dipakai **hanya jika `ready`** (enabled + key/base_url ada).
2. Jika tidak → coba urutan primary: `perplexity → brave → kagi → searxng → tavily → gemini`
   (`autoPrimaryWebSearchProviders`, `web.go:1567`).
3. Jika keduanya ready (`sogou` + `duckduckgo`): pilih **duckduckgo** untuk query non-China,
   **sogou** untuk query China (`prefersDuckDuckGoQuery`).
4. Lainnya → fallback: `baidu_search → glm_search` (`autoFallbackWebSearchProviders`, `web.go:1568`).
5. Tidak ada yang ready → `""` (search tak tersedia).

Provider ready = enabled **dan** punya kredensial (`providerReady`, `web.go:1581`).

## 6. Native search (opsi khusus)

- `prefer_native` (config `tools.web.prefer_native`, default di server = **true**): kalau LLM
  mendukung **built-in web search** (mis. OpenAI `web_search_preview`, Codex), tool client-side
  `web_search` **disembunyikan** dan search pakai kemampuan provider. Fallback otomatis ke
  client-side kalau provider tak mendukung.
  → `pkg/providers/openai_compat/provider.go:848`,
    `pkg/providers/openai_responses_common/responses_common.go:163`,
    `pkg/providers/types.go:75`.

## 7. Peta kode — di mana mengedit apa

| Mau ubah… | Sentuh file:line |
|---|---|
| Tambah provider baru | definisi struct + `Search()` di `pkg/tools/integration/web.go`; tambah ke `knownWebSearchProviders` (:1555); tambah case di `providerReady` (:1581) & `providerByName` (:1655); tambah config di `pkg/config/config.go` (:1386–1420); tambah opsi di `WebSearchToolOptionsFromConfig` (:1505) |
| Ubah urutan auto | `autoPrimaryWebSearchProviders` / `autoFallbackWebSearchProviders` (:1567–1568) |
| Jadikan Tavily/Perplexity synthesis | lihat §3 |
| Label/UI provider | `web/backend/api/tools.go:655` (list `webSearchProviderOption`) |
| Bentuk output `web_search` (tool-level) | `WebSearchTool.Execute` (`web.go:1955`) & `Description/Parameters` (:1927) |
| `web_fetch` | `WebFetchTool` (`web.go:2073`+) |

## 8. Status server `192.168.1.20` saat ini

| Setting | Nilai |
|---|---|
| `tools.web.enabled` | ✅ true |
| `provider` | **`tavily`** |
| `prefer_native` | **true** |
| Key terpasang (di `.security.yml`) | **Brave** ✅, **Tavily** ✅ |
| Enabled tanpa key (gratis) | `sogou`, `duckduckgo` |
| Disabled | kagi, gemini, perplexity, searxng, glm_search, baidu_search |
| Proxy | — |

**Efektif sekarang:** provider eksplisit `tavily` punya key → **Tavily aktif** (tapi outputnya
daftar link, lihat §3 — bukan jawaban sintesis). Cadangan ready: **Brave** (conventional).
Gratis tersedia: **sogou, duckduckgo** (conventional). Karena `prefer_native=true`, kalau model
LLM aktif (snifox-deepseek) mendukung native search, tool `web_search` client-side bisa
disembunyikan dan search pakai search bawaan provider.

**Ringkas kategori:** conventional = `duckduckgo`, `sogou`, `searxng`, `brave`, `kagi`,
`baidu_search` · research = `tavily`, `perplexity`, `gemini`, `glm_search`
(⚠️ hanya `gemini` yang benar-benar synthesis di implementasi picoclaw saat ini, §3).
