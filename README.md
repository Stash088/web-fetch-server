# web-fetch-server

Self-hosted **Web-Search / Web-Fetch provider** for AI agents, implemented as a
**remote MCP server** in Go. Agents (opencode, hermes, DeepSeek harness, Claude,
Cursor, ...) connect over HTTP and get two tools:

- `web_search` — search the web via a **SearXNG** metasearch backend
- `web_fetch` — fetch a page and get its content as clean **Markdown**, with
  chunked reading for long pages

Content is returned in an LLM-friendly form (no raw HTML noise), so models spend
context on facts, not markup.

## How it fits together

```
Agent (opencode / hermes / ...)
   │   MCP over HTTP (Streamable HTTP) + Bearer API key
   ▼
web-fetch-server (this Go server)
   ├─ tool: web_search ────────► SearXNG (self-hosted, JSON API)
   └─ tool: web_fetch  ────────► target web page (HTML → Markdown, chunked)
```

Only **web-fetch-server** talks to SearXNG. Agents never call SearXNG directly —
they only ever see the MCP endpoint (`/mcp`) exposed by this server. SearXNG is
the private search backend; this server is the single public gateway.

## Quick start (Docker Compose)

Bring up both SearXNG and this server:

```bash
export API_KEYS=super-secret-token-1,super-secret-token-2   # required, protects /mcp
docker compose up -d --build
```

- MCP endpoint: `http://localhost:8080/mcp` (Bearer `$API_KEY`)
- SearXNG (private backend): `http://localhost:8888`

SearXNG's JSON API format must be enabled. With the default image it is. If you
use an existing SearXNG instance, set `SEARXNG_URL` to it.

## Run locally (without Docker)

```bash
# 1. SearXNG
docker run -d -p 8888:8080 -e SEARXNG_BASE_URL=http://localhost:8888/ searxng/searxng

# 2. this server
API_KEYS=key-for-user-a,key-for-user-b SEARXNG_URL=http://localhost:8888 go run ./cmd/server
```

## Configuration (env vars)

| Var              | Default                | Description                                   |
|------------------|------------------------|-----------------------------------------------|
| `PORT`           | `8080`                 | HTTP listen port                              |
| `MCP_PATH`       | `/mcp`                 | MCP endpoint path                             |
| `API_KEYS`      | *(empty)*              | **Required** Comma-separated Bearer tokens for `/mcp` — one per user. If unset the endpoint is open — set it in production. |
| `API_KEY`       | *(empty)*              | Legacy alias for a single key. Used only when `API_KEYS` is unset. |
| `SEARXNG_URL`    | `http://localhost:8888`| Base URL of SearXNG                            |
| `SEARXNG_KEY`    | *(empty)*              | Optional SearXNG API key (Bearer)             |
| `MAX_FETCH_BYTES`| `2097152` (2 MiB)      | Max body size fetched from a page              |
| `FETCH_TIMEOUT`  | `20s`                  | HTTP timeout for SearXNG and page fetches      |
| `PDF_FETCH_TIMEOUT`| `60s`                | Timeout for PDF fetches (by `.pdf`/`/pdf/` URL hint, or a retry when a PDF body outlived `FETCH_TIMEOUT`). Used only when larger than `FETCH_TIMEOUT`. |
| `USER_AGENT`     | Chrome 126 UA          | User-Agent sent to target sites. Defaults to a real Chrome UA to reduce antibot blocking. |
| `TLS_FINGERPRINT`| `chrome`               | TLS ClientHello fingerprint: `chrome` (uTLS, mimics Chrome — bypasses JA3-based TLS blocks) or `off` (stdlib TLS) |
| `JS_RENDER`      | `never`                | JS rendering via headless browser: `never` (off), `auto` (fallback when a bot block is detected), `always` (always render). Requires Chromium, see below. |
| `JS_RENDER_TIMEOUT`| `30s`                | Timeout for a single browser render           |
| `CHROME_BIN`     | *(auto)*               | Path to the Chrome/Chromium binary. Empty = auto-detect (`chromium`, `chromium-browser`, `google-chrome`, ...) |
| `RENDER_PROFILE_DIR`| *(tmpdir)*          | Base directory for persistent render browser profiles. Cookies (`cf_clearance`, shop sessions) survive between renders and are wiped on every server start. |
| `RENDER_POOL_SIZE`| `1`                   | Number of pooled render browsers; each gets its own profile so parallel renders never lock each other. |
| `FETCH_CACHE_TTL`| `20m`                 | TTL cache for direct `web_fetch` results (`0` disables it). Rendered pages are keyed separately. |
| `RENDER_CACHE_TTL`| `5m`                 | TTL cache for `render:true` fetches — shorter, because browser cookies/profiles drift over time. |
| `SEARCH_CACHE_TTL`| `10m`                | TTL cache for `web_search` results (`0` disables it). |
| `RERANK`         | `rrf`                  | Re-ranking of `web_search` results: `rrf` (BM25 + engine consensus + RRF fusion), `semantic` (adds an external cross-encoder rerank-API vote, requires `RERANK_API_KEY`) or `none` (SearXNG order passthrough). |
| `RERANK_API_URL` | `https://routerai.ru/api/v1` | Base URL of a Cohere-compatible rerank API (used by `RERANK=semantic`). |
| `RERANK_API_KEY` | *(empty)*              | API key for the rerank service. Empty = `semantic` mode falls back to `rrf`. |
| `RERANK_MODEL`   | `voyageai/rerank-2.5-lite` | Rerank model id sent to the API. |
| `RERANK_TIMEOUT` | `3s`                   | Timeout for a single rerank API call; on timeout/error the order degrades to `rrf` (fail-open). |
| `DEFAULT_MAX_LEN`| `8000`                 | Default `max_length` for `web_fetch`           |
| `MAX_RESULTS`    | `10`                   | Default `max_results` for `web_search`           |
| `BLOCK_PRIVATE_NETWORKS` | `true`          | SSRF protection: reject private/loopback ranges in `web_fetch` targets |
| `LOG_LEVEL`      | `info`                 | `debug`, `info`, `warn`, `error` — logging verbosity |

## Connecting opencode

Add this server as a **remote MCP** in your `opencode.json`:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "web-tools": {
      "type": "remote",
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer {env:API_KEY}"
      },
      "oauth": false
    }
  }
}
```

Then prompt: `use web-tools to search for the latest Go release notes`.

## MCP tools

### `web_search`
- `query` *(required)* — search query
- `max_results` *(default 10)* — max results
- `language` *(optional)* — e.g. `en`, `ru`
- `time_range` *(optional)* — `day`, `month`, `year`

Returns: list of `{title, url, snippet, engine, engines, score, reranked}`.
`engines` is the list of SearXNG engines that returned the result; `score` and
`reranked` are present when re-ranking is enabled (default).

### Re-ranking (`RERANK`)

By default `web_search` results are re-ranked in-process (pure stdlib, no
external services):

1. **BM25** over `title + snippet` — the corpus is the candidate set itself,
   so IDF is computed on it; unicode tokenization, `k1=1.5`, `b=0.75`.
2. **Engine consensus** — results returned by more engines rank higher.
3. **RRF fusion** — the BM25 order and the engine-consensus order are fused
   with reciprocal rank fusion (`1/(60 + rank)`, ranks from 1); ties keep the
   original SearXNG order.

Every result comes back with `score` (the fused RRF sum) and `reranked: true`.
Results that match no query terms get no BM25 vote; a reranker failure never
breaks the search (fail-open: original order is returned). Set `RERANK=none`
to disable re-ranking and pass the SearXNG order through untouched.

### Semantic re-ranking (`RERANK=semantic`)

Adds a third vote: an external cross-encoder reranker (Cohere-compatible API,
default: RouterAI `voyageai/rerank-2.5-lite`) scoring `query` against each
`title + snippet`. This closes the gaps BM25 cannot see — synonyms, paraphrase
and cross-lingual queries (RU query → EN pages). Exact identifiers (error
strings, API names) still benefit from the BM25 vote, so both run together.

- Cost: ~2.5K input tokens per uncached search ≈ 0.006₽ on RouterAI.
- Latency: one API call (+200–500ms) on cache misses only; `RERANK_TIMEOUT`
  bounds it, and a failure degrades the order to plain `rrf`.
- Snippets are truncated to 1000 runes before being sent to the API.
- Requires `RERANK_API_KEY`; without it the server logs a warning and falls
  back to `rrf`.

#### Measuring the gain (golden set)

`internal/rerank/testdata/golden.json` holds ~10 queries with expected result
domains (edit to taste — expected entries match the result host as a
substring). Run the live measurement against a running SearXNG:

```sh
GOLDEN_LIVE=1 \
SEARXNG_URL=http://localhost:8888 \
RERANK_API_KEY=... \
go test ./internal/rerank/ -run TestGoldenSetLive -v
```

It prints per-query ranks and a `SUMMARY` line per mode (`top3` hit rate and
MRR) for `none` / `rrf` / `semantic`. Without `RERANK_API_KEY` the semantic
row is skipped. The test is skipped entirely in normal `go test ./...` runs.

### `web_fetch`
- `url` *(required)* — page to fetch
- `max_length` *(default 8000)* — max chars to return
- `start_index` *(default 0)* — read from this char index (for long pages)
- `format` *(default `markdown`)* — `markdown` or `text`
- `render` *(default `false`)* — load the page in a headless browser (JS rendering) instead of fetching raw HTML

Returns: `{title, url, content, total_chars, format}`. Use `start_index` to page
through documents larger than `max_length`.

## Antibot resilience

`web_fetch` is hardened against bot detection in three layers:

1. **Browser-like HTTP fingerprint** — a real Chrome User-Agent by default,
   plus `Accept-Language`, `Sec-Fetch-*`, `Upgrade-Insecure-Requests` headers,
   HTTP/2, a cookie jar and TLS 1.2+. Retries (2 attempts) with exponential
   backoff are applied on `408`/`429`/`5xx`/`498` and network/TLS errors,
   honoring `Retry-After`.
2. **TLS fingerprint spoofing** (`TLS_FINGERPRINT=chrome`, default) — the TLS
   ClientHello is generated by [uTLS](https://github.com/refraction-networking/utls)
   to look like Chrome, defeating JA3-based TLS blocks (the usual cause of
   "TLS handshake timeout" on marketplaces like Yandex/Rozetka).
3. **JS rendering** (optional) — a pool of headless Chrome sessions with
   persistent profiles renders pages whose content is loaded dynamically and
   can answer WAF browser challenges. Cookies (`cf_clearance`, shop sessions)
   persist between renders — a challenge solved once is not solved again —
   and are wiped on every server start. Stealth patches (from
   [go-rod/stealth](https://github.com/go-rod/stealth)) plus coherent
   lang/timezone/viewport personas hide the automation surface.
4. **Structured block reporting** — when a page still turns out to be an antibot
   block (Cloudflare challenge, JS check, CAPTCHA, rate limit), the agent gets
   a clear `blocked (challenge_cloudflare|challenge_js|captcha|rate_limited|block_wall)`
   status instead of challenge HTML disguised as content. `block_wall` covers
   200-OK denial notices ("You've been blocked by network security",
   "Pardon our interruption") that offer no challenge to solve.

### When things still get blocked

| Symptom                        | Likely cause        | Fix                                        |
|--------------------------------|---------------------|--------------------------------------------|
| HTTP 429 / 408 / 5xx / 498     | rate limit / WAF    | auto-retry is built in; raise `FETCH_TIMEOUT` |
| TLS handshake timeout          | JA3 TLS fingerprint | keep `TLS_FINGERPRINT=chrome` (default)    |
| HTTP 403 + JS challenge, or JS-only content | needs a real browser | `render: true` in `web_fetch`, or `JS_RENDER=auto` |
| `blocked (captcha)` status     | reCAPTCHA/Turnstile | cannot be solved automatically; fetch from a different source |
| `blocked (block_wall)` status  | outright denial (200-OK) | nothing to solve; fetch from a different source |
| Heavy PDF times out (`read body`) | large PDF on a slow host | raise `PDF_FETCH_TIMEOUT` (default 60s) |

### Enabling JS rendering

The renderer needs a Chrome/Chromium binary:

- **Locally**: install `chromium`/`google-chrome`, or point `CHROME_BIN` at it.
- **Docker**: build the image with the browser baked in:

  ```bash
  docker build --build-arg WITH_JS_RENDER=1 -t web-fetch-server .
  ```

  Then set `JS_RENDER=auto` (fallback when a block is detected) or `always` in
  your `.env`. Note the image grows by ~200 MB and the container wants more
  memory — raise `mem_limit` to ~1–2 GB when `JS_RENDER` is enabled.

With `JS_RENDER=auto`, `web_fetch` tries the plain HTTP path first and falls
back to the browser only when it detects a bot block (403/429/498/5xx/TLS).
Per-call `render: true` forces the browser regardless of the mode.

### Content quality

`web_fetch` optimizes what lands in the model's context:

- **Article extraction** (default `extract: true`) — the main article content
  is extracted with [go-readability](https://github.com/go-shiori/go-readability)
  before HTML→Markdown conversion: menus, footers and cookie banners are
  dropped. Non-article pages (docs, landing pages) fall back to full-page
  conversion automatically. Responses carry `extracted: true` plus `metadata`
  (title, description, published_time, ...) when extraction succeeded.
  Pass `extract: false` to get the full page. Non-HTML bodies (PDF text) are
  never extracted.
- **`format: text`** returns real plain text (no tags), with line breaks on
  block elements.
- **Chunking is rune-based**: `total_chars` counts characters (not bytes), so
  cyrillic text pages correctly via `start_index`; chunks snap to paragraph
  boundaries when possible.
- **Caching**: successful `web_search` / `web_fetch` results are cached by TTL
  (see `FETCH_CACHE_TTL` / `RENDER_CACHE_TTL` / `SEARCH_CACHE_TTL`, `0` = off).
  Cache hits carry `cached: true`. Errors and antibot blocks are never cached.

## Logging

Each upstream call is logged as a correlated pair of structured log lines tagged
`[request]` and `[response]`, sharing a `request_id`:

- `[request] tool web_search` — args the agent sent to the tool
- `[request] searxng` / `[response] searxng` — the HTTP call to SearXNG and what it returned
- `[request] tool web_fetch` — args the agent sent to `web_fetch`
- `[request] fetch` / `[response] fetch` — the HTTP call to the target page and its outcome
- `[request] http` — the incoming MCP HTTP request (method, path, JSON-RPC body, status)

This lets you see exactly what the server received from the agent and what the
search engine / target site returned. Set `LOG_LEVEL=debug` for more detail.

Example (`docker compose logs -f web-fetch-server`):

```
[request] tool web_search query="golang" max_results=10
[request] searxng request_id=ab12cd34 url="http://searxng:8080/search?q=golang&format=json"
[response] searxng request_id=ab12cd34 status=200 total_results=10 returned_results=10 first_result="The Go Programming Language | https://go.dev/ | ..."
```

## Notes / roadmap

- Auth: Bearer tokens enforced via middleware. Configure one key per user via
  `API_KEYS` (comma-separated); the requesting key is logged as a `key_id`
  fingerprint so you can see who called. OAuth (Dynamic Client Registration)
  is a possible phase-2 addition for public deployments.
- Optional LLM summarization (`web_extract`) is a future extension.
- Security: `web_fetch` only allows `http`/`https` URLs; body size is capped.
