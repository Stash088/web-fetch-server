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
| `USER_AGENT`     | Chrome 126 UA          | User-Agent sent to target sites. Defaults to a real Chrome UA to reduce antibot blocking. |
| `TLS_FINGERPRINT`| `chrome`               | TLS ClientHello fingerprint: `chrome` (uTLS, mimics Chrome — bypasses JA3-based TLS blocks) or `off` (stdlib TLS) |
| `JS_RENDER`      | `never`                | JS rendering via headless browser: `never` (off), `auto` (fallback when a bot block is detected), `always` (always render). Requires Chromium, see below. |
| `JS_RENDER_TIMEOUT`| `30s`                | Timeout for a single browser render           |
| `CHROME_BIN`     | *(auto)*               | Path to the Chrome/Chromium binary. Empty = auto-detect (`chromium`, `chromium-browser`, `google-chrome`, ...) |
| `RENDER_PROFILE_DIR`| *(tmpdir)*          | Base directory for persistent render browser profiles. Cookies (`cf_clearance`, shop sessions) survive between renders and are wiped on every server start. |
| `RENDER_POOL_SIZE`| `1`                   | Number of pooled render browsers; each gets its own profile so parallel renders never lock each other. |
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

Returns: list of `{title, url, snippet, engine}`.

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
   a clear `blocked (challenge_cloudflare|challenge_js|captcha|rate_limited)`
   status instead of challenge HTML disguised as content.

### When things still get blocked

| Symptom                        | Likely cause        | Fix                                        |
|--------------------------------|---------------------|--------------------------------------------|
| HTTP 429 / 408 / 5xx / 498     | rate limit / WAF    | auto-retry is built in; raise `FETCH_TIMEOUT` |
| TLS handshake timeout          | JA3 TLS fingerprint | keep `TLS_FINGERPRINT=chrome` (default)    |
| HTTP 403 + JS challenge, or JS-only content | needs a real browser | `render: true` in `web_fetch`, or `JS_RENDER=auto` |
| `blocked (captcha)` status     | reCAPTCHA/Turnstile | cannot be solved automatically; fetch from a different source |

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
