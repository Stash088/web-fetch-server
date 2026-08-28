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
export API_KEY=super-secret-token   # required, protects /mcp
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
API_KEY=super-secret-token SEARXNG_URL=http://localhost:8888 go run ./cmd/server
```

## Configuration (env vars)

| Var              | Default                | Description                                   |
|------------------|------------------------|-----------------------------------------------|
| `PORT`           | `8080`                 | HTTP listen port                              |
| `MCP_PATH`       | `/mcp`                 | MCP endpoint path                             |
| `API_KEY`        | *(empty)*              | **Required** Bearer token for `/mcp`. If unset the endpoint is open — set it in production. |
| `SEARXNG_URL`    | `http://localhost:8888`| Base URL of SearXNG                            |
| `SEARXNG_KEY`    | *(empty)*              | Optional SearXNG API key (Bearer)             |
| `MAX_FETCH_BYTES`| `2097152` (2 MiB)      | Max body size fetched from a page              |
| `FETCH_TIMEOUT`  | `20s`                  | HTTP timeout for SearXNG and page fetches      |
| `USER_AGENT`     | `web-fetch-server/...` | User-Agent sent to target sites                |
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

Returns: `{title, url, content, total_chars, format}`. Use `start_index` to page
through documents larger than `max_length`.

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

- Auth: Bearer token enforced via middleware. OAuth (Dynamic Client Registration)
  is a possible phase-2 addition for public deployments.
- Optional LLM summarization (`web_extract`) is a future extension.
- Security: `web_fetch` only allows `http`/`https` URLs; body size is capped.
