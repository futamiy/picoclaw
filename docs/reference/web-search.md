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
| Pencarian | **`web_search`** | "Search the web for current information" | `query` (wajib), `count` (1–10, default 10), `range` (`d`/`w`/`m`/`y`), `strategy` (`auto`/`conventional`/`reasoning`, opsional) |
| Ambil halaman | **`web_fetch`** | Fetch/ekstrak isi URL | lihat `pkg/tools/integration/web.go:2257` |

Implementasi: `pkg/tools/integration/web.go` (`WebSearchTool`:1997, `Execute`:2054,
`WebFetchTool`:2257), di-bridge lewat `pkg/tools/integration_facade.go`.

Interface inti (semua provider wajib implement ini):

```go
// pkg/tools/integration/web.go:103
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

1. `auto` (mode **hybrid**) **bukan kategori sendiri** — dia pemilih sekaligus pelaksana:
   ia punya dua pool hardcoded (`conventionalWebSearchPool`, `reasoningWebSearchPool`,
   `web.go:1593`) dan bisa menjalankan **keduanya paralel** dalam satu panggilan tool
   (lihat §5). `auto_legacy` = perilaku auto lama (pilih 1 provider pertama yang ready).
2. Batas tak selalu tegas (brave/kagi/tavily punya mode snippet + ringkasan). Tapi desainnya:
   **Brave & Kagi = index-first**, **Tavily & Perplexity = synthesis-first**.

## 3. ⚠️ Realita implementasi picoclaw (SELISIH dengan taxonomy §2)

**Ini bagian paling penting untuk edit kode.** Di `pkg/tools/integration/web.go`, bentuk output
per-provider **tidak selalu sama dengan desain provider**:

| Provider | Desain (§2) | **Output aktual di picoclaw** | Bukti kode |
|---|---|---|---|
| `duckduckgo` | conventional | daftar link | `web.go:1039` |
| `sogou` | conventional | daftar link | `web.go:974` |
| `searxng` | conventional | daftar link | `web.go:1273` |
| `brave` | conventional | daftar link | `web.go:371` |
| `kagi` | conventional | daftar link | `web.go:607` |
| `baidu_search` | conventional | daftar link | `web.go:1458` |
| `tavily` | research | **jawaban sintesis + daftar sumber** (`include_answer: "advanced"`) | `web.go:423`, render `web.go:486` |
| `glm_search` | research | **daftar link** | `web.go:1365` — output `title+link+content` dari `search_result` |
| `perplexity` | research | **sintesis jawaban + sitasi inline** (prompt sintesis) | `web.go:1119`, render `web.go:1196` |
| `gemini` | research | **sintesis + sitasi** | `web.go:867` — `parts.text` + `groundingMetadata.groundingChunks` |

**Kesimpulan:** `tavily`, `perplexity`, dan `gemini` kini benar-benar synthesis-style.
`glm_search` masih mengembalikan daftar link — jangan mengandalkan GLM untuk jawaban jadi.

> **Riwayat:** sebelumnya Tavily (`include_answer: false`) dan Perplexity (prompt memaksa
> format daftar link) dipaksa jadi daftar link. Dihapus saat mode hybrid `auto` diperkenalkan
> (lihat §5) — tanpa perubahan itu, "reasoning side" hybrid hanyalah daftar link kedua.

> Simpan perilaku ini konsisten: kalau menambah provider baru, tentukan dulu ia **conventional
> atau research**, lalu pastikan fungsi `Search()` benar-benar menghasilkan bentuk yang
> dimaksud (jangan cuma ikut default provider).

## 4. Daftar lengkap provider (11 + mode auto)

Sumber otoritatif: `knownWebSearchProviders` (`pkg/tools/integration/web.go:1576`)
& label UI di `web/backend/api/tools.go:662`. Config struct: `pkg/config/config.go:1641`
(`WebToolsConfig`), field `provider` di :1653.

| ID | Label | Butuh auth/key | Kategori desain |
|---|---|---|---|
| `auto` | Auto (Hybrid) | — | pemilih hybrid: jalankan kedua pool (lihat §5) |
| `auto_legacy` | Auto (Legacy) | — | auto lama: pilih 1 provider pertama yang ready |
| `sogou` | Sogou | ❌ (gratis) | conventional |
| `duckduckgo` | DuckDuckGo | ❌ (gratis) | conventional |
| `gemini` | Gemini (Google Search) | ✅ api_key | research |
| `brave` | Brave Search | ✅ api_keys | conventional |
| `tavily` | Tavily | ✅ api_keys | research |
| `kagi` | Kagi Search | ✅ api_keys | conventional |
| `perplexity` | Perplexity | ✅ api_keys | research |
| `searxng` | SearXNG | ❌ (butuh base_url) | conventional (self-host) |
| `glm_search` | GLM Search | ✅ api_key | research (→ link, §3) |
| `baidu_search` | Baidu Search | ✅ api_key | conventional |

Keys **disimpan di `.security.yml`**, bukan `config.json`.

## 5. Logika pemilihan provider (mode auto)

### 5a. `auto` (hybrid, default)

`Execute` (`web.go:2054`) memanggil `executeHybrid` (`web.go:2124`) dengan pool:

- **conventional pool** (`web.go:1593`): `brave → duckduckgo → sogou → searxng → kagi → baidu_search`
- **reasoning pool** (`web.go:1594`): `perplexity → tavily → gemini → glm_search`

Urutan eksekusi ditentukan `resolveStrategy(query, hint)` (`web.go:1705`), **precedence**:

1. **Sinyal heuristik kuat di query menang** (model lemah seperti DeepSeek sering salah pilih
   strategy — lihat `docs/adr/0001`):
   - sinyal berita/recency (`berita|terbaru|latest|hari ini|…`, `web.go:1694`) → **conventional saja**
   - sinyal sintesis (`apa itu|why|how|jelaskan|…`, `web.go:1697`) → **auto** (reasoning dulu +
     conventional sebagai fact-check)
2. Heuristik netral → pakai param LLM `strategy` (`auto`/`conventional`/`reasoning`)
3. Param kosong/tak valid → `auto`

Lalu (`web.go:2124`):

- `conventional` → provider pertama ready di conventional pool (fallback: reasoning pool)
- `reasoning` → provider pertama ready di reasoning pool (fallback: conventional pool)
- `auto` → **kedua pool dijalankan paralel** (2 goroutine), digabung jadi satu hasil:

  ```
  [Research]
  <jawaban sintesis>

  [Conventional — fact check]
  <daftar link>
  ```

- Salah satu provider gagal → hasil yang lain tetap dipakai + baris
  `note: <provider> failed: …` (partial success); dua-duanya gagal → error.
- `count`/`range` dilepas sama ke kedua provider.

Catatan: param `strategy` **diabaikan jika config `provider` menunjuk provider eksplisit**
(config pin menang).

### 5b. `auto_legacy` (auto lama)

Resolusi persis perilaku auto sebelumnya, disimpan di `resolveLegacyProviderName`
(`web.go:1654`):

1. Provider eksplisit dipakai **hanya jika `ready`** (enabled + key/base_url ada).
2. Jika tidak → coba urutan primary: `perplexity → brave → kagi → searxng → tavily → gemini`
   (`autoPrimaryWebSearchProviders`, `web.go:1589`).
3. Jika keduanya ready (`sogou` + `duckduckgo`): pilih **duckduckgo** untuk query non-China,
   **sogou** untuk query China (`prefersDuckDuckGoQuery`).
4. Lainnya → fallback: `baidu_search → glm_search` (`autoFallbackWebSearchProviders`, `web.go:1590`).
5. Tidak ada yang ready → `""` (search tak tersedia).

Provider ready = enabled **dan** punya kredensial (`providerReady`, `web.go:1607`).

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
| Tambah provider baru | definisi struct + `Search()` di `pkg/tools/integration/web.go`; tambah ke `knownWebSearchProviders` (:1576); tambah case di `providerReady` (:1607) & `providerByName` (:1720); tambah config di `pkg/config/config.go` (`WebToolsConfig`:1641); tambah opsi di `WebSearchToolOptionsFromConfig` (:1526); **masukkan ke pool hybrid yang sesuai** (`conventionalWebSearchPool`:1593 / `reasoningWebSearchPool`:1594) |
| Ubah urutan auto legacy | `autoPrimaryWebSearchProviders` / `autoFallbackWebSearchProviders` (:1589–1590) |
| Ubah perilaku hybrid `auto` | pool (:1593–1594), `resolveStrategy` (:1705), `executeHybrid` (:2124), `searchBoth` (:2188) |
| Ubah heuristic strategy (sinyal berita/sintesis) | regex `strategyNewsRe` (:1694) / `strategySynthRe` (:1697) |
| Label/UI provider | `web/backend/api/tools.go:662` (list `webSearchProviderOption`), validasi `normalizeWebSearchProvider` (:556) |
| Bentuk output `web_search` (tool-level) | `WebSearchTool.Execute` (`web.go:2054`) & `Description/Parameters` (:2021/:2025) |
| `web_fetch` | `WebFetchTool` (`web.go:2257`+) |

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
(synthesis-style: `tavily`, `perplexity`, `gemini`; `glm_search` masih daftar link, §3).

**Mode hybrid:** ganti `provider` ke `auto` untuk menjalankan kedua pool paralel
(reasoning + conventional fact-check, lihat §5) — atau `auto_legacy` untuk perilaku
auto lama. Di UI tampil sebagai "Auto (Hybrid)" / "Auto (Legacy)".
